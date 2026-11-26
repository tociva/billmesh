package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

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
	require.Empty(t, config.AccountID)
	require.Empty(t, config.PreviousWebhookSecret)
	require.Nil(t, config.PreviousWebhookSecretExpiresAt)
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

func TestLoadRazorpayRequiresProductionProviderIdentity(t *testing.T) {
	t.Run("valid live configuration", func(t *testing.T) {
		setRazorpayEnvironment(t, "production", "rzp_live_billmesh")

		config, err := loadRazorpay()

		require.NoError(t, err)
		require.Equal(t, razorpayProductionBaseURL, config.BaseURL)
		require.Equal(t, "acc_billmesh", config.AccountID)
	})

	t.Run("alternate provider endpoint", func(t *testing.T) {
		setRazorpayEnvironment(t, "production", "rzp_live_billmesh")
		t.Setenv("RAZORPAY_BASE_URL", "https://provider-proxy.example")

		_, err := loadRazorpay()

		require.ErrorContains(t, err, "production RAZORPAY_BASE_URL")
	})

	t.Run("missing merchant account", func(t *testing.T) {
		setRazorpayEnvironment(t, "production", "rzp_live_billmesh")
		t.Setenv("RAZORPAY_ACCOUNT_ID", "")

		_, err := loadRazorpay()

		require.ErrorContains(t, err, "RAZORPAY_ACCOUNT_ID")
	})

	t.Run("malformed merchant account", func(t *testing.T) {
		setRazorpayEnvironment(t, "production", "rzp_live_billmesh")
		t.Setenv("RAZORPAY_ACCOUNT_ID", "merchant_billmesh")

		_, err := loadRazorpay()

		require.ErrorContains(t, err, "RAZORPAY_ACCOUNT_ID")
	})
}

func TestLoadRazorpayRejectsSharedSecrets(t *testing.T) {
	t.Run("api and webhook", func(t *testing.T) {
		setRazorpayEnvironment(t, "development", "rzp_test_billmesh")
		t.Setenv("RAZORPAY_WEBHOOK_SECRET", "provider-secret-value")

		_, err := loadRazorpay()

		require.ErrorContains(t, err, "must be distinct")
		require.NotContains(t, err.Error(), "provider-secret-value")
	})

	t.Run("previous and current webhook", func(t *testing.T) {
		setRazorpayEnvironment(t, "development", "rzp_test_billmesh")
		t.Setenv("RAZORPAY_PREVIOUS_WEBHOOK_SECRET", "webhook-secret-value")
		t.Setenv("RAZORPAY_PREVIOUS_WEBHOOK_SECRET_EXPIRES_AT", "2099-01-02T03:04:05Z")

		_, err := loadRazorpay()

		require.ErrorContains(t, err, "must be distinct")
		require.NotContains(t, err.Error(), "webhook-secret-value")
	})

	t.Run("previous webhook and API", func(t *testing.T) {
		setRazorpayEnvironment(t, "development", "rzp_test_billmesh")
		t.Setenv("RAZORPAY_PREVIOUS_WEBHOOK_SECRET", "provider-secret-value")
		t.Setenv("RAZORPAY_PREVIOUS_WEBHOOK_SECRET_EXPIRES_AT", "2099-01-02T03:04:05Z")

		_, err := loadRazorpay()

		require.ErrorContains(t, err, "must be distinct")
		require.NotContains(t, err.Error(), "provider-secret-value")
	})
}

func TestLoadRazorpayRejectsUnknownRuntimeEnvironment(t *testing.T) {
	setRazorpayEnvironment(t, "mystery", "rzp_test_billmesh")
	_, err := loadRazorpay()
	require.ErrorContains(t, err, "BILLMESH_ENVIRONMENT")
}

