//go:build e2e

package security_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tociva/billmesh/tests/testkit"
)

func TestSecuritySSEConnectionCapacityIsAccountScoped(t *testing.T) {
	h := testkit.NewHTTP(t)
	orgA := testkit.Unique("sse-cap-a")
	tokenA := h.IssueToken(t, orgA, "daybook", testkit.AllPermissions(), nil)
	testkit.CreateFixtureAccount(t, h, orgA, tokenA)
	orgB := testkit.Unique("sse-cap-b")
	tokenB := h.IssueToken(t, orgB, "daybook", testkit.AllPermissions(), nil)
	testkit.CreateFixtureAccount(t, h, orgB, tokenB)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := &http.Client{}
	open := func(token string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.BaseURL+"/v1/events", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("connect event stream: %v", err)
		}
		return resp
	}
	var streams []*http.Response
	defer func() {
		for _, stream := range streams {
			stream.Body.Close()
		}
	}()
	for range 8 {
		stream := open(tokenA)
		if stream.StatusCode != http.StatusOK {
			stream.Body.Close()
			t.Fatalf("legitimate stream was rejected: %d", stream.StatusCode)
		}
		streams = append(streams, stream)
	}
	full := open(tokenA)
	if full.StatusCode != http.StatusTooManyRequests {
		full.Body.Close()
		t.Fatalf("ninth stream: want 429, got %d", full.StatusCode)
	}
	full.Body.Close()
	other := open(tokenB)
	if other.StatusCode != http.StatusOK {
		other.Body.Close()
		t.Fatalf("other account lost stream capacity: %d", other.StatusCode)
	}
	other.Body.Close()
	streams[0].Body.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		replacement := open(tokenA)
		if replacement.StatusCode == http.StatusOK {
			streams = append(streams, replacement)
			break
		}
		replacement.Body.Close()
		if replacement.StatusCode != http.StatusTooManyRequests || time.Now().After(deadline) {
			t.Fatalf("closed stream did not restore capacity: %d", replacement.StatusCode)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestSecuritySSEEmitsOnlyAuthorizedTenantEvents(t *testing.T) {
	h := testkit.NewHTTP(t)
	orgA := testkit.Unique("sse-a")
	tokenA := h.IssueToken(t, orgA, "daybook", testkit.AllPermissions(), nil)
	accountA := testkit.CreateFixtureAccount(t, h, orgA, tokenA)
	subscriptionA := testkit.CreateFixtureSubscription(t, h, accountA, tokenA)
	orgB := testkit.Unique("sse-b")
	tokenB := h.IssueToken(t, orgB, "daybook", testkit.AllPermissions(), nil)
	accountB := testkit.CreateFixtureAccount(t, h, orgB, tokenB)
	subscriptionB := testkit.CreateFixtureSubscription(t, h, accountB, tokenB)
	req, err := http.NewRequest(http.MethodGet, h.BaseURL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tokenA)
	client := *h.Client
	client.Timeout = 1500 * time.Millisecond
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("LIVE-006: connect SSE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("LIVE-006: unexpected stream response %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		var netErr net.Error
		if !errors.As(readErr, &netErr) || !netErr.Timeout() {
			t.Fatalf("LIVE-006: read SSE: %v", readErr)
		}
	}
	if !strings.Contains(string(raw), subscriptionA) || strings.Contains(string(raw), subscriptionB) {
		t.Fatalf("LIVE-006: incorrect tenant events in stream: %s", raw)
	}
}
