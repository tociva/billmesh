//go:build e2e

package security_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestSecuritySharedAccountProductBoundaries(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("shared-products")
	linker := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	daybook := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write", "credits:reserve", "credits:settle"}, nil)
	taskmesh := h.IssueToken(t, org, "taskmesh", []string{"billing:read", "billing:write", "credits:reserve", "credits:settle"}, nil)
	taskmeshRuntime := testkit.RuntimeToken(t, h, org, "taskmesh")
	account := testkit.CreateFixtureAccount(t, h, org, linker)
	taskmeshAccount := testkit.CreateFixtureAccount(t, h, org, taskmesh)
	daybookSub := testkit.CreateFixtureSubscription(t, h, account, daybook)
	taskmeshSub := testkit.ActivatePaidSubscription(t, h, "taskmesh", "professional", taskmesh)
	h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/billing-snapshot?product=daybook", nil, daybook)
	h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/billing-snapshot?product=taskmesh", nil, taskmesh)
	h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/billing-snapshot?product=taskmesh", nil, daybook)
	h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/entitlements?product=taskmesh", nil, daybook)
	h.RequireStatus(t, http.StatusNotFound, http.MethodPost, "/v1/subscriptions/"+taskmeshSub+"/change-plan", map[string]any{"plan": "professional"}, daybook)
	h.RequireStatus(t, http.StatusNotFound, http.MethodPost, "/v1/subscriptions/"+taskmeshSub+"/cancel", map[string]any{"immediate": true}, daybook)
	h.RequireStatus(t, http.StatusNotFound, http.MethodPost, "/v1/subscriptions/"+taskmeshSub+"/renew", map[string]any{"operation_ref": testkit.Unique("foreign-renew")}, daybook)

	installationRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts/"+taskmeshAccount+"/installations", map[string]any{"application": "taskmesh", "organization_id": org}, taskmesh)
	installation := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, installationRaw).ID
	h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/installations/"+installation+"/revoke", nil, daybook)
	h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/accounts/"+taskmeshAccount+"/installations", map[string]any{"application": "taskmesh", "organization_id": org}, daybook)

	taskmeshLimits := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/limits", nil, taskmesh)
	daybookLimits := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/limits", nil, daybook)
	if !strings.Contains(string(taskmeshLimits), `"product":"taskmesh"`) || strings.Contains(string(daybookLimits), `"product":"taskmesh"`) {
		t.Fatalf("GAP-AUTHZ-006: wallet limits crossed product boundary: taskmesh=%s daybook=%s", taskmeshLimits, daybookLimits)
	}
	var limits []struct {
		ID      string `json:"wallet_id"`
		Product string `json:"product"`
	}
	limits = testkit.Decode[[]struct {
		ID      string `json:"wallet_id"`
		Product string `json:"product"`
	}](t, taskmeshLimits)
	var taskmeshWallet string
	for _, limit := range limits {
		if limit.Product == "taskmesh" {
			taskmeshWallet = limit.ID
		}
	}
	if taskmeshWallet == "" {
		t.Fatal("taskmesh subscription did not fund a wallet")
	}
	reservation := testkit.ReserveFixture(t, h, taskmeshWallet, taskmeshRuntime)
	usageRef := testkit.Unique("taskmesh-usage")
	h.RequireStatus(t, http.StatusAccepted, http.MethodPost, "/v1/usage-events", map[string]any{"event_id": usageRef, "reservation_id": reservation, "meter": "workflow.execution", "application": "taskmesh", "quantity": 1}, taskmeshRuntime)
	for _, path := range []string{"/v1/usage-events?product=taskmesh&application=taskmesh&limit=1", "/v1/usage-events?application=taskmesh&limit=200"} {
		if body := h.RequireStatus(t, http.StatusOK, http.MethodGet, path, nil, daybook); strings.Contains(string(body), usageRef) {
			t.Fatalf("GAP-AUTHZ-006: foreign product usage leaked: %s", body)
		}
	}
	if body := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/usage-events?product=taskmesh", nil, taskmesh); !strings.Contains(string(body), usageRef) {
		t.Fatalf("GAP-AUTHZ-006: owner cannot read taskmesh usage: %s", body)
	}

	pack := testkit.Unique("taskmesh-pack")
	packRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/credit-packs", map[string]any{"product_id": productID(t, h, "taskmesh", linker), "slug": pack, "name": "Taskmesh Pack", "credits": 10, "price_minor": 100, "currency": "INR"}, linker)
	packID := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, packRaw).ID
	orderRaw := h.RequireStatusWithHeaders(t, http.StatusCreated, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack_id": packID}, taskmesh,
		http.Header{"Idempotency-Key": []string{testkit.Unique("taskmesh-order")}})
	order := testkit.Decode[testkit.PaymentOrderFixture](t, orderRaw)
	config := order.Checkout.ClientConfig
	if status := h.SignedWebhook(t, "/v1/payments/webhook", map[string]any{"id": testkit.Unique("taskmesh-capture"), "type": "payment.captured", "payment_id": testkit.Unique("provider-payment"), "order_id": config.OrderID, "status": "captured", "amount_minor": config.AmountMinor, "currency": config.Currency}, "test-webhook-secret"); status != http.StatusNoContent {
		t.Fatalf("taskmesh payment capture returned %d", status)
	}
	if body := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/payments?limit=1", nil, daybook); strings.Contains(string(body), config.OrderID) {
		t.Fatalf("GAP-AUTHZ-006: taskmesh payment leaked: %s", body)
	}
	if body := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/payments", nil, taskmesh); !strings.Contains(string(body), config.OrderID) {
		t.Fatalf("GAP-AUTHZ-006: taskmesh owner cannot see payment: %s", body)
	}
	taskmeshInvoices := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/invoices", nil, taskmesh)
	daybookInvoices := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/invoices?limit=1", nil, daybook)
	if !strings.Contains(string(taskmeshInvoices), `"status":"paid"`) || strings.Contains(string(daybookInvoices), `"status":"paid"`) {
		t.Fatalf("GAP-AUTHZ-006: invoice boundary failed: taskmesh=%s daybook=%s", taskmeshInvoices, daybookInvoices)
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
	var cursor int64
	if err := pool.QueryRow(context.Background(), `SELECT sequence FROM outbox_events WHERE aggregate_type='subscription' AND aggregate_id=$1 ORDER BY sequence DESC LIMIT 1`, taskmeshSub).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, h.BaseURL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+daybook)
	req.Header.Set("Last-Event-ID", fmt.Sprint(cursor))
	resp, err := h.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GAP-AUTHZ-006: foreign product event cursor returned %d", resp.StatusCode)
	}
	streamReq, err := http.NewRequest(http.MethodGet, h.BaseURL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	streamReq.Header.Set("Authorization", "Bearer "+daybook)
	client := *h.Client
	client.Timeout = 1500 * time.Millisecond
	stream, err := client.Do(streamReq)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK {
		t.Fatalf("GAP-AUTHZ-006: product stream returned %d", stream.StatusCode)
	}
	events, readErr := io.ReadAll(stream.Body)
	if readErr != nil {
		var netErr net.Error
		if !errors.As(readErr, &netErr) || !netErr.Timeout() {
			t.Fatalf("GAP-AUTHZ-006: read product stream: %v", readErr)
		}
	}
	if !strings.Contains(string(events), daybookSub) || strings.Contains(string(events), taskmeshSub) {
		t.Fatalf("GAP-AUTHZ-006: stream crossed product boundary: %s", events)
	}
}
