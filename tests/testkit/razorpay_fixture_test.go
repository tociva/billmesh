package testkit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRazorpayWebhookFixturesUseProviderWireContract(t *testing.T) {
	fixtures := []RazorpayWebhookFixture{
		RazorpayPaymentCaptured("evt_captured", "order_1", "pay_1", 50000, "INR"),
		RazorpayPaymentFailed("evt_failed", "order_1", "pay_2", 50000, "INR"),
		RazorpayOrderPaid("evt_paid", "order_1", "pay_1", 50000, "INR"),
		RazorpayRefundProcessed("evt_refund", "pay_1", "rfnd_1", 25000, "INR"),
	}
	for _, fixture := range fixtures {
		if fixture.EventID == "" || len(fixture.Raw) == 0 || !json.Valid(fixture.Raw) {
			t.Fatalf("invalid fixture: %+v", fixture)
		}
		var envelope map[string]any
		if err := json.Unmarshal(fixture.Raw, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope["entity"] != "event" || envelope["event"] == "" || envelope["account_id"] == "" {
			t.Fatalf("fixture does not match Razorpay event envelope: %s", fixture.Raw)
		}
		if _, exists := envelope["type"]; exists {
			t.Fatalf("fixture used Billmesh synthetic type field: %s", fixture.Raw)
		}
		if _, exists := envelope["id"]; exists {
			t.Fatalf("fixture placed the webhook event ID in the JSON body: %s", fixture.Raw)
		}
	}
}

func TestRecordedRazorpayWebhookFixtures(t *testing.T) {
	for _, name := range []string{"payment.captured", "payment.failed", "order.paid", "refund.processed"} {
		fixture := LoadRazorpayWebhookFixture(t, name, "evt_"+strings.ReplaceAll(name, ".", "_"))
		if fixture.EventID == "" || !json.Valid(fixture.Raw) {
			t.Fatalf("invalid recorded fixture %s", name)
		}
		var envelope map[string]any
		if err := json.Unmarshal(fixture.Raw, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope["event"] != name || envelope["entity"] != "event" {
			t.Fatalf("recorded fixture %s has unexpected envelope: %s", name, fixture.Raw)
		}
	}
}

func TestRazorpaySignatureUsesExactRawBody(t *testing.T) {
	raw := []byte("{\n  \"entity\": \"event\", \"event\": \"payment.captured\"\n}\n")
	secret := "webhook-secret-current"
	signature := RazorpayWebhookSignature(raw, secret)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)
	if signature != hex.EncodeToString(mac.Sum(nil)) {
		t.Fatalf("signature = %q, want exact raw-body HMAC", signature)
	}
	compact := []byte(`{"entity":"event","event":"payment.captured"}`)
	if RazorpayWebhookSignature(compact, secret) == signature {
		t.Fatal("reserialized JSON unexpectedly retained the raw-body signature")
	}
}

func TestRazorpayCheckoutResultSignature(t *testing.T) {
	result := RazorpayCheckoutResult("order_1", "pay_1", "provider-secret")
	if result.RazorpayOrderID != "order_1" || result.RazorpayPaymentID != "pay_1" || result.RazorpaySignature == "" {
		t.Fatalf("unexpected checkout result: %+v", result)
	}
	want := RazorpayWebhookSignature([]byte("order_1|pay_1"), "provider-secret")
	if result.RazorpaySignature != want {
		t.Fatalf("checkout signature = %q, want %q", result.RazorpaySignature, want)
	}
}

func TestRazorpayWebhookRequestPreservesRawBodyAndHeaders(t *testing.T) {
	raw := []byte("{\n  \"entity\":\"event\",\n  \"event\":\"payment.captured\"\n}\n")
	secret := "webhook-secret-current"
	var receivedBody []byte
	var receivedEventID, receivedSignature string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		receivedEventID = request.Header.Get("X-Razorpay-Event-Id")
		receivedSignature = request.Header.Get("X-Razorpay-Signature")
		receivedBody, _ = io.ReadAll(request.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	harness := &HTTP{BaseURL: server.URL, Client: server.Client()}
	if status := harness.RazorpayWebhookRaw(t, "/webhook", "evt_exact_raw", raw, secret); status != http.StatusNoContent {
		t.Fatalf("webhook status = %d", status)
	}
	if receivedEventID != "evt_exact_raw" || !json.Valid(receivedBody) || string(receivedBody) != string(raw) {
		t.Fatalf("request changed provider event: id=%q body=%q", receivedEventID, receivedBody)
	}
	if receivedSignature != RazorpayWebhookSignature(raw, secret) {
		t.Fatalf("signature = %q, want raw-body signature", receivedSignature)
	}
}

func TestRazorpaySecretRotationSignatures(t *testing.T) {
	raw := []byte(`{"entity":"event","event":"payment.captured"}`)
	rotation := RazorpaySecretRotation{Current: "current-secret", Previous: "previous-secret"}
	if rotation.CurrentSignature(raw) != RazorpayWebhookSignature(raw, rotation.Current) {
		t.Fatal("current-secret signature mismatch")
	}
	if rotation.PreviousSignature(raw) != RazorpayWebhookSignature(raw, rotation.Previous) {
		t.Fatal("previous-secret signature mismatch")
	}
	if rotation.CurrentSignature(raw) == rotation.PreviousSignature(raw) {
		t.Fatal("current and previous secrets produced the same signature")
	}
}

func TestRazorpayFixturesDoNotExposeSecrets(t *testing.T) {
	secrets := []string{"provider-secret-value", "webhook-secret-current", "webhook-secret-previous"}
	values := []any{
		RazorpayPaymentCaptured("evt_1", "order_1", "pay_1", 100, "INR"),
		RazorpayCheckoutResult("order_1", "pay_1", secrets[0]),
		RazorpaySecretRotation{Current: secrets[1], Previous: secrets[2]},
	}
	for index, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range secrets {
			if index < 2 && strings.Contains(string(raw), secret) {
				t.Fatalf("fixture %d exposed secret %q: %s", index, secret, raw)
			}
		}
	}
}
