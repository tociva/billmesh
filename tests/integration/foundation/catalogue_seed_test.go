//go:build integration

package foundation_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestFreshMigrationsDoNotInstallCatalogueOffers(t *testing.T) {
	pool := testkit.Database(t)

	var plans, creditPacks int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM plans`).Scan(&plans))
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM credit_packs`).Scan(&creditPacks))
	require.Zero(t, plans)
	require.Zero(t, creditPacks)
}
