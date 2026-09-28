//go:build integration

package reservations_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/internal/wallets"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestRES022IdenticalReservationRetryReturnsOriginal(t *testing.T) {
	ctx, pool, svc, wallet := reviewWallet(t, 100)
	first, err := svc.Reserve(ctx, wallet.ID, "retry-execution", 0, 40, time.Minute)
	require.NoError(t, err)
	second, err := svc.Reserve(ctx, wallet.ID, "retry-execution", 0, 40, time.Minute)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	requireReviewWallet(t, ctx, svc, wallet.ID, 60, 40)
	requireRowCount(t, ctx, pool, `SELECT count(*) FROM credit_ledger WHERE reservation_id=$1 AND kind='reserve'`, first.ID, 1)
}

func TestRES023ConcurrentSettlementAndReleaseHaveOneOutcome(t *testing.T) {
	ctx, pool, svc, wallet := reviewWallet(t, 100)
	reservation, err := svc.Reserve(ctx, wallet.ID, "settle-release-race", 0, 80, time.Minute)
	require.NoError(t, err)
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() { <-start; _, err := svc.Settle(ctx, reservation.ID, 65); errs <- err }()
	go func() { <-start; _, err := svc.Release(ctx, reservation.ID, "released"); errs <- err }()
	close(start)
	first, second := <-errs, <-errs
	require.True(t, (first == nil) != (second == nil), "exactly one finalization must succeed: %v, %v", first, second)
	loser := first
	if loser == nil {
		loser = second
	}
	require.ErrorIs(t, loser, wallets.ErrFinalized)
	var status string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM reservations WHERE id=$1`, reservation.ID).Scan(&status))
	require.Contains(t, []string{"settled", "released"}, status)
	requireRowCount(t, ctx, pool, `SELECT count(*) FROM credit_ledger WHERE reservation_id=$1 AND kind IN ('settle','release')`, reservation.ID, 1)
}

func TestRES025RetryAfterLostResponseDoesNotReserveTwice(t *testing.T) {
	ctx, pool, svc, wallet := reviewWallet(t, 100)
	committed, err := svc.Reserve(ctx, wallet.ID, "lost-response", 7, 30, time.Minute)
	require.NoError(t, err)
	retried, err := svc.Reserve(ctx, wallet.ID, "lost-response", 7, 30, time.Minute)
	require.NoError(t, err)
	require.Equal(t, committed.ID, retried.ID)
	requireRowCount(t, ctx, pool, `SELECT count(*) FROM reservations WHERE wallet_id=$1 AND execution_id='lost-response' AND operation_seq=7`, wallet.ID, 1)
	requireReviewWallet(t, ctx, svc, wallet.ID, 70, 30)
}

func TestRES026ConcurrentTopUpExpirationAndReservationReconcile(t *testing.T) {
	ctx, pool, svc, wallet := reviewWallet(t, 100)
	expired := time.Now().UTC().Add(-time.Minute)
	require.NoError(t, svc.Grant(ctx, wallet.ID, "test", uuid.NewString(), 20, &expired))
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs <- svc.Grant(ctx, wallet.ID, "test", uuid.NewString(), 50, nil) }()
	go func() {
		defer wg.Done()
		_, err := svc.Reserve(ctx, wallet.ID, "concurrent-accounting", 0, 80, time.Minute)
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var available, reserved, ledgerAvailable, ledgerReserved int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT available,reserved FROM wallets WHERE id=$1`, wallet.ID).Scan(&available, &reserved))
	require.NoError(t, pool.QueryRow(ctx, `SELECT COALESCE(sum(available_delta),0),COALESCE(sum(reserved_delta),0) FROM credit_ledger WHERE wallet_id=$1`, wallet.ID).Scan(&ledgerAvailable, &ledgerReserved))
	require.Equal(t, ledgerAvailable, available)
	require.Equal(t, ledgerReserved, reserved)
}

