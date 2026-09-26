package config

import (
	"errors"
	"os"
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
		HTTPAddr:       value("HTTP_ADDR", ":8080"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		OIDCIssuer:     os.Getenv("OIDC_ISSUER"),
		OIDCAudience:   os.Getenv("OIDC_AUDIENCE"),
		JWKSURL:        os.Getenv("JWKS_URL"),
		WebhookTimeout: duration("WEBHOOK_TIMEOUT", 5*time.Second),
		WorkerInterval: duration("WORKER_INTERVAL", time.Second),
	}
	if c.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	return c, nil
}

func value(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func duration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
