package bff

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tociva/billmesh/internal/config"
)

func testConfig() config.BFFConfig {
	return config.BFFConfig{
		Realm: "console", AppOrigin: "https://console.billmesh.example", Issuer: "https://identity.example",
		ClientID: "billmesh-web", ClientSecret: "secret", Audience: "billmesh",
		RedirectURI:           "https://api.billmesh.example/api/v1/auth/console/callback",
		PostLogoutRedirectURI: "https://api.billmesh.example/api/v1/auth/console/logout/callback",
		StandaloneLogoutURI:   "https://auth.idnest.example/logout",
		SessionEncryptionKeys: "v1:" + encodedKey(1), SessionIdleTTL: time.Hour,
		SessionAbsoluteTTL: 24 * time.Hour, LoginTTL: 5 * time.Minute,
		LogoutTTL: 2 * time.Minute, RefreshSkew: time.Minute,
		DefaultReturnPath: "/app", ReturnPathPrefixes: []string{"/app"}, LoginAttemptsPerMinute: 2,
	}
}

func TestValidateConfigRequiresSecureConsistentOrigins(t *testing.T) {
	require.NoError(t, validateConfig(testConfig()))
	for name, mutate := range map[string]func(*config.BFFConfig){
		"insecure":       func(c *config.BFFConfig) { c.AppOrigin = "http://billmesh.example" },
		"localhost HTTP": func(c *config.BFFConfig) { c.Issuer = "http://localhost:8090" },
		"invalid scheme": func(c *config.BFFConfig) { c.AppOrigin = "ftp://billmesh.example" },
		"logout origin": func(c *config.BFFConfig) {
			c.PostLogoutRedirectURI = "https://other.example/api/v1/auth/console/logout/callback"
		},
		"return prefix": func(c *config.BFFConfig) { c.ReturnPathPrefixes = []string{"https://evil.example"} },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			mutate(&cfg)
			require.Error(t, validateConfig(cfg))
		})
	}
}

func TestSafeReturnToRejectsExternalAndAuthPaths(t *testing.T) {
	manager := &Manager{config: testConfig()}
	require.Equal(t, "/app/billing?tab=plans", manager.safeReturnTo("/app/billing?tab=plans"))
	require.Equal(t, "/app/billing#usage", manager.safeReturnTo("/app/billing#usage"))
	for _, value := range []string{"", "//evil.example", "https://evil.example", "/auth/callback", "/other", "/application", "/app\\evil"} {
		require.Equal(t, "/app", manager.safeReturnTo(value))
	}
}

func TestSecureCookiesUseHostOnlyBrowserContract(t *testing.T) {
	cookie := secureCookie("__Host-billmesh-console-session", "opaque", time.Hour)
	require.True(t, cookie.Secure)
	require.True(t, cookie.HttpOnly)
	require.Equal(t, "/", cookie.Path)
	require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	require.Empty(t, cookie.Domain)
}

func TestBrowserMiddlewareRejectsAuthorizationBeforeSessionLookup(t *testing.T) {
	manager := &Manager{config: testConfig()}
	called := false
	handler := manager.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodGet, "/v1/products", nil)
	req.Header.Set("Authorization", "Bearer should-not-be-forwarded")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	require.Equal(t, http.StatusBadRequest, resp.Code)
	require.False(t, called)
	require.Equal(t, "no-store", resp.Header().Get("Cache-Control"))
}

func TestAuthAttemptLimiterIsClientScoped(t *testing.T) {
	now := time.Now()
	limiter := &attemptLimiter{entries: make(map[string]attemptCount), limit: 2, now: func() time.Time { return now }}
	require.True(t, limiter.allow("192.0.2.1"))
	require.True(t, limiter.allow("192.0.2.1"))
	require.False(t, limiter.allow("192.0.2.1"))
	require.True(t, limiter.allow("198.51.100.1"))
	now = now.Add(time.Minute)
	require.True(t, limiter.allow("192.0.2.1"))
}