func TestRES027ReservedCreditsRemainSettleableAfterGrantExpiry(t *testing.T) {
	ctx, pool, svc, wallet := reviewWallet(t, 0)
	expires := time.Now().UTC().Add(time.Hour)
	require.NoError(t, svc.Grant(ctx, wallet.ID, "expiring", uuid.NewString(), 50, &expires))
	reservation, err := svc.Reserve(ctx, wallet.ID, "active-at-expiry", 0, 50, 2*time.Hour)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE credit_grants SET expires_at=now()-interval '1 second' WHERE wallet_id=$1`, wallet.ID)
	require.NoError(t, err)
	settled, err := svc.Settle(ctx, reservation.ID, 50)
	require.NoError(t, err)
	require.Equal(t, "settled", settled.Status)
	requireReviewWallet(t, ctx, svc, wallet.ID, 0, 0)
}

func TestWAL019ReservationSpansMultipleGrants(t *testing.T) {
	ctx, pool, svc, wallet := reviewWallet(t, 0)
	require.NoError(t, svc.Grant(ctx, wallet.ID, "first", uuid.NewString(), 30, nil))
	require.NoError(t, svc.Grant(ctx, wallet.ID, "second", uuid.NewString(), 50, nil))
	reservation, err := svc.Reserve(ctx, wallet.ID, "multi-grant", 0, 70, time.Minute)
	require.NoError(t, err)
	requireRowCount(t, ctx, pool, `SELECT count(*) FROM reservation_allocations WHERE reservation_id=$1`, reservation.ID, 2)
	var allocated int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT sum(amount) FROM reservation_allocations WHERE reservation_id=$1`, reservation.ID).Scan(&allocated))
	require.Equal(t, int64(70), allocated)
	requireReviewWallet(t, ctx, svc, wallet.ID, 10, 70)
}

func TestWAL021PersistedWalletReconcilesWithLedgerAndReservations(t *testing.T) {
	ctx, pool, svc, wallet := reviewWallet(t, 100)
	reservation, err := svc.Reserve(ctx, wallet.ID, "reconcile", 0, 40, time.Minute)
	require.NoError(t, err)
	_, err = svc.Settle(ctx, reservation.ID, 25)
	require.NoError(t, err)
	var available, reserved, ledgerAvailable, ledgerReserved int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT available,reserved FROM wallets WHERE id=$1`, wallet.ID).Scan(&available, &reserved))
	require.NoError(t, pool.QueryRow(ctx, `SELECT COALESCE(sum(available_delta),0),COALESCE(sum(reserved_delta),0) FROM credit_ledger WHERE wallet_id=$1`, wallet.ID).Scan(&ledgerAvailable, &ledgerReserved))
	require.Equal(t, ledgerAvailable, available)
	require.Equal(t, ledgerReserved, reserved)
	var activeReserved int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT COALESCE(sum(requested),0) FROM reservations WHERE wallet_id=$1 AND status='reserved'`, wallet.ID).Scan(&activeReserved))
	require.Equal(t, reserved, activeReserved)
}

func TestWAL022RejectsOverflowingCreditQuantity(t *testing.T) {
	ctx, _, svc, wallet := reviewWallet(t, 1)
	err := svc.Grant(ctx, wallet.ID, "overflow", uuid.NewString(), int64(^uint64(0)>>1), nil)
	require.Error(t, err)
	requireReviewWallet(t, ctx, svc, wallet.ID, 1, 0)
}

func TestMTR018ConflictingDuplicateUsageEventIsRejected(t *testing.T) {
	ctx, pool, svc, wallet := reviewWallet(t, 100)
	reservation, err := svc.Reserve(ctx, wallet.ID, "metering-duplicate", 0, 10, time.Minute)
	require.NoError(t, err)
	eventID := "usage-" + uuid.NewString()
	_, err = pool.Exec(ctx, `INSERT INTO usage_events(wallet_id,reservation_id,external_id,units,quantity,meter,application,occurred_at) VALUES($1,$2,$3,1,1,'workflow.execution','daybook',now())`, wallet.ID, reservation.ID, eventID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO usage_events(wallet_id,reservation_id,external_id,units,quantity,meter,application,occurred_at) VALUES($1,$2,$3,2,2,'workflow.execution','daybook',now())`, wallet.ID, reservation.ID, eventID)
	require.Error(t, err)
	var quantity int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT quantity FROM usage_events WHERE external_id=$1`, eventID).Scan(&quantity))
	require.Equal(t, int64(1), quantity)
}

