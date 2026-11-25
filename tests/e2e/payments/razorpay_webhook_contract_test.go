//go:build e2e

package payments_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/internal/app"
	"github.com/tociva/billmesh/internal/config"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestRazorpayWebhookTransportContract(t *testing.T) {
	h := testkit.NewHTTP(t)
	pool := e2ePool(t)
	raw := []byte(`{ "entity": "event", "account_id": "acc_billmesh_test", "event": "invoice.paid", "payload": {}, "created_at": 1700000000 }`)
	validSignature := testkit.RazorpayWebhookSignature(raw, "test-webhook-secret")
	uppercaseSignature := strings.ToUpper(validSignature)

	tests := []struct {
		name, eventID string
		body          []byte
		signature     string
		want          int
	}{
		{name: "missing event ID", body: raw, signature: validSignature, want: http.StatusBadRequest},
		{name: "blank event ID", eventID: " ", body: raw, signature: validSignature, want: http.StatusBadRequest},
		{name: "malformed event ID", eventID: "evt/not-valid", body: raw, signature: validSignature, want: http.StatusBadRequest},
		{name: "oversized event ID", eventID: strings.Repeat("e", 129), body: raw, signature: validSignature, want: http.StatusBadRequest},
		{name: "missing signature", eventID: "evt_missing_signature", body: raw, want: http.StatusUnauthorized},
		{name: "malformed signature", eventID: "evt_malformed_signature", body: raw, signature: "not-hex", want: http.StatusUnauthorized},
		{name: "wrong signature", eventID: "evt_wrong_signature", body: raw, signature: testkit.RazorpayWebhookSignature(raw, "wrong-secret"), want: http.StatusUnauthorized},
		{name: "truncated signature", eventID: "evt_truncated_signature", body: raw, signature: validSignature[:63], want: http.StatusUnauthorized},
		{name: "uppercase signature", eventID: "evt_uppercase_signature", body: raw, signature: uppercaseSignature, want: http.StatusUnauthorized},
		{name: "oversized body", eventID: "evt_oversized_body", body: bytes.Repeat([]byte("x"), (1<<20)+1), signature: "ignored", want: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := postRazorpayWebhook(t, h, test.eventID, test.body, test.signature); got != test.want {
				t.Fatalf("want status %d, got %d", test.want, got)
			}
			if test.eventID != "" && len(test.eventID) <= 128 && !strings.Contains(test.eventID, "/") && strings.TrimSpace(test.eventID) != "" {
				assertProviderEventCount(t, pool, test.eventID, 0)
			}
		})
	}

	compact := []byte(`{"entity":"event","account_id":"acc_billmesh_test","event":"invoice.paid","payload":{},"created_at":1700000000}`)
	if status := postRazorpayWebhook(t, h, "evt_reserialized", compact, validSignature); status != http.StatusUnauthorized {
		t.Fatalf("signature for reserialized JSON returned %d", status)
	}
	assertProviderEventCount(t, pool, "evt_reserialized", 0)

	malformed := []byte(`{"entity":`)
	if status := postRazorpayWebhook(t, h, "evt_malformed_json", malformed, testkit.RazorpayWebhookSignature(malformed, "test-webhook-secret")); status != http.StatusBadRequest {
		t.Fatalf("signed malformed JSON returned %d", status)
	}
	assertProviderEventCount(t, pool, "evt_malformed_json", 0)

	wrongAccount := []byte(`{"entity":"event","account_id":"acc_other","event":"invoice.paid","payload":{}}`)
	if status := postRazorpayWebhook(t, h, "evt_wrong_account", wrongAccount, testkit.RazorpayWebhookSignature(wrongAccount, "test-webhook-secret")); status != http.StatusBadRequest {
		t.Fatalf("unexpected merchant account returned %d", status)
	}
	assertProviderEventCount(t, pool, "evt_wrong_account", 0)

	undurable := testkit.RazorpayRefundProcessed("evt_undurable", "pay_unknown", "rfnd_unknown", 100, "INR")
	if status := h.RazorpayWebhook(t, "/v1/payments/webhook", undurable, "test-webhook-secret"); status >= 200 && status < 300 {
		t.Fatalf("non-durable receipt returned success %d", status)
	}
	assertProviderEventCount(t, pool, "evt_undurable", 0)
}

