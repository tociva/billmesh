package testkit

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const RazorpayTestAccountID = "acc_billmesh_test"

type RazorpayWebhookFixture struct {
	EventID string          `json:"-"`
	Raw     json.RawMessage `json:"-"`
}

type RazorpayCheckoutResponse struct {
	RazorpayPaymentID string `json:"razorpay_payment_id"`
	RazorpayOrderID   string `json:"razorpay_order_id"`
	RazorpaySignature string `json:"razorpay_signature"`
}

type RazorpaySecretRotation struct {
	Current  string
	Previous string
}

type razorpayEventEnvelope struct {
	Entity    string                    `json:"entity"`
	AccountID string                    `json:"account_id"`
	Event     string                    `json:"event"`
	Contains  []string                  `json:"contains"`
	Payload   map[string]map[string]any `json:"payload"`
	CreatedAt int64                     `json:"created_at"`
}

func RazorpayPaymentCaptured(eventID, orderID, paymentID string, amount int64, currency string) RazorpayWebhookFixture {
	payment := razorpayPaymentEntity(orderID, paymentID, "captured", amount, currency)
	return razorpayFixture(eventID, "payment.captured", []string{"payment"}, map[string]map[string]any{"payment": {"entity": payment}})
}

func RazorpayPaymentFailed(eventID, orderID, paymentID string, amount int64, currency string) RazorpayWebhookFixture {
	payment := razorpayPaymentEntity(orderID, paymentID, "failed", amount, currency)
	return razorpayFixture(eventID, "payment.failed", []string{"payment"}, map[string]map[string]any{"payment": {"entity": payment}})
}

func RazorpayOrderPaid(eventID, orderID, paymentID string, amount int64, currency string) RazorpayWebhookFixture {
	payment := razorpayPaymentEntity(orderID, paymentID, "captured", amount, currency)
	order := map[string]any{
		"id": orderID, "entity": "order", "amount": amount, "amount_paid": amount,
		"amount_due": int64(0), "currency": currency, "receipt": "billmesh-test", "status": "paid",
	}
	return razorpayFixture(eventID, "order.paid", []string{"payment", "order"}, map[string]map[string]any{
		"payment": {"entity": payment}, "order": {"entity": order},
	})
}

func RazorpayRefundProcessed(eventID, paymentID, refundID string, amount int64, currency string) RazorpayWebhookFixture {
	refund := map[string]any{
		"id": refundID, "entity": "refund", "amount": amount, "currency": currency,
		"payment_id": paymentID, "status": "processed",
	}
	return razorpayFixture(eventID, "refund.processed", []string{"refund"}, map[string]map[string]any{"refund": {"entity": refund}})
}

func LoadRazorpayWebhookFixture(t testing.TB, name, eventID string) RazorpayWebhookFixture {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve Razorpay fixture path")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "testdata", "razorpay", name+".json"))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Razorpay fixture %s: %v", name, err)
	}
	if !json.Valid(raw) {
		t.Fatalf("Razorpay fixture %s is not valid JSON", name)
	}
	return RazorpayWebhookFixture{EventID: eventID, Raw: raw}
}

func RazorpayWebhookSignature(raw []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

func RazorpayCheckoutResult(orderID, paymentID, keySecret string) RazorpayCheckoutResponse {
	return RazorpayCheckoutResponse{
		RazorpayPaymentID: paymentID,
		RazorpayOrderID:   orderID,
		RazorpaySignature: RazorpayWebhookSignature([]byte(orderID+"|"+paymentID), keySecret),
	}
}

func (rotation RazorpaySecretRotation) CurrentSignature(raw []byte) string {
	return RazorpayWebhookSignature(raw, rotation.Current)
}

func (rotation RazorpaySecretRotation) PreviousSignature(raw []byte) string {
	return RazorpayWebhookSignature(raw, rotation.Previous)
}

func (h *HTTP) RazorpayWebhook(t testing.TB, path string, fixture RazorpayWebhookFixture, secret string) int {
	t.Helper()
	return h.RazorpayWebhookRaw(t, path, fixture.EventID, fixture.Raw, secret)
}

func (h *HTTP) RazorpayWebhookRaw(t testing.TB, path, eventID string, raw []byte, secret string) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, h.BaseURL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Razorpay-Event-Id", eventID)
	request.Header.Set("X-Razorpay-Signature", RazorpayWebhookSignature(raw, secret))
	response, err := h.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

func razorpayPaymentEntity(orderID, paymentID, status string, amount int64, currency string) map[string]any {
	return map[string]any{
		"id": paymentID, "entity": "payment", "amount": amount, "currency": currency,
		"status": status, "order_id": orderID,
	}
}

func razorpayFixture(eventID, event string, contains []string, payload map[string]map[string]any) RazorpayWebhookFixture {
	raw, err := json.Marshal(razorpayEventEnvelope{
		Entity: "event", AccountID: RazorpayTestAccountID, Event: event,
		Contains: contains, Payload: payload, CreatedAt: 1_700_000_000,
	})
	if err != nil {
		panic(err)
	}
	return RazorpayWebhookFixture{EventID: eventID, Raw: raw}
}