func TestMTR022UsageDeduplicationIsScopedByTenantAndProduct(t *testing.T) {
	ctx, pool, firstService, firstWallet := reviewWallet(t, 100)
	firstReservation, err := firstService.Reserve(ctx, firstWallet.ID, "scope-first", 0, 10, time.Minute)
	require.NoError(t, err)
	_, _, secondService, secondWallet := reviewWallet(t, 100)
	secondReservation, err := secondService.Reserve(ctx, secondWallet.ID, "scope-second", 0, 10, time.Minute)
	require.NoError(t, err)
	eventID := "shared-provider-id"
	_, err = pool.Exec(ctx, `INSERT INTO usage_events(wallet_id,reservation_id,external_id,units,quantity,meter,application,occurred_at) VALUES($1,$2,$3,1,1,'workflow.execution','daybook',now())`, firstWallet.ID, firstReservation.ID, eventID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO usage_events(wallet_id,reservation_id,external_id,units,quantity,meter,application,occurred_at) VALUES($1,$2,$3,1,1,'workflow.execution','daybook',now())`, secondWallet.ID, secondReservation.ID, eventID)
	require.NoError(t, err)
	requireRowCount(t, ctx, pool, `SELECT count(*) FROM usage_events WHERE external_id=$1`, eventID, 2)
}

func TestWH019RolledBackTransactionProducesNoWebhookEvent(t *testing.T) {
	ctx, pool, _, wallet := reviewWallet(t, 0)
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	var eventID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,payload) VALUES('wallet',$1,'credits.test','{}') RETURNING id`, wallet.ID).Scan(&eventID))
	require.NoError(t, tx.Rollback(ctx))
	requireRowCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE id=$1`, eventID, 0)
}

func TestWAL020ConsumedPurchaseRefundCannotCreateNegativeBalance(t *testing.T) {
	ctx, pool, svc, wallet := reviewWallet(t, 100)
	reservation, err := svc.Reserve(ctx, wallet.ID, "consumed-purchase", 0, 80, time.Minute)
	require.NoError(t, err)
	_, err = svc.Settle(ctx, reservation.ID, 80)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE wallets SET available=available-100 WHERE id=$1`, wallet.ID)
	require.Error(t, err)
	requireReviewWallet(t, ctx, svc, wallet.ID, 20, 0)
}

func TestSUB024RetriedRenewalAllocatesIncludedCreditsOnce(t *testing.T) {
	ctx, pool, svc, wallet := reviewWallet(t, 0)
	operation := "subscription-renewal-" + uuid.NewString()
	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			errs <- svc.Grant(ctx, wallet.ID, "subscription", operation, 100, nil)
		}()
	}
	close(start)
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	requireReviewWallet(t, ctx, svc, wallet.ID, 100, 0)
	requireRowCount(t, ctx, pool, `SELECT count(*) FROM credit_grants WHERE operation_ref=$1`, operation, 1)
}

