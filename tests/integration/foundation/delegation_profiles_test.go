//go:build integration

package foundation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/tociva/billmesh/tests/testkit"
)

func TestDelegationProfilesAreValidatedVersionedAndAudited(t *testing.T) {
	pool := testkit.Database(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	authorizer := testkit.Unique("profile-authorizer")
	actor := testkit.Unique("profile-actor")
	_, err = tx.Exec(ctx, `INSERT INTO delegation_client_profiles(
		authorizer_client_id,actor_client_id,scope,client_type,actor_type,application,environment
	) VALUES($1,$2,'billmesh.billing','billing','user','daybook','test')`, authorizer, actor)
	require.NoError(t, err)

	var revision int64
	require.NoError(t, tx.QueryRow(ctx, `SELECT revision FROM delegation_client_profiles
		WHERE authorizer_client_id=$1 AND actor_client_id=$2`, authorizer, actor).Scan(&revision))
	require.Equal(t, int64(1), revision)

	_, err = tx.Exec(ctx, `UPDATE delegation_client_profiles SET enabled=false
		WHERE authorizer_client_id=$1 AND actor_client_id=$2`, authorizer, actor)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRow(ctx, `SELECT revision FROM delegation_client_profiles
		WHERE authorizer_client_id=$1 AND actor_client_id=$2`, authorizer, actor).Scan(&revision))
	require.Equal(t, int64(2), revision)

	var events int
	require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FROM delegation_client_profile_events
		WHERE authorizer_client_id=$1 AND actor_client_id=$2
		AND changed_by=current_user
		AND action IN ('insert','update')
		AND (before_profile IS NOT NULL OR action='insert')
		AND after_profile IS NOT NULL`, authorizer, actor).Scan(&events))
	require.Equal(t, 2, events)

	_, err = tx.Exec(ctx, `INSERT INTO delegation_client_profiles(
		authorizer_client_id,actor_client_id,scope,client_type,actor_type,application,environment
	) VALUES($1,$2,'billmesh.admin','billing','user','daybook','test')`, authorizer, testkit.Unique("invalid-profile"))
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr))
	require.Equal(t, "23514", pgErr.Code)
}