func TestRazorpayWebhookDurableAcknowledgementRotationAndDeadline(t *testing.T) {
	h := testkit.NewHTTP(t)
	pool := e2ePool(t)
	raw := []byte("{\n  \"entity\": \"event\",\n  \"account_id\": \"acc_billmesh_test\",\n  \"event\": \"invoice.paid\",\n  \"payload\": {}\n}\n")

	started := time.Now()
	if status := postRazorpayWebhook(t, h, "evt_current_secret", raw, testkit.RazorpayWebhookSignature(raw, "test-webhook-secret")); status != http.StatusNoContent {
		t.Fatalf("current secret returned %d", status)
	}
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("durable acknowledgement exceeded Razorpay deadline: %s", elapsed)
	}
	if status := postRazorpayWebhook(t, h, "evt_previous_secret", raw, testkit.RazorpayWebhookSignature(raw, "test-previous-webhook-secret")); status != http.StatusNoContent {
		t.Fatalf("previous secret during grace period returned %d", status)
	}

	var stored []byte
	var digest string
	var processed bool
	if err := pool.QueryRow(context.Background(), `SELECT raw_payload,payload_sha256,processed_at IS NOT NULL FROM provider_events WHERE provider_event_id='evt_current_secret'`).Scan(&stored, &digest, &processed); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, raw) || len(digest) != 64 || !processed {
		t.Fatalf("provider event was not durably stored: raw=%t digest=%q processed=%t", bytes.Equal(stored, raw), digest, processed)
	}

	// A retry through a new client models a delivery after the original response
	// was lost; durable deduplication must still acknowledge without reapplying.
	retry := testkit.NewHTTP(t)
	if status := postRazorpayWebhook(t, retry, "evt_current_secret", raw, testkit.RazorpayWebhookSignature(raw, "test-webhook-secret")); status != http.StatusNoContent {
		t.Fatalf("durable retry returned %d", status)
	}
	assertProviderEventCount(t, pool, "evt_current_secret", 1)
}

func TestRazorpayWebhookConcurrentDeduplicationSurvivesAPIRecreation(t *testing.T) {
	pool := e2ePool(t)
	provider := config.RazorpayConfig{WebhookSecret: "test-webhook-secret", AccountID: testkit.RazorpayTestAccountID}
	first, second := app.NewAPI(pool, nil, nil), app.NewAPI(pool, nil, nil)
	first.ConfigureRazorpay(provider)
	second.ConfigureRazorpay(provider)
	raw := []byte(`{"entity":"event","account_id":"acc_billmesh_test","event":"invoice.paid","payload":{}}`)
	eventID := testkit.Unique("evt_concurrent_restart")

	var wait sync.WaitGroup
	results := make(chan int, 2)
	for _, handler := range []http.Handler{first.Handler(), second.Handler()} {
		wait.Add(1)
		go func(handler http.Handler) {
			defer wait.Done()
			results <- serveRazorpayWebhook(handler, eventID, raw, "test-webhook-secret")
		}(handler)
	}
	wait.Wait()
	close(results)
	for status := range results {
		if status != http.StatusNoContent {
			t.Fatalf("concurrent delivery returned %d", status)
		}
	}

	// Recreate the API again to prove deduplication is database-backed rather
	// than process memory. Worker/API process restarts cannot reset this record.
	recreated := app.NewAPI(pool, nil, nil)
	recreated.ConfigureRazorpay(provider)
	if status := serveRazorpayWebhook(recreated.Handler(), eventID, raw, "test-webhook-secret"); status != http.StatusNoContent {
		t.Fatalf("replay after API recreation returned %d", status)
	}
	assertProviderEventCount(t, pool, eventID, 1)
}

