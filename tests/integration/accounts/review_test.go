//go:build integration

package accounts_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestACC010SameOrganizationIDRemainsDistinctAcrossApplications(t *testing.T) {
	ctx := context.Background()
	pool := testkit.Database(t)
	organizationID := "shared-org-" + uuid.NewString()
	var first, second uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO billing_accounts(name,external_ref) VALUES('Daybook',$1) RETURNING id`, uuid.NewString()).Scan(&first))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO billing_accounts(name,external_ref) VALUES('Taskmesh',$1) RETURNING id`, uuid.NewString()).Scan(&second))
	_, err := pool.Exec(ctx, `INSERT INTO account_links(account_id,application,organization_id,environment) VALUES($1,'daybook',$3,'production'),($2,'taskmesh',$3,'production')`, first, second, organizationID)
	require.NoError(t, err)
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(DISTINCT account_id) FROM account_links WHERE organization_id=$1`, organizationID).Scan(&count))
	require.Equal(t, 2, count)
}