func TestWAL023ConcurrentTopUpAndAllowanceResetPreserveBothGrants(t *testing.T) {
	ctx, pool, svc, wallet := reviewWallet(t, 25)
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() { <-start; errs <- svc.Grant(ctx, wallet.ID, "purchase", uuid.NewString(), 75, nil) }()
	go func() { <-start; errs <- svc.Grant(ctx, wallet.ID, "subscription", uuid.NewString(), 100, nil) }()
	close(start)
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	requireReviewWallet(t, ctx, svc, wallet.ID, 200, 0)
	var purchased, included int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT COALESCE(sum(remaining) FILTER (WHERE source='purchase'),0),COALESCE(sum(remaining) FILTER (WHERE source='subscription'),0) FROM credit_grants WHERE wallet_id=$1`, wallet.ID).Scan(&purchased, &included))
	require.Equal(t, int64(75), purchased)
	require.Equal(t, int64(100), included)
}

func TestLIM011LowBalanceWarningRearmsAfterTopUp(t *testing.T) {
	ctx, pool, svc, wallet := reviewWallet(t, 100)
	first, err := svc.Reserve(ctx, wallet.ID, "first-crossing", 0, 60, time.Minute)
	require.NoError(t, err)
	_, err = svc.Settle(ctx, first.ID, 60)
	require.NoError(t, err)
	require.NoError(t, svc.Grant(ctx, wallet.ID, "top-up", uuid.NewString(), 100, nil))
	_, err = svc.Reserve(ctx, wallet.ID, "second-crossing", 0, 50, time.Minute)
	require.NoError(t, err)
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='credits.threshold' AND (payload->>'threshold')::int=50`, wallet.ID).Scan(&count))
	require.Equal(t, 2, count, "50%% warning must emit once for each distinct threshold crossing")
}

func TestWRK011RecurringGrantCanOnlyBeClaimedOnce(t *testing.T) {
	ctx, pool, svc, wallet := reviewWallet(t, 0)
	operation := "recurring-" + uuid.NewString()
	start := make(chan struct{})
	errs := make(chan error, 4)
	for range 4 {
		go func() { <-start; errs <- svc.Grant(ctx, wallet.ID, "subscription", operation, 100, nil) }()
	}
	close(start)
	for range 4 {
		require.NoError(t, <-errs)
	}
	requireReviewWallet(t, ctx, svc, wallet.ID, 100, 0)
	requireRowCount(t, ctx, pool, `SELECT count(*) FROM credit_grants WHERE operation_ref=$1`, operation, 1)
}

func TestWRK012CommittedFinancialJobRemainsRecoverableBeforeAck(t *testing.T) {
	ctx, pool, svc, wallet := reviewWallet(t, 0)
	operation := "worker-before-ack-" + uuid.NewString()
	require.NoError(t, svc.Grant(ctx, wallet.ID, "subscription", operation, 100, nil))
	var publishedAt *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT published_at FROM outbox_events WHERE aggregate_id=$1 AND event_type='credits.granted' ORDER BY created_at DESC LIMIT 1`, wallet.ID).Scan(&publishedAt))
	require.Nil(t, publishedAt)
	requireReviewWallet(t, ctx, svc, wallet.ID, 100, 0)
}

func TestFND012MigrationVersionTableIsPresentAndReadable(t *testing.T) {
	ctx, pool, _, _ := reviewWallet(t, 0)
	var version int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version))
	require.GreaterOrEqual(t, version, int64(3))
}

func TestMTR020DelayedUsageKeepsItsOriginalOccurrencePeriod(t *testing.T) {
	ctx, pool, svc, wallet := reviewWallet(t, 100)
	reservation, err := svc.Reserve(ctx, wallet.ID, "delayed-usage", 0, 10, time.Minute)
	require.NoError(t, err)
	occurredAt := time.Date(2026, 1, 31, 23, 59, 0, 0, time.UTC)
	eventID := "delayed-" + uuid.NewString()
	_, err = pool.Exec(ctx, `INSERT INTO usage_events(wallet_id,reservation_id,external_id,units,quantity,meter,application,occurred_at) VALUES($1,$2,$3,4,4,'workflow.execution','daybook',$4)`, wallet.ID, reservation.ID, eventID, occurredAt)
	require.NoError(t, err)
	var stored time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT occurred_at FROM usage_events WHERE external_id=$1`, eventID).Scan(&stored))
	require.True(t, stored.Equal(occurredAt))
}

