//go:build e2e

package security_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

type securityProduct struct {
	ID      string `json:"id"`
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Version int64  `json:"version"`
}

func TestCATSEC001Through006CataloguePermissionMatrixAndNoSideEffects(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("catalogue-security")
	admin := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), map[string]any{"sub": "user:catalogue-admin"})
	product := testkit.Decode[securityProduct](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/products", map[string]any{
		"slug": testkit.Unique("secured-product"), "name": "Secured Product",
	}, admin))

	databaseURL := os.Getenv("BILLMESH_E2E_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("BILLMESH_E2E_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var auditsBefore, successfulUpdatesBefore int64
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM billmesh.audit_log WHERE resource_type='product' AND action='product.update.denied'`).Scan(&auditsBefore); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM billmesh.audit_log WHERE resource_type='product' AND action='product.update' AND resource_id=$1`, product.ID).Scan(&successfulUpdatesBefore); err != nil {
		t.Fatal(err)
	}

	h.RequireStatus(t, http.StatusUnauthorized, http.MethodGet, "/v1/admin/products", nil, "")
	roles := []struct {
		name        string
		permissions []string
		subject     string
	}{
		{"reader", []string{"billing:read"}, "user:reader"},
		{"writer", []string{"billing:write"}, "user:writer"},
		{"runtime", []string{"credits:reserve", "credits:settle"}, "service:runtime"},
		{"service-admin", []string{"billing:admin"}, "service:catalogue"},
	}
	for _, role := range roles {
		t.Run(role.name, func(t *testing.T) {
			token := h.IssueToken(t, org, "daybook", role.permissions, map[string]any{"sub": role.subject})
			h.RequireStatus(t, http.StatusForbidden, http.MethodPatch, "/v1/admin/products/"+product.ID, map[string]any{
				"name": "Unauthorized Name", "version": product.Version,
			}, token)
		})
	}

	current := testkit.Decode[securityProduct](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/products/"+product.ID, nil, admin))
	if current.Name != product.Name || current.Version != product.Version {
		t.Fatalf("CATSEC-005: denied requests changed product: before=%+v after=%+v", product, current)
	}
	var auditsAfter int64
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM billmesh.audit_log WHERE resource_type='product' AND action='product.update.denied'`).Scan(&auditsAfter); err != nil {
		t.Fatal(err)
	}
	if auditsAfter-auditsBefore != int64(len(roles)) {
		t.Fatalf("CATSEC-006: want %d denied audits, got %d", len(roles), auditsAfter-auditsBefore)
	}
	var successfulUpdatesAfter int64
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM billmesh.audit_log WHERE resource_type='product' AND action='product.update' AND resource_id=$1`, product.ID).Scan(&successfulUpdatesAfter); err != nil {
		t.Fatal(err)
	}
	if successfulUpdatesAfter != successfulUpdatesBefore {
		t.Fatalf("PRD-022: denied mutations wrote a successful audit: %d -> %d", successfulUpdatesBefore, successfulUpdatesAfter)
	}
}