func TestRazorpayWebhookOrderingAndBusinessIdempotency(t *testing.T) {
	tests := []struct {
		name string
		send func(*testing.T, *testkit.HTTP, testkit.PaymentOrderFixture, string)
	}{
		{name: "different event IDs for one capture", send: func(t *testing.T, h *testkit.HTTP, order testkit.PaymentOrderFixture, paymentID string) {
			sendCapture(t, h, order, paymentID, testkit.Unique("evt_capture_first"))
			sendCapture(t, h, order, paymentID, testkit.Unique("evt_capture_second"))
		}},
		{name: "order paid then payment captured", send: func(t *testing.T, h *testkit.HTTP, order testkit.PaymentOrderFixture, paymentID string) {
			config := order.Checkout.ClientConfig
			if status := h.RazorpayWebhook(t, "/v1/payments/webhook", testkit.RazorpayOrderPaid(testkit.Unique("evt_order_paid"), config.OrderID, paymentID, config.AmountMinor, config.Currency), "test-webhook-secret"); status != http.StatusNoContent {
				t.Fatalf("order.paid returned %d", status)
			}
			sendCapture(t, h, order, paymentID, testkit.Unique("evt_capture"))
		}},
		{name: "payment captured then order paid", send: func(t *testing.T, h *testkit.HTTP, order testkit.PaymentOrderFixture, paymentID string) {
			sendCapture(t, h, order, paymentID, testkit.Unique("evt_capture"))
			config := order.Checkout.ClientConfig
			if status := h.RazorpayWebhook(t, "/v1/payments/webhook", testkit.RazorpayOrderPaid(testkit.Unique("evt_order_paid"), config.OrderID, paymentID, config.AmountMinor, config.Currency), "test-webhook-secret"); status != http.StatusNoContent {
				t.Fatalf("order.paid returned %d", status)
			}
		}},
		{name: "failed then captured", send: func(t *testing.T, h *testkit.HTTP, order testkit.PaymentOrderFixture, paymentID string) {
			config := order.Checkout.ClientConfig
			if status := h.RazorpayWebhook(t, "/v1/payments/webhook", testkit.RazorpayPaymentFailed(testkit.Unique("evt_failed"), config.OrderID, paymentID, config.AmountMinor, config.Currency), "test-webhook-secret"); status != http.StatusNoContent {
				t.Fatalf("payment.failed returned %d", status)
			}
			sendCapture(t, h, order, paymentID, testkit.Unique("evt_capture"))
		}},
		{name: "captured then stale failure", send: func(t *testing.T, h *testkit.HTTP, order testkit.PaymentOrderFixture, paymentID string) {
			sendCapture(t, h, order, paymentID, testkit.Unique("evt_capture"))
			config := order.Checkout.ClientConfig
			if status := h.RazorpayWebhook(t, "/v1/payments/webhook", testkit.RazorpayPaymentFailed(testkit.Unique("evt_failed"), config.OrderID, paymentID, config.AmountMinor, config.Currency), "test-webhook-secret"); status != http.StatusNoContent {
				t.Fatalf("stale payment.failed returned %d", status)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := testkit.NewHTTP(t)
			org := testkit.Unique("webhook-ordering")
			token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
			testkit.CreateFixtureAccount(t, h, org, token)
			order := testkit.CreateCreditPackOrder(t, h, "daybook", 500, token)
			paymentID := testkit.Unique("pay")
			test.send(t, h, order, paymentID)
			assertCapturedOnce(t, e2ePool(t), order.Checkout.ClientConfig.OrderID)
		})
	}
}

func postRazorpayWebhook(t *testing.T, h *testkit.HTTP, eventID string, raw []byte, signature string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.BaseURL+"/v1/payments/webhook", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if eventID != "" {
		req.Header.Set("X-Razorpay-Event-Id", eventID)
	}
	if signature != "" {
		req.Header.Set("X-Razorpay-Signature", signature)
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func serveRazorpayWebhook(handler http.Handler, eventID string, raw []byte, secret string) int {
	req := httptest.NewRequest(http.MethodPost, "/v1/payments/webhook", bytes.NewReader(raw))
	req.Header.Set("X-Razorpay-Event-Id", eventID)
	req.Header.Set("X-Razorpay-Signature", testkit.RazorpayWebhookSignature(raw, secret))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response.Code
}

func assertProviderEventCount(t *testing.T, pool *pgxpool.Pool, eventID string, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM provider_events WHERE provider='razorpay' AND provider_event_id=$1`, eventID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("provider event %q count = %d, want %d", eventID, got, want)
	}
}

func sendCapture(t *testing.T, h *testkit.HTTP, order testkit.PaymentOrderFixture, paymentID, eventID string) {
	t.Helper()
	config := order.Checkout.ClientConfig
	if status := h.RazorpayWebhook(t, "/v1/payments/webhook", testkit.RazorpayPaymentCaptured(eventID, config.OrderID, paymentID, config.AmountMinor, config.Currency), "test-webhook-secret"); status != http.StatusNoContent {
		t.Fatalf("payment.captured returned %d", status)
	}
}

func assertCapturedOnce(t *testing.T, pool *pgxpool.Pool, orderID string) {
	t.Helper()
	var status string
	var grants, ledger, invoices int
	err := pool.QueryRow(context.Background(), `SELECT p.status,
		(SELECT count(*) FROM credit_grants WHERE operation_ref='payment:'||p.id),
		(SELECT count(*) FROM credit_ledger WHERE operation_ref='grant:payment:'||p.id),
		(SELECT count(*) FROM invoices WHERE payment_id=p.id)
		FROM payments p WHERE p.provider_order_id=$1`, orderID).Scan(&status, &grants, &ledger, &invoices)
	if err != nil {
		t.Fatal(err)
	}
	if status != "captured" || grants != 1 || ledger != 1 || invoices != 1 {
		t.Fatal(fmt.Sprintf("payment state = %s, grants=%d ledger=%d invoices=%d", status, grants, ledger, invoices))
	}
}