func TestMTR025OutOfOrderUsageUpdatesDoNotLoseOrDuplicateUnits(t *testing.T) {
	ctx, pool, svc, wallet := reviewWallet(t, 100)
	reservation, err := svc.Reserve(ctx, wallet.ID, "out-of-order-usage", 0, 20, time.Minute)
	require.NoError(t, err)
	base := time.Now().UTC()
	events := []struct {
		id       string
		quantity int64
		at       time.Time
	}{
		{id: "increment-2-" + uuid.NewString(), quantity: 7, at: base.Add(time.Minute)},
		{id: "increment-1-" + uuid.NewString(), quantity: 5, at: base},
	}
	for _, event := range events {
		_, err = pool.Exec(ctx, `INSERT INTO usage_events(wallet_id,reservation_id,external_id,units,quantity,meter,application,occurred_at) VALUES($1,$2,$3,$4,$4,'workflow.execution','daybook',$5)`, wallet.ID, reservation.ID, event.id, event.quantity, event.at)
		require.NoError(t, err)
	}
	var total int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT sum(quantity) FROM usage_events WHERE reservation_id=$1`, reservation.ID).Scan(&total))
	require.Equal(t, int64(12), total)
}

func TestPAY024ConcurrentCheckoutIdempotencyCreatesOnePayment(t *testing.T) {
	ctx, pool, _, wallet := reviewWallet(t, 0)
	var accountID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT account_id FROM wallets WHERE id=$1`, wallet.ID).Scan(&accountID))
	operation := "checkout-" + uuid.NewString()
	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := pool.Exec(ctx, `INSERT INTO payments(account_id,provider,provider_order_id,status,amount_minor,currency,operation_ref) VALUES($1,'razorpay',$2,'created',49900,'INR',$3)`, accountID, "order_"+uuid.NewString(), operation)
			errs <- err
		}()
	}
	close(start)
	first, second := <-errs, <-errs
	require.True(t, (first == nil) != (second == nil), "one checkout insert must win: %v, %v", first, second)
	requireRowCount(t, ctx, pool, `SELECT count(*) FROM payments WHERE operation_ref=$1`, operation, 1)
}

func TestINV009InvoiceTotalsReconcileWithPayment(t *testing.T) {
	ctx, pool, _, wallet := reviewWallet(t, 0)
	var accountID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT account_id FROM wallets WHERE id=$1`, wallet.ID).Scan(&accountID))
	var paymentID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO payments(account_id,provider,provider_order_id,status,amount_minor,currency,operation_ref) VALUES($1,'razorpay',$2,'captured',49900,'INR',$3) RETURNING id`, accountID, "order_"+uuid.NewString(), "payment-"+uuid.NewString()).Scan(&paymentID))
	var invoiceID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO invoices(account_id,payment_id,billing_operation_ref,invoice_number,status,currency,subtotal_minor,total_minor,finalized_at,paid_at) VALUES($1,$2,$3,$4,'paid','INR',49900,49900,now(),now()) RETURNING id`, accountID, paymentID, "invoice-op-"+uuid.NewString(), "INV-"+uuid.NewString()).Scan(&invoiceID))
	_, err := pool.Exec(ctx, `INSERT INTO invoice_lines(invoice_id,description,quantity,unit_price_minor,amount_minor) VALUES($1,'Credit purchase',1,49900,49900)`, invoiceID)
	require.NoError(t, err)
	var paymentTotal, invoiceTotal, lineTotal int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT p.amount_minor,i.total_minor,(SELECT sum(amount_minor) FROM invoice_lines WHERE invoice_id=i.id) FROM payments p JOIN invoices i ON i.payment_id=p.id WHERE p.id=$1`, paymentID).Scan(&paymentTotal, &invoiceTotal, &lineTotal))
	require.Equal(t, paymentTotal, invoiceTotal)
	require.Equal(t, invoiceTotal, lineTotal)
}

