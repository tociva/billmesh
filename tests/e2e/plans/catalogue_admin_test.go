//go:build e2e

package plans_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

type productResponse struct {
	ID                       string         `json:"id"`
	Slug                     string         `json:"slug"`
	Name                     string         `json:"name"`
	Description              string         `json:"description"`
	EntitlementSchema        map[string]any `json:"entitlement_schema"`
	EntitlementSchemaVersion int64          `json:"entitlement_schema_version"`
	BillingPolicy            map[string]any `json:"billing_policy"`
	BillingPolicyVersion     int64          `json:"billing_policy_version"`
	Active                   bool           `json:"active"`
	Version                  int64          `json:"version"`
}

func TestProductBillingPolicyContractAndVersioning(t *testing.T) {
	h := testkit.NewHTTP(t)
	admin := h.IssueToken(t, testkit.Unique("policy-admin"), "daybook", testkit.AllPermissions(), nil)

	metadataRaw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/product-policy-metadata", nil, admin)
	metadata := testkit.Decode[struct {
		SchemaVersion int            `json:"schema_version"`
		Defaults      map[string]any `json:"defaults"`
		Options       map[string]any `json:"options"`
	}](t, metadataRaw)
	if metadata.SchemaVersion != 1 || len(metadata.Options) == 0 {
		t.Fatalf("incomplete policy metadata: %+v", metadata)
	}
	policy := metadata.Defaults
	policy["customer"].(map[string]any)["free_allowance"] = float64(2)
	policy["catalogue"].(map[string]any)["access"] = "public"
	policy["projection"].(map[string]any)["fresh_seconds"] = float64(120)

	product := testkit.Decode[productResponse](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/products", map[string]any{
		"slug": testkit.Unique("policy-product"), "name": "Policy product", "billing_policy": policy,
	}, admin))
	if product.BillingPolicyVersion != 1 || product.BillingPolicy["customer"].(map[string]any)["free_allowance"] != float64(2) {
		t.Fatalf("policy was not materialized: %+v", product)
	}
	h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/public/catalog?product="+product.Slug, nil, "")

	policy["projection"].(map[string]any)["fresh_seconds"] = float64(180)
	updated := testkit.Decode[productResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/products/"+product.ID, map[string]any{
		"version": product.Version, "billing_policy": policy,
	}, admin))
	if updated.BillingPolicyVersion != 2 {
		t.Fatalf("policy version = %d, want 2", updated.BillingPolicyVersion)
	}

	nameOnly := testkit.Decode[productResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/products/"+product.ID, map[string]any{
		"version": updated.Version, "name": "Renamed policy product",
	}, admin))
	if nameOnly.BillingPolicyVersion != updated.BillingPolicyVersion {
		t.Fatalf("non-policy edit changed policy version: %d -> %d", updated.BillingPolicyVersion, nameOnly.BillingPolicyVersion)
	}

	policy["customer"].(map[string]any)["free_allowance"] = float64(-1)
	h.RequireStatus(t, http.StatusBadRequest, http.MethodPatch, "/v1/admin/products/"+product.ID, map[string]any{
		"version": nameOnly.Version, "billing_policy": policy,
	}, admin)
}

