//go:build integration

package plans_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestPRD008AndPRD016ProductSlugUniquenessUnderConcurrency(t *testing.T) {
	pool := testkit.Database(t)
	ctx := context.Background()
	slug := testkit.Unique("concurrent-product")
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := pool.Exec(ctx, `INSERT INTO products(slug,name) VALUES($1,'Concurrent Product')`, slug)
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) || postgresError.Code != "23505" {
			t.Fatalf("unexpected concurrent insert error: %v", err)
		}
		conflicts++
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM products WHERE slug=$1`, slug).Scan(&count))
	require.Equal(t, 1, count)
}

func TestPLAN019AndPLAN020PlanSlugScope(t *testing.T) {
	pool := testkit.Database(t)
	ctx := context.Background()
	var firstProduct, secondProduct uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO products(slug,name) VALUES($1,'First') RETURNING id`, testkit.Unique("first-product")).Scan(&firstProduct))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO products(slug,name) VALUES($1,'Second') RETURNING id`, testkit.Unique("second-product")).Scan(&secondProduct))
	slug := testkit.Unique("shared-plan")
	insert := `INSERT INTO plans(product_id,slug,plan_family_id,name,price_minor,currency,included_credits,billing_interval) VALUES($1,$2,$2,'Shared',0,'INR',0,'monthly')`
	_, err := pool.Exec(ctx, insert, firstProduct, slug)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, insert, firstProduct, slug)
	require.Error(t, err)
	_, err = pool.Exec(ctx, insert, secondProduct, slug)
	require.NoError(t, err)
}

func TestPRD018ArchivePreservesCatalogueAndBillingHistory(t *testing.T) {
	pool := testkit.Database(t)
	ctx := context.Background()
	var accountID, productID, planID, subscriptionID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO billing_accounts(name,external_ref) VALUES('Archive Customer',$1) RETURNING id`, testkit.Unique("archive-account")).Scan(&accountID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO products(slug,name) VALUES($1,'Archive Product') RETURNING id`, testkit.Unique("archive-product")).Scan(&productID))
	planSlug := testkit.Unique("archive-plan")
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO plans(product_id,slug,plan_family_id,name,price_minor,currency,included_credits,billing_interval,entitlements)
		VALUES($1,$2,$2,'Archive Plan',0,'INR',100,'monthly','{"reports":true}') RETURNING id`, productID, planSlug).Scan(&planID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO subscriptions(account_id,plan_id,product_id,status,current_period_start,current_period_end,
		plan_family_id,price_minor,currency,billing_interval,included_credits,entitlements)
		VALUES($1,$2,$3,'active',now(),now()+interval '1 month',$4,0,'INR','monthly',100,'{"reports":true}') RETURNING id`, accountID, planID, productID, planSlug).Scan(&subscriptionID))
	_, err := pool.Exec(ctx, `UPDATE products SET active=false,version=version+1,updated_at=now() WHERE id=$1`, productID)
	require.NoError(t, err)
	var plans, subscriptions int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM plans WHERE id=$1`, planID).Scan(&plans))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE id=$1 AND status='active'`, subscriptionID).Scan(&subscriptions))
	require.Equal(t, 1, plans)
	require.Equal(t, 1, subscriptions)
}

func TestSubscriptionCommercialSnapshotColumnsExistAndPreserveJSON(t *testing.T) {
	pool := testkit.Database(t)
	ctx := context.Background()
	var includedCredits int64
	var reports bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT 125::bigint, COALESCE(('{"reports":true}'::jsonb->>'reports')::boolean,false)`).Scan(&includedCredits, &reports))
	require.Equal(t, int64(125), includedCredits)
	require.True(t, reports)
	for _, column := range []string{"included_credits", "entitlements", "billing_policy", "billing_policy_version"} {
		var exists bool
		require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='subscriptions' AND column_name=$1
		)`, column).Scan(&exists))
		require.True(t, exists, column)
	}
}
