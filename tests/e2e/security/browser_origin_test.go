//go:build e2e

package security_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/tociva/billmesh/tests/testkit"
)

// Billmesh uses bearer tokens and serves the API to same-origin browser clients.
// There is no cookie-backed session or cross-origin allowlist in this contract.
func TestSecurityBrowserOriginContract(t *testing.T) {
	h := testkit.NewHTTP(t)
	token := h.IssueToken(t, "", "daybook", []string{"catalogue:read"}, nil)
	sameOrigin, err := http.NewRequest(http.MethodGet, h.BaseURL+"/v1/catalog", nil)
	if err != nil {
		t.Fatal(err)
	}
	sameOrigin.Header.Set("Origin", h.BaseURL)
	sameOrigin.Header.Set("Authorization", "Bearer "+token)
	resp, err := h.Client.Do(sameOrigin)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(resp.Cookies()) != 0 {
		t.Fatalf("GAP-SEC-011: same-origin bearer request returned %d with %d session cookies", resp.StatusCode, len(resp.Cookies()))
	}
	for _, method := range []string{http.MethodOptions, http.MethodGet} {
		req, err := http.NewRequest(method, h.BaseURL+"/v1/catalog", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", "https://untrusted.example")
		req.Header.Set("Authorization", "Bearer "+token)
		if method == http.MethodOptions {
			req.Header.Set("Access-Control-Request-Method", http.MethodGet)
			req.Header.Set("Access-Control-Request-Headers", "authorization")
		}
		resp, err := h.Client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if allow := resp.Header.Get("Access-Control-Allow-Origin"); allow == "*" || allow == "https://untrusted.example" || resp.Header.Get("Access-Control-Allow-Credentials") == "true" {
			t.Fatalf("GAP-SEC-011: untrusted origin received browser access: origin=%q credentials=%q", allow, resp.Header.Get("Access-Control-Allow-Credentials"))
		}
	}
}
