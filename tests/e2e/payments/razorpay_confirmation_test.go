//go:build e2e

package payments_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/tociva/billmesh/tests/testkit"
)

func TestRazorpayCheckoutConfirmationValidationScopeAndReplay(t *testing.T) {
	h, pool, _, _, order := captureOrderFixture(t, "checkout-confirmation")
	config := order.Checkout.ClientConfig
	providerPaymentID := testkit.Unique("pay_confirmation")
	valid := testkit.RazorpayCheckoutResult(config.OrderID, providerPaymentID, "test-provider-secret")

	cases := []struct {
		name string
		body any
		want int
	}{
		{name: "missing result", body: map[string]any{}, want: http.StatusBadRequest},
		{name: "malformed signature", body: map[string]any{"razorpay_payment_id": providerPaymentID, "razorpay_order_id": config.OrderID, "razorpay_signature": "not-hex"}, want: http.StatusUnauthorized},
		{name: "wrong signature", body: testkit.RazorpayCheckoutResult(config.OrderID, providerPaymentID, "wrong-secret"), want: http.StatusUnauthorized},
		{name: "browser order mismatch", body: map[string]any{"razorpay_payment_id": providerPaymentID, "razorpay_order_id": "order_browser_tampered", "razorpay_signature": valid.RazorpaySignature}, want: http.StatusBadRequest},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			status, _, _ := h.JSON(t, http.MethodPost, "/v1/payments/"+order.PaymentID+"/confirm", test.body, h.Token)
			if status != test.want {
				t.Fatalf("confirmation returned %d, want %d", status, test.want)
			}
		})
	}

	foreignOrg := testkit.Unique("foreign-confirmation")
	foreignToken := h.IssueToken(t, foreignOrg, "daybook", testkit.AllPermissions(), nil)
	testkit.CreateFixtureAccount(t, h, foreignOrg, foreignToken)
	if status, _, _ := h.JSON(t, http.MethodPost, "/v1/payments/"+order.PaymentID+"/confirm", valid, foreignToken); status != http.StatusNotFound {
		t.Fatalf("foreign confirmation returned %d", status)
	}

	addRazorpayPayment(t, h, map[string]any{
		"id": providerPaymentID, "order_id": config.OrderID, "status": "captured",
		"amount": config.AmountMinor, "currency": config.Currency,
	})
	status, raw, _ := h.JSON(t, http.MethodPost, "/v1/payments/"+order.PaymentID+"/confirm", valid, h.Token)
	if status != http.StatusOK {
		t.Fatalf("valid confirmation returned %d: %s", status, raw)
	}
	assertCapturedOnce(t, pool, config.OrderID)
	var confirmed bool
	if err := pool.QueryRow(context.Background(), `SELECT checkout_confirmed_at IS NOT NULL AND checkout_confirmation_hash IS NOT NULL FROM payments WHERE id=$1`, order.PaymentID).Scan(&confirmed); err != nil {
		t.Fatal(err)
	}
	if !confirmed {
		t.Fatal("successful confirmation was not recorded")
	}
	if status, _, _ := h.JSON(t, http.MethodPost, "/v1/payments/"+order.PaymentID+"/confirm", valid, h.Token); status != http.StatusConflict {
		t.Fatalf("replayed confirmation returned %d", status)
	}
}

func TestRazorpayCheckoutConfirmationDoesNotGrantBeforeCapture(t *testing.T) {
	h, pool, _, _, order := captureOrderFixture(t, "checkout-processing")
	config := order.Checkout.ClientConfig
	providerPaymentID := testkit.Unique("pay_authorized_confirmation")
	addRazorpayPayment(t, h, map[string]any{
		"id": providerPaymentID, "order_id": config.OrderID, "status": "authorized",
		"amount": config.AmountMinor, "currency": config.Currency,
	})
	result := testkit.RazorpayCheckoutResult(config.OrderID, providerPaymentID, "test-provider-secret")
	status, raw, _ := h.JSON(t, http.MethodPost, "/v1/payments/"+order.PaymentID+"/confirm", result, h.Token)
	if status != http.StatusAccepted || !containsJSONStatus(raw, "processing") {
		t.Fatalf("authorized confirmation returned %d: %s", status, raw)
	}
	assertUnfulfilledPayment(t, pool, order.PaymentID, "unused-confirmation-event", false)
	var confirmed bool
	if err := pool.QueryRow(context.Background(), `SELECT checkout_confirmed_at IS NOT NULL FROM payments WHERE id=$1`, order.PaymentID).Scan(&confirmed); err != nil {
		t.Fatal(err)
	}
	if confirmed {
		t.Fatal("non-captured checkout consumed confirmation replay protection")
	}
}

