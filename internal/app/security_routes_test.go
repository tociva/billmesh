package app

import (
	"context"
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
				if resp.Code != http.StatusUnauthorized {
					t.Fatalf("token %q: want 401, got %d", token, resp.Code)
				}
			}
			if permission != "" {
				req := httptest.NewRequest(method, path, nil)
				req.Header.Set("Authorization", "Bearer valid")
				resp := httptest.NewRecorder()
				handler.ServeHTTP(resp, req)
				if resp.Code != http.StatusForbidden {
					t.Fatalf("missing %s: want 403, got %d", permission, resp.Code)
				}
			}
			authorized := "valid"
			if permission != "" {
				authorized = "with:" + permission
			}
			req := httptest.NewRequest(method, path, nil)
			req.Header.Set("Authorization", "Bearer "+authorized)
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, req)
			if resp.Code == http.StatusUnauthorized || resp.Code == http.StatusNotFound || (resp.Code == http.StatusForbidden && strings.TrimSpace(resp.Body.String()) == "forbidden") {
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
