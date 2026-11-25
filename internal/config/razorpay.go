package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type RazorpayConfig struct {
	Enabled                        bool
	Environment                    string
	BaseURL                        string
	KeyID                          string
	KeySecret                      string
	CheckoutURL                    string
	WebhookSecret                  string
	AccountID                      string
	PreviousWebhookSecret          string
	PreviousWebhookSecretExpiresAt *time.Time
}

func (config RazorpayConfig) String() string {
	return fmt.Sprintf("enabled=%t environment=%s base_url=%s key_id=%s account_id=%s key_secret=[redacted] webhook_secret=[redacted] previous_webhook_secret=[redacted]",
		config.Enabled, config.Environment, config.BaseURL, config.KeyID, config.AccountID)
}

func (config RazorpayConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Bool("enabled", config.Enabled),
		slog.String("environment", config.Environment),
		slog.String("base_url", config.BaseURL),
		slog.String("key_id", config.KeyID),
		slog.String("account_id", config.AccountID),
		slog.String("key_secret", "[redacted]"),
		slog.String("webhook_secret", "[redacted]"),
		slog.String("previous_webhook_secret", "[redacted]"),
	)
}

func loadRazorpay() (RazorpayConfig, error) {
	environment := strings.ToLower(strings.TrimSpace(value("BILLMESH_ENVIRONMENT", "development")))
	validEnvironments := map[string]bool{
		"local": true, "test": true, "development": true, "staging": true, "production": true,
	}
	if !validEnvironments[environment] {
		return RazorpayConfig{}, fmt.Errorf("BILLMESH_ENVIRONMENT must be local, test, development, staging, or production")
	}

	values := map[string]string{
		"RAZORPAY_BASE_URL":       strings.TrimSpace(os.Getenv("RAZORPAY_BASE_URL")),
		"RAZORPAY_KEY_ID":         strings.TrimSpace(os.Getenv("RAZORPAY_KEY_ID")),
		"RAZORPAY_KEY_SECRET":     strings.TrimSpace(os.Getenv("RAZORPAY_KEY_SECRET")),
		"RAZORPAY_WEBHOOK_SECRET": strings.TrimSpace(os.Getenv("RAZORPAY_WEBHOOK_SECRET")),
	}
	checkoutURL := strings.TrimSpace(os.Getenv("RAZORPAY_CHECKOUT_URL"))
	accountID := strings.TrimSpace(os.Getenv("RAZORPAY_ACCOUNT_ID"))
	previousWebhookSecret := strings.TrimSpace(os.Getenv("RAZORPAY_PREVIOUS_WEBHOOK_SECRET"))
	previousWebhookSecretExpiresAtRaw := strings.TrimSpace(os.Getenv("RAZORPAY_PREVIOUS_WEBHOOK_SECRET_EXPIRES_AT"))
	present := 0
	missing := make([]string, 0, len(values))
	for key, configured := range values {
		if configured == "" {
			missing = append(missing, key)
		} else {
			present++
		}
	}
	if present == 0 {
		if checkoutURL != "" || accountID != "" || previousWebhookSecret != "" || previousWebhookSecretExpiresAtRaw != "" {
			return RazorpayConfig{}, fmt.Errorf("optional Razorpay settings require the complete Razorpay configuration")
		}
		return RazorpayConfig{Environment: environment}, nil
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return RazorpayConfig{}, fmt.Errorf("Razorpay configuration is incomplete; missing %s", strings.Join(missing, ", "))
	}
	if err := requireHTTPSURL(values["RAZORPAY_BASE_URL"], "RAZORPAY_BASE_URL"); err != nil {
		return RazorpayConfig{}, err
	}
	if checkoutURL != "" {
		if err := requireHTTPSURL(checkoutURL, "RAZORPAY_CHECKOUT_URL"); err != nil {
			return RazorpayConfig{}, err
		}
	}
	if (previousWebhookSecret == "") != (previousWebhookSecretExpiresAtRaw == "") {
		return RazorpayConfig{}, fmt.Errorf("RAZORPAY_PREVIOUS_WEBHOOK_SECRET and RAZORPAY_PREVIOUS_WEBHOOK_SECRET_EXPIRES_AT must be configured together")
	}
	var previousWebhookSecretExpiresAt *time.Time
	if previousWebhookSecretExpiresAtRaw != "" {
		parsed, err := time.Parse(time.RFC3339, previousWebhookSecretExpiresAtRaw)
		if err != nil {
			return RazorpayConfig{}, fmt.Errorf("RAZORPAY_PREVIOUS_WEBHOOK_SECRET_EXPIRES_AT must use RFC3339")
		}
		previousWebhookSecretExpiresAt = &parsed
	}
	expectedPrefix := "rzp_test_"
	if environment == "production" {
		expectedPrefix = "rzp_live_"
	}
	if !strings.HasPrefix(values["RAZORPAY_KEY_ID"], expectedPrefix) {
		return RazorpayConfig{}, fmt.Errorf("RAZORPAY_KEY_ID must start with %s in %s", expectedPrefix, environment)
	}
	return RazorpayConfig{
		Enabled: true, Environment: environment, BaseURL: strings.TrimRight(values["RAZORPAY_BASE_URL"], "/"),
		KeyID: values["RAZORPAY_KEY_ID"], KeySecret: values["RAZORPAY_KEY_SECRET"],
		CheckoutURL: strings.TrimRight(checkoutURL, "/"), WebhookSecret: values["RAZORPAY_WEBHOOK_SECRET"],
		AccountID: accountID, PreviousWebhookSecret: previousWebhookSecret,
		PreviousWebhookSecretExpiresAt: previousWebhookSecretExpiresAt,
	}, nil
}

func requireHTTPSURL(raw, name string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("%s must be an absolute HTTPS URL without embedded credentials", name)
	}
	return nil
}
