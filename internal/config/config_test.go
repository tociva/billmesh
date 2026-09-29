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
