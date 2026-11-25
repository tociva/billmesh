package app

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestRazorpayWebhookRejectsUnsafeTransportAndParserInputsBeforePersistence(t *testing.T) {
	api := NewAPI(nil, nil, slog.New(slog.NewTextHandler(ioDiscard{}, nil)))
	api.webhookSecret = "webhook-secret"
	malformed := []byte(`{"entity":`)

	tests := []struct {
		name       string
		eventIDs   []string
		signatures []string
		want       int
	}{
		{name: "missing event ID", signatures: []string{"00"}, want: http.StatusBadRequest},
		{name: "duplicate event ID", eventIDs: []string{"evt_one", "evt_two"}, signatures: []string{"00"}, want: http.StatusBadRequest},
		{name: "missing signature", eventIDs: []string{"evt_missing_signature"}, want: http.StatusUnauthorized},
		{name: "duplicate signature", eventIDs: []string{"evt_duplicate_signature"}, signatures: []string{"00", "11"}, want: http.StatusBadRequest},
		{name: "invalid signature precedes JSON and database work", eventIDs: []string{"evt_invalid_signature"}, signatures: []string{strings.Repeat("0", 64)}, want: http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/payments/webhook", bytes.NewReader(malformed))
			for _, value := range test.eventIDs {
				request.Header.Add("X-Razorpay-Event-Id", value)
			}
			for _, value := range test.signatures {
				request.Header.Add("X-Razorpay-Signature", value)
			}
			response := httptest.NewRecorder()
			api.paymentWebhook(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}

func TestParseRazorpayWebhookBoundsDepthEncodingAndAllowsForwardCompatibleFields(t *testing.T) {
	prefix := `{"entity":"event","account_id":"acc_1","event":"invoice.paid","payload":{},"future":`
	deep := []byte(prefix + strings.Repeat("[", razorpayWebhookMaxDepth) + "0" + strings.Repeat("]", razorpayWebhookMaxDepth) + "}")
	if _, err := parseRazorpayWebhook("evt_deep", deep, "acc_1"); err == nil || !strings.Contains(err.Error(), "nesting") {
		t.Fatalf("deep payload error = %v", err)
	}

	invalidUTF8 := append([]byte(prefix+`"`), 0xff)
	invalidUTF8 = append(invalidUTF8, []byte(`"}`)...)
	if _, err := parseRazorpayWebhook("evt_encoding", invalidUTF8, "acc_1"); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("invalid UTF-8 error = %v", err)
	}

	forwardCompatible := []byte(`{"entity":"event","account_id":"acc_1","event":"invoice.paid","payload":{},"future":{"token":"must-not-be-used"}}`)
	event, err := parseRazorpayWebhook("evt_future", forwardCompatible, "acc_1")
	if err != nil || event.Supported || event.ID != "evt_future" {
		t.Fatalf("forward-compatible event = %#v, error = %v", event, err)
	}
}

func TestRazorpayWebhookAdmissionLimitsRateAndConcurrentWork(t *testing.T) {
	api := NewAPI(nil, nil, slog.New(slog.NewTextHandler(ioDiscard{}, nil)))
	api.ConfigureWebhookLimits(1, 1, 1)
	request := func() *http.Request {
		return httptest.NewRequest(http.MethodPost, "/v1/payments/webhook", strings.NewReader("{}"))
	}
	first := httptest.NewRecorder()
	api.paymentWebhook(first, request())
	if first.Code != http.StatusBadRequest {
		t.Fatalf("first admitted status = %d", first.Code)
	}
	second := httptest.NewRecorder()
	api.paymentWebhook(second, request())
	if second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") != "1" {
		t.Fatalf("rate-limited status = %d headers = %v", second.Code, second.Header())
	}

	api.ConfigureWebhookLimits(100, 100, 1)
	api.webhookSlots <- struct{}{}
	defer func() { <-api.webhookSlots }()
	capacity := httptest.NewRecorder()
	api.paymentWebhook(capacity, request())
	if capacity.Code != http.StatusTooManyRequests {
		t.Fatalf("capacity status = %d", capacity.Code)
	}
}

func TestRazorpayWebhookLoggingIncludesSafeCorrelationOnly(t *testing.T) {
	var output bytes.Buffer
	api := NewAPI(nil, nil, slog.New(slog.NewJSONHandler(&output, nil)))
	event := normalizedRazorpayEvent{
		ID: "evt_safe", Type: "payment.captured", AccountID: "acc_safe",
		PaymentID: "pay_safe", OrderID: "order_safe", RefundID: "rfnd_safe", Supported: true,
	}
	api.logRazorpayWebhook("request-safe", event)
	logged := output.String()
	for _, wanted := range []string{"request-safe", "evt_safe", "pay_safe", "order_safe", "rfnd_safe"} {
		if !strings.Contains(logged, wanted) {
			t.Fatalf("safe reference %q missing from log: %s", wanted, logged)
		}
	}
	for _, forbidden := range []string{"card", "bank", "token", "signature", "secret"} {
		if strings.Contains(strings.ToLower(logged), forbidden) {
			t.Fatalf("forbidden field %q present in log: %s", forbidden, logged)
		}
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(value []byte) (int, error) { return len(value), nil }

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
