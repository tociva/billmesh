//go:build e2e

package security_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestSecurityMutationPermissionMatrixAndSideEffects(t *testing.T) {
	h := testkit.NewHTTP(t)
	resetMockFailure(t, h)
	org := testkit.Unique("mutation-matrix")
	admin := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	account := testkit.CreateFixtureAccount(t, h, org, admin)
	wallet := testkit.CreateFundedWallet(t, h, account, admin)
	product := productID(t, h, "daybook", admin)
	planSlug := testkit.Unique("matrix-plan")
	packSlug := testkit.Unique("matrix-pack")
	accountRef := testkit.Unique("matrix-account")
	taskmeshSubRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/subscriptions", map[string]any{"account_id": account, "product": "taskmesh", "plan": "professional", "payment_status": "verified"}, admin)
	taskmeshSub := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, taskmeshSubRaw).ID
	installationRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts/"+account+"/installations", map[string]any{"application": "daybook", "organization_id": org}, admin)
	installation := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, installationRaw).ID
	operations := []struct {
		name, method, path string
		body               any
		success            int
	}{
		{"account", http.MethodPost, "/v1/accounts", map[string]any{"name": "Authorized", "external_ref": accountRef, "application": "daybook", "organization_id": org}, http.StatusCreated},
		{"plan", http.MethodPost, "/v1/plans", map[string]any{"product_id": product, "slug": planSlug, "name": "Matrix Plan", "price_minor": 100, "currency": "INR", "included_credits": 10, "billing_interval": "monthly"}, http.StatusCreated},
		{"credit pack", http.MethodPost, "/v1/credit-packs", map[string]any{"product_id": product, "slug": packSlug, "name": "Matrix Pack", "credits": 10, "price_minor": 100, "currency": "INR"}, http.StatusCreated},
		{"subscription", http.MethodPost, "/v1/subscriptions", map[string]any{"account_id": account, "product": "daybook", "plan": "daybook-free"}, http.StatusCreated},
		{"grant", http.MethodPost, "/v1/wallets/" + wallet + "/grants", map[string]any{"source": "test", "operation_ref": testkit.Unique("matrix-grant"), "amount": 1}, http.StatusOK},
		{"adjustment", http.MethodPost, "/v1/admin/adjustments", map[string]any{"wallet_id": wallet, "amount": 1, "reason": "matrix"}, http.StatusCreated},
		{"installation", http.MethodPost, "/v1/accounts/" + account + "/installations", map[string]any{"application": "taskmesh", "organization_id": org}, http.StatusCreated},
		{"webhook", http.MethodPost, "/v1/webhooks", map[string]any{"application": "daybook", "target_url": h.MockURL + "/receivers/matrix", "secret": "matrix-secret"}, http.StatusCreated},
		{"payment order", http.MethodPost, "/v1/payments/orders", map[string]any{"account_id": account, "credit_pack": "credits-500"}, http.StatusCreated},
		{"subscription cancel", http.MethodPost, "/v1/subscriptions/" + taskmeshSub + "/cancel", map[string]any{"immediate": true}, http.StatusOK},
		{"installation revoke", http.MethodPost, "/v1/installations/" + installation + "/revoke", nil, http.StatusNoContent},
	}
	url := os.Getenv("BILLMESH_E2E_DATABASE_URL")
	if url == "" {
		t.Fatal("BILLMESH_E2E_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	providerCalls := func() int64 {
		t.Helper()
		resp, err := h.Client.Get(h.MockURL + "/test/order-count")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result struct {
			Count int64 `json:"count"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result.Count
	}
	snapshot := func() [15]int64 {
		t.Helper()
		var counts [15]int64
		queries := []string{
			`SELECT count(*) FROM billing_accounts WHERE external_ref=$1`,
			`SELECT count(*) FROM plans WHERE slug=$1`,
			`SELECT count(*) FROM credit_packs WHERE slug=$1`,
			`SELECT count(*) FROM subscriptions WHERE account_id=$1`,
			`SELECT COALESCE(sum(version),0) FROM subscriptions WHERE account_id=$1`,
			`SELECT count(*) FROM credit_ledger WHERE wallet_id=$1`,
			`SELECT available FROM wallets WHERE id=$1`,
			`SELECT reserved FROM wallets WHERE id=$1`,
			`SELECT count(*) FROM credit_grants WHERE wallet_id=$1`,
			`SELECT count(*) FROM webhook_endpoints WHERE account_id=$1`,
			`SELECT count(*) FROM application_installations WHERE account_id=$1 AND active`,
			`SELECT count(*) FROM payments WHERE account_id=$1`,
			`SELECT count(*) FROM provider_events`,
			`SELECT count(*) FROM outbox_events e WHERE (e.aggregate_type='account' AND e.aggregate_id=$1) OR (e.aggregate_type='wallet' AND e.aggregate_id IN (SELECT id FROM wallets WHERE account_id=$1)) OR (e.aggregate_type='subscription' AND e.aggregate_id IN (SELECT id FROM subscriptions WHERE account_id=$1))`,
			`SELECT count(*) FROM audit_log WHERE account_id=$1`,
		}
		args := []any{accountRef, planSlug, packSlug, account, account, wallet, wallet, wallet, wallet, account, account, account, nil, account, account}
		for i, query := range queries {
			var err error
			if args[i] == nil {
				err = pool.QueryRow(context.Background(), query).Scan(&counts[i])
			} else {
				err = pool.QueryRow(context.Background(), query, args[i]).Scan(&counts[i])
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		return counts
	}
	before := snapshot()
	providerBefore := providerCalls()
	roles := []struct {
		name        string
		permissions []string
		subject     string
	}{
		{"viewer", []string{"billing:read"}, "viewer"},
		{"runtime", []string{"credits:reserve", "credits:settle"}, "service:runtime"},
		{"service", []string{"billing:read"}, "service:worker"},
		{"empty", nil, "user:empty"},
		{"unknown", []string{"billing:superuser"}, "user:unknown"},
	}
	for _, role := range roles {
		roleToken := h.IssueToken(t, org, "daybook", role.permissions, map[string]any{"sub": role.subject})
		for _, operation := range operations {
			t.Run(role.name+"/"+operation.name, func(t *testing.T) {
				h.RequireStatus(t, http.StatusForbidden, operation.method, operation.path, operation.body, roleToken)
			})
		}
	}
	after := snapshot()
	for i := 0; i < len(before)-1; i++ {
		if after[i] != before[i] {
			t.Fatalf("GAP-AUTHZ-009: rejected mutations changed business state at index %d: before=%v after=%v", i, before, after)
		}
	}
	if want := before[len(before)-1] + int64(len(roles)*3); after[len(after)-1] != want {
		t.Fatalf("GAP-SEC-009: want %d security audits after denied plan, pack, and adjustment requests; got %d", want, after[len(after)-1])
	}
	if after := providerCalls(); after != providerBefore {
		t.Fatalf("GAP-AUTHZ-009: rejected mutations called payment provider: %d->%d", providerBefore, after)
	}
	for _, operation := range operations {
		t.Run("allowed/"+operation.name, func(t *testing.T) {
			allowedToken := admin
			allowedBody := operation.body
			if operation.name == "account" {
				newOrg := testkit.Unique("matrix-account-owner")
				allowedToken = h.IssueToken(t, newOrg, "daybook", testkit.AllPermissions(), nil)
				allowedBody = map[string]any{"name": "Authorized", "external_ref": accountRef, "application": "daybook", "organization_id": newOrg}
			}
			h.RequireStatus(t, operation.success, operation.method, operation.path, allowedBody, allowedToken)
		})
	}
}
