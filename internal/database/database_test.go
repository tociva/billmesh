package database

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRuntimeConnectionsUseBillmeshSchemaOnly(t *testing.T) {
	cfg, err := poolConfig("postgres://user:password@localhost:5432/billmesh?sslmode=disable&search_path=legacy")
	require.NoError(t, err)

	require.Equal(t, "billmesh", cfg.ConnConfig.RuntimeParams["search_path"])
}

func TestRuntimeDatabasePoolHasFiniteConnectionLimits(t *testing.T) {
	cfg, err := poolConfig("postgres://user:password@localhost:5432/billmesh?sslmode=disable")
	require.NoError(t, err)
	configurePoolLimits(cfg)

	require.Equal(t, int32(20), cfg.MaxConns)
	require.Equal(t, int32(2), cfg.MinConns)
	require.Equal(t, time.Hour, cfg.MaxConnLifetime)
	require.Equal(t, 15*time.Minute, cfg.MaxConnIdleTime)
}

func TestMigrationURLUsesBillmeshThenPublic(t *testing.T) {
	databaseURL, err := withSearchPath(
		"postgres://user:password@localhost:5432/billmesh?sslmode=disable&search_path=legacy",
		migrationSearchPath,
	)
	require.NoError(t, err)

	parsed, err := url.Parse(databaseURL)
	require.NoError(t, err)
	require.Equal(t, "disable", parsed.Query().Get("sslmode"))
	require.Equal(t, "billmesh,public", parsed.Query().Get("search_path"))
}
