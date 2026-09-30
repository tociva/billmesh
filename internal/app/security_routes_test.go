package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/tociva/billmesh/internal/auth"
)

type routeTestVerifier struct{}

type routeTestBrowserAuth struct{}

func assertRouteJSONError(t *testing.T, response *httptest.ResponseRecorder, status int) string {
	t.Helper()
	if response.Code != status {
		t.Fatalf("want status %d, got %d: %s", status, response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v: %s", err, response.Body.String())
	}
	if body.Error == "" {
		t.Fatal("error response has an empty error message")
	}
	return body.Error
}

func (routeTestBrowserAuth) Authenticate(*http.Request) (*auth.Claims, int, error) {
	return &auth.Claims{Permissions: []string{"billing:read"}, OrgID: "browser-org", App: "daybook"}, 0, nil
}

func (routeTestBrowserAuth) AuthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
}

func (routeTestBrowserAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := &auth.Claims{Permissions: []string{"billing:read"}, OrgID: "browser-org", App: "daybook"}
		next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), claims)))
	})
}

func (routeTestBrowserAuth) CORS(next http.Handler) http.Handler { return next }

func (routeTestVerifier) Verify(_ context.Context, token string) (*auth.Claims, error) {
	if token == "valid" {
		return &auth.Claims{}, nil
	}
	if strings.HasPrefix(token, "with:") {
		return &auth.Claims{Permissions: []string{strings.TrimPrefix(token, "with:")}, OrgID: "route-test", App: "daybook"}, nil
	}
	return nil, context.Canceled
}

func TestSecurityProtectedRouteAuthenticationAndPermissionMatrix(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve source")
	}
	raw, err := os.ReadFile(strings.TrimSuffix(source, "security_routes_test.go") + "api.go")
	if err != nil {
		t.Fatal(err)
	}
	routes := regexp.MustCompile(`protected\.HandleFunc\("([A-Z]+) ([^\"]+)", (.+)\)`).FindAllStringSubmatch(string(raw), -1)
	if len(routes) < 40 {
		t.Fatalf("route matrix parsed only %d protected routes", len(routes))
	}
	a := NewAPI(nil, routeTestVerifier{}, nil)
	a.ConfigureRequestLimits(1000, 1000, 1000)
	handler := a.Handler()
	for _, route := range routes {
		method, path, registration := route[1], route[2], route[3]
		permission := ""
		switch {
		case strings.HasPrefix(registration, "auth.Require("):
			match := regexp.MustCompile(`auth\.Require\("([^\"]+)"`).FindStringSubmatch(registration)
			if len(match) != 2 {
				t.Fatalf("cannot parse permission for %s %s", method, path)
			}
			permission = match[1]
		case strings.HasPrefix(registration, "a.requireAdmin("):
			permission = "billing:admin"
		case !strings.HasPrefix(registration, "a."):
			t.Fatalf("cannot parse handler for %s %s: %s", method, path, registration)
		}
		path = strings.ReplaceAll(path, "{id}", "00000000-0000-4000-8000-000000000001")
		t.Run(method+" "+path, func(t *testing.T) {
			for _, token := range []string{"", "invalid"} {
				req := httptest.NewRequest(method, path, nil)
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				resp := httptest.NewRecorder()
				handler.ServeHTTP(resp, req)
				assertRouteJSONError(t, resp, http.StatusUnauthorized)
			}
			if permission != "" {
				req := httptest.NewRequest(method, path, nil)
				req.Header.Set("Authorization", "Bearer valid")
				resp := httptest.NewRecorder()
				handler.ServeHTTP(resp, req)
				assertRouteJSONError(t, resp, http.StatusForbidden)
			}
			authorized := "valid"
			if permission != "" {
				authorized = "with:" + permission
			}
			req := httptest.NewRequest(method, path, nil)
			req.Header.Set("Authorization", "Bearer "+authorized)
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, req)
			if resp.Code == http.StatusUnauthorized || resp.Code == http.StatusNotFound {
				t.Fatalf("correct %s permission did not reach route handler: %d %q", permission, resp.Code, resp.Body.String())
			}
		})
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("public health route returned %d", resp.Code)
	}
}

func TestProtectedRouterNotFoundAndMethodErrorsAreJSON(t *testing.T) {
	a := NewAPI(nil, routeTestVerifier{}, nil)
	handler := a.Handler()

	for _, tc := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/v1/not-a-route", http.StatusNotFound},
		{http.MethodDelete, "/v1/products", http.StatusMethodNotAllowed},
	} {
		request := httptest.NewRequest(tc.method, tc.path, nil)
		request.Header.Set("Authorization", "Bearer valid")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		assertRouteJSONError(t, response, tc.status)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	assertRouteJSONError(t, response, http.StatusMethodNotAllowed)
}

func TestBrowserAndBearerSurfacesShareProtectedHandlersWithoutAuthAmbiguity(t *testing.T) {
	a := NewAPI(nil, routeTestVerifier{}, nil)
	a.ConfigureBrowserAuth(routeTestBrowserAuth{})
	handler := a.Handler()

	browser := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
	request.Header.Set("Origin", "https://console.billmesh.example")
	handler.ServeHTTP(browser, request)
	if browser.Code == http.StatusUnauthorized || browser.Code == http.StatusNotFound {
		t.Fatalf("browser authentication did not reach shared handler: %d", browser.Code)
	}

	bearer := httptest.NewRecorder()
	handler.ServeHTTP(bearer, httptest.NewRequest(http.MethodGet, "/v1/products", nil))
	if bearer.Code != http.StatusUnauthorized {
		t.Fatalf("bearer surface accepted a cookie-style request: %d", bearer.Code)
	}

	authRoute := httptest.NewRecorder()
	handler.ServeHTTP(authRoute, httptest.NewRequest(http.MethodGet, "/api/v1/auth/console/session", nil))
	if authRoute.Code != http.StatusNoContent {
		t.Fatalf("browser auth route was not mounted: %d", authRoute.Code)
	}
}
