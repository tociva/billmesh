package app

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityResponsesAndLogsDoNotEchoCredentials(t *testing.T) {
	secret := "postgres://billmesh:private-password@database.internal/billmesh"
	recorder := httptest.NewRecorder()
	writeDBError(recorder, errors.New(secret))
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), secret) {
		t.Fatalf("GAP-SEC-008: database error exposed a credential: %d %s", recorder.Code, recorder.Body.String())
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	handler := NewAPI(nil, routeTestVerifier{}, logger).Handler()
	req := httptest.NewRequest(http.MethodGet, "/v1/products", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized || strings.Contains(recorder.Body.String(), secret) || strings.Contains(logs.String(), secret) {
		t.Fatalf("GAP-SEC-008: authentication response or log exposed a credential: %d %s %s", recorder.Code, recorder.Body.String(), logs.String())
	}
}