func TestAutomaticDefaultPlanOnboardingUsesProductPolicy(t *testing.T) {
	h := testkit.NewHTTP(t)
	admin := h.IssueToken(t, testkit.Unique("onboarding-admin"), "daybook", testkit.AllPermissions(), nil)
	metadata := testkit.Decode[struct {
		Defaults map[string]any `json:"defaults"`
	}](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/product-policy-metadata", nil, admin))
	policy := metadata.Defaults
	policy["onboarding"].(map[string]any)["initial_plan"] = "automatic_default"
	policy["onboarding"].(map[string]any)["allow_without_subscription"] = false
	policy["onboarding"].(map[string]any)["ineligible_action"] = "reject"
	policy["catalogue"].(map[string]any)["required_before_account"] = true

	product := testkit.Decode[productResponse](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/products", map[string]any{
		"slug": testkit.Unique("automatic-product"), "name": "Automatic product", "billing_policy": policy,
	}, admin))
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/products/"+product.ID+"/plans", map[string]any{
		"slug": testkit.Unique("automatic-free"), "name": "Automatic Free", "billing_model": "free", "price_minor": 0,
		"currency": "INR", "included_credits": 10, "billing_interval": "monthly", "entitlements": map[string]any{},
		"entitlement_schema_version": product.EntitlementSchemaVersion, "default_for_product": true,
	}, admin)

	org := testkit.Unique("automatic-org")
	consumer := h.IssueToken(t, org, product.Slug, []string{"billing:read", "billing:write"}, map[string]any{"sub": testkit.Unique("automatic-customer")})
	accountBody := map[string]any{
		"name": "Automatic account", "application": product.Slug, "organization_id": org,
	}
	h.RequireStatus(t, http.StatusPreconditionRequired, http.MethodPost, "/v1/accounts", accountBody, consumer)
	status, _, catalogueHeaders := h.JSON(t, http.MethodGet, "/v1/catalog?product="+product.Slug, nil, consumer)
	if status != http.StatusOK || catalogueHeaders.Get("ETag") == "" {
		t.Fatalf("catalogue prerequisite returned status %d without an ETag", status)
	}
	status, raw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/accounts", accountBody, consumer,
		http.Header{"If-Match": []string{catalogueHeaders.Get("ETag")}})
	if status != http.StatusCreated {
		t.Fatalf("policy-compliant account creation returned %d: %s", status, raw)
	}
	snapshot := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/billing-snapshot?product="+product.Slug, nil, consumer)
	if !strings.Contains(string(snapshot), `"status":"active"`) || !strings.Contains(string(snapshot), `"billing_model":"free"`) {
		t.Fatalf("automatic onboarding did not activate the default Free plan: %s", snapshot)
	}
}

type planResponse struct {
	ID              string         `json:"id"`
	ProductID       string         `json:"product_id"`
	Product         string         `json:"product"`
	Slug            string         `json:"slug"`
	Name            string         `json:"name"`
	PriceMinor      int64          `json:"price_minor"`
	Currency        string         `json:"currency"`
	IncludedCredits int64          `json:"included_credits"`
	Entitlements    map[string]any `json:"entitlements"`
	BillingInterval string         `json:"billing_interval"`
	Active          bool           `json:"active"`
	Version         int64          `json:"version"`
}

func createAdminProduct(t *testing.T, h *testkit.HTTP, token, prefix string) productResponse {
	t.Helper()
	slug := testkit.Unique(prefix)
	raw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/products", map[string]any{
		"slug": slug, "name": "Catalogue Product", "description": "Managed by Billmesh Admin",
		"entitlement_schema": map[string]any{"fields": []any{
			map[string]any{"key": "reports", "label": "Reports", "type": "boolean", "required": true, "default": false},
			map[string]any{"key": "members", "label": "Maximum members", "type": "integer", "required": true, "default": 0, "minimum": 0},
		}},
	}, token)
	return testkit.Decode[productResponse](t, raw)
}

func createAdminPlan(t *testing.T, h *testkit.HTTP, token string, product productResponse, active bool) planResponse {
	t.Helper()
	raw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/products/"+product.ID+"/plans", map[string]any{
		"slug": testkit.Unique("admin-plan"), "name": "Admin Plan", "price_minor": 9900,
		"currency": "INR", "included_credits": 100, "billing_interval": "monthly",
		"entitlements": map[string]any{"reports": true, "members": 5}, "entitlement_schema_version": product.EntitlementSchemaVersion, "active": active,
	}, token)
	return testkit.Decode[planResponse](t, raw)
}

