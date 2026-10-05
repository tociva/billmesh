//go:build e2e

package security_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tociva/billmesh/tests/testkit"
)

func TestSecurityRemainingProtectedRoutesHaveOwningClientSuccess(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("route-positive")
	token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	account := testkit.CreateFixtureAccount(t, h, org, token)
	wallet := testkit.CreateFundedWallet(t, h, account, token)
	testkit.CreateFixtureSubscription(t, h, account, token)

	h.RequireStatus(t, http.StatusNoContent, http.MethodGet, "/v1/entitlements/check?feature=workflow_execution", nil, token)
	ledger := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/wallets/"+wallet+"/ledger", nil, token)
	if !strings.Contains(string(ledger), "grant") {
		t.Fatalf("owning client's wallet ledger omitted its grant: %s", ledger)
	}
	reservation := testkit.ReserveFixture(t, h, wallet, token)
	extended := h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/reservations/"+reservation+"/extend", map[string]any{"ttl_seconds": 60}, token)
	if !strings.Contains(string(extended), reservation) {
		t.Fatalf("reservation extension returned the wrong resource: %s", extended)
	}
	released := h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/reservations/"+reservation+"/release", nil, token)
	if !strings.Contains(string(released), reservation) {
		t.Fatalf("reservation release returned the wrong resource: %s", released)
	}

	snapshot := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/billing-snapshot", nil, token)
	if !strings.Contains(string(snapshot), `"status":"active"`) {
		t.Fatalf("billing snapshot omitted the active subscription: %s", snapshot)
	}
}
