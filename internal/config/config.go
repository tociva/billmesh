package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr              string
	DatabaseURL           string
	OIDCIssuer            string
	OIDCAudience          string
	JWKSURL               string
	WebhookTimeout        time.Duration
	WorkerInterval        time.Duration
	AuthFailuresPerMinute int
	MutationRatePerSecond int
	MutationBurst         int
	BFF                   BFFConfig
}

type BFFConfig struct {
	Enabled                bool
	AppOrigin              string
	Issuer                 string
	ClientID               string
	ClientSecret           string
	Audience               string
	Scope                  string
	RedirectURI            string
	PostLogoutRedirectURI  string
	StandaloneLogoutURI    string
	SessionEncryptionKeys  string
	SessionIdleTTL         time.Duration
	SessionAbsoluteTTL     time.Duration
	LoginTTL               time.Duration
	LogoutTTL              time.Duration
	RefreshSkew            time.Duration
	ReturnPathPrefixes     []string
	LoginAttemptsPerMinute int
	AllowInsecureHTTP      bool
}

func Load() (Config, error) {
	c := Config{
		HTTPAddr:     value("HTTP_ADDR", ":8080"),
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		OIDCIssuer:   os.Getenv("OIDC_ISSUER"),
		OIDCAudience: os.Getenv("OIDC_AUDIENCE"),
		JWKSURL:      os.Getenv("JWKS_URL"),
	}
	if c.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	parsed, err := url.Parse(c.DatabaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Hostname() == "" {
		return Config{}, errors.New("DATABASE_URL must be a valid PostgreSQL URL")
	}
	if port := parsed.Port(); port != "" {
		if value, err := strconv.Atoi(port); err != nil || value < 1 || value > 65535 {
			return Config{}, errors.New("DATABASE_URL contains an invalid port")
		}
	}
	if _, _, err := net.SplitHostPort(parsed.Host); err != nil && parsed.Port() != "" {
		return Config{}, fmt.Errorf("DATABASE_URL host: %w", err)
	}
	if c.WebhookTimeout, err = duration("WEBHOOK_TIMEOUT", 5*time.Second); err != nil {
		return Config{}, err
	}
	if c.WorkerInterval, err = duration("WORKER_INTERVAL", time.Second); err != nil {
		return Config{}, err
	}
	if c.AuthFailuresPerMinute, err = positiveInt("AUTH_FAILURES_PER_MINUTE", 30); err != nil {
		return Config{}, err
	}
	if c.MutationRatePerSecond, err = positiveInt("MUTATION_RATE_PER_SECOND", 10); err != nil {
		return Config{}, err
	}
	if c.MutationBurst, err = positiveInt("MUTATION_BURST", 50); err != nil {
		return Config{}, err
	}
	if c.BFF, err = loadBFF(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func loadBFF() (BFFConfig, error) {
	enabled, err := strconv.ParseBool(value("BFF_ENABLED", "false"))
	if err != nil {
		return BFFConfig{}, errors.New("BFF_ENABLED must be true or false")
	}
	c := BFFConfig{
		Enabled:               enabled,
		AppOrigin:             os.Getenv("BFF_APP_ORIGIN"),
		Issuer:                os.Getenv("BFF_ISSUER"),
		ClientID:              os.Getenv("BFF_CLIENT_ID"),
		ClientSecret:          os.Getenv("BFF_CLIENT_SECRET"),
		Audience:              os.Getenv("BFF_AUDIENCE"),
		Scope:                 value("BFF_SCOPE", "openid profile email offline_access"),
		RedirectURI:           os.Getenv("BFF_REDIRECT_URI"),
		PostLogoutRedirectURI: os.Getenv("BFF_POST_LOGOUT_REDIRECT_URI"),
		StandaloneLogoutURI:   os.Getenv("BFF_STANDALONE_LOGOUT_URI"),
		SessionEncryptionKeys: os.Getenv("BFF_SESSION_ENCRYPTION_KEYS"),
	}
	settings := []struct {
		key      string
		target   *time.Duration
		fallback time.Duration
	}{
		{"BFF_SESSION_IDLE_TTL", &c.SessionIdleTTL, 12 * time.Hour},
		{"BFF_SESSION_ABSOLUTE_TTL", &c.SessionAbsoluteTTL, 7 * 24 * time.Hour},
		{"BFF_LOGIN_TTL", &c.LoginTTL, 5 * time.Minute},
		{"BFF_LOGOUT_TTL", &c.LogoutTTL, 2 * time.Minute},
		{"BFF_REFRESH_SKEW", &c.RefreshSkew, time.Minute},
	}
	for _, setting := range settings {
		*setting.target, err = duration(setting.key, setting.fallback)
		if err != nil {
			return BFFConfig{}, err
		}
	}
	prefixes := value("BFF_RETURN_PATH_PREFIXES", "/")
	for _, prefix := range strings.Split(prefixes, ",") {
		if prefix = strings.TrimSpace(prefix); prefix != "" {
			c.ReturnPathPrefixes = append(c.ReturnPathPrefixes, prefix)
		}
	}
	if enabled {
		for name, field := range map[string]string{
			"BFF_APP_ORIGIN": c.AppOrigin, "BFF_ISSUER": c.Issuer, "BFF_CLIENT_ID": c.ClientID,
			"BFF_CLIENT_SECRET": c.ClientSecret, "BFF_AUDIENCE": c.Audience, "BFF_REDIRECT_URI": c.RedirectURI,
			"BFF_POST_LOGOUT_REDIRECT_URI": c.PostLogoutRedirectURI, "BFF_SESSION_ENCRYPTION_KEYS": c.SessionEncryptionKeys,
		} {
			if strings.TrimSpace(field) == "" {
				return BFFConfig{}, fmt.Errorf("%s is required when BFF_ENABLED=true", name)
			}
		}
		if c.SessionAbsoluteTTL < c.SessionIdleTTL {
			return BFFConfig{}, errors.New("BFF_SESSION_ABSOLUTE_TTL must be at least BFF_SESSION_IDLE_TTL")
		}
	}
	if c.LoginAttemptsPerMinute, err = positiveInt("BFF_LOGIN_ATTEMPTS_PER_MINUTE", 30); err != nil {
		return BFFConfig{}, err
	}
	if c.AllowInsecureHTTP, err = strconv.ParseBool(value("BFF_ALLOW_INSECURE_HTTP", "false")); err != nil {
		return BFFConfig{}, errors.New("BFF_ALLOW_INSECURE_HTTP must be true or false")
	}
	return c, nil
}

func positiveInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
}

func value(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func duration(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return d, nil
}