func TestPRD001ThroughPRD015ProductAdminContract(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("product-admin")
	admin := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	viewer := h.IssueToken(t, org, "daybook", []string{"billing:read"}, nil)

	product := createAdminProduct(t, h, admin, "catalogue")
	if product.ID == "" || !product.Active || product.Version != 1 || product.Description == "" || product.EntitlementSchemaVersion != 1 {
		t.Fatalf("PRD-004: incomplete product response: %+v", product)
	}

	detail := testkit.Decode[productResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/products/"+product.ID, nil, admin))
	if detail.Slug != product.Slug {
		t.Fatalf("PRD-003: got product %q, want %q", detail.Slug, product.Slug)
	}

	customerProducts := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/products", nil, viewer)
	if !strings.Contains(string(customerProducts), product.Slug) {
		t.Fatalf("PRD-001: active product missing from customer catalogue: %s", customerProducts)
	}

	updated := testkit.Decode[productResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/products/"+product.ID, map[string]any{
		"name": "Updated Product", "description": "Updated description", "version": product.Version,
	}, admin))
	if updated.Name != "Updated Product" || updated.Version != product.Version+1 {
		t.Fatalf("PRD-010: product update was not applied: %+v", updated)
	}
	h.RequireStatus(t, http.StatusBadRequest, http.MethodPatch, "/v1/admin/products/"+product.ID, map[string]any{
		"slug": "changed-slug", "version": updated.Version,
	}, admin)

	h.RequireStatus(t, http.StatusConflict, http.MethodPatch, "/v1/admin/products/"+product.ID, map[string]any{
		"name": "Stale Product", "version": product.Version,
	}, admin)

	archived := testkit.Decode[productResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/products/"+product.ID, map[string]any{
		"active": false, "version": updated.Version,
	}, admin))
	if archived.Active {
		t.Fatal("PRD-012: product remained active")
	}
	archivedAgain := testkit.Decode[productResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/products/"+product.ID, map[string]any{
		"active": false, "version": archived.Version,
	}, admin))
	if archivedAgain.Active || archivedAgain.Version != archived.Version {
		t.Fatalf("PRD-013: repeated archive was not idempotent: before=%+v after=%+v", archived, archivedAgain)
	}
	customerProducts = h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/products", nil, viewer)
	if strings.Contains(string(customerProducts), product.Slug) {
		t.Fatalf("PRD-001: archived product leaked into customer catalogue: %s", customerProducts)
	}
	adminProducts := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/products?status=archived&query="+product.Slug+"&limit=1&offset=0", nil, admin)
	if !strings.Contains(string(adminProducts), product.Slug) {
		t.Fatalf("PRD-002/015: archived product missing from admin catalogue: %s", adminProducts)
	}

	reactivated := testkit.Decode[productResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/products/"+product.ID, map[string]any{
		"active": true, "version": archivedAgain.Version,
	}, admin))
	if !reactivated.Active {
		t.Fatal("PRD-012: product was not reactivated")
	}

	h.RequireStatus(t, http.StatusNotFound, http.MethodGet, "/v1/admin/products/00000000-0000-0000-0000-000000000001", nil, admin)
	h.RequireStatus(t, http.StatusBadRequest, http.MethodGet, "/v1/admin/products/not-a-uuid", nil, admin)
}

func TestPRD007ThroughPRD009ProductValidationAndUniqueness(t *testing.T) {
	h := testkit.NewHTTP(t)
	admin := h.IssueToken(t, testkit.Unique("product-validation"), "daybook", testkit.AllPermissions(), nil)

	for _, body := range []map[string]any{
		{"slug": "UPPER", "name": "Upper"},
		{"slug": "two--hyphens", "name": "Bad"},
		{"slug": "valid-product", "name": "   "},
		{"slug": strings.Repeat("a", 64), "name": "Long"},
	} {
		h.RequireStatus(t, http.StatusBadRequest, http.MethodPost, "/v1/admin/products", body, admin)
	}

	product := createAdminProduct(t, h, admin, "unique-product")
	h.RequireStatus(t, http.StatusConflict, http.MethodPost, "/v1/admin/products", map[string]any{
		"slug": product.Slug, "name": "Duplicate",
	}, admin)
	trimmed := testkit.Decode[productResponse](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/products", map[string]any{
		"slug": testkit.Unique("trimmed-product"), "name": "  Trimmed Product  ", "description": "  Trimmed description  ",
	}, admin))
	if trimmed.Name != "Trimmed Product" || trimmed.Description != "Trimmed description" {
		t.Fatalf("PRD-005: Product text was not trimmed: %+v", trimmed)
	}
}

