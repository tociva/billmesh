//go:build e2e

package security_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestSecuritySameOrganizationEnvironmentIsolationAcrossRoutes(t *testing.T) {
	h := testkit.NewHTTP(t)
	resetMockFailure(t, h)
	org := testkit.Unique("two-environments")
	type environment struct {
		token, account, wallet, subscription, invoice, order, webhook, usage, audit string
	}
	create := func(name string) environment {
		t.Helper()
		item := environment{token: h.IssueToken(t, org, "daybook", testkit.AllPermissions(), map[string]any{"environment": name})}
		item.account = testkit.CreateFixtureAccount(t, h, org, item.token)
		item.wallet = testkit.CreateFundedWallet(t, h, item.account, item.token)
		item.subscription = testkit.ActivatePaidSubscription(t, h, "daybook", "daybook-paid", item.token)
		invoices := testkit.Decode[struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/invoices", nil, item.token)).Items
		if len(invoices) == 0 {
			t.Fatal("environment fixture did not create an invoice")
		}
		item.invoice = invoices[0].ID
		item.order = testkit.CreateCreditPackOrder(t, h, "daybook", 500, item.token).Checkout.ClientConfig.OrderID
		item.webhook = h.MockURL + "/receivers/" + testkit.Unique(name+"-hook")
		h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{"target_url": item.webhook, "secret": "environment-secret-0123456789abcdef"}, item.token)
		reservation := testkit.ReserveFixture(t, h, item.wallet, item.token)
		item.usage = testkit.Unique(name + "-usage")
		h.RequireStatus(t, http.StatusAccepted, http.MethodPost, "/v1/usage-events", map[string]any{"event_id": item.usage, "reservation_id": reservation, "meter": "workflow.execution", "application": "daybook", "quantity": 1}, item.token)
		item.audit = testkit.Unique(name + "-audit")
		h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/adjustments", map[string]any{"wallet_id": item.wallet, "amount": 1, "reason": item.audit}, item.token)
		return item
	}
	staging := create("staging")
	production := create("production")
	h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/accounts/current", nil, production.token)
	h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/wallets/"+staging.wallet, nil, production.token)
	h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/wallets/"+staging.wallet+"/grants", map[string]any{"source": "test", "operation_ref": testkit.Unique("cross-env"), "amount": 1}, production.token)
	h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/admin/adjustments", map[string]any{"wallet_id": staging.wallet, "amount": 1, "reason": "cross-environment"}, production.token)
	for _, tc := range []struct {
		path, own, foreign string
	}{
		{"/v1/payments?limit=200", production.order, staging.order},
		{"/v1/invoices?limit=200", production.invoice, staging.invoice},
		{"/v1/webhooks", production.webhook, staging.webhook},
		{"/v1/usage-events?application=daybook&limit=200", production.usage, staging.usage},
		{"/v1/admin/audit?limit=200", production.audit, staging.audit},
		{"/v1/limits", production.wallet, staging.wallet},
	} {
		body := h.RequireStatus(t, http.StatusOK, http.MethodGet, tc.path, nil, production.token)
		if !strings.Contains(string(body), tc.own) || strings.Contains(string(body), tc.foreign) {
			t.Fatalf("AUTH-016: %s crossed environment boundary: %s", tc.path, body)
		}
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
	var stagingCursor int64
	if err := pool.QueryRow(context.Background(), `SELECT sequence FROM outbox_events WHERE aggregate_type='subscription' AND aggregate_id=$1 ORDER BY sequence DESC LIMIT 1`, staging.subscription).Scan(&stagingCursor); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, h.BaseURL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+production.token)
	req.Header.Set("Last-Event-ID", fmt.Sprint(stagingCursor))
	resp, err := h.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("AUTH-016: staging event cursor returned %d", resp.StatusCode)
	}
	var deliveryID string
	if err := pool.QueryRow(context.Background(), `SELECT d.id FROM webhook_deliveries d JOIN webhook_endpoints ep ON ep.id=d.endpoint_id WHERE ep.account_id=$1 LIMIT 1`, staging.account).Scan(&deliveryID); err != nil {
		t.Fatal(err)
	}
	h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/admin/webhooks/"+deliveryID+"/replay", nil, production.token)
}