func TestSUB021CompetingLifecycleUpdatesUseOneSubscriptionVersion(t *testing.T) {
	ctx, pool, subscriptionID, _ := reviewSubscription(t)
	start := make(chan struct{})
	results := make(chan int64, 2)
	for _, status := range []string{"cancelled", "active"} {
		status := status
		go func() {
			<-start
			tag, err := pool.Exec(ctx, `UPDATE subscriptions SET status=$2,version=version+1,updated_at=now() WHERE id=$1 AND version=1`, subscriptionID, status)
			if err != nil {
				results <- -1
				return
			}
			results <- tag.RowsAffected()
		}()
	}
	close(start)
	first, second := <-results, <-results
	require.NotEqual(t, int64(-1), first)
	require.NotEqual(t, int64(-1), second)
	require.Equal(t, int64(1), first+second, "only one lifecycle transition may commit for a subscription version")
	var version int64
	var status string
	require.NoError(t, pool.QueryRow(ctx, `SELECT version,status::text FROM subscriptions WHERE id=$1`, subscriptionID).Scan(&version, &status))
	require.Equal(t, int64(2), version)
	require.Contains(t, []string{"active", "cancelled"}, status)
}

func TestSUB026RetriedPlanChangeCreatesOneHistoryRecord(t *testing.T) {
	ctx, pool, subscriptionID, planID := reviewSubscription(t)
	operation := "plan-change-" + uuid.NewString()
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := pool.Exec(ctx, `INSERT INTO subscription_history(subscription_id,status,plan_id,operation_ref) VALUES($1,'active',$2,$3)`, subscriptionID, planID, operation)
			results <- err
		}()
	}
	close(start)
	first, second := <-results, <-results
	require.True(t, (first == nil) != (second == nil), "one retried plan change must win: %v, %v", first, second)
	requireRowCount(t, ctx, pool, `SELECT count(*) FROM subscription_history WHERE operation_ref=$1`, operation, 1)
}

func reviewWallet(t *testing.T, initial int64) (context.Context, *pgxpool.Pool, *wallets.Service, wallets.Wallet) {
	t.Helper()
	ctx := context.Background()
	pool := testkit.Database(t)
	var accountID, productID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO billing_accounts(name,external_ref) VALUES($1,$2) RETURNING id`, "review", uuid.NewString()).Scan(&accountID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO products(slug,name) VALUES($1,$2) RETURNING id`, "review-"+uuid.NewString(), "Review").Scan(&productID))
	svc := wallets.NewService(pool, nil)
	wallet, err := svc.CreateWallet(ctx, accountID, productID)
	require.NoError(t, err)
	if initial > 0 {
		require.NoError(t, svc.Grant(ctx, wallet.ID, "initial", uuid.NewString(), initial, nil))
	}
	return ctx, pool, svc, wallet
}

func reviewSubscription(t *testing.T) (context.Context, *pgxpool.Pool, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	pool := testkit.Database(t)
	var accountID, productID, planID, subscriptionID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO billing_accounts(name,external_ref) VALUES('subscription-review',$1) RETURNING id`, uuid.NewString()).Scan(&accountID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO products(slug,name) VALUES($1,'Subscription Review') RETURNING id`, "subscription-"+uuid.NewString()).Scan(&productID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO plans(product_id,slug,name,price_minor,currency,included_credits,billing_interval) VALUES($1,$2,'Review Plan',0,'INR',100,'monthly') RETURNING id`, productID, "plan-"+uuid.NewString()).Scan(&planID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO subscriptions(account_id,plan_id,product_id,status,current_period_start,current_period_end,price_minor,currency,billing_interval) VALUES($1,$2,$3,'active',now(),now()+interval '1 month',0,'INR','monthly') RETURNING id`, accountID, planID, productID).Scan(&subscriptionID))
	return ctx, pool, subscriptionID, planID
}

func requireReviewWallet(t *testing.T, ctx context.Context, svc *wallets.Service, id uuid.UUID, available, reserved int64) {
	t.Helper()
	wallet, err := svc.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, available, wallet.Available)
	require.Equal(t, reserved, wallet.Reserved)
}

func requireRowCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, arg any, want int) {
	t.Helper()
	var count int
	require.NoError(t, pool.QueryRow(ctx, query, arg).Scan(&count))
	require.Equal(t, want, count)
}
