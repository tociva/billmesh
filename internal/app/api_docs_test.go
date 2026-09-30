package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tociva/billmesh/internal/auth"
)

type documentationBrowserAuth struct {
	claims *auth.Claims
	status int
	err    error
	called int
}

func (b *documentationBrowserAuth) Authenticate(*http.Request) (*auth.Claims, int, error) {
	b.called++
	return b.claims, b.status, b.err
}

func (*documentationBrowserAuth) AuthHandler() http.Handler                 { return http.NotFoundHandler() }
func (*documentationBrowserAuth) Middleware(next http.Handler) http.Handler { return next }
func (*documentationBrowserAuth) CORS(next http.Handler) http.Handler       { return next }

func TestAPIDocumentationRequiresAuthentication(t *testing.T) {
	handler := NewAPI(nil, routeTestVerifier{}, nil).Handler()
	for _, path := range []string{"/openapi.yaml", "/docs/"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", response.Code)
			}
			if response.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("WWW-Authenticate = %q", response.Header().Get("WWW-Authenticate"))
			}
			if response.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestAPIDocumentationAcceptsValidBearerToken(t *testing.T) {
	handler := NewAPI(nil, routeTestVerifier{}, nil).Handler()
	for _, path := range []string{"/openapi.yaml", "/docs/"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("Authorization", "Bearer valid")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAPIDocumentationAcceptsBrowserSession(t *testing.T) {
	browser := &documentationBrowserAuth{claims: &auth.Claims{OrgID: "docs-user"}}
	api := NewAPI(nil, routeTestVerifier{}, nil)
	api.ConfigureBrowserAuth(browser)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if browser.called != 1 {
		t.Fatalf("browser authentication calls = %d, want 1", browser.called)
	}
}

func TestAPIDocumentationDoesNotFallBackFromBearerToBrowserSession(t *testing.T) {
	browser := &documentationBrowserAuth{claims: &auth.Claims{OrgID: "docs-user"}}
	api := NewAPI(nil, routeTestVerifier{}, nil)
	api.ConfigureBrowserAuth(browser)
	handler := api.Handler()
	for _, headers := range [][]string{{"Bearer invalid"}, {"Basic value"}, {"Bearer valid", "Bearer valid"}} {
		request := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
		for _, header := range headers {
			request.Header.Add("Authorization", header)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("headers %q: status = %d, want 401", headers, response.Code)
		}
	}
	if browser.called != 0 {
		t.Fatalf("browser authentication was called %d times", browser.called)
	}
}

func TestAPIDocumentationBrowserAuthenticationFailure(t *testing.T) {
	browser := &documentationBrowserAuth{status: http.StatusUnauthorized, err: errors.New("invalid session")}
	api := NewAPI(nil, routeTestVerifier{}, nil)
	api.ConfigureBrowserAuth(browser)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/docs/", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

func TestDocumentationLauncherAndAssetsArePublic(t *testing.T) {
	handler := NewAPI(nil, nil, nil).Handler()
	for _, path := range []string{"/docs/access", "/docs/swagger-ui.css", "/docs/swagger-ui-bundle.js"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || response.Body.Len() == 0 {
			t.Fatalf("%s: status = %d, bytes = %d", path, response.Code, response.Body.Len())
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/docs/access", nil))
	body := response.Body.String()
	if !strings.Contains(body, "kept only in this page's memory") {
		t.Fatal("launcher does not explain in-memory token handling")
	}
	if strings.Contains(body, "localStorage") || strings.Contains(body, "sessionStorage") {
		t.Fatal("launcher must not use browser token storage")
	}
}
