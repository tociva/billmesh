//go:build e2e

package security_test

import (
	"net/http"
	"testing"

	"github.com/tociva/billmesh/tests/testkit"
)

func TestSecurityForeignNestedResourceIdentifiers(t *testing.T) {
	h := testkit.NewHTTP(t)
	ownerOrg := testkit.Unique("resource-owner")
	owner := h.IssueToken(t, ownerOrg, "daybook", testkit.AllPermissions(), nil)
	account := testkit.CreateFixtureAccount(t, h, ownerOrg, owner)
	wallet := testkit.CreateFundedWallet(t, h, account, owner)
	subscription := testkit.CreateFixtureSubscription(t, h, account, owner)
	reservation := testkit.ReserveFixture(t, h, wallet, owner)
	installationRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts/"+account+"/installations", map[string]any{"application": "taskmesh", "organization_id": ownerOrg}, owner)
	installation := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, installationRaw).ID
	attackerOrg := testkit.Unique("resource-attacker")
	attacker := h.IssueToken(t, attackerOrg, "daybook", testkit.AllPermissions(), nil)
	testkit.CreateFixtureAccount(t, h, attackerOrg, attacker)
	cases := []struct {
		name, method, path string
		body               any
	}{
		{"account read", http.MethodGet, "/v1/accounts/" + account, nil},
		{"account update", http.MethodPatch, "/v1/accounts/" + account, map[string]any{"name": "stolen"}},
		{"account link", http.MethodPost, "/v1/accounts/" + account + "/links", map[string]any{"application": "daybook", "organization_id": attackerOrg}},
		{"wallet read", http.MethodGet, "/v1/wallets/" + wallet, nil},
		{"wallet ledger", http.MethodGet, "/v1/wallets/" + wallet + "/ledger", nil},
		{"wallet grant", http.MethodPost, "/v1/wallets/" + wallet + "/grants", map[string]any{"source": "attack", "operation_ref": testkit.Unique("grant"), "amount": 1}},
		{"wallet reserve", http.MethodPost, "/v1/wallets/" + wallet + "/reservations", map[string]any{"execution_id": testkit.Unique("execution"), "amount": 1}},
		{"reservation settle", http.MethodPost, "/v1/reservations/" + reservation + "/settle", map[string]any{"actual": 1}},
		{"reservation release", http.MethodPost, "/v1/reservations/" + reservation + "/release", nil},
		{"reservation extend", http.MethodPost, "/v1/reservations/" + reservation + "/extend", map[string]any{"ttl_seconds": 60}},
		{"subscription change", http.MethodPost, "/v1/subscriptions/" + subscription + "/change-plan", map[string]any{"plan": "daybook-pro"}},
		{"subscription cancel", http.MethodPost, "/v1/subscriptions/" + subscription + "/cancel", map[string]any{"immediate": true}},
		{"subscription renew", http.MethodPost, "/v1/subscriptions/" + subscription + "/renew", map[string]any{"operation_ref": testkit.Unique("renew")}},
		{"subscription reactivate", http.MethodPost, "/v1/subscriptions/" + subscription + "/reactivate", nil},
		{"installation revoke", http.MethodPost, "/v1/installations/" + installation + "/revoke", nil},
		{"installation settle", http.MethodPost, "/v1/installations/" + installation + "/settle-active", map[string]any{"actual": 1}},
		{"payment order body", http.MethodPost, "/v1/payments/orders", map[string]any{"account_id": account, "credit_pack": "credits-500"}},
		{"wallet create body", http.MethodPost, "/v1/wallets", map[string]any{"account_id": account, "product_id": productID(t, h, "daybook", attacker)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body, _ := h.JSON(t, tc.method, tc.path, tc.body, attacker)
			if status != http.StatusForbidden && status != http.StatusNotFound {
				t.Fatalf("GAP-AUTHZ-003: foreign resource request got %d: %s", status, body)
			}
		})
	}
	h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/accounts/"+account, nil, owner)
	h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/wallets/"+wallet, nil, owner)
}
