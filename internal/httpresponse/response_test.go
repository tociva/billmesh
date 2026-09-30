package httpresponse

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestErrorWritesJSONEnvelope(t *testing.T) {
	response := httptest.NewRecorder()
	Error(response, http.StatusUnauthorized, "invalid bearer token")

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["error"] != "invalid bearer token" {
		t.Fatalf("error = %q", body["error"])
	}
}

func TestNoContentAndRedirectHaveEmptyBodies(t *testing.T) {
	noContent := httptest.NewRecorder()
	NoContent(noContent)
	if noContent.Code != http.StatusNoContent || noContent.Body.Len() != 0 {
		t.Fatalf("no-content response = %d %q", noContent.Code, noContent.Body.String())
	}

	redirect := httptest.NewRecorder()
	Redirect(redirect, "https://identity.example/login", http.StatusFound)
	if redirect.Code != http.StatusFound || redirect.Body.Len() != 0 {
		t.Fatalf("redirect response = %d %q", redirect.Code, redirect.Body.String())
	}
	if got := redirect.Header().Get("Location"); got != "https://identity.example/login" {
		t.Fatalf("Location = %q", got)
	}
}

func TestJSONFallbacksPreservesRoutesAndConvertsRouterErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /resource", func(w http.ResponseWriter, _ *http.Request) {
		JSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	handler := JSONFallbacks(mux)

	for _, tc := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/resource", http.StatusOK},
		{http.MethodPost, "/resource", http.StatusMethodNotAllowed},
		{http.MethodGet, "/missing", http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
		if response.Code != tc.status {
			t.Fatalf("%s %s: status = %d", tc.method, tc.path, response.Code)
		}
		if response.Header().Get("Content-Type") != "application/json" || !json.Valid(response.Body.Bytes()) {
			t.Fatalf("%s %s: invalid JSON response %q", tc.method, tc.path, response.Body.String())
		}
	}
}
