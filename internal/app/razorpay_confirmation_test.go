package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestRazorpayCheckoutSignatureUsesStoredOrderMessage(t *testing.T) {
	orderID, paymentID, secret := "order_server", "pay_checkout", "provider-secret"
	if got := razorpayCheckoutSignatureMessage(orderID, paymentID); got != "order_server|pay_checkout" {
		t.Fatalf("signature message = %q", got)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(orderID + "|" + paymentID))
	signature := hex.EncodeToString(mac.Sum(nil))
	if !verifyRazorpayCheckoutSignature(orderID, paymentID, signature, secret) {
		t.Fatal("valid checkout signature was rejected")
	}
	for _, candidate := range []string{"", signature[:63], "not-hex", signature + "0"} {
		if verifyRazorpayCheckoutSignature(orderID, paymentID, candidate, secret) {
			t.Fatalf("invalid signature %q was accepted", candidate)
		}
	}
	if verifyRazorpayCheckoutSignature("order_browser", paymentID, signature, secret) {
		t.Fatal("browser-supplied order changed the verified signature message")
	}
}
