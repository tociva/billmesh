package bff

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/tociva/billmesh/internal/auth"
)

// Router exposes realm-specific OAuth endpoints and selects the matching BFF
// session for cross-origin browser API requests by their exact Origin header.
type Router struct {
	managers []*Manager
	byOrigin map[string]*Manager
}

func NewRouter(managers ...*Manager) (*Router, error) {
	router := &Router{managers: managers, byOrigin: make(map[string]*Manager, len(managers))}
	seenRealms := make(map[string]bool, len(managers))
	callbackOrigin := ""
	for _, manager := range managers {
		if manager == nil {
			return nil, errors.New("BFF router requires non-nil managers")
		}
		if seenRealms[manager.config.Realm] {
			return nil, errors.New("BFF router realms must be unique")
		}
		if _, exists := router.byOrigin[manager.AppOrigin()]; exists {
			return nil, errors.New("BFF application origins must be unique")
		}
		seenRealms[manager.config.Realm] = true
		router.byOrigin[manager.AppOrigin()] = manager
		redirect, err := url.Parse(manager.config.RedirectURI)
		if err != nil {
			return nil, err
		}
		origin := redirect.Scheme + "://" + redirect.Host
		if callbackOrigin == "" {
			callbackOrigin = origin
		} else if callbackOrigin != origin {
			return nil, errors.New("BFF realms must use the same API callback origin")
		}
	}
	if len(router.managers) == 0 {
		return nil, errors.New("BFF router requires at least one manager")
	}
	return router, nil
}

func (r *Router) AuthHandler() http.Handler {
	mux := http.NewServeMux()
	for _, manager := range r.managers {
		mux.Handle(manager.AuthBasePath()+"/", manager.AuthHandler())
	}
	return mux
}

func (r *Router) managerForRequest(request *http.Request) *Manager {
	origins := request.Header.Values("Origin")
	if len(origins) != 1 {
		return nil
	}
	return r.byOrigin[origins[0]]
}

func (r *Router) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		manager := r.managerForRequest(request)
		if manager == nil {
			http.Error(w, "origin is not allowed", http.StatusForbidden)
			return
		}
		manager.Middleware(next).ServeHTTP(w, request)
	})
}

func (r *Router) Authenticate(request *http.Request) (*auth.Claims, int, error) {
	manager := r.managerForRequest(request)
	if manager == nil {
		return nil, http.StatusForbidden, errors.New("origin is not allowed")
	}
	return manager.Authenticate(request)
}

// CORS permits credentialed browser traffic only from the configured Console
// and Admin origins. Authentication endpoints still accept top-level OAuth
// navigations, which normally carry no Origin header.
func (r *Router) CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		_, allowed := r.byOrigin[origin]
		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
		}
		if request.Method == http.MethodOptions && strings.HasPrefix(request.URL.Path, "/api/v1/") {
			w.Header().Add("Vary", "Access-Control-Request-Method")
			w.Header().Add("Vary", "Access-Control-Request-Headers")
			if !allowed {
				http.Error(w, "origin is not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, X-CSRF-Token")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, request)
	})
}