func TestRazorpayCheckoutConfirmationRacesWebhookIdempotently(t *testing.T) {
	h, pool, _, _, order := captureOrderFixture(t, "checkout-webhook-race")
	config := order.Checkout.ClientConfig
	providerPaymentID := testkit.Unique("pay_confirmation_race")
	addRazorpayPayment(t, h, map[string]any{
		"id": providerPaymentID, "order_id": config.OrderID, "status": "captured",
		"amount": config.AmountMinor, "currency": config.Currency,
	})
	result := testkit.RazorpayCheckoutResult(config.OrderID, providerPaymentID, "test-provider-secret")
	event := testkit.RazorpayPaymentCaptured(testkit.Unique("evt_confirmation_race"), config.OrderID, providerPaymentID, config.AmountMinor, config.Currency)

	var wait sync.WaitGroup
	statuses := make(chan int, 2)
	wait.Add(2)
	go func() {
		defer wait.Done()
		status, _, _ := h.JSON(t, http.MethodPost, "/v1/payments/"+order.PaymentID+"/confirm", result, h.Token)
		statuses <- status
	}()
	go func() {
		defer wait.Done()
		statuses <- h.RazorpayWebhook(t, "/v1/payments/webhook", event, "test-webhook-secret")
	}()
	wait.Wait()
	close(statuses)
	seenOK, seenNoContent := false, false
	for status := range statuses {
		seenOK = seenOK || status == http.StatusOK
		seenNoContent = seenNoContent || status == http.StatusNoContent
	}
	if !seenOK || !seenNoContent {
		t.Fatalf("race statuses did not converge: ok=%t no_content=%t", seenOK, seenNoContent)
	}
	assertCapturedOnce(t, pool, config.OrderID)
}

func TestRazorpayCheckoutConfirmationRacesReconciliationIdempotently(t *testing.T) {
	h, pool, _, _, order := captureOrderFixture(t, "checkout-reconciliation-race")
	setRazorpayBehavior(t, h, map[string]any{"delay_ms": 750})
	t.Cleanup(func() { setRazorpayBehavior(t, h, map[string]any{}) })
	config := order.Checkout.ClientConfig
	providerPaymentID := testkit.Unique("pay_confirmation_reconciliation")
	addRazorpayPayment(t, h, map[string]any{
		"id": providerPaymentID, "order_id": config.OrderID, "status": "captured",
		"amount": config.AmountMinor, "currency": config.Currency,
	})
	if _, err := pool.Exec(context.Background(), `UPDATE payments SET last_reconciled_at=now()-interval '1 hour',created_at=now()-interval '1 hour' WHERE id=$1`, order.PaymentID); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		var workerStarted bool
		if err := pool.QueryRow(context.Background(), `SELECT COALESCE(last_reconciled_at > now()-interval '2 seconds',false) FROM payments WHERE id=$1`, order.PaymentID).Scan(&workerStarted); err != nil {
			t.Fatal(err)
		}
		if workerStarted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not start provider reconciliation")
		}
		time.Sleep(50 * time.Millisecond)
	}

	result := testkit.RazorpayCheckoutResult(config.OrderID, providerPaymentID, "test-provider-secret")
	status, raw, _ := h.JSON(t, http.MethodPost, "/v1/payments/"+order.PaymentID+"/confirm", result, h.Token)
	if status != http.StatusOK {
		t.Fatalf("racing confirmation returned %d: %s", status, raw)
	}
	setRazorpayBehavior(t, h, map[string]any{})

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var reconciled int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM provider_events WHERE provider_event_id=$1`, "reconciliation:"+providerPaymentID).Scan(&reconciled); err != nil {
			t.Fatal(err)
		}
		if reconciled == 1 {
			assertCapturedOnce(t, pool, config.OrderID)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("reconciliation did not converge after checkout confirmation")
}

func containsJSONStatus(raw []byte, status string) bool {
	var result struct {
		Status string `json:"status"`
	}
	return json.Unmarshal(raw, &result) == nil && result.Status == status
}
