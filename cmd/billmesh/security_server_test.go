package main

import (
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tociva/billmesh/internal/app"
	"github.com/tociva/billmesh/internal/auth"
	"github.com/tociva/billmesh/internal/config"
)

func TestSecurityAPIRejectsMissingOIDCConfigurationBeforeOpeningDatabase(t *testing.T) {
	for _, missing := range []string{"OIDC_ISSUER", "OIDC_AUDIENCE"} {
		t.Run(missing, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://unreachable.invalid:5432/billmesh")
			t.Setenv("OIDC_ISSUER", "https://issuer.example")
			t.Setenv("OIDC_AUDIENCE", "billmesh")
			t.Setenv("OIDC_CLIENT_PROFILES", `[{"client_id":"test-client","type":"billing","app":"daybook","environment":"test"}]`)
			t.Setenv(missing, "")
			originalArgs := os.Args
			os.Args = []string{"billmesh", "api"}
			defer func() { os.Args = originalArgs }()
			if err := run(); err == nil || !strings.Contains(err.Error(), "OIDC_ISSUER and OIDC_AUDIENCE are required") {
				t.Fatalf("missing %s should prevent API startup before database access, got %v", missing, err)
			}
		})
	}
}

func TestSecurityAPIRejectsInvalidBFFConfigurationBeforeOpeningDatabase(t *testing.T) {
	key := make([]byte, 32)
	cfg := config.Config{
		OIDCIssuer: "https://issuer.example", OIDCAudience: "billmesh",
		OIDCClients: auth.ClientRegistry{"test-client": {ClientID: "test-client", Type: auth.ClientBilling, App: "daybook", Environment: "test"}},
		BFF: config.BrowserAuthConfig{Realms: []config.BFFConfig{{
			Realm: "console", AppOrigin: "http://billmesh.example", Issuer: "https://issuer.example",
			ClientID: "billmesh-web", ClientSecret: "secret", Audience: "billmesh",
			RedirectURI:           "https://api.billmesh.example/api/v1/auth/console/callback",
			PostLogoutRedirectURI: "https://api.billmesh.example/api/v1/auth/console/logout/callback",
			StandaloneLogoutURI:   "https://auth.idnest.example/logout",
			SessionEncryptionKeys: "v1:" + base64.RawURLEncoding.EncodeToString(key),
			SessionIdleTTL:        time.Hour, SessionAbsoluteTTL: 24 * time.Hour,
			LoginTTL: time.Minute, LogoutTTL: time.Minute, RefreshSkew: time.Minute,
			DefaultReturnPath: "/app", ReturnPathPrefixes: []string{"/app"}, LoginAttemptsPerMinute: 30,
		}}},
	}
	admin := cfg.BFF.Realms[0]
	admin.Realm = "admin"
	admin.AppOrigin = "https://admin.billmesh.example"
	admin.Scope = "openid profile email"
	admin.RedirectURI = "https://api.billmesh.example/api/v1/auth/admin/callback"
	admin.PostLogoutRedirectURI = "https://api.billmesh.example/api/v1/auth/admin/logout/callback"
	cfg.BFF.Realms = append(cfg.BFF.Realms, admin)
	if err := validateAPIAuthConfig(cfg); err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
		t.Fatalf("invalid BFF configuration should fail before database access, got %v", err)
	}
}

func TestSecurityAPIRequiresBothBFFRealms(t *testing.T) {
	cfg := config.Config{OIDCIssuer: "https://issuer.example", OIDCAudience: "billmesh", OIDCClients: auth.ClientRegistry{"test-client": {ClientID: "test-client", Type: auth.ClientBilling, App: "daybook", Environment: "test"}}}
	if err := validateAPIAuthConfig(cfg); err == nil || !strings.Contains(err.Error(), "console and admin") {
		t.Fatalf("missing BFF realms should fail before database access, got %v", err)
	}
}

func TestSecurityAPIRequiresClientProfiles(t *testing.T) {
	cfg := config.Config{OIDCIssuer: "https://issuer.example", OIDCAudience: "billmesh"}
	if err := validateAPIAuthConfig(cfg); err == nil || !strings.Contains(err.Error(), "OIDC_CLIENT_PROFILES") {
		t.Fatalf("missing client profiles should fail before database access, got %v", err)
	}
}

func TestSecurityAPIRequiresBFFCredentials(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("OIDC_ISSUER", "https://issuer.example")
	t.Setenv("OIDC_AUDIENCE", "billmesh")
	t.Setenv("OIDC_CLIENT_PROFILES", `[{"client_id":"test-client","type":"billing","app":"daybook","environment":"test"}]`)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err = validateAPIAuthConfig(cfg); err == nil || !strings.Contains(err.Error(), "is required") {
		t.Fatalf("missing BFF credentials should fail before database access, got %v", err)
	}
}

func TestSecurityAPIExcludesTestOnlyRoutes(t *testing.T) {
	handler := app.NewAPI(nil, nil, nil).Handler()
	for _, path := range []string{"/test/token", "/test/rotate-key", "/test/failure"} {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, path, nil))
		if resp.Code != http.StatusNotFound {
			t.Fatalf("test-only route %s is exposed by API: %d", path, resp.Code)
		}
	}
}

func TestSecurityAPIServerClosesSlowHeaderAndBodyClients(t *testing.T) {
	requestRead := make(chan error, 1)
	server := newAPIServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		requestRead <- err
		if err == nil {
			w.WriteHeader(http.StatusOK)
		}
	}))
	if server.ReadHeaderTimeout == 0 || server.ReadTimeout == 0 || server.IdleTimeout == 0 || server.MaxHeaderBytes == 0 {
		t.Fatal("API server must configure request and connection limits")
	}
	server.ReadHeaderTimeout = 75 * time.Millisecond
	server.ReadTimeout = 125 * time.Millisecond
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	headerConn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer headerConn.Close()
	headerConn.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(headerConn, "GET / HTTP/1.1\r\nHost: example\r\nX-Slow: "); err != nil {
		t.Fatal(err)
	}
	headerReply, err := io.ReadAll(headerConn)
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		t.Fatal("slow header connection stayed open")
	}
	if len(headerReply) > 0 && !strings.Contains(string(headerReply), "408") {
		t.Fatalf("unexpected slow header response: %q", headerReply)
	}

	bodyConn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer bodyConn.Close()
	bodyConn.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(bodyConn, "POST / HTTP/1.1\r\nHost: example\r\nContent-Length: 5\r\n\r\nx"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-requestRead:
		if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
			t.Fatalf("slow body read: want timeout, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow body was not timed out")
	}
}
