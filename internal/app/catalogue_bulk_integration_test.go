//go:build integration

package app

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/tociva/billmesh/internal/auth"
	"github.com/tociva/billmesh/internal/database"
)

func TestCatalogueBulkCreateAndVersionedUpdateAreAtomic(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required")
	}
	claims := &auth.Claims{}
	claims.Subject = "admin:test"
	ctx := auth.WithClaims(context.Background(), claims)
	pool, err := database.Open(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	_, err = pool.Exec(ctx, `TRUNCATE product_delegation_configuration_events,delegation_client_profile_events,
		product_delegation_actors,product_delegation_configurations,delegation_client_profiles,audit_log,credit_packs,plans,products CASCADE`)
	require.NoError(t, err)

	api := NewAPI(pool, nil, nil)
	document := validBulkDocument()
	var created catalogueBulkResult
	require.NoError(t, pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		created, err = api.applyCatalogueBulk(ctx, tx, document)
		return err
	}))
	require.Equal(t, 1, created.Products.Created)
	require.Equal(t, 1, created.Plans.Created)
	require.Equal(t, 1, created.DelegatedAccess.Created)

	var profileCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM delegation_client_profiles WHERE application='daybook' AND enabled`).Scan(&profileCount))
	require.Equal(t, 4, profileCount)

	update := validBulkDocument()
	update.Products[0].Operation = "update"
	update.Products[0].ExpectedVersion = 1
	update.Products[0].Name = "Daybook Cloud"
	update.Products[0].DelegatedAccess[0].ExpectedVersion = 1
	update.Products[0].DelegatedAccess[0].Actors = []productDelegationActorInput{
		{ClientID: "daybook-billmesh-runtime-dev", Profile: "runtime", ActorType: "service", Enabled: true},
	}
	update.Products[0].Plans[0].Operation = "update"
	update.Products[0].Plans[0].ExpectedVersion = 1
	var updated catalogueBulkResult
	require.NoError(t, pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		updated, err = api.applyCatalogueBulk(ctx, tx, update)
		return err
	}))
	require.Equal(t, 1, updated.Products.Updated)
	require.Equal(t, 1, updated.Plans.Updated)
	require.Equal(t, 1, updated.DelegatedAccess.Updated)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM delegation_client_profiles WHERE application='daybook' AND enabled`).Scan(&profileCount))
	require.Equal(t, 1, profileCount)
	var scope, actorType string
	require.NoError(t, pool.QueryRow(ctx, `SELECT scope,actor_type FROM delegation_client_profiles WHERE application='daybook' AND enabled`).Scan(&scope, &actorType))
	require.Equal(t, "billmesh.runtime", scope)
	require.Equal(t, "service", actorType)

	stale := update
	stale.Products[0].Name = "Must Roll Back"
	require.Error(t, pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		_, err = api.applyCatalogueBulk(ctx, tx, stale)
		return err
	}))
	var name string
	require.NoError(t, pool.QueryRow(ctx, `SELECT name FROM products WHERE slug='daybook'`).Scan(&name))
	require.Equal(t, "Daybook Cloud", name)
}