func TestPLAN013ThroughPLAN029PlanAdminContract(t *testing.T) {
	h := testkit.NewHTTP(t)
	admin := h.IssueToken(t, testkit.Unique("plan-admin"), "daybook", testkit.AllPermissions(), nil)
	viewer := h.IssueToken(t, testkit.Unique("plan-viewer"), "daybook", []string{"billing:read"}, nil)
	product := createAdminProduct(t, h, admin, "plans-product")
	active := createAdminPlan(t, h, admin, product, true)
	inactive := createAdminPlan(t, h, admin, product, false)
	freeAnnual := testkit.Decode[planResponse](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/products/"+product.ID+"/plans", map[string]any{
		"slug": testkit.Unique("free-annual"), "name": "Free Annual", "price_minor": 0,
		"currency": "INR", "billing_interval": "annual",
	}, admin))
	if freeAnnual.PriceMinor != 0 || freeAnnual.BillingInterval != "annual" {
		t.Fatalf("PLAN-016: free annual Plan response is wrong: %+v", freeAnnual)
	}

	if active.ProductID != product.ID || active.Product != product.Slug || active.Version != 1 {
		t.Fatalf("PLAN-016: incomplete plan response: %+v", active)
	}
	all := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/products/"+product.ID+"/plans?status=all", nil, admin)
	if !strings.Contains(string(all), active.Slug) || !strings.Contains(string(all), inactive.Slug) {
		t.Fatalf("PLAN-013: admin list omitted plans: %s", all)
	}
	activeOnly := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/products/"+product.ID+"/plans?status=active", nil, admin)
	if !strings.Contains(string(activeOnly), active.Slug) || strings.Contains(string(activeOnly), inactive.Slug) {
		t.Fatalf("PLAN-014: active filter returned wrong plans: %s", activeOnly)
	}
	detail := testkit.Decode[planResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/plans/"+active.ID, nil, admin))
	if detail.Slug != active.Slug {
		t.Fatalf("PLAN-015: got %q, want %q", detail.Slug, active.Slug)
	}

	public := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/plans?product="+product.Slug, nil, viewer)
	if !strings.Contains(string(public), active.Slug) || strings.Contains(string(public), inactive.Slug) {
		t.Fatalf("PLAN-017: customer plan visibility is wrong: %s", public)
	}

	updated := testkit.Decode[planResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/plans/"+active.ID, map[string]any{
		"name": "Updated Plan", "price_minor": 12900, "currency": "USD", "included_credits": 250,
		"billing_interval": "annual", "entitlements": map[string]any{"reports": true, "members": 10}, "version": active.Version,
	}, admin))
	if updated.Version != active.Version+1 || updated.PriceMinor != 12900 || updated.Currency != "USD" || updated.BillingInterval != "annual" {
		t.Fatalf("PLAN-026: plan update was not applied: %+v", updated)
	}
	nameOnly := testkit.Decode[planResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/plans/"+active.ID, map[string]any{
		"name": "Name Only Update", "version": updated.Version,
	}, admin))
	if nameOnly.PriceMinor != updated.PriceMinor || nameOnly.Currency != updated.Currency || nameOnly.IncludedCredits != updated.IncludedCredits || nameOnly.BillingInterval != updated.BillingInterval || nameOnly.Active != updated.Active || nameOnly.Slug != updated.Slug || nameOnly.ProductID != updated.ProductID {
		t.Fatalf("PLAN-026: omitted fields changed: before=%+v after=%+v", updated, nameOnly)
	}
	h.RequireStatus(t, http.StatusConflict, http.MethodPatch, "/v1/admin/plans/"+active.ID, map[string]any{
		"name": "Stale", "version": active.Version,
	}, admin)

	archived := testkit.Decode[planResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/plans/"+active.ID, map[string]any{
		"active": false, "version": nameOnly.Version,
	}, admin))
	if archived.Active {
		t.Fatal("PLAN-023: plan remained active")
	}
	public = h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/plans?product="+product.Slug, nil, viewer)
	if strings.Contains(string(public), active.Slug) {
		t.Fatalf("PLAN-017: inactive plan leaked into customer list: %s", public)
	}
	reactivated := testkit.Decode[planResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/plans/"+active.ID, map[string]any{
		"active": true, "version": archived.Version,
	}, admin))
	if !reactivated.Active {
		t.Fatal("PLAN-023: plan was not reactivated")
	}
}

