package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/tociva/billmesh/internal/auth"
)

type limitTestVerifier struct{}

func (limitTestVerifier) Verify(_ context.Context, token string) (*auth.Claims, error) {
	if token == "tenant-a" || token == "tenant-b" {
		return &auth.Claims{OrgID: token, App: "daybook", Permissions: []string{"billing:admin"}}, nil
	}
	return nil, errors.New("invalid test token")
}

func TestSecurityAuthenticationFloodDoesNotBlockValidTenant(t *testing.T) {
	a := NewAPI(nil, limitTestVerifier{}, nil)
	a.ConfigureRequestLimits(2, 1, 2)
	handler := a.Handler()
	request := func(token, remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/products", nil)
		req.RemoteAddr = remote
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		return resp
	}
	for i := 0; i < 2; i++ {
		if got := request("invalid", "192.0.2.1:1234").Code; got != http.StatusUnauthorized {
			t.Fatalf("authentication failure %d: want 401, got %d", i, got)
		}
	}
	limited := request("invalid", "192.0.2.1:1234")
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("authentication flood: want 429 and Retry-After, got %d %q", limited.Code, limited.Header().Get("Retry-After"))
	}
	if got := request("invalid", "198.51.100.2:1234").Code; got != http.StatusUnauthorized {
		t.Fatalf("another client lost authentication capacity: %d", got)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/adjustments", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("Authorization", "Bearer tenant-b")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("a valid tenant behind the flooded client address was blocked: %d", resp.Code)
	}
}

func TestSecurityMutationFloodIsTenantScoped(t *testing.T) {
	a := NewAPI(nil, limitTestVerifier{}, nil)
	a.ConfigureRequestLimits(30, 1, 10)
	handler := a.Handler()
	request := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/adjustments", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		return resp
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 30)
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- request("tenant-a")
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	allowed, limited := 0, 0
	for resp := range results {
		switch resp.Code {
		case http.StatusUnsupportedMediaType:
			allowed++
		case http.StatusTooManyRequests:
			limited++
			if resp.Header().Get("Retry-After") == "" {
				t.Fatal("throttled request omitted Retry-After")
			}
		default:
			t.Fatalf("mutation flood returned unexpected status %d", resp.Code)
		}
	}
	if allowed != 10 || limited != 20 {
		t.Fatalf("want 10 allowed and 20 throttled requests, got %d and %d", allowed, limited)
	}
	if got := request("tenant-b").Code; got != http.StatusUnsupportedMediaType {
		t.Fatalf("another tenant lost mutation capacity: %d", got)
	}
}
