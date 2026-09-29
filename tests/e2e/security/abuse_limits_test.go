//go:build e2e

package security_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tociva/billmesh/tests/testkit"
)

func TestSecurityUsageBatchAndBodyLimits(t *testing.T) {
	h := testkit.NewHTTP(t)
	token := h.IssueToken(t, testkit.Unique("usage-limits"), "daybook", testkit.AllPermissions(), nil)
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "batch over 1000 events", body: "[" + strings.Repeat("{},", 1000) + "{}]"},
		{name: "body over 1 MiB", body: `{"events":[{"metadata":"` + strings.Repeat("x", 1<<20) + `"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, h.BaseURL+"/v1/usage-events", strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := h.Client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("want 400 for %s, got %d", tc.name, resp.StatusCode)
			}
			if tc.name == "body over 1 MiB" {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(body), "invalid usage payload") {
					t.Fatalf("oversized body bypassed the size limit: %s", body)
				}
			}
		})
	}
}
