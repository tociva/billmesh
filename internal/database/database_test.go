package database

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeConnectionsUseBillmeshSchemaOnly(t *testing.T) {
	cfg, err := poolConfig("postgres://user:password@localhost:5432/billmesh?sslmode=disable&search_path=legacy")
	require.NoError(t, err)

	require.Equal(t, "billmesh", cfg.ConnConfig.RuntimeParams["search_path"])
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
