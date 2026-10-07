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
	return &auth.Claims{ClientType: auth.ClientConsole, OrgID: "browser-org", App: "daybook", Environment: "test"}, 0, nil
}

func (routeTestBrowserAuth) AuthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
}

func (routeTestBrowserAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := &auth.Claims{ClientType: auth.ClientConsole, OrgID: "browser-org", App: "daybook", Environment: "test"}
		next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), claims)))
	})
}

func (routeTestBrowserAuth) CORS(next http.Handler) http.Handler { return next }

func (routeTestVerifier) Verify(_ context.Context, token string) (*auth.Claims, error) {
	if token == "valid" {
		return &auth.Claims{}, nil
	}
	if strings.HasPrefix(token, "with:") {
		clientType := auth.ClientType(strings.TrimPrefix(token, "with:"))
		actorType := "service"
		if clientType == auth.ClientAdmin {
			actorType = "user"
		}
		return &auth.Claims{ClientType: clientType, ActorType: actorType, OrgID: "route-test", App: "daybook", Environment: "test"}, nil
	}
	return nil, context.Canceled
}

func TestSecurityProtectedRouteAuthenticationAndClientMatrix(t *testing.T) {
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
		clientType := ""
		switch {
		case strings.HasPrefix(registration, "auth.RequireClient(catalogueClients,"):
			clientType = string(auth.ClientCatalogue)
		case strings.HasPrefix(registration, "auth.RequireClient(billingClients,"):
			clientType = string(auth.ClientBilling)
		case strings.HasPrefix(registration, "auth.RequireClient(runtimeClients,"):
			clientType = string(auth.ClientRuntime)
		case strings.HasPrefix(registration, "auth.RequireClient([]auth.ClientType{auth.ClientAdmin},"):
			clientType = string(auth.ClientAdmin)
		case strings.HasPrefix(registration, "a.requireAdmin("), strings.HasPrefix(registration, "a.requireCatalogueAdmin("):
			clientType = string(auth.ClientAdmin)
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
			if clientType != "" {
				req := httptest.NewRequest(method, path, nil)
				req.Header.Set("Authorization", "Bearer valid")
				resp := httptest.NewRecorder()
				handler.ServeHTTP(resp, req)
				assertRouteJSONError(t, resp, http.StatusForbidden)
			}
			for _, candidate := range []auth.ClientType{auth.ClientCatalogue, auth.ClientBilling, auth.ClientRuntime} {
				req := httptest.NewRequest(method, path, nil)
				req.Header.Set("Authorization", "Bearer with:"+string(candidate))
				resp := httptest.NewRecorder()
				handler.ServeHTTP(resp, req)
				if string(candidate) == clientType {
					if resp.Code == http.StatusUnauthorized || resp.Code == http.StatusForbidden {
						t.Fatalf("allowed %s client was stopped before the handler: %d %s", candidate, resp.Code, resp.Body.String())
					}
					continue
				}
				assertRouteJSONError(t, resp, http.StatusForbidden)
			}
			authorized := "valid"
			if clientType != "" {
				authorized = "with:" + clientType
			}
			req := httptest.NewRequest(method, path, nil)
			req.Header.Set("Authorization", "Bearer "+authorized)
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, req)
			if resp.Code == http.StatusUnauthorized || resp.Code == http.StatusNotFound {
				t.Fatalf("correct %s client did not reach route handler: %d %q", clientType, resp.Code, resp.Body.String())
			}
		})
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("public health route returned %d", resp.Code)
	}
}

func TestSecurityProductClientsCannotCrossAPISurfaces(t *testing.T) {
	a := NewAPI(nil, routeTestVerifier{}, nil)
	a.ConfigureRequestLimits(1000, 1000, 1000)
	handler := a.Handler()
	cases := []struct {
		name, method, path string
		allowed            auth.ClientType
	}{
		{"catalogue", http.MethodGet, "/v1/catalog", auth.ClientCatalogue},
		{"billing", http.MethodGet, "/v1/accounts/current", auth.ClientBilling},
		{"runtime", http.MethodPost, "/v1/executions/authorize", auth.ClientRuntime},
	}
	clients := []auth.ClientType{auth.ClientCatalogue, auth.ClientBilling, auth.ClientRuntime}
	for _, endpoint := range cases {
		for _, clientType := range clients {
			t.Run(string(clientType)+"/"+endpoint.name, func(t *testing.T) {
				req := httptest.NewRequest(endpoint.method, endpoint.path, nil)
				req.Header.Set("Authorization", "Bearer with:"+string(clientType))
				resp := httptest.NewRecorder()
				handler.ServeHTTP(resp, req)
				if clientType == endpoint.allowed {
					if resp.Code == http.StatusUnauthorized || resp.Code == http.StatusForbidden {
						t.Fatalf("allowed %s client was stopped before the handler: %d %s", clientType, resp.Code, resp.Body.String())
					}
					return
				}
				assertRouteJSONError(t, resp, http.StatusForbidden)
			})
		}
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
		{http.MethodDelete, "/v1/admin/products", http.StatusMethodNotAllowed},
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
	request := httptest.NewRequest(http.MethodGet, "/api/v1/catalog", nil)
	request.Header.Set("Origin", "https://console.billmesh.example")
	handler.ServeHTTP(browser, request)
	if browser.Code == http.StatusUnauthorized || browser.Code == http.StatusNotFound {
		t.Fatalf("browser authentication did not reach shared handler: %d", browser.Code)
	}

	bearer := httptest.NewRecorder()
	handler.ServeHTTP(bearer, httptest.NewRequest(http.MethodGet, "/v1/catalog", nil))
	if bearer.Code != http.StatusUnauthorized {
		t.Fatalf("bearer surface accepted a cookie-style request: %d", bearer.Code)
	}

	authRoute := httptest.NewRecorder()
	handler.ServeHTTP(authRoute, httptest.NewRequest(http.MethodGet, "/api/v1/auth/console/session", nil))
	if authRoute.Code != http.StatusNoContent {
		t.Fatalf("browser auth route was not mounted: %d", authRoute.Code)
	}
}
