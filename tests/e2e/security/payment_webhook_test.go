//go:build e2e

package security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

func paymentState(t *testing.T, pool *pgxpool.Pool, orderID string) (status string, grants, ledger, invoices int) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `SELECT status FROM payments WHERE provider_order_id=$1`, orderID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_grants WHERE operation_ref=(SELECT 'payment:'||id FROM payments WHERE provider_order_id=$1)`, orderID).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_ledger WHERE operation_ref=(SELECT 'grant:payment:'||id FROM payments WHERE provider_order_id=$1)`, orderID).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE payment_id=(SELECT id FROM payments WHERE provider_order_id=$1)`, orderID).Scan(&invoices); err != nil {
		t.Fatal(err)
	}
	return
}

func TestSecurityPaymentWebhookSignatureCaptureAndReplay(t *testing.T) {
	h := testkit.NewHTTP(t)
	resetMockFailure(t, h)
	org := testkit.Unique("payment-security")
	token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	account := testkit.CreateFixtureAccount(t, h, org, token)
	orderRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/payments/orders", map[string]any{"account_id": account, "credit_pack": "credits-500"}, token)
	order := testkit.Decode[struct {
		Order struct {
			ID       string `json:"id"`
			Amount   int64  `json:"amount"`
			Currency string `json:"currency"`
		} `json:"order"`
	}](t, orderRaw)
	pool, err := pgxpool.New(context.Background(), os.Getenv("BILLMESH_E2E_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	eventID := testkit.Unique("provider-event")
	providerPaymentID := testkit.Unique("provider-payment")
	event := map[string]any{"id": eventID, "type": "payment.captured", "payment_id": providerPaymentID, "order_id": order.Order.ID, "status": "captured", "amount_minor": order.Order.Amount, "currency": order.Order.Currency}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if status := h.SignedWebhookRaw(t, "/v1/payments/webhook", raw, "wrong-secret"); status != http.StatusUnauthorized {
		t.Fatalf("PAY-006: invalid signature returned %d", status)
	}
	modified := bytes.Replace(raw, []byte(`"captured"`), []byte(`"failed"`), 1)
	req, err := http.NewRequest(http.MethodPost, h.BaseURL+"/v1/payments/webhook", bytes.NewReader(modified))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Razorpay-Signature", webhookSignature(string(raw), "test-webhook-secret"))
	resp, err := h.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GAP-SEC-003: modified raw body returned %d", resp.StatusCode)
	}
	missingSigStatus, _ := rawJSON(t, h, http.MethodPost, "/v1/payments/webhook", raw, "", "application/json")
	if missingSigStatus != http.StatusUnauthorized {
		t.Fatalf("GAP-SEC-003: absent signature returned %d", missingSigStatus)
	}
	if status := h.SignedWebhookRaw(t, "/v1/payments/webhook", []byte(`{"id":`), "test-webhook-secret"); status != http.StatusBadRequest {
		t.Fatalf("PAY-007: signed malformed JSON returned %d", status)
	}
	status, grants, ledger, invoices := paymentState(t, pool, order.Order.ID)
	if status != "created" || grants != 0 || ledger != 0 || invoices != 0 {
		t.Fatalf("invalid webhook changed financial state: %s %d %d %d", status, grants, ledger, invoices)
	}
	var persisted int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM provider_events WHERE provider_event_id=$1`, eventID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != 0 {
		t.Fatalf("invalid signature persisted provider event")
	}
	if status := h.SignedWebhookRaw(t, "/v1/payments/webhook", raw, "test-webhook-secret"); status != http.StatusNoContent {
		t.Fatalf("PAY-005: valid capture returned %d", status)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM provider_events WHERE provider_event_id=$1 AND processed_at IS NOT NULL`, eventID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != 1 {
		t.Fatalf("PAY-005: valid capture did not persist processed provider event")
	}
	status, grants, ledger, invoices = paymentState(t, pool, order.Order.ID)
	if status != "captured" || grants != 1 || ledger != 1 || invoices != 1 {
		t.Fatalf("valid capture did not atomically grant and invoice: %s %d %d %d", status, grants, ledger, invoices)
	}
	if status := h.SignedWebhookRaw(t, "/v1/payments/webhook", raw, "test-webhook-secret"); status != http.StatusNoContent {
		t.Fatalf("identical replay returned %d", status)
	}
	event["payment_id"] = testkit.Unique("conflicting-payment")
	conflictRaw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if status := h.SignedWebhookRaw(t, "/v1/payments/webhook", conflictRaw, "test-webhook-secret"); status != http.StatusConflict {
		t.Fatalf("GAP-SEC-004: conflicting replay returned %d", status)
	}
	status, grants, ledger, invoices = paymentState(t, pool, order.Order.ID)
	if status != "captured" || grants != 1 || ledger != 1 || invoices != 1 {
		t.Fatalf("replay changed financial state: %s %d %d %d", status, grants, ledger, invoices)
	}
	var wg sync.WaitGroup
	results := make(chan int, 8)
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodPost, h.BaseURL+"/v1/payments/webhook", bytes.NewReader(raw))
			if err != nil {
				errors <- err
				return
			}
			req.Header.Set("X-Razorpay-Signature", webhookSignature(string(raw), "test-webhook-secret"))
			resp, err := h.Client.Do(req)
			if err != nil {
				errors <- err
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			results <- resp.StatusCode
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Fatalf("GAP-SEC-004: concurrent replay failed: %v", err)
	}
	for code := range results {
		if code != http.StatusNoContent {
			t.Fatalf("GAP-SEC-004: concurrent replay returned %d", code)
		}
	}
	status, grants, ledger, invoices = paymentState(t, pool, order.Order.ID)
	if status != "captured" || grants != 1 || ledger != 1 || invoices != 1 {
		t.Fatalf("GAP-SEC-004: concurrent replay changed financial state: %s %d %d %d", status, grants, ledger, invoices)
	}
	otherOrg := testkit.Unique("cross-payment")
	otherToken := h.IssueToken(t, otherOrg, "daybook", testkit.AllPermissions(), map[string]any{"environment": "staging"})
	otherAccount := testkit.CreateFixtureAccount(t, h, otherOrg, otherToken)
	otherOrderRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/payments/orders", map[string]any{"account_id": otherAccount, "credit_pack": "credits-500"}, otherToken)
	otherOrder := testkit.Decode[struct {
		Order struct {
			ID       string `json:"id"`
			Amount   int64  `json:"amount"`
			Currency string `json:"currency"`
		} `json:"order"`
	}](t, otherOrderRaw)
	crossEventID := testkit.Unique("cross-provider-event")
	crossEvent := map[string]any{"id": crossEventID, "type": "payment.captured", "payment_id": providerPaymentID, "order_id": otherOrder.Order.ID, "status": "captured", "amount_minor": otherOrder.Order.Amount, "currency": otherOrder.Order.Currency}
	if status := h.SignedWebhook(t, "/v1/payments/webhook", crossEvent, "test-webhook-secret"); status != http.StatusConflict {
		t.Fatalf("GAP-SEC-004: reused payment reference across accounts returned %d", status)
	}
	status, grants, ledger, invoices = paymentState(t, pool, otherOrder.Order.ID)
	if status != "created" || grants != 0 || ledger != 0 || invoices != 0 {
		t.Fatalf("GAP-SEC-004: cross-account payment reference changed state: %s %d %d %d", status, grants, ledger, invoices)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM provider_events WHERE provider_event_id=$1`, crossEventID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != 0 {
		t.Fatal("GAP-SEC-004: rejected cross-account event was persisted")
	}
}

func resetMockFailure(t *testing.T, h *testkit.HTTP) {
	t.Helper()
	resp, err := h.Client.Post(h.MockURL+"/test/failure", "application/json", bytes.NewReader([]byte(`{"status":0}`)))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reset mock provider failure: status %d", resp.StatusCode)
	}
}
