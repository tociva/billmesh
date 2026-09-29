//go:build e2e

package security_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestBrowserBFFAuthorizationCodeSessionAndCSRF(t *testing.T) {
	const apiOrigin = "http://api:8080"
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

	login, err := client.Get(apiOrigin + "/auth/login?returnTo=%2Fapp%2Fbilling")
	if err != nil {
		t.Fatal(err)
	}
	defer login.Body.Close()
	if login.StatusCode != http.StatusFound {
		t.Fatalf("login: want 302, got %d", login.StatusCode)
	}
	correlation := requireCookie(t, login.Cookies(), "__Host-billmesh-login")
	requireSecureHostCookie(t, correlation)
	authorizationURL := login.Header.Get("Location")
	if !strings.Contains(authorizationURL, "code_challenge_method=S256") ||
		!strings.Contains(authorizationURL, "nonce=") || !strings.Contains(authorizationURL, "state=") {
		t.Fatalf("authorization URL omitted OIDC security parameters: %s", authorizationURL)
	}

	authorization, err := client.Get(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	defer authorization.Body.Close()
	if authorization.StatusCode != http.StatusFound {
		t.Fatalf("authorize: want 302, got %d", authorization.StatusCode)
	}
	callbackRequest, _ := http.NewRequest(http.MethodGet, authorization.Header.Get("Location"), nil)
	callbackRequest.AddCookie(correlation)
	callback, err := client.Do(callbackRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer callback.Body.Close()
	if callback.StatusCode != http.StatusFound || callback.Header.Get("Location") != apiOrigin+"/app/billing" {
		t.Fatalf("callback: got status %d and location %q", callback.StatusCode, callback.Header.Get("Location"))
	}
	sessionCookie := requireCookie(t, callback.Cookies(), "__Host-billmesh-session")
	requireSecureHostCookie(t, sessionCookie)

	sessionRequest, _ := http.NewRequest(http.MethodGet, apiOrigin+"/auth/session", nil)
	sessionRequest.Header.Set("Origin", apiOrigin)
	sessionRequest.AddCookie(sessionCookie)
	sessionResponse, err := client.Do(sessionRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer sessionResponse.Body.Close()
	if sessionResponse.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(sessionResponse.Body)
		t.Fatalf("session: want 200, got %d: %s", sessionResponse.StatusCode, raw)
	}
	var projection struct {
		Authenticated bool `json:"authenticated"`
		User          struct {
			Subject string `json:"subject"`
			Email   string `json:"email"`
		} `json:"user"`
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.NewDecoder(sessionResponse.Body).Decode(&projection); err != nil {
		t.Fatal(err)
	}
	if !projection.Authenticated || projection.User.Subject == "" || projection.User.Email == "" || projection.CSRFToken == "" {
		t.Fatalf("incomplete safe session projection: %+v", projection)
	}

	productsRequest, _ := http.NewRequest(http.MethodGet, apiOrigin+"/bff/v1/products", nil)
	productsRequest.Header.Set("Origin", apiOrigin)
	productsRequest.AddCookie(sessionCookie)
	productsResponse, err := client.Do(productsRequest)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, productsResponse.Body)
	productsResponse.Body.Close()
	if productsResponse.StatusCode != http.StatusOK {
		t.Fatalf("cookie-authenticated API read: want 200, got %d", productsResponse.StatusCode)
	}

	mutationRequest, _ := http.NewRequest(http.MethodPost, apiOrigin+"/bff/v1/accounts", strings.NewReader("{}"))
	mutationRequest.Header.Set("Origin", apiOrigin)
	mutationRequest.Header.Set("Content-Type", "application/json")
	mutationRequest.AddCookie(sessionCookie)
	mutationResponse, err := client.Do(mutationRequest)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, mutationResponse.Body)
	mutationResponse.Body.Close()
	if mutationResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("mutation without CSRF: want 403, got %d", mutationResponse.StatusCode)
	}

	mutationRequest, _ = http.NewRequest(http.MethodPost, apiOrigin+"/bff/v1/accounts", strings.NewReader("{}"))
	mutationRequest.Header.Set("Origin", apiOrigin)
	mutationRequest.Header.Set("Content-Type", "application/json")
	mutationRequest.Header.Set("X-CSRF-Token", projection.CSRFToken)
	mutationRequest.AddCookie(sessionCookie)
	mutationResponse, err = client.Do(mutationRequest)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, mutationResponse.Body)
	mutationResponse.Body.Close()
	if mutationResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("mutation with CSRF should reach existing handler: want 400, got %d", mutationResponse.StatusCode)
	}

	foreignRequest, _ := http.NewRequest(http.MethodGet, apiOrigin+"/bff/v1/products", nil)
	foreignRequest.Header.Set("Origin", "https://untrusted.example")
	foreignRequest.AddCookie(sessionCookie)
	foreignResponse, err := client.Do(foreignRequest)
	if err != nil {
		t.Fatal(err)
	}
	foreignResponse.Body.Close()
	if foreignResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin: want 403, got %d", foreignResponse.StatusCode)
	}

	logoutRequest, _ := http.NewRequest(http.MethodPost, apiOrigin+"/auth/logout", nil)
	logoutRequest.Header.Set("Origin", apiOrigin)
	logoutRequest.Header.Set("X-CSRF-Token", projection.CSRFToken)
	logoutRequest.AddCookie(sessionCookie)
	logoutResponse, err := client.Do(logoutRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer logoutResponse.Body.Close()
	if logoutResponse.StatusCode != http.StatusOK {
		t.Fatalf("logout: want 200, got %d", logoutResponse.StatusCode)
	}

	expiredRequest, _ := http.NewRequest(http.MethodGet, apiOrigin+"/auth/session", nil)
	expiredRequest.Header.Set("Origin", apiOrigin)
	expiredRequest.AddCookie(sessionCookie)
	expiredResponse, err := client.Do(expiredRequest)
	if err != nil {
		t.Fatal(err)
	}
	expiredResponse.Body.Close()
	if expiredResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("deleted session remained usable: got %d", expiredResponse.StatusCode)
	}
}

func TestBrowserBFFRejectsUnsafeReturnTargetsAndBearerConfusion(t *testing.T) {
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	login, err := client.Get("http://api:8080/auth/login?returnTo=" + url.QueryEscape("//evil.example"))
	if err != nil {
		t.Fatal(err)
	}
	defer login.Body.Close()
	if login.StatusCode != http.StatusFound {
		t.Fatalf("login: want 302, got %d", login.StatusCode)
	}
	request, _ := http.NewRequest(http.MethodGet, "http://api:8080/bff/v1/products", nil)
	request.Header.Set("Authorization", "Bearer not-accepted-here")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("BFF accepted bearer/session ambiguity: got %d", response.StatusCode)
	}
}

func requireCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response omitted cookie %s", name)
	return nil
}

func requireSecureHostCookie(t *testing.T, cookie *http.Cookie) {
	t.Helper()
	if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie does not satisfy host-only browser contract: %+v", cookie)
	}
}
