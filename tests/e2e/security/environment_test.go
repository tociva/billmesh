//go:build e2e

package security_test

import (
	"net/http"
	"testing"

	"github.com/tociva/billmesh/tests/testkit"
)

func TestSecurityEnvironmentDefaultAndExplicitBridge(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("environment")
	staging := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, map[string]any{"environment": "staging"})
	stagingAccount := testkit.CreateFixtureAccount(t, h, org, staging)
	defaultEnvironment := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, nil)
	h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/accounts/"+stagingAccount, nil, defaultEnvironment)
	productionAccount := testkit.CreateFixtureAccount(t, h, org, defaultEnvironment)
	h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/accounts/"+productionAccount, nil, defaultEnvironment)
	h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/accounts/"+productionAccount, nil, staging)
	bridgeOrg := testkit.Unique("explicit-bridge")
	bridge := map[string]any{"application": "taskmesh", "organization_id": bridgeOrg, "environment": "production"}
	h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/accounts/"+stagingAccount+"/links", bridge, staging)
	trustedLinker := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write", "billing:link"}, map[string]any{"environment": "staging"})
	h.RequireStatus(t, http.StatusNoContent, http.MethodPost, "/v1/accounts/"+stagingAccount+"/links", bridge, trustedLinker)
	bridgeReader := h.IssueToken(t, bridgeOrg, "taskmesh", []string{"billing:read"}, map[string]any{"environment": "production"})
	h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/accounts/"+stagingAccount, nil, bridgeReader)
}