func TestPLAN018PlanValidationAndMassAssignment(t *testing.T) {
	h := testkit.NewHTTP(t)
	admin := h.IssueToken(t, testkit.Unique("plan-validation"), "daybook", testkit.AllPermissions(), nil)
	product := createAdminProduct(t, h, admin, "validation-product")

	cases := []map[string]any{
		{"slug": "negative-price", "name": "Invalid", "price_minor": -1, "currency": "INR"},
		{"slug": "negative-credits", "name": "Invalid", "included_credits": -1, "currency": "INR"},
		{"slug": "bad-currency", "name": "Invalid", "currency": "inr"},
		{"slug": "bad-interval", "name": "Invalid", "currency": "INR", "billing_interval": "weekly"},
		{"slug": "unknown-field", "name": "Invalid", "currency": "INR", "product_id": "00000000-0000-0000-0000-000000000001"},
	}
	for _, body := range cases {
		h.RequireStatus(t, http.StatusBadRequest, http.MethodPost, "/v1/admin/products/"+product.ID+"/plans", body, admin)
	}
	h.RequireStatus(t, http.StatusNotFound, http.MethodPost, "/v1/admin/products/00000000-0000-0000-0000-000000000001/plans", map[string]any{
		"slug": "missing-product-plan", "name": "Missing Product", "currency": "INR",
	}, admin)
	plan := createAdminPlan(t, h, admin, product, true)
	h.RequireStatus(t, http.StatusBadRequest, http.MethodPatch, "/v1/admin/plans/"+plan.ID, map[string]any{
		"slug": "changed-slug", "version": plan.Version,
	}, admin)
}

func TestPLAN027AndPLAN028PlanJSONContract(t *testing.T) {
	h := testkit.NewHTTP(t)
	admin := h.IssueToken(t, testkit.Unique("plan-json"), "daybook", testkit.AllPermissions(), nil)
	product := createAdminProduct(t, h, admin, "plan-json-product")
	product = testkit.Decode[productResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/products/"+product.ID, map[string]any{
		"version": product.Version,
		"entitlement_schema": map[string]any{"fields": []any{
			map[string]any{"key": "enabled", "label": "Enabled", "type": "boolean", "required": true},
			map[string]any{"key": "limit", "label": "Limit", "type": "integer", "required": true, "minimum": 0},
			map[string]any{"key": "label", "label": "Label", "type": "string", "required": true},
			map[string]any{"key": "nested", "label": "Nested settings", "type": "object", "required": true, "fields": []any{
				map[string]any{"key": "mode", "label": "Mode", "type": "string", "required": true},
			}},
			map[string]any{"key": "regions", "label": "Regions", "type": "array", "required": true, "items": map[string]any{"type": "string"}},
			map[string]any{"key": "unset", "label": "Unset value", "type": "string", "nullable": true},
		}},
	}, admin))

	h.RequireStatus(t, http.StatusBadRequest, http.MethodPost, "/v1/admin/products/"+product.ID+"/plans", map[string]any{
		"slug": "unknown-json-field", "name": "Unknown JSON Field", "currency": "INR", "unexpected": true,
	}, admin)

	request, err := http.NewRequest(http.MethodPost, h.BaseURL+"/v1/admin/products/"+product.ID+"/plans",
		strings.NewReader(`{"slug":"duplicate-plan","name":"First","name":"Second","currency":"INR"}`))
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
		t.Fatalf("PLAN-027 duplicate JSON keys: want 400, got %d", response.StatusCode)
	}

	wantEntitlements := map[string]any{
		"enabled": true,
		"limit":   12,
		"label":   "team",
		"nested":  map[string]any{"mode": "strict"},
		"regions": []any{"in", "us"},
		"unset":   nil,
	}
	created := testkit.Decode[planResponse](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/products/"+product.ID+"/plans", map[string]any{
		"slug": "json-entitlements", "name": "JSON Entitlements", "currency": "INR", "entitlements": wantEntitlements,
		"entitlement_schema_version": product.EntitlementSchemaVersion,
	}, admin))
	if !equalJSON(created.Entitlements, wantEntitlements) {
		t.Fatalf("PLAN-028 entitlement JSON changed: got %#v want %#v", created.Entitlements, wantEntitlements)
	}
}

