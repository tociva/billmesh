//go:build e2e

package security_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type adminBrowserSession struct {
	cookie *http.Cookie
	csrf   string
}

func loginAdminBrowser(t *testing.T, client *http.Client) adminBrowserSession {
	t.Helper()
	const apiOrigin = "http://api:8080"
	const adminOrigin = "https://admin.test"

	login, err := client.Get(apiOrigin + "/api/v1/auth/admin/login?returnTo=%2Fapp")
	if err != nil {
		t.Fatal(err)
	}
	defer login.Body.Close()
	if login.StatusCode != http.StatusFound {
		t.Fatalf("admin login: want 302, got %d", login.StatusCode)
	}
	correlation := requireCookie(t, login.Cookies(), "__Host-billmesh-admin-login")
	authorization, err := client.Get(login.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	defer authorization.Body.Close()
	callbackURL, err := url.Parse(authorization.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	callbackURL.Scheme = "http"
	callbackURL.Host = "api:8080"
	callbackRequest, _ := http.NewRequest(http.MethodGet, callbackURL.String(), nil)
	callbackRequest.AddCookie(correlation)
	callback, err := client.Do(callbackRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer callback.Body.Close()
	if callback.StatusCode != http.StatusFound || callback.Header.Get("Location") != adminOrigin+"/app" {
		t.Fatalf("admin callback: got %d and %q", callback.StatusCode, callback.Header.Get("Location"))
	}
	sessionCookie := requireCookie(t, callback.Cookies(), "__Host-billmesh-admin-session")

	sessionRequest, _ := http.NewRequest(http.MethodGet, apiOrigin+"/api/v1/auth/admin/session", nil)
	sessionRequest.Header.Set("Origin", adminOrigin)
	sessionRequest.AddCookie(sessionCookie)
	sessionResponse, err := client.Do(sessionRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer sessionResponse.Body.Close()
	if sessionResponse.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(sessionResponse.Body)
		t.Fatalf("admin session: want 200, got %d: %s", sessionResponse.StatusCode, raw)
	}
	var projection struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.NewDecoder(sessionResponse.Body).Decode(&projection); err != nil {
		t.Fatal(err)
	}
	if projection.CSRFToken == "" {
		t.Fatal("admin session omitted CSRF token")
	}
	return adminBrowserSession{cookie: sessionCookie, csrf: projection.CSRFToken}
}

func TestCATSEC014Through018AdminBFFCatalogueIsolation(t *testing.T) {
	const apiOrigin = "http://api:8080"
	const adminOrigin = "https://admin.test"
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	first := loginAdminBrowser(t, client)
	second := loginAdminBrowser(t, client)
	body := fmt.Sprintf(`{"slug":"bff-catalogue-%d","name":"BFF Catalogue Product"}`, time.Now().UnixNano())

	request, _ := http.NewRequest(http.MethodPost, apiOrigin+"/api/v1/admin/products", strings.NewReader(body))
	request.Header.Set("Origin", adminOrigin)
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(first.cookie)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("admin mutation without CSRF: want 403, got %d", response.StatusCode)
	}

	request, _ = http.NewRequest(http.MethodPost, apiOrigin+"/api/v1/admin/products", strings.NewReader(body))
	request.Header.Set("Origin", adminOrigin)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", first.csrf)
	request.AddCookie(second.cookie)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("CSRF token from another session: want 403, got %d", response.StatusCode)
	}

	request, _ = http.NewRequest(http.MethodPost, apiOrigin+"/api/v1/admin/products", strings.NewReader(body))
	request.Header.Set("Origin", adminOrigin)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", first.csrf)
	request.AddCookie(first.cookie)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("valid admin BFF mutation: want 201, got %d", response.StatusCode)
	}

	for _, origin := range []string{"https://console.test", "https://untrusted.example"} {
		request, _ = http.NewRequest(http.MethodGet, apiOrigin+"/api/v1/admin/products", nil)
		request.Header.Set("Origin", origin)
		request.AddCookie(first.cookie)
		response, err = client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized && response.StatusCode != http.StatusForbidden {
			t.Fatalf("origin %s used admin cookie: got %d", origin, response.StatusCode)
		}
	}

	logout, _ := http.NewRequest(http.MethodPost, apiOrigin+"/api/v1/auth/admin/logout", nil)
	logout.Header.Set("Origin", adminOrigin)
	logout.Header.Set("X-CSRF-Token", first.csrf)
	logout.AddCookie(first.cookie)
	logoutResponse, err := client.Do(logout)
	if err != nil {
		t.Fatal(err)
	}
	logoutResponse.Body.Close()

	request, _ = http.NewRequest(http.MethodGet, apiOrigin+"/api/v1/admin/products", nil)
	request.Header.Set("Origin", adminOrigin)
	request.AddCookie(first.cookie)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("logged-out admin session remained usable: got %d", response.StatusCode)
	}
}
