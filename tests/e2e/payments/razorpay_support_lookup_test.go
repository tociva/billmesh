//go:build e2e

package payments_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tociva/billmesh/internal/app"
	appconfig "github.com/tociva/billmesh/internal/config"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestRazorpaySupportLookupCorrelatesSafeReferencesAndAuditsAccess(t *testing.T) {
	h, pool, _, _, order := captureOrderFixture(t, "support-lookup")
	config := order.Checkout.ClientConfig
	providerPaymentID := testkit.Unique("pay_support")
	eventID := testkit.Unique("evt_support")
	requestID := testkit.Unique("request-support")
	fixture := testkit.RazorpayPaymentCaptured(eventID, config.OrderID, providerPaymentID, config.AmountMinor, config.Currency)

	request, err := http.NewRequest(http.MethodPost, h.BaseURL+"/v1/payments/webhook", bytes.NewReader(fixture.Raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Request-ID", requestID)
	request.Header.Set("X-Razorpay-Event-Id", eventID)
	request.Header.Set("X-Razorpay-Signature", testkit.RazorpayWebhookSignature(fixture.Raw, "test-webhook-secret"))
	response, err := h.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("capture status = %d", response.StatusCode)
	}

	lookups := map[string]string{
		"payment_id": order.PaymentID, "provider_order_id": config.OrderID,
		"provider_payment_id": providerPaymentID, "event_id": eventID, "request_id": requestID,
	}
	for key, value := range lookups {
		status, raw, _ := h.JSON(t, http.MethodGet, "/v1/admin/payments/lookup?"+key+"="+url.QueryEscape(value), nil, h.Token)
		if status != http.StatusOK {
			t.Fatalf("lookup %s returned %d: %s", key, status, raw)
		}
		for _, expected := range []string{order.PaymentID, config.OrderID, providerPaymentID, eventID, requestID} {
			if !bytes.Contains(raw, []byte(expected)) {
				t.Fatalf("lookup %s lacks %s: %s", key, expected, raw)
			}
		}
		lower := strings.ToLower(string(raw))
		for _, forbidden := range []string{"raw_payload", "razorpay_signature", "key_secret", "webhook_secret", "card", "bank", "token"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("lookup %s exposed %s: %s", key, forbidden, raw)
			}
		}
	}

	var audits int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action='payment.support.lookup' AND resource_id=$1`, order.PaymentID).Scan(&audits); err != nil || audits != len(lookups) {
		t.Fatalf("support lookup audits = %d, error = %v", audits, err)
	}

	if status, _, _ := h.JSON(t, http.MethodGet, "/v1/admin/payments/lookup?event_id="+eventID+"&request_id="+requestID, nil, h.Token); status != http.StatusBadRequest {
		t.Fatalf("ambiguous lookup status = %d", status)
	}
	foreign := h.IssueToken(t, testkit.Unique("support-foreign"), "daybook", []string{"billing:read"}, nil)
	if status, _, _ := h.JSON(t, http.MethodGet, "/v1/admin/payments/lookup?payment_id="+order.PaymentID, nil, foreign); status != http.StatusForbidden {
		t.Fatalf("non-admin support lookup status = %d", status)
	}

	if _, err := pool.Exec(context.Background(), `UPDATE provider_events SET created_at=now()-interval '31 days' WHERE provider_event_id=$1`, eventID); err != nil {
		t.Fatal(err)
	}
	worker := app.NewWorker(pool, h.Client, time.Hour, nil)
	worker.ConfigureRazorpay(appconfig.RazorpayConfig{Environment: "test"})
	worker.ConfigureOperationalRetention(30 * 24 * time.Hour)
	if err := worker.RunOperationalMaintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	var rawPurged, normalized bool
	var digest string
	if err := pool.QueryRow(context.Background(), `SELECT raw_payload IS NULL,payload->>'retained'='false',payload_sha256
		FROM provider_events WHERE provider_event_id=$1`, eventID).Scan(&rawPurged, &normalized, &digest); err != nil {
		t.Fatal(err)
	}
	if !rawPurged || !normalized || len(digest) != 64 {
		t.Fatalf("retention state raw_purged=%t normalized=%t digest=%q", rawPurged, normalized, digest)
	}
	if status := h.RazorpayWebhook(t, "/v1/payments/webhook", fixture, "test-webhook-secret"); status != http.StatusNoContent {
		t.Fatalf("replay after raw-payload retention returned %d", status)
	}
}
