package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tociva/billmesh/internal/config"
)

func TestRazorpayDisabledDoesNotAffectNonPaymentHealthEndpoint(t *testing.T) {
	api := NewAPI(nil, nil, nil)
	api.ConfigureRazorpay(config.RazorpayConfig{})
	if api.paymentProviderConfigured() {
		t.Fatal("payment provider is configured without credentials")
	}
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"ok"`) {
		t.Fatalf("health response = %d %s", response.Code, response.Body.String())
	}
}

func TestConfigureRazorpayAppliesValidatedProviderValues(t *testing.T) {
	provider := config.RazorpayConfig{
		Enabled: true, Environment: "development", BaseURL: "https://api.razorpay.com",
		KeyID: "rzp_test_billmesh", KeySecret: "provider-secret", WebhookSecret: "webhook-secret",
	}
	api := NewAPI(nil, nil, nil)
	api.ConfigureRazorpay(provider)
	if !api.paymentProviderConfigured() {
		t.Fatal("validated payment provider was not configured")
	}
	if api.providerBaseURL != provider.BaseURL || api.providerPublicKey != provider.KeyID || api.providerSecret != provider.KeySecret || api.webhookSecret != provider.WebhookSecret {
		t.Fatal("API did not receive the validated Razorpay configuration")
	}
}

func TestReadinessPayloadReportsPaymentCapabilityWithoutCredentials(t *testing.T) {
	api := NewAPI(nil, nil, nil)
	disabled := api.readinessPayload()
	disabledRaw, err := json.Marshal(disabled)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(disabledRaw), `"razorpay_checkout":"disabled"`) {
		t.Fatalf("disabled readiness payload = %s", disabledRaw)
	}

	provider := config.RazorpayConfig{
		Enabled: true, Environment: "development", BaseURL: "https://api.razorpay.com",
		KeyID: "rzp_test_billmesh", KeySecret: "provider-secret", WebhookSecret: "webhook-secret",
	}
	api.ConfigureRazorpay(provider)
	enabledRaw, err := json.Marshal(api.readinessPayload())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(enabledRaw), `"razorpay_checkout":"enabled"`) {
		t.Fatalf("enabled readiness payload = %s", enabledRaw)
	}
	for _, secret := range []string{provider.KeyID, provider.KeySecret, provider.WebhookSecret} {
		if strings.Contains(string(enabledRaw), secret) {
			t.Fatalf("readiness payload exposed credential %q: %s", secret, enabledRaw)
		}
	}
}
