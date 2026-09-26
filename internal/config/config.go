package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr       string
	DatabaseURL    string
	OIDCIssuer     string
	OIDCAudience   string
	JWKSURL        string
	WebhookTimeout time.Duration
	WorkerInterval time.Duration
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
	return c, nil
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