func TestLoadRazorpayWebhookIdentityAndSecretRotation(t *testing.T) {
	setRazorpayEnvironment(t, "development", "rzp_test_billmesh")
	t.Setenv("RAZORPAY_ACCOUNT_ID", "acc_billmesh")
	t.Setenv("RAZORPAY_PREVIOUS_WEBHOOK_SECRET", "previous-secret-value")
	t.Setenv("RAZORPAY_PREVIOUS_WEBHOOK_SECRET_EXPIRES_AT", "2099-01-02T03:04:05Z")

	config, err := loadRazorpay()
	require.NoError(t, err)
	require.Equal(t, "acc_billmesh", config.AccountID)
	require.Equal(t, "previous-secret-value", config.PreviousWebhookSecret)
	require.Equal(t, "2099-01-02T03:04:05Z", config.PreviousWebhookSecretExpiresAt.Format(time.RFC3339))
}

func TestLoadRazorpayRejectsIncompleteOrMalformedSecretRotation(t *testing.T) {
	tests := []struct {
		name, secret, expires string
	}{
		{name: "missing expiry", secret: "previous-secret"},
		{name: "missing secret", expires: "2099-01-02T03:04:05Z"},
		{name: "malformed expiry", secret: "previous-secret", expires: "tomorrow"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setRazorpayEnvironment(t, "development", "rzp_test_billmesh")
			t.Setenv("RAZORPAY_PREVIOUS_WEBHOOK_SECRET", test.secret)
			t.Setenv("RAZORPAY_PREVIOUS_WEBHOOK_SECRET_EXPIRES_AT", test.expires)
			_, err := loadRazorpay()
			require.Error(t, err)
			if test.secret != "" {
				require.NotContains(t, err.Error(), test.secret)
			}
		})
	}
}

func TestRazorpayConfigStringRedactsSecrets(t *testing.T) {
	config := RazorpayConfig{
		Enabled: true, Environment: "development", BaseURL: "https://api.razorpay.com",
		KeyID: "rzp_test_billmesh", KeySecret: "provider-secret-value", WebhookSecret: "webhook-secret-value",
		PreviousWebhookSecret: "previous-webhook-secret-value",
	}
	formatted := fmt.Sprintf("%s", config)
	require.Contains(t, formatted, "enabled=true")
	require.Contains(t, formatted, "environment=development")
	require.NotContains(t, formatted, config.KeySecret)
	require.NotContains(t, formatted, config.WebhookSecret)
	require.NotContains(t, formatted, config.PreviousWebhookSecret)
}

func TestRazorpayConfigStructuredLogValueRedactsSecrets(t *testing.T) {
	config := RazorpayConfig{
		Enabled: true, Environment: "development", BaseURL: "https://api.razorpay.com",
		KeyID: "rzp_test_billmesh", KeySecret: "provider-secret-value", WebhookSecret: "webhook-secret-value",
		PreviousWebhookSecret: "previous-webhook-secret-value",
	}
	var output bytes.Buffer
	slog.New(slog.NewJSONHandler(&output, nil)).Info("Razorpay configured", "razorpay", config)

	logged := output.String()
	require.Contains(t, logged, config.KeyID)
	require.Contains(t, logged, "[redacted]")
	require.NotContains(t, logged, config.KeySecret)
	require.NotContains(t, logged, config.WebhookSecret)
	require.NotContains(t, logged, config.PreviousWebhookSecret)
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
	if environment == "production" {
		t.Setenv("RAZORPAY_ACCOUNT_ID", "acc_billmesh")
	}
}

func clearRazorpayEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"RAZORPAY_BASE_URL", "RAZORPAY_KEY_ID", "RAZORPAY_KEY_SECRET",
		"RAZORPAY_CHECKOUT_URL", "RAZORPAY_WEBHOOK_SECRET",
		"RAZORPAY_ACCOUNT_ID", "RAZORPAY_PREVIOUS_WEBHOOK_SECRET", "RAZORPAY_PREVIOUS_WEBHOOK_SECRET_EXPIRES_AT",
	} {
		t.Setenv(key, "")
	}
}
