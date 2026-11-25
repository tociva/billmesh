package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadRazorpayDisabledWhenAllValuesAreOmitted(t *testing.T) {
	clearRazorpayEnvironment(t)
	t.Setenv("BILLMESH_ENVIRONMENT", "development")

	config, err := loadRazorpay()
	require.NoError(t, err)
	require.False(t, config.Enabled)
	require.Equal(t, "development", config.Environment)
	require.Empty(t, config.BaseURL)
	require.Empty(t, config.KeyID)
	require.Empty(t, config.KeySecret)
	require.Empty(t, config.WebhookSecret)
}

func TestLoadRazorpayRejectsPartialConfiguration(t *testing.T) {
	values := map[string]string{
		"RAZORPAY_BASE_URL":       "https://api.razorpay.com",
		"RAZORPAY_KEY_ID":         "rzp_test_billmesh",
		"RAZORPAY_KEY_SECRET":     "provider-secret",
		"RAZORPAY_WEBHOOK_SECRET": "webhook-secret",
	}
	for missing := range values {
		t.Run(missing, func(t *testing.T) {
			clearRazorpayEnvironment(t)
			t.Setenv("BILLMESH_ENVIRONMENT", "development")
			for key, value := range values {
				if key != missing {
					t.Setenv(key, value)
				}
			}
			_, err := loadRazorpay()
			require.ErrorContains(t, err, missing)
			require.NotContains(t, err.Error(), "provider-secret")
			require.NotContains(t, err.Error(), "webhook-secret")
		})
	}
}

func TestLoadRazorpayAcceptsModalConfigurationWithoutCheckoutURL(t *testing.T) {
	setRazorpayEnvironment(t, "development", "rzp_test_billmesh")
	t.Setenv("RAZORPAY_CHECKOUT_URL", "")

	config, err := loadRazorpay()
	require.NoError(t, err)
	require.True(t, config.Enabled)
	require.Empty(t, config.CheckoutURL)
	require.Equal(t, "https://api.razorpay.com", config.BaseURL)
}

func TestLoadRazorpayRejectsInvalidURLs(t *testing.T) {
	setRazorpayEnvironment(t, "production", "rzp_live_billmesh")
	for key, value := range map[string]string{
		"RAZORPAY_BASE_URL":     "http://api.razorpay.com",
		"RAZORPAY_CHECKOUT_URL": "http://checkout.razorpay.com",
	} {
		t.Run(key, func(t *testing.T) {
			setRazorpayEnvironment(t, "production", "rzp_live_billmesh")
			t.Setenv(key, value)
			_, err := loadRazorpay()
			require.ErrorContains(t, err, key)
			require.ErrorContains(t, err, "HTTPS")
		})
	}
}

func TestLoadRazorpayRejectsEnvironmentKeyMismatch(t *testing.T) {
	for _, test := range []struct {
		environment string
		keyID       string
		want        string
	}{
		{environment: "production", keyID: "rzp_test_billmesh", want: "rzp_live_"},
		{environment: "development", keyID: "rzp_live_billmesh", want: "rzp_test_"},
		{environment: "staging", keyID: "rzp_live_billmesh", want: "rzp_test_"},
		{environment: "test", keyID: "rzp_live_billmesh", want: "rzp_test_"},
	} {
		t.Run(test.environment, func(t *testing.T) {
			setRazorpayEnvironment(t, test.environment, test.keyID)
			_, err := loadRazorpay()
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestLoadRazorpayRejectsUnknownRuntimeEnvironment(t *testing.T) {
	setRazorpayEnvironment(t, "mystery", "rzp_test_billmesh")
	_, err := loadRazorpay()
	require.ErrorContains(t, err, "BILLMESH_ENVIRONMENT")
}

func TestRazorpayConfigStringRedactsSecrets(t *testing.T) {
	config := RazorpayConfig{
		Enabled: true, Environment: "development", BaseURL: "https://api.razorpay.com",
		KeyID: "rzp_test_billmesh", KeySecret: "provider-secret-value", WebhookSecret: "webhook-secret-value",
	}
	formatted := fmt.Sprintf("%s", config)
	require.Contains(t, formatted, "enabled=true")
	require.Contains(t, formatted, "environment=development")
	require.NotContains(t, formatted, config.KeySecret)
	require.NotContains(t, formatted, config.WebhookSecret)
}

func TestRazorpayConfigStructuredLogValueRedactsSecrets(t *testing.T) {
	config := RazorpayConfig{
		Enabled: true, Environment: "development", BaseURL: "https://api.razorpay.com",
		KeyID: "rzp_test_billmesh", KeySecret: "provider-secret-value", WebhookSecret: "webhook-secret-value",
	}
	var output bytes.Buffer
	slog.New(slog.NewJSONHandler(&output, nil)).Info("Razorpay configured", "razorpay", config)

	logged := output.String()
	require.Contains(t, logged, config.KeyID)
	require.Contains(t, logged, "[redacted]")
	require.NotContains(t, logged, config.KeySecret)
	require.NotContains(t, logged, config.WebhookSecret)
}

func TestLoadIncludesRazorpayConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	setRazorpayEnvironment(t, "development", "rzp_test_billmesh")
	config, err := Load()
	require.NoError(t, err)
	require.True(t, config.Razorpay.Enabled)
	require.True(t, strings.HasPrefix(config.Razorpay.KeyID, "rzp_test_"))
}

func setRazorpayEnvironment(t *testing.T, environment, keyID string) {
	t.Helper()
	clearRazorpayEnvironment(t)
	t.Setenv("BILLMESH_ENVIRONMENT", environment)
	t.Setenv("RAZORPAY_BASE_URL", "https://api.razorpay.com")
	t.Setenv("RAZORPAY_KEY_ID", keyID)
	t.Setenv("RAZORPAY_KEY_SECRET", "provider-secret-value")
	t.Setenv("RAZORPAY_WEBHOOK_SECRET", "webhook-secret-value")
}

func clearRazorpayEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"RAZORPAY_BASE_URL", "RAZORPAY_KEY_ID", "RAZORPAY_KEY_SECRET",
		"RAZORPAY_CHECKOUT_URL", "RAZORPAY_WEBHOOK_SECRET",
	} {
		t.Setenv(key, "")
	}
}
