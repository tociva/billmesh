//go:build e2e

package journeys_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

type catalogueProduct struct {
	ID                       string `json:"id"`
	Slug                     string `json:"slug"`
	Version                  int64  `json:"version"`
	EntitlementSchemaVersion int64  `json:"entitlement_schema_version"`
}

type cataloguePlan struct {
	ID      string `json:"id"`
	Slug    string `json:"slug"`
	Version int64  `json:"version"`
}

func TestJOURNEYPRD001Through004CatalogueLifecycleAndSubscriptionSnapshots(t *testing.T) {
	h := testkit.NewHTTP(t)
	admin := h.IssueToken(t, testkit.Unique("catalogue-admin"), "daybook", testkit.AllPermissions(), nil)
	product := testkit.Decode[catalogueProduct](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/products", map[string]any{
		"slug": testkit.Unique("journey-product"), "name": "Journey Product", "description": "Lifecycle coverage",
		"entitlement_schema": map[string]any{"fields": []any{
			map[string]any{"key": "reports", "label": "Reports", "type": "boolean", "required": true},
			map[string]any{"key": "members", "label": "Maximum members", "type": "integer", "required": true, "minimum": 0},
		}},
	}, admin))
	plan := testkit.Decode[cataloguePlan](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/products/"+product.ID+"/plans", map[string]any{
		"slug": testkit.Unique("journey-plan"), "name": "Journey Plan", "currency": "INR", "billing_interval": "monthly",
		"price_minor": 0, "included_credits": 100, "entitlements": map[string]any{"reports": true, "members": 5},
		"entitlement_schema_version": product.EntitlementSchemaVersion,
	}, admin))

	firstOrg := testkit.Unique("journey-first")
	first := h.IssueToken(t, firstOrg, product.Slug, []string{"billing:read", "billing:write"}, nil)
	firstAccount := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{
		"name": "First Customer", "application": product.Slug, "organization_id": firstOrg,
	}, first)).ID
	firstSubscription := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/subscriptions", map[string]any{
		"account_id": firstAccount, "plan_id": plan.ID,
	}, first)).ID

	h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/plans/"+plan.ID, map[string]any{
		"price_minor": 2500, "included_credits": 250,
		"entitlements": map[string]any{"reports": false, "members": 10}, "version": plan.Version,
	}, admin)

	databaseURL := os.Getenv("BILLMESH_E2E_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("BILLMESH_E2E_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var price, credits int64
	var reports bool
	if err := pool.QueryRow(context.Background(), `SELECT price_minor,included_credits,COALESCE((entitlements->>'reports')::boolean,false)
		FROM billmesh.subscriptions WHERE id=$1`, firstSubscription).Scan(&price, &credits, &reports); err != nil {
		t.Fatal(err)
	}
	if price != 0 || credits != 100 || !reports {
		t.Fatalf("JOURNEY-PRD-003: existing subscription snapshot changed: price=%d credits=%d reports=%v", price, credits, reports)
	}

	secondOrg := testkit.Unique("journey-second")
	second := h.IssueToken(t, secondOrg, product.Slug, []string{"billing:read", "billing:write"}, nil)
	secondAccount := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{
		"name": "Second Customer", "application": product.Slug, "organization_id": secondOrg,
	}, second)).ID
	secondSubscription := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/subscriptions", map[string]any{
		"account_id": secondAccount, "plan_id": plan.ID, "payment_status": "verified",
	}, second)).ID
	if err := pool.QueryRow(context.Background(), `SELECT price_minor,included_credits,COALESCE((entitlements->>'reports')::boolean,false)
		FROM billmesh.subscriptions WHERE id=$1`, secondSubscription).Scan(&price, &credits, &reports); err != nil {
		t.Fatal(err)
	}
	if price != 2500 || credits != 250 || reports {
		t.Fatalf("JOURNEY-PRD-003: new subscription did not receive updated snapshot: price=%d credits=%d reports=%v", price, credits, reports)
	}

	planDetail := testkit.Decode[cataloguePlan](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/plans/"+plan.ID, nil, admin))
	h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/plans/"+plan.ID, map[string]any{"active": false, "version": planDetail.Version}, admin)
	h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/subscriptions/current", nil, first)

	thirdOrg := testkit.Unique("journey-third")
	third := h.IssueToken(t, thirdOrg, product.Slug, []string{"billing:read", "billing:write"}, nil)
	thirdAccount := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{
		"name": "Third Customer", "application": product.Slug, "organization_id": thirdOrg,
	}, third)).ID
	h.RequireStatus(t, http.StatusConflict, http.MethodPost, "/v1/subscriptions", map[string]any{
		"account_id": thirdAccount, "plan_id": plan.ID, "payment_status": "verified",
	}, third)

	productDetail := testkit.Decode[catalogueProduct](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/products/"+product.ID, nil, admin))
	archivedProduct := testkit.Decode[catalogueProduct](t, h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/products/"+product.ID, map[string]any{"active": false, "version": productDetail.Version}, admin))
	publicProducts := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/products", nil, first)
	if strings.Contains(string(publicProducts), product.Slug) {
		t.Fatalf("JOURNEY-PRD-004: archived product remains public: %s", publicProducts)
	}
	h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/subscriptions/current", nil, first)
	h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/products/"+product.ID, map[string]any{"active": true, "version": archivedProduct.Version}, admin)
	publicProducts = h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/products", nil, first)
	if !strings.Contains(string(publicProducts), product.Slug) {
		t.Fatalf("JOURNEY-PRD-004: reactivated product did not return to catalogue: %s", publicProducts)
	}
}

func TestJOURNEYPRD005CustomersCannotAccessEachOthersSubscriptions(t *testing.T) {
	h := testkit.NewHTTP(t)
	firstOrg := testkit.Unique("isolation-first")
	secondOrg := testkit.Unique("isolation-second")
	first := h.IssueToken(t, firstOrg, "daybook", testkit.AllPermissions(), nil)
	second := h.IssueToken(t, secondOrg, "daybook", testkit.AllPermissions(), nil)
	firstAccount := testkit.CreateFixtureAccount(t, h, firstOrg, first)
	testkit.CreateFixtureAccount(t, h, secondOrg, second)
	firstSubscription := testkit.CreateFixtureSubscription(t, h, firstAccount, first)

	for _, request := range []struct {
		path string
		body any
	}{
		{"/v1/subscriptions/" + firstSubscription + "/change-plan", map[string]any{"plan": "daybook-free"}},
		{"/v1/subscriptions/" + firstSubscription + "/cancel", map[string]any{"immediate": true}},
		{"/v1/subscriptions/" + firstSubscription + "/renew", map[string]any{"operation_ref": testkit.Unique("foreign-renew")}},
		{"/v1/subscriptions/" + firstSubscription + "/reactivate", nil},
	} {
		status, raw, _ := h.JSON(t, http.MethodPost, request.path, request.body, second)
		if status != http.StatusForbidden && status != http.StatusNotFound {
			t.Fatalf("JOURNEY-PRD-005: foreign subscription request got %d: %s", status, raw)
		}
	}
}
