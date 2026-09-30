package bff

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func testManagers(t *testing.T) (*Manager, *Manager, *Router) {
	t.Helper()
	consoleConfig := testConfig()
	adminConfig := testConfig()
	adminConfig.Realm = "admin"
	adminConfig.AppOrigin = "https://admin.billmesh.example"
	adminConfig.ClientID = "billmesh-admin"
	adminConfig.Scope += " billing:admin"
	adminConfig.RedirectURI = "https://api.billmesh.example/api/v1/auth/admin/callback"
	adminConfig.PostLogoutRedirectURI = "https://api.billmesh.example/api/v1/auth/admin/logout/callback"
	console := &Manager{config: consoleConfig}
	admin := &Manager{config: adminConfig}
	router, err := NewRouter(console, admin)
	require.NoError(t, err)
	return console, admin, router
}

func TestRouterAllowsCredentialedCORSOnlyForConfiguredOrigins(t *testing.T) {
	_, _, router := testManagers(t)
	handler := router.CORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	preflight := httptest.NewRequest(http.MethodOptions, "/api/v1/products", nil)
	preflight.Header.Set("Origin", "https://admin.billmesh.example")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, preflight)
	require.Equal(t, http.StatusNoContent, response.Code)
	require.Equal(t, "https://admin.billmesh.example", response.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "true", response.Header().Get("Access-Control-Allow-Credentials"))

	foreign := httptest.NewRequest(http.MethodOptions, "/api/v1/products", nil)
	foreign.Header.Set("Origin", "https://evil.example")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, foreign)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
}

func TestRouterRejectsUnknownBrowserOriginsBeforeAuthentication(t *testing.T) {
	_, _, router := testManagers(t)
	called := false
	handler := router.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
	request.Header.Set("Origin", "https://evil.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.False(t, called)
}

func TestRouterRequiresOneSharedAPICallbackOrigin(t *testing.T) {
	console, admin, _ := testManagers(t)
	admin.config.RedirectURI = "https://other-api.billmesh.example/api/v1/auth/admin/callback"
	admin.config.PostLogoutRedirectURI = "https://other-api.billmesh.example/api/v1/auth/admin/logout/callback"
	_, err := NewRouter(console, admin)
	require.ErrorContains(t, err, "same API callback origin")
}
