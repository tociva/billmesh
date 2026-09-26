package foundation_test

import (
	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/internal/config"
	"testing"
)

func TestFND001RequiredEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	_, err := config.Load()
	require.Error(t, err)
}
func TestFND002InvalidConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "://invalid")
	_, err := config.Load()
	require.Error(t, err)
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/billmesh")
	t.Setenv("WORKER_INTERVAL", "not-a-duration")
	_, err = config.Load()
	require.Error(t, err)
}
