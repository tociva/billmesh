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
	subscription := testkit.CreateFixtureSubscription(t, h, account, token)

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

	renewed := h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/subscriptions/"+subscription+"/renew", map[string]any{"operation_ref": testkit.Unique("route-renew")}, token)
	if !strings.Contains(string(renewed), subscription) {
		t.Fatalf("subscription renewal returned the wrong resource: %s", renewed)
	}
	changed := h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/subscriptions/"+subscription+"/change-plan", map[string]any{"plan": "daybook-paid", "payment_status": "verified"}, token)
	if !strings.Contains(string(changed), subscription) {
		t.Fatalf("subscription plan change returned the wrong resource: %s", changed)
	}
	h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/subscriptions/"+subscription+"/cancel", map[string]any{"immediate": true}, token)
	reactivated := h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/subscriptions/"+subscription+"/reactivate", nil, token)
	if !strings.Contains(string(reactivated), `"status":"active"`) {
		t.Fatalf("reactivation did not restore active status: %s", reactivated)
	}
}
