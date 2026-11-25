package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

func TestRazorpayWebhookSignatureUsesExactRawBodyAndStrictEncoding(t *testing.T) {
	raw := []byte(`{ "entity": "event", "event": "payment.captured" }`)
	compact := []byte(`{"entity":"event","event":"payment.captured"}`)
	signature := webhookTestSignature(raw, "current-secret")
	if !verifyRazorpayWebhookSignature(raw, signature, "current-secret", "", nil, time.Now()) {
		t.Fatal("exact raw body signature was rejected")
	}
	if verifyRazorpayWebhookSignature(compact, signature, "current-secret", "", nil, time.Now()) {
		t.Fatal("signature for differently serialized JSON was accepted")
	}
	invalid := []string{"", "xyz", signature[:63], signature + "0", fmt.Sprintf("%X", mustDecodeHex(t, signature))}
	for _, candidate := range invalid {
		if verifyRazorpayWebhookSignature(raw, candidate, "current-secret", "", nil, time.Now()) {
			t.Fatalf("invalid signature %q was accepted", candidate)
		}
	}
}

func TestRazorpayWebhookSignatureRotationHasExplicitGracePeriod(t *testing.T) {
	raw := []byte(`{"entity":"event"}`)
	signature := webhookTestSignature(raw, "previous-secret")
	expires := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	if !verifyRazorpayWebhookSignature(raw, signature, "current-secret", "previous-secret", &expires, expires.Add(-time.Nanosecond)) {
		t.Fatal("previous secret was rejected during grace period")
	}
	if verifyRazorpayWebhookSignature(raw, signature, "current-secret", "previous-secret", &expires, expires) {
		t.Fatal("previous secret was accepted after grace period")
	}
}

func TestParseRazorpayWebhookUsesAuthenticEnvelopeAndHeaderIdentity(t *testing.T) {
	raw := []byte(`{"entity":"event","account_id":"acc_expected","event":"payment.captured","contains":["payment"],"payload":{"payment":{"entity":{"id":"pay_1","entity":"payment","order_id":"order_1","status":"captured","amount":50100,"currency":"INR"}}},"created_at":1700000000}`)
	event, err := parseRazorpayWebhook("evt_from_header", raw, "acc_expected")
	if err != nil {
		t.Fatal(err)
	}
	if event.ID != "evt_from_header" || event.Type != "payment.captured" || event.PaymentID != "pay_1" || event.OrderID != "order_1" || event.Amount != 50100 || event.Currency != "INR" || !event.Supported {
		t.Fatalf("unexpected normalized event: %#v", event)
	}
	if _, err := parseRazorpayWebhook("evt_from_header", raw, "acc_other"); err == nil {
		t.Fatal("unexpected merchant account was accepted")
	}
}

func TestParseRazorpayWebhookExtractsSupportedEntitiesAndAcceptsUnsupportedEvents(t *testing.T) {
	tests := []struct {
		name, raw, paymentID, orderID, refundID string
	}{
		{name: "payment failed", paymentID: "pay_failed", orderID: "order_1", raw: `{"entity":"event","account_id":"acc_1","event":"payment.failed","payload":{"payment":{"entity":{"id":"pay_failed","entity":"payment","order_id":"order_1","status":"failed","amount":100,"currency":"INR"}}}}`},
		{name: "order paid", paymentID: "pay_paid", orderID: "order_2", raw: `{"entity":"event","account_id":"acc_1","event":"order.paid","payload":{"payment":{"entity":{"id":"pay_paid","entity":"payment","order_id":"order_2","status":"captured","amount":100,"currency":"INR"}},"order":{"entity":{"id":"order_2","entity":"order","status":"paid","amount":100,"currency":"INR"}}}}`},
		{name: "refund", paymentID: "pay_refund", refundID: "rfnd_1", raw: `{"entity":"event","account_id":"acc_1","event":"refund.processed","payload":{"refund":{"entity":{"id":"rfnd_1","entity":"refund","payment_id":"pay_refund","status":"processed","amount":50,"currency":"INR"}}}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event, err := parseRazorpayWebhook("evt_1", []byte(test.raw), "")
			if err != nil {
				t.Fatal(err)
			}
			if event.PaymentID != test.paymentID || event.OrderID != test.orderID || event.RefundID != test.refundID || !event.Supported {
				t.Fatalf("unexpected normalized event: %#v", event)
			}
		})
	}
	unsupported, err := parseRazorpayWebhook("evt_unknown", []byte(`{"entity":"event","account_id":"acc_1","event":"invoice.paid","payload":{}}`), "")
	if err != nil || unsupported.Supported || unsupported.Type != "invoice.paid" {
		t.Fatalf("signed unsupported event policy failed: %#v, %v", unsupported, err)
	}
}

func webhookTestSignature(raw []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

func mustDecodeHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
