//go:build integration

package reservations_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/internal/database"
	"github.com/tociva/billmesh/internal/wallets"
)

func TestConcurrentReservationsCannotOverspend(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not configured")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, url)
	require.NoError(t, err)
	defer pool.Close()
	_, _ = pool.Exec(ctx, `TRUNCATE audit_log,invoice_lines,invoices,webhook_deliveries,outbox_events,credit_ledger,reservation_allocations,reservations,credit_grants,wallets,subscriptions,plans,products,account_links,billing_accounts CASCADE`)
	var accountID, productID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO billing_accounts(name) VALUES('test') RETURNING id`).Scan(&accountID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO products(slug,name) VALUES('daybook','Daybook') RETURNING id`).Scan(&productID))
	svc := wallets.NewService(pool, nil)
	wallet, err := svc.CreateWallet(ctx, accountID, productID)
	require.NoError(t, err)
	require.NoError(t, svc.Grant(ctx, wallet.ID, "test", "grant-1", 100, nil))
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Reserve(ctx, wallet.ID, "execution-"+uuid.NewString(), i, 80, time.Minute)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	require.Equal(t, 1, successes)
	wallet, err = svc.Get(ctx, wallet.ID)
	require.NoError(t, err)
	require.Equal(t, int64(20), wallet.Available)
	require.Equal(t, int64(80), wallet.Reserved)
}

func TestSettlementIsIdempotent(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not configured")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, url)
	require.NoError(t, err)
	defer pool.Close()
	var accountID, productID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO billing_accounts(name) VALUES('settle') RETURNING id`).Scan(&accountID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO products(slug,name) VALUES($1,'Settle') RETURNING id`, "settle-"+uuid.NewString()).Scan(&productID))
	svc := wallets.NewService(pool, nil)
	wallet, _ := svc.CreateWallet(ctx, accountID, productID)
	require.NoError(t, svc.Grant(ctx, wallet.ID, "test", uuid.NewString(), 100, nil))
	reservation, err := svc.Reserve(ctx, wallet.ID, "run", 0, 80, time.Minute)
	require.NoError(t, err)
	_, err = svc.Settle(ctx, reservation.ID, 65)
	require.NoError(t, err)
	_, err = svc.Settle(ctx, reservation.ID, 65)
	require.NoError(t, err)
	wallet, _ = svc.Get(ctx, wallet.ID)
	require.Equal(t, int64(35), wallet.Available)
	require.Zero(t, wallet.Reserved)
}
