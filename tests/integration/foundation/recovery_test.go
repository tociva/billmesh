//go:build integration

package foundation_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestFND011RestoredDatabasePreservesFinancialState(t *testing.T) {
	ctx := context.Background()
	primary := testkit.Database(t)
	restoreURL := os.Getenv("BILLMESH_RESTORE_DATABASE_URL")
	if restoreURL == "" {
		t.Fatal("BILLMESH_RESTORE_DATABASE_URL is required for the backup/restore integration test")
	}
	restored, err := pgxpool.New(ctx, restoreURL)
	require.NoError(t, err)
	t.Cleanup(restored.Close)

	queries := []string{
		`SELECT count(*)::bigint,COALESCE(sum(version),0)::bigint FROM subscriptions`,
		`SELECT count(*)::bigint,COALESCE(sum(available_delta+reserved_delta),0)::bigint FROM credit_ledger`,
		`SELECT count(*)::bigint,COALESCE(sum(requested),0)::bigint FROM reservations WHERE status='reserved'`,
	}
	for _, query := range queries {
		var primaryCount, primaryValue, restoredCount, restoredValue int64
		require.NoError(t, primary.QueryRow(ctx, query).Scan(&primaryCount, &primaryValue))
		require.NoError(t, restored.QueryRow(ctx, query).Scan(&restoredCount, &restoredValue))
		require.Equal(t, primaryCount, restoredCount, query)
		require.Equal(t, primaryValue, restoredValue, query)
	}
}
