package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("WORKER_INTERVAL", "250ms")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, ":8080", cfg.HTTPAddr)
	require.Equal(t, 250*time.Millisecond, cfg.WorkerInterval)
	require.Equal(t, 30, cfg.AuthFailuresPerMinute)
	require.Equal(t, 10, cfg.MutationRatePerSecond)
	require.Equal(t, 50, cfg.MutationBurst)
}

func TestLoadRejectsInvalidRequestLimits(t *testing.T) {
	for _, key := range []string{"AUTH_FAILURES_PER_MINUTE", "MUTATION_RATE_PER_SECOND", "MUTATION_BURST"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://example")
			t.Setenv(key, "0")
			_, err := Load()
			require.ErrorContains(t, err, key)
		})
	}
}
func TestLoadRequiresDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	_, err := Load()
	require.Error(t, err)
}

func TestLoadBFFConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("BFF_ENABLED", "true")
	t.Setenv("BFF_APP_ORIGIN", "https://billmesh.example")
	t.Setenv("BFF_ISSUER", "https://identity.example")
	t.Setenv("BFF_CLIENT_ID", "billmesh-web")
	t.Setenv("BFF_CLIENT_SECRET", "secret")
	t.Setenv("BFF_AUDIENCE", "billmesh")
	t.Setenv("BFF_REDIRECT_URI", "https://billmesh.example/auth/callback")
	t.Setenv("BFF_POST_LOGOUT_REDIRECT_URI", "https://billmesh.example/auth/logout/callback")
	t.Setenv("BFF_SESSION_ENCRYPTION_KEYS", "v1:key")
	t.Setenv("BFF_RETURN_PATH_PREFIXES", "/app,/admin")
	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.BFF.Enabled)
	require.Equal(t, []string{"/app", "/admin"}, cfg.BFF.ReturnPathPrefixes)
	require.Equal(t, 30, cfg.BFF.LoginAttemptsPerMinute)
}

func TestLoadBFFRequiresSecretsAndValidLifetimes(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("BFF_ENABLED", "true")
	_, err := Load()
	require.ErrorContains(t, err, "required")

	t.Setenv("BFF_APP_ORIGIN", "https://billmesh.example")
	t.Setenv("BFF_ISSUER", "https://identity.example")
	t.Setenv("BFF_CLIENT_ID", "billmesh-web")
	t.Setenv("BFF_CLIENT_SECRET", "secret")
	t.Setenv("BFF_AUDIENCE", "billmesh")
	t.Setenv("BFF_REDIRECT_URI", "https://billmesh.example/auth/callback")
	t.Setenv("BFF_POST_LOGOUT_REDIRECT_URI", "https://billmesh.example/auth/logout/callback")
	t.Setenv("BFF_SESSION_ENCRYPTION_KEYS", "v1:key")
	t.Setenv("BFF_SESSION_IDLE_TTL", "2h")
	t.Setenv("BFF_SESSION_ABSOLUTE_TTL", "1h")
	_, err = Load()
	require.ErrorContains(t, err, "must be at least")
}
