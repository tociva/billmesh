package httpresponse

import (
	"encoding/json"
	"net/http"
)

// JSON writes a JSON response with the supplied status code.
func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// Error writes the standard API error envelope.
func Error(w http.ResponseWriter, status int, message string) {
	code := map[int]string{
		http.StatusBadRequest:           "invalid_request",
		http.StatusUnauthorized:         "unauthenticated",
		http.StatusForbidden:            "forbidden",
		http.StatusNotFound:             "not_found",
		http.StatusMethodNotAllowed:     "method_not_allowed",
		http.StatusConflict:             "conflict",
		http.StatusPreconditionFailed:   "stale_revision",
		http.StatusPreconditionRequired: "precondition_required",
		http.StatusTooManyRequests:      "rate_limited",
		http.StatusInternalServerError:  "internal_error",
		http.StatusBadGateway:           "dependency_invalid_response",
		http.StatusServiceUnavailable:   "dependency_unavailable",
	}[status]
	if code == "" {
		code = "request_failed"
	}
	JSON(w, status, map[string]string{
		"error":      message,
		"code":       code,
		"request_id": w.Header().Get("X-Request-ID"),
	})
}

// NoContent completes a request without writing a response body.
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// Redirect writes a bodyless redirect response.
func Redirect(w http.ResponseWriter, location string, status int) {
	w.Header().Set("Location", location)
	w.WriteHeader(status)
}

// JSONFallbacks preserves registered handlers while replacing ServeMux's
// plain-text not-found and method-not-allowed responses with JSON errors.
func JSONFallbacks(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler, pattern := mux.Handler(r)
		if pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		probe := &statusProbe{header: make(http.Header)}
		handler.ServeHTTP(probe, r)
		if allow := probe.header.Get("Allow"); allow != "" {
			w.Header().Set("Allow", allow)
		}
		if probe.status == http.StatusMethodNotAllowed {
			Error(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		Error(w, http.StatusNotFound, "not found")
	})
}

type statusProbe struct {
	header http.Header
	status int
}

func (p *statusProbe) Header() http.Header { return p.header }

func (p *statusProbe) WriteHeader(status int) {
	if p.status == 0 {
		p.status = status
	}
}

func (p *statusProbe) Write(body []byte) (int, error) {
	if p.status == 0 {
		p.status = http.StatusOK
	}
	return len(body), nil
}
