package app

import (
	"net/http"
	"strings"

	"github.com/tociva/billmesh/internal/auth"
)

const documentationAuthError = "a valid browser session or bearer token is required to view API documentation"

func (a *API) documentationGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Add("Vary", "Authorization")
		w.Header().Add("Vary", "Cookie")
		if a.publicOpenAPI {
			next.ServeHTTP(w, r)
			return
		}

		// The deliberate presence of Authorization selects bearer authentication.
		// Never fall back to a browser cookie when that credential is malformed or
		// invalid, as doing so can mask a bad credential supplied by a caller.
		if len(r.Header.Values("Authorization")) > 0 {
			values := r.Header.Values("Authorization")
			if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || strings.TrimPrefix(values[0], "Bearer ") == "" || a.auth == nil {
				a.rejectDocumentationRequest(w, r)
				return
			}
			claims, err := a.auth.Verify(r.Context(), strings.TrimPrefix(values[0], "Bearer "))
			if err != nil {
				a.rejectDocumentationRequest(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), claims)))
			return
		}

		if a.browser != nil {
			claims, status, err := a.browser.Authenticate(r)
			if err == nil {
				next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), claims)))
				return
			}
			if status == http.StatusServiceUnavailable {
				writeError(w, status, "browser authentication unavailable")
				return
			}
		}
		a.rejectDocumentationRequest(w, r)
	})
}

func (a *API) rejectDocumentationRequest(w http.ResponseWriter, r *http.Request) {
	if a.authFailures != nil && a.authFailures.record(remoteHost(r.RemoteAddr)) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many authentication failures")
		return
	}
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeError(w, http.StatusUnauthorized, documentationAuthError)
}
