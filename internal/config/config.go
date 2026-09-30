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
	WebhookTimeout        time.Duration
	WorkerInterval        time.Duration
	AuthFailuresPerMinute int
	MutationRatePerSecond int
	MutationBurst         int
	BFF                   BrowserAuthConfig
}

type BrowserAuthConfig struct {
	Realms []BFFConfig
}

type BFFConfig struct {
	Realm                  string
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
	DefaultReturnPath      string
	ReturnPathPrefixes     []string
	LoginAttemptsPerMinute int
}

func Load() (Config, error) {
	databaseURL, err := loadDatabaseURL()
	if err != nil {
		return Config{}, err
	}
	c := Config{
		HTTPAddr:     value("HTTP_ADDR", ":8080"),
		DatabaseURL:  databaseURL,
		OIDCIssuer:   os.Getenv("OIDC_ISSUER"),
		OIDCAudience: os.Getenv("OIDC_AUDIENCE"),
	}
	parsed, err := url.Parse(databaseURL)
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
	if c.BFF, err = loadBrowserAuth(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func loadDatabaseURL() (string, error) {
	if databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL")); databaseURL != "" {
		return databaseURL, nil
	}

	required := []string{"DB_HOST", "DB_NAME", "DB_USER", "DB_PASSWORD"}
	for _, key := range required {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			return "", fmt.Errorf("DATABASE_URL or %s is required", strings.Join(required, ", "))
		}
	}

	port := value("DB_PORT", "5432")
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", errors.New("DB_PORT must be a valid TCP port")
	}
	sslMode := value("DB_SSLMODE", "require")
	validSSLModes := map[string]bool{
		"disable": true, "allow": true, "prefer": true, "require": true,
		"verify-ca": true, "verify-full": true,
	}
	if !validSSLModes[sslMode] {
		return "", errors.New("DB_SSLMODE must be disable, allow, prefer, require, verify-ca, or verify-full")
	}

	databaseURL := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD")),
		Host:   net.JoinHostPort(os.Getenv("DB_HOST"), port),
		Path:   os.Getenv("DB_NAME"),
	}
	query := databaseURL.Query()
	query.Set("sslmode", sslMode)
	databaseURL.RawQuery = query.Encode()
	return databaseURL.String(), nil
}

func loadBrowserAuth() (BrowserAuthConfig, error) {
	var err error
	commonKeys := os.Getenv("BFF_SESSION_ENCRYPTION_KEYS")
	loginAttempts, err := positiveInt("BFF_LOGIN_ATTEMPTS_PER_MINUTE", 30)
	if err != nil {
		return BrowserAuthConfig{}, err
	}
	configs := []BFFConfig{
		{
			Realm: "console", AppOrigin: os.Getenv("CONSOLE_APP_ORIGIN"),
			DefaultReturnPath: "/app", ReturnPathPrefixes: []string{"/app"},
		},
		{
			Realm: "admin", AppOrigin: os.Getenv("ADMIN_APP_ORIGIN"),
			DefaultReturnPath: "/app", ReturnPathPrefixes: []string{"/app"},
		},
	}
	for index := range configs {
		c := &configs[index]
		prefix := "BFF_" + strings.ToUpper(c.Realm)
		c.Issuer = os.Getenv(prefix + "_ISSUER")
		c.ClientID = os.Getenv(prefix + "_CLIENT_ID")
		c.ClientSecret = os.Getenv(prefix + "_CLIENT_SECRET")
		c.Audience = os.Getenv(prefix + "_AUDIENCE")
		c.Scope = value(prefix+"_SCOPE", "openid profile email offline_access")
		c.RedirectURI = os.Getenv(prefix + "_REDIRECT_URI")
		c.PostLogoutRedirectURI = os.Getenv(prefix + "_POST_LOGOUT_REDIRECT_URI")
		c.StandaloneLogoutURI = os.Getenv(prefix + "_STANDALONE_LOGOUT_URI")
		c.SessionEncryptionKeys = commonKeys
		c.LoginAttemptsPerMinute = loginAttempts
	}
	settings := []struct {
		key      string
		fallback time.Duration
	}{
		{"BFF_SESSION_IDLE_TTL", 12 * time.Hour},
		{"BFF_SESSION_ABSOLUTE_TTL", 7 * 24 * time.Hour},
		{"BFF_LOGIN_TTL", 5 * time.Minute},
		{"BFF_LOGOUT_TTL", 2 * time.Minute},
		{"BFF_REFRESH_SKEW", time.Minute},
	}
	durations := make(map[string]time.Duration, len(settings))
	for _, setting := range settings {
		durations[setting.key], err = duration(setting.key, setting.fallback)
		if err != nil {
			return BrowserAuthConfig{}, err
		}
	}
	for index := range configs {
		c := &configs[index]
		c.SessionIdleTTL = durations["BFF_SESSION_IDLE_TTL"]
		c.SessionAbsoluteTTL = durations["BFF_SESSION_ABSOLUTE_TTL"]
		c.LoginTTL = durations["BFF_LOGIN_TTL"]
		c.LogoutTTL = durations["BFF_LOGOUT_TTL"]
		c.RefreshSkew = durations["BFF_REFRESH_SKEW"]
		if c.SessionAbsoluteTTL < c.SessionIdleTTL {
			return BrowserAuthConfig{}, errors.New("BFF_SESSION_ABSOLUTE_TTL must be at least BFF_SESSION_IDLE_TTL")
		}
	}
	return BrowserAuthConfig{Realms: configs}, nil
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