func TestCATSEC007Through013TenantAndProductIsolation(t *testing.T) {
	h := testkit.NewHTTP(t)
	ownerOrg := testkit.Unique("catalogue-owner")
	attackerOrg := testkit.Unique("catalogue-attacker")
	owner := h.IssueToken(t, ownerOrg, "daybook", testkit.AllPermissions(), nil)
	attacker := h.IssueToken(t, attackerOrg, "daybook", testkit.AllPermissions(), nil)
	adminOnlyAttacker := h.IssueToken(t, attackerOrg, "daybook", []string{"billing:admin"}, nil)
	account := testkit.CreateFixtureAccount(t, h, ownerOrg, owner)
	testkit.CreateFixtureAccount(t, h, attackerOrg, attacker)
	subscription := testkit.CreateFixtureSubscription(t, h, account, owner)
	wallet := testkit.CreateFundedWallet(t, h, account, owner)

	cases := []struct {
		method, path string
		body         any
		token        string
	}{
		{http.MethodGet, "/v1/accounts/" + account, nil, attacker},
		{http.MethodPatch, "/v1/accounts/" + account, map[string]any{"name": "Stolen"}, attacker},
		{http.MethodPost, "/v1/subscriptions", map[string]any{"account_id": account, "product": "daybook", "plan": "daybook-free"}, attacker},
		{http.MethodPost, "/v1/subscriptions/" + subscription + "/cancel", map[string]any{"immediate": true}, attacker},
		{http.MethodPost, "/v1/subscriptions/" + subscription + "/cancel", map[string]any{"immediate": true}, adminOnlyAttacker},
		{http.MethodGet, "/v1/wallets/" + wallet, nil, attacker},
		{http.MethodGet, "/v1/wallets/" + wallet + "/ledger", nil, attacker},
	}
	for _, tc := range cases {
		status, raw, _ := h.JSON(t, tc.method, tc.path, tc.body, tc.token)
		if status != http.StatusForbidden && status != http.StatusNotFound {
			t.Fatalf("CATSEC tenant isolation: %s %s returned %d: %s", tc.method, tc.path, status, raw)
		}
	}

	daybookID := testkit.FixtureProduct(t, h, "daybook", owner)
	taskmeshID := testkit.FixtureProduct(t, h, "taskmesh", owner)
	h.RequireStatus(t, http.StatusBadRequest, http.MethodPost, "/v1/admin/products/"+taskmeshID+"/plans", map[string]any{
		"product_id": daybookID, "slug": testkit.Unique("mismatch-plan"), "name": "Mismatch", "currency": "INR",
	}, owner)
	taskmeshPlan := testkit.FixturePlan(t, h, "taskmesh", "paid", owner)
	daybookOnly := h.IssueToken(t, ownerOrg, "daybook", []string{"billing:read", "billing:write"}, nil)
	h.RequireStatusWithHeaders(t, http.StatusForbidden, http.MethodPost, "/v1/subscription-transitions", map[string]any{
		"plan_id": taskmeshPlan,
	}, daybookOnly, http.Header{"Idempotency-Key": []string{testkit.Unique("cross-product")}})
	_ = subscription
}

func TestCATSEC019CatalogueRejectsMassAssignmentDuplicateKeysAndUnsupportedMedia(t *testing.T) {
	h := testkit.NewHTTP(t)
	admin := h.IssueToken(t, testkit.Unique("catalogue-input"), "daybook", testkit.AllPermissions(), nil)
	h.RequireStatus(t, http.StatusBadRequest, http.MethodPost, "/v1/admin/products", map[string]any{
		"slug": testkit.Unique("mass-assignment"), "name": "Mass Assignment", "version": 999,
	}, admin)

	request, err := http.NewRequest(http.MethodPost, h.BaseURL+"/v1/admin/products", bytes.NewBufferString(`{"slug":"duplicate-key","slug":"other-key","name":"Duplicate"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+admin)
	request.Header.Set("Content-Type", "application/json")
	response, err := h.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate JSON keys: want 400, got %d", response.StatusCode)
	}

	request, err = http.NewRequest(http.MethodPost, h.BaseURL+"/v1/admin/products", bytes.NewBufferString(`slug=wrong-media`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+admin)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err = h.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("unsupported media: want 415, got %d", response.StatusCode)
	}
}

func TestCATSEC021CatalogueErrorsDoNotLeakDatabaseDetails(t *testing.T) {
	h := testkit.NewHTTP(t)
	admin := h.IssueToken(t, testkit.Unique("catalogue-errors"), "daybook", testkit.AllPermissions(), nil)
	product := testkit.Decode[securityProduct](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/products", map[string]any{
		"slug": testkit.Unique("redacted-product"), "name": "Redacted Product",
	}, admin))

	for _, request := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/v1/admin/products", map[string]any{"slug": product.Slug, "name": "Duplicate"}},
		{http.MethodPost, "/v1/admin/products/00000000-0000-0000-0000-000000000001/plans", map[string]any{"slug": "missing-parent", "name": "Missing", "currency": "INR"}},
		{http.MethodGet, "/v1/admin/products/00000000-0000-0000-0000-000000000001", nil},
	} {
		status, raw, _ := h.JSON(t, request.method, request.path, request.body, admin)
		if status < 400 || status >= 500 {
			t.Fatalf("expected redacted client error for %s %s, got %d: %s", request.method, request.path, status, raw)
		}
		message := strings.ToLower(string(raw))
		for _, leaked := range []string{"postgres", "sqlstate", "duplicate key", "violates foreign key", "billmesh."} {
			if strings.Contains(message, leaked) {
				t.Fatalf("CATSEC-021 leaked %q in response: %s", leaked, raw)
			}
		}
	}
}