func equalJSON(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func TestPRD020AndPLAN021ArchivedProductBlocksNewBusiness(t *testing.T) {
	h := testkit.NewHTTP(t)
	admin := h.IssueToken(t, testkit.Unique("archive-admin"), "daybook", testkit.AllPermissions(), nil)
	product := createAdminProduct(t, h, admin, "archived-business")
	plan := createAdminPlan(t, h, admin, product, true)
	org := testkit.Unique("archived-customer")
	customer := h.IssueToken(t, org, product.Slug, []string{"billing:read", "billing:write"}, nil)
	account := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{
		"name": "Archived Product Customer", "application": product.Slug, "organization_id": org,
	}, customer)).ID

	archived := testkit.Decode[productResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/products/"+product.ID, map[string]any{
		"active": false, "version": product.Version,
	}, admin))
	if archived.Active {
		t.Fatal("product was not archived")
	}
	h.RequireStatus(t, http.StatusConflict, http.MethodPost, "/v1/admin/products/"+product.ID+"/plans", map[string]any{
		"slug": testkit.Unique("blocked-plan"), "name": "Blocked Plan", "currency": "INR", "active": true,
	}, admin)

	h.RequireStatus(t, http.StatusConflict, http.MethodPost, "/v1/subscriptions", map[string]any{
		"account_id": account, "plan_id": plan.ID, "payment_status": "verified",
	}, customer)
}

func TestCatalogueResponsesRemainValidJSON(t *testing.T) {
	h := testkit.NewHTTP(t)
	admin := h.IssueToken(t, testkit.Unique("catalogue-json"), "daybook", testkit.AllPermissions(), nil)
	raw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/products", nil, admin)
	var page struct {
		Items  []productResponse `json:"items"`
		Total  int               `json:"total"`
		Limit  int               `json:"limit"`
		Offset int               `json:"offset"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("catalogue response is not JSON: %v", err)
	}
}

func TestPRD021AndPLAN032SuccessfulCatalogueMutationsAreAudited(t *testing.T) {
	h := testkit.NewHTTP(t)
	admin := h.IssueToken(t, testkit.Unique("catalogue-audit"), "daybook", testkit.AllPermissions(), map[string]any{"sub": "user:catalogue-auditor"})
	product := createAdminProduct(t, h, admin, "audited-product")
	plan := createAdminPlan(t, h, admin, product, true)
	h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/products/"+product.ID, map[string]any{
		"description": "Audited update", "version": product.Version,
	}, admin)
	h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/plans/"+plan.ID, map[string]any{
		"name": "Audited Plan Update", "version": plan.Version,
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
	for _, expectation := range []struct {
		action, resourceType, resourceID string
	}{
		{"product.create", "product", product.ID},
		{"product.update", "product", product.ID},
		{"plan.create", "plan", plan.ID},
		{"plan.update", "plan", plan.ID},
	} {
		var count int
		var actor string
		var hasAfter bool
		err := pool.QueryRow(context.Background(), `SELECT count(*),COALESCE(max(actor_subject),''),bool_or(after_state IS NOT NULL)
			FROM billmesh.audit_log WHERE action=$1 AND resource_type=$2 AND resource_id=$3`, expectation.action, expectation.resourceType, expectation.resourceID).
			Scan(&count, &actor, &hasAfter)
		if err != nil {
			t.Fatal(err)
		}
		if count != 1 || actor != "user:catalogue-auditor" || !hasAfter {
			t.Fatalf("missing catalogue audit %+v: count=%d actor=%q after=%v", expectation, count, actor, hasAfter)
		}
	}
}
