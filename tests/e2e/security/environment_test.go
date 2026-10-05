//go:build e2e

package security_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tociva/billmesh/tests/testkit"
)

func TestSecurityEnvironmentDefaultAndExplicitBridge(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("environment")
	staging := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, map[string]any{"environment": "staging"})
	stagingAccount := testkit.CreateFixtureAccount(t, h, org, staging)
	defaultEnvironment := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, nil)
	h.RequireStatus(t, http.StatusNotFound, http.MethodGet, "/v1/accounts/current", nil, defaultEnvironment)
	productionAccount := testkit.CreateFixtureAccount(t, h, org, defaultEnvironment)
	productionCurrent := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/accounts/current", nil, defaultEnvironment)
	if !strings.Contains(string(productionCurrent), productionAccount) || strings.Contains(string(productionCurrent), stagingAccount) {
		t.Fatalf("production current account crossed environment boundary: %s", productionCurrent)
	}
	stagingCurrent := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/accounts/current", nil, staging)
	if !strings.Contains(string(stagingCurrent), stagingAccount) || strings.Contains(string(stagingCurrent), productionAccount) {
		t.Fatalf("staging current account crossed environment boundary: %s", stagingCurrent)
	}
	bridgeOrg := testkit.Unique("explicit-bridge")
	bridge := map[string]any{"application": "taskmesh", "organization_id": bridgeOrg, "environment": "production"}
	h.RequireStatus(t, http.StatusNotFound, http.MethodPost, "/v1/accounts/"+stagingAccount+"/links", bridge, staging)
	trustedLinker := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write", "billing:link"}, map[string]any{"environment": "staging"})
	h.RequireStatus(t, http.StatusNotFound, http.MethodPost, "/v1/accounts/"+stagingAccount+"/links", bridge, trustedLinker)
	bridgeReader := h.IssueToken(t, bridgeOrg, "taskmesh", []string{"billing:read"}, map[string]any{"environment": "production"})
	h.RequireStatus(t, http.StatusNotFound, http.MethodGet, "/v1/accounts/current", nil, bridgeReader)
}
