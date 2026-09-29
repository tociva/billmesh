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

func TestSecurityTenantListsFiltersLimitsAndForeignCursor(t *testing.T) {
	h := testkit.NewHTTP(t)
	resetMockFailure(t, h)
	type tenant struct {
		token, account, wallet, subscription, order, invoice, webhook, usage, audit string
	}
	createTenant := func(label string) tenant {
		t.Helper()
		org := testkit.Unique(label)
		item := tenant{token: h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)}
		item.account = testkit.CreateFixtureAccount(t, h, org, item.token)
		item.wallet = testkit.CreateFundedWallet(t, h, item.account, item.token)
		raw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/subscriptions", map[string]any{"account_id": item.account, "product": "daybook", "plan": "daybook-paid", "payment_status": "verified"}, item.token)
		item.subscription = testkit.Decode[struct {
			ID string `json:"id"`
		}](t, raw).ID
		invoices := testkit.Decode[[]struct {
			ID string `json:"id"`
		}](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/invoices", nil, item.token))
		if len(invoices) == 0 {
			t.Fatal("paid subscription produced no invoice")
		}
		item.invoice = invoices[0].ID
		orderRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/payments/orders", map[string]any{"account_id": item.account, "credit_pack": "credits-500"}, item.token)
		item.order = testkit.Decode[struct {
			Order struct {
				ID string `json:"id"`
			} `json:"order"`
		}](t, orderRaw).Order.ID
		item.webhook = h.MockURL + "/receivers/" + testkit.Unique(label+"-hook")
		h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{"application": "daybook", "target_url": item.webhook, "secret": "tenant-secret"}, item.token)
		reservation := testkit.ReserveFixture(t, h, item.wallet, item.token)
		item.usage = testkit.Unique(label + "-usage")
		h.RequireStatus(t, http.StatusAccepted, http.MethodPost, "/v1/usage-events", map[string]any{"event_id": item.usage, "reservation_id": reservation, "meter": "workflow.execution", "application": "daybook", "quantity": 1}, item.token)
		item.audit = testkit.Unique(label + "-audit")
		h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/adjustments", map[string]any{"wallet_id": item.wallet, "amount": 1, "reason": item.audit}, item.token)
		return item
	}
	a := createTenant("list-a")
	b := createTenant("list-b")
	for _, tc := range []struct {
		path, own, foreign string
	}{
		{"/v1/payments?limit=200", a.order, b.order},
		{"/v1/invoices?limit=200", a.invoice, b.invoice},
		{"/v1/webhooks", a.webhook, b.webhook},
		{"/v1/usage-events?product=daybook&application=daybook&limit=200", a.usage, b.usage},
		{"/v1/admin/audit?limit=200", a.audit, b.audit},
		{"/v1/limits", a.wallet, b.wallet},
	} {
		body := h.RequireStatus(t, http.StatusOK, http.MethodGet, tc.path, nil, a.token)
		if !strings.Contains(string(body), tc.own) || strings.Contains(string(body), tc.foreign) {
			t.Fatalf("GAP-AUTHZ-008: list %s crossed tenant boundary: %s", tc.path, body)
		}
	}
	for _, path := range []string{"/v1/payments?limit=1", "/v1/invoices?limit=1", "/v1/usage-events?product=daybook&application=daybook&limit=1", "/v1/admin/audit?limit=1"} {
		body := h.RequireStatus(t, http.StatusOK, http.MethodGet, path, nil, a.token)
		for _, secret := range []string{b.order, b.invoice, b.usage, b.audit} {
			if strings.Contains(string(body), secret) {
				t.Fatalf("GAP-AUTHZ-008: limited list %s leaked tenant B: %s", path, body)
			}
		}
	}
	if body := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/usage-events?product=taskmesh&application=taskmesh", nil, a.token); strings.Contains(string(body), b.usage) {
		t.Fatalf("GAP-AUTHZ-008: altered filters leaked tenant B usage: %s", body)
	}
	h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/accounts/"+b.account, nil, a.token)
	url := os.Getenv("BILLMESH_E2E_DATABASE_URL")
	if url == "" {
		t.Fatal("BILLMESH_E2E_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var cursor int64
	if err := pool.QueryRow(context.Background(), `SELECT sequence FROM outbox_events WHERE aggregate_type='subscription' AND aggregate_id=$1 ORDER BY sequence DESC LIMIT 1`, b.subscription).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, h.BaseURL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Last-Event-ID", fmt.Sprint(cursor))
	resp, err := h.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GAP-AUTHZ-008: foreign cursor returned %d", resp.StatusCode)
	}
}
