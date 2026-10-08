//go:build e2e

package security_test

import (
	"net/http"
	"testing"

	"github.com/tociva/billmesh/tests/testkit"
)

func TestOAuthClientsCannotCrossAPISurfaces(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("client-profile")
	tokens := map[string]string{
		"catalogue": h.IssueToken(t, "", "daybook", []string{"catalogue:read"}, nil),
		"billing":   h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, nil),
		"runtime":   h.IssueToken(t, org, "daybook", []string{"credits:reserve", "credits:settle"}, map[string]any{"sub": "service:runtime"}),
	}
	endpoints := []struct {
		name, method, path, allowed string
		body                        any
	}{
		{"catalogue", http.MethodGet, "/v1/catalog", "catalogue", nil},
		{"billing", http.MethodGet, "/v1/accounts/current", "billing", nil},
		{"runtime", http.MethodPost, "/v1/executions/authorize", "runtime", map[string]any{"execution_id": "not-reached", "credits": 1}},
		{"admin", http.MethodGet, "/v1/admin/audit", "admin", nil},
	}
	for clientName, token := range tokens {
		for _, endpoint := range endpoints {
			if clientName == endpoint.allowed {
				continue
			}
			t.Run(clientName+" cannot call "+endpoint.name, func(t *testing.T) {
				h.RequireStatus(t, http.StatusForbidden, endpoint.method, endpoint.path, endpoint.body, token)
			})
		}
	}
}

func TestOAuthClientIdentityIsBoundToProductAndEnvironment(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("client-binding")
	for name, overrides := range map[string]map[string]any{
		"unknown client":   {"client_id": "unknown-client"},
		"missing client":   {"omit_claims": []string{"client_id"}},
		"wrong product":    {"client_id": "taskmesh-billing-test", "authorizer_client_id": "daybook-billmesh-authorizer-test"},
		"wrong authorizer": {"authorizer_client_id": "taskmesh-billmesh-authorizer-test"},
		"wrong environment": {
			"client_id": "daybook-billing-test",
			"claim_overrides": map[string]any{
				"environment": "staging",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			token := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, overrides)
			h.RequireStatus(t, http.StatusUnauthorized, http.MethodGet, "/v1/accounts/current", nil, token)
		})
	}
}

func TestDelegatedClientProfileCannotExpandAcrossSurfaces(t *testing.T) {
	h := testkit.NewHTTP(t)
	token := h.IssueToken(t, testkit.Unique("profile-boundary"), "daybook", testkit.AllPermissions(), map[string]any{
		"client_id": "daybook-catalogue-test",
	})
	h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/accounts/current", nil, token)
	h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/executions/authorize", map[string]any{"execution_id": "not-reached", "credits": 1}, token)
	h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/admin/audit", nil, token)
}
