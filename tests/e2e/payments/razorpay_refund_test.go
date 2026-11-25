//go:build e2e

package payments_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

type capturedRefundFixture struct {
	h                 *testkit.HTTP
	pool              *pgxpool.Pool
	accountID         string
	productID         string
	order             testkit.PaymentOrderFixture
	providerPaymentID string
	walletID          string
	grantID           string
	amount            int64
	credits           int64
}

func TestRazorpayFailureRecordsAttemptWithoutValueAndLateCaptureWins(t *testing.T) {
	h, pool, _, _, order := captureOrderFixture(t, "failed-late-capture")
	config := order.Checkout.ClientConfig
	failedPaymentID := testkit.Unique("pay_failed")
	failedEventID := testkit.Unique("evt_failed")
	failed := testkit.RazorpayPaymentFailed(failedEventID, config.OrderID, failedPaymentID, config.AmountMinor, config.Currency)
	if status := h.RazorpayWebhook(t, "/v1/payments/webhook", failed, "test-webhook-secret"); status != http.StatusNoContent {
		t.Fatalf("payment.failed returned %d", status)
	}

	var paymentStatus, storedPaymentID, failureReason string
	var grants, ledger, invoices, successEvents, failedEvents int
	if err := pool.QueryRow(context.Background(), `SELECT p.status,p.provider_payment_id,COALESCE(p.failure_reason,''),
		(SELECT count(*) FROM credit_grants WHERE operation_ref='payment:'||p.id),
		(SELECT count(*) FROM credit_ledger WHERE operation_ref='grant:payment:'||p.id),
		(SELECT count(*) FROM invoices WHERE payment_id=p.id),
		(SELECT count(*) FROM outbox_events WHERE event_type='payment.succeeded' AND payload->>'payment_id'=p.id::text),
		(SELECT count(*) FROM outbox_events WHERE event_type='payment.failed' AND payload->>'payment_id'=p.id::text)
		FROM payments p WHERE p.id=$1`, order.PaymentID).
		Scan(&paymentStatus, &storedPaymentID, &failureReason, &grants, &ledger, &invoices, &successEvents, &failedEvents); err != nil {
		t.Fatal(err)
	}
	if paymentStatus != "failed" || storedPaymentID != failedPaymentID || failureReason == "" || grants != 0 || ledger != 0 || invoices != 0 || successEvents != 0 || failedEvents != 1 {
		t.Fatalf("unexpected failed state: status=%s payment=%s reason=%q grants=%d ledger=%d invoices=%d success=%d failed=%d",
			paymentStatus, storedPaymentID, failureReason, grants, ledger, invoices, successEvents, failedEvents)
	}

	capturedPaymentID := testkit.Unique("pay_late_capture")
	sendCapture(t, h, order, capturedPaymentID, testkit.Unique("evt_late_capture"))
	assertCapturedOnce(t, pool, config.OrderID)
	if err := pool.QueryRow(context.Background(), `SELECT status,provider_payment_id FROM payments WHERE id=$1`, order.PaymentID).
		Scan(&paymentStatus, &storedPaymentID); err != nil {
		t.Fatal(err)
	}
	if paymentStatus != "captured" || storedPaymentID != capturedPaymentID {
		t.Fatalf("late capture did not supersede failed attempt: status=%s payment=%s", paymentStatus, storedPaymentID)
	}
}

func TestRazorpayCreditPackRefundsArePartialFullAndIndependentlyIdempotent(t *testing.T) {
	f := newCapturedRefundFixture(t, "refund-lifecycle")
	firstAmount := f.amount / 2
	firstRefundID := testkit.Unique("rfnd_partial")
	if status := sendRefund(t, f.h, f.providerPaymentID, firstRefundID, firstAmount, f.order.Checkout.ClientConfig.Currency); status != http.StatusNoContent {
		t.Fatalf("partial refund returned %d", status)
	}
	// A distinct webhook delivery for the same provider refund must not create a
	// second financial mutation.
	if status := sendRefund(t, f.h, f.providerPaymentID, firstRefundID, firstAmount, f.order.Checkout.ClientConfig.Currency); status != http.StatusNoContent {
		t.Fatalf("duplicate provider refund returned %d", status)
	}

	firstReversal := f.credits * firstAmount / f.amount
	assertCreditRefundState(t, f, "captured", 1, firstAmount, firstReversal, f.credits-firstReversal)

	secondAmount := f.amount - firstAmount
	secondRefundID := testkit.Unique("rfnd_final")
	if status := sendRefund(t, f.h, f.providerPaymentID, secondRefundID, secondAmount, f.order.Checkout.ClientConfig.Currency); status != http.StatusNoContent {
		t.Fatalf("final refund returned %d", status)
	}
	assertCreditRefundState(t, f, "refunded", 2, f.amount, f.credits, 0)

	if status := sendRefund(t, f.h, f.providerPaymentID, firstRefundID, firstAmount+1, f.order.Checkout.ClientConfig.Currency); status != http.StatusConflict {
		t.Fatalf("conflicting provider refund ID returned %d", status)
	}
	var issues int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM reconciliation_issues
		WHERE payment_id=$1 AND issue_type='refund_mismatch' AND provider_refund_id=$2 AND status='open'`, f.order.PaymentID, firstRefundID).Scan(&issues); err != nil || issues != 1 {
		t.Fatalf("refund mismatch issues=%d err=%v", issues, err)
	}
}

func TestRazorpayRefundRejectsMalformedMismatchedOverAndOutOfOrderEvents(t *testing.T) {
	f := newCapturedRefundFixture(t, "refund-rejection")
	tests := []struct {
		name, paymentID, refundID, currency string
		amount                              int64
		want                                int
	}{
		{name: "missing refund ID", paymentID: f.providerPaymentID, amount: 1, currency: f.order.Checkout.ClientConfig.Currency, want: http.StatusBadRequest},
		{name: "missing payment ID", refundID: testkit.Unique("rfnd_missing_payment"), amount: 1, currency: f.order.Checkout.ClientConfig.Currency, want: http.StatusBadRequest},
		{name: "zero amount", paymentID: f.providerPaymentID, refundID: testkit.Unique("rfnd_zero"), currency: f.order.Checkout.ClientConfig.Currency, want: http.StatusBadRequest},
		{name: "negative amount", paymentID: f.providerPaymentID, refundID: testkit.Unique("rfnd_negative"), amount: -1, currency: f.order.Checkout.ClientConfig.Currency, want: http.StatusBadRequest},
		{name: "missing currency", paymentID: f.providerPaymentID, refundID: testkit.Unique("rfnd_no_currency"), amount: 1, want: http.StatusBadRequest},
		{name: "wrong currency", paymentID: f.providerPaymentID, refundID: testkit.Unique("rfnd_currency"), amount: 1, currency: "USD", want: http.StatusConflict},
		{name: "over refund", paymentID: f.providerPaymentID, refundID: testkit.Unique("rfnd_over"), amount: f.amount + 1, currency: f.order.Checkout.ClientConfig.Currency, want: http.StatusConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if status := sendRefund(t, f.h, test.paymentID, test.refundID, test.amount, test.currency); status != test.want {
				t.Fatalf("want %d, got %d", test.want, status)
			}
		})
	}
	assertCreditRefundState(t, f, "captured", 0, 0, 0, f.credits)

	// A provider can deliver refund information before the capture webhook. The
	// first attempt is surfaced for review; replay after capture is then safe.
	h, pool, _, _, order := captureOrderFixture(t, "refund-out-of-order")
	paymentID := testkit.Unique("pay_out_of_order")
	refundID := testkit.Unique("rfnd_out_of_order")
	config := order.Checkout.ClientConfig
	if status := sendRefund(t, h, paymentID, refundID, config.AmountMinor, config.Currency); status != http.StatusConflict {
		t.Fatalf("refund before capture returned %d", status)
	}
	sendCapture(t, h, order, paymentID, testkit.Unique("evt_capture_after_refund"))
	if status := sendRefund(t, h, paymentID, refundID, config.AmountMinor, config.Currency); status != http.StatusNoContent {
		t.Fatalf("refund replay after capture returned %d", status)
	}
	var paymentStatus string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM payments WHERE id=$1`, order.PaymentID).Scan(&paymentStatus); err != nil || paymentStatus != "refunded" {
		t.Fatalf("out-of-order refund status=%s err=%v", paymentStatus, err)
	}
}

func TestRazorpayCreditReversalUsesOnlyUnspentCredits(t *testing.T) {
	for _, remaining := range []int64{500, 200, 0} {
		t.Run(fmt.Sprintf("remaining_%d", remaining), func(t *testing.T) {
			f := newCapturedRefundFixture(t, fmt.Sprintf("refund-remaining-%d", remaining))
			if f.credits != 500 {
				t.Fatalf("fixture credits=%d, want 500", f.credits)
			}
			if _, err := f.pool.Exec(context.Background(), `UPDATE credit_grants SET remaining=$2 WHERE id=$1`, f.grantID, remaining); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(context.Background(), `UPDATE wallets SET available=$2 WHERE id=$1`, f.walletID, remaining); err != nil {
				t.Fatal(err)
			}
			if status := sendRefund(t, f.h, f.providerPaymentID, testkit.Unique("rfnd_spend_policy"), f.amount, f.order.Checkout.ClientConfig.Currency); status != http.StatusNoContent {
				t.Fatalf("full refund returned %d", status)
			}
			assertCreditRefundState(t, f, "refunded", 1, f.amount, remaining, 0)
		})
	}
}

func TestRazorpayRefundCommitsAccountingStateAndRollsBackEveryStep(t *testing.T) {
	success := newCapturedRefundFixture(t, "refund-atomic")
	var beforeRevision int64
	if err := success.pool.QueryRow(context.Background(), `SELECT revision FROM billing_state_revisions WHERE account_id=$1 AND product_id=$2`, success.accountID, success.productID).Scan(&beforeRevision); err != nil {
		t.Fatal(err)
	}
	refundID := testkit.Unique("rfnd_atomic")
	if status := sendRefund(t, success.h, success.providerPaymentID, refundID, success.amount, success.order.Checkout.ClientConfig.Currency); status != http.StatusNoContent {
		t.Fatalf("atomic refund returned %d", status)
	}
	var refunds, ledger, notes, outbox, events int
	var afterRevision, eventRevision int64
	if err := success.pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM refunds WHERE payment_id=$1),
		(SELECT count(*) FROM credit_ledger WHERE operation_ref=$2),
		(SELECT count(*) FROM credit_notes cn JOIN refunds r ON r.id=cn.refund_id WHERE r.payment_id=$1),
		(SELECT count(*) FROM outbox_events WHERE event_type='payment.refunded' AND aggregate_id=$3),
		(SELECT count(*) FROM provider_events WHERE provider_event_id LIKE 'evt_refund%' AND processed_at IS NOT NULL
		 AND payload->'payload'->'refund'->'entity'->>'id'=$4),
		(SELECT revision FROM billing_state_revisions WHERE account_id=$5 AND product_id=$6),
		COALESCE((SELECT max(billing_revision) FROM outbox_events WHERE event_type='payment.refunded' AND aggregate_id=$3),0)`,
		success.order.PaymentID, "refund:"+refundID, success.walletID, refundID, success.accountID, success.productID).
		Scan(&refunds, &ledger, &notes, &outbox, &events, &afterRevision, &eventRevision); err != nil {
		t.Fatal(err)
	}
	if refunds != 1 || ledger != 1 || notes != 1 || outbox != 1 || events != 1 || afterRevision <= beforeRevision || eventRevision != afterRevision {
		t.Fatalf("incomplete atomic refund: refunds=%d ledger=%d notes=%d outbox=%d events=%d revision=%d->%d event_revision=%d",
			refunds, ledger, notes, outbox, events, beforeRevision, afterRevision, eventRevision)
	}

	scenarios := []struct {
		name, table, action string
		condition           func(f capturedRefundFixture, refundID string) string
	}{
		{name: "provider event", table: "provider_events", action: "INSERT", condition: func(_ capturedRefundFixture, _ string) string {
			return "NEW.event_type='refund.processed'"
		}},
		{name: "refund", table: "refunds", action: "INSERT", condition: func(f capturedRefundFixture, _ string) string {
			return fmt.Sprintf("NEW.payment_id='%s'::uuid", f.order.PaymentID)
		}},
		{name: "credit grant", table: "credit_grants", action: "UPDATE", condition: func(f capturedRefundFixture, _ string) string { return fmt.Sprintf("NEW.id='%s'::uuid", f.grantID) }},
		{name: "wallet", table: "wallets", action: "UPDATE", condition: func(f capturedRefundFixture, _ string) string {
			return fmt.Sprintf("NEW.id='%s'::uuid AND NEW.available<>OLD.available", f.walletID)
		}},
		{name: "ledger", table: "credit_ledger", action: "INSERT", condition: func(_ capturedRefundFixture, refundID string) string {
			return fmt.Sprintf("NEW.operation_ref='refund:%s'", refundID)
		}},
		{name: "payment", table: "payments", action: "UPDATE", condition: func(f capturedRefundFixture, _ string) string {
			return fmt.Sprintf("NEW.id='%s'::uuid AND NEW.status='refunded'", f.order.PaymentID)
		}},
		{name: "credit note", table: "credit_notes", action: "INSERT", condition: func(f capturedRefundFixture, _ string) string {
			return fmt.Sprintf("EXISTS(SELECT 1 FROM billmesh.invoices i WHERE i.id=NEW.invoice_id AND i.payment_id='%s'::uuid)", f.order.PaymentID)
		}},
		{name: "billing revision", table: "billing_state_revisions", action: "UPDATE", condition: func(f capturedRefundFixture, _ string) string {
			return fmt.Sprintf("NEW.account_id='%s'::uuid AND NEW.product_id='%s'::uuid", f.accountID, f.productID)
		}},
		{name: "outbox", table: "outbox_events", action: "INSERT", condition: func(f capturedRefundFixture, _ string) string {
			return fmt.Sprintf("NEW.account_id='%s'::uuid AND NEW.event_type='payment.refunded'", f.accountID)
		}},
	}
	for _, scenario := range scenarios {
		t.Run("rollback_"+strings.ReplaceAll(scenario.name, " ", "_"), func(t *testing.T) {
			f := newCapturedRefundFixture(t, "refund-rollback-"+strings.ReplaceAll(scenario.name, " ", "-"))
			refundID := testkit.Unique("rfnd_rollback")
			installCaptureFailureTrigger(t, f.pool, scenario.table, scenario.action, scenario.condition(f, refundID))
			status := sendRefund(t, f.h, f.providerPaymentID, refundID, f.amount, f.order.Checkout.ClientConfig.Currency)
			removeCaptureFailureTrigger(t, f.pool, scenario.table)
			if status < 500 {
				t.Fatalf("injected %s failure returned %d", scenario.name, status)
			}
			assertCreditRefundState(t, f, "captured", 0, 0, 0, f.credits)
			var events int
			if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM provider_events
				WHERE payload->'payload'->'refund'->'entity'->>'id'=$1`, refundID).Scan(&events); err != nil || events != 0 {
				t.Fatalf("rolled-back provider events=%d err=%v", events, err)
			}
		})
	}
}

func TestRazorpaySubscriptionRefundHonoursRevokeAndRetainPolicies(t *testing.T) {
	for _, policy := range []string{"revoke", "retain"} {
		t.Run(policy, func(t *testing.T) {
			h := testkit.NewHTTP(t)
			org := testkit.Unique("subscription-refund-" + policy)
			token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
			h.Token = token
			testkit.CreateFixtureAccount(t, h, org, token)
			subscriptionID := testkit.ActivatePaidSubscription(t, h, "daybook", "daybook-paid", token)
			pool := e2ePool(t)

			var paymentID, providerPaymentID, currency, walletID, grantID string
			var amount, remaining, available int64
			if err := pool.QueryRow(context.Background(), `SELECT p.id,p.provider_payment_id,p.amount_minor,p.currency,w.id,g.id,g.remaining,w.available
				FROM payments p JOIN subscription_transitions st ON st.id=p.transition_id
				JOIN subscriptions s ON s.id=st.subscription_id
				JOIN wallets w ON w.account_id=p.account_id AND w.product_id=st.product_id
				JOIN credit_grants g ON g.wallet_id=w.id AND g.source='subscription' AND g.operation_ref LIKE 'subscription:'||s.id::text||':%'
				WHERE s.id=$1 ORDER BY g.created_at DESC LIMIT 1`, subscriptionID).
				Scan(&paymentID, &providerPaymentID, &amount, &currency, &walletID, &grantID, &remaining, &available); err != nil {
				t.Fatal(err)
			}
			if policy == "retain" {
				if _, err := pool.Exec(context.Background(), `UPDATE subscriptions SET billing_policy=jsonb_set(billing_policy,'{lifecycle,refund_entitlements}','"retain"') WHERE id=$1`, subscriptionID); err != nil {
					t.Fatal(err)
				}
			}

			firstAmount := amount / 2
			if status := sendRefund(t, h, providerPaymentID, testkit.Unique("rfnd_subscription_partial"), firstAmount, currency); status != http.StatusNoContent {
				t.Fatalf("partial subscription refund returned %d", status)
			}
			var subscriptionStatus, paymentStatus string
			if err := pool.QueryRow(context.Background(), `SELECT s.status,p.status FROM subscriptions s
				JOIN subscription_transitions st ON st.subscription_id=s.id JOIN payments p ON p.transition_id=st.id
				WHERE s.id=$1`, subscriptionID).Scan(&subscriptionStatus, &paymentStatus); err != nil {
				t.Fatal(err)
			}
			if subscriptionStatus != "active" || paymentStatus != "captured" {
				t.Fatalf("partial refund changed entitlement: subscription=%s payment=%s", subscriptionStatus, paymentStatus)
			}

			if status := sendRefund(t, h, providerPaymentID, testkit.Unique("rfnd_subscription_final"), amount-firstAmount, currency); status != http.StatusNoContent {
				t.Fatalf("final subscription refund returned %d", status)
			}
			var storedRemaining, storedAvailable, reversed int64
			if err := pool.QueryRow(context.Background(), `SELECT s.status,p.status,g.remaining,w.available,
				(SELECT COALESCE(sum(r.credits_reversed),0) FROM refunds r WHERE r.payment_id=p.id)
				FROM subscriptions s JOIN subscription_transitions st ON st.subscription_id=s.id
				JOIN payments p ON p.transition_id=st.id JOIN wallets w ON w.id=$2 JOIN credit_grants g ON g.id=$3 WHERE s.id=$1`,
				subscriptionID, walletID, grantID).Scan(&subscriptionStatus, &paymentStatus, &storedRemaining, &storedAvailable, &reversed); err != nil {
				t.Fatal(err)
			}
			wantStatus, wantRemaining, wantAvailable, wantReversed := "cancelled", int64(0), available-remaining, remaining
			if policy == "retain" {
				wantStatus, wantRemaining, wantAvailable, wantReversed = "active", remaining, available, 0
			}
			if paymentStatus != "refunded" || subscriptionStatus != wantStatus || storedRemaining != wantRemaining || storedAvailable != wantAvailable || reversed != wantReversed {
				t.Fatalf("%s policy: subscription=%s payment=%s remaining=%d available=%d reversed=%d", policy, subscriptionStatus, paymentStatus, storedRemaining, storedAvailable, reversed)
			}
		})
	}
}

func newCapturedRefundFixture(t *testing.T, prefix string) capturedRefundFixture {
	t.Helper()
	h, pool, accountID, productID, order := captureOrderFixture(t, prefix)
	providerPaymentID := testkit.Unique("pay_refund")
	sendCapture(t, h, order, providerPaymentID, testkit.Unique("evt_refund_capture"))
	f := capturedRefundFixture{h: h, pool: pool, accountID: accountID, productID: productID, order: order, providerPaymentID: providerPaymentID}
	if err := pool.QueryRow(context.Background(), `SELECT p.amount_minor,p.credits,w.id,g.id
		FROM payments p JOIN credit_packs cp ON cp.id=p.credit_pack_id
		JOIN wallets w ON w.account_id=p.account_id AND w.product_id=cp.product_id
		JOIN credit_grants g ON g.wallet_id=w.id AND g.operation_ref='payment:'||p.id WHERE p.id=$1`, order.PaymentID).
		Scan(&f.amount, &f.credits, &f.walletID, &f.grantID); err != nil {
		t.Fatal(err)
	}
	return f
}

func sendRefund(t *testing.T, h *testkit.HTTP, paymentID, refundID string, amount int64, currency string) int {
	t.Helper()
	event := testkit.RazorpayRefundProcessed(testkit.Unique("evt_refund"), paymentID, refundID, amount, currency)
	return h.RazorpayWebhook(t, "/v1/payments/webhook", event, "test-webhook-secret")
}

func assertCreditRefundState(t *testing.T, f capturedRefundFixture, wantStatus string, wantRefunds int, wantAmount, wantReversed, wantAvailable int64) {
	t.Helper()
	var status string
	var refunds int
	var amount, reversed, available, remaining int64
	var notes, refundLedger, refundOutbox int
	if err := f.pool.QueryRow(context.Background(), `SELECT p.status,
		(SELECT count(*) FROM refunds r WHERE r.payment_id=p.id),
		(SELECT COALESCE(sum(r.amount_minor),0) FROM refunds r WHERE r.payment_id=p.id),
		(SELECT COALESCE(sum(r.credits_reversed),0) FROM refunds r WHERE r.payment_id=p.id),
		w.available,g.remaining,
		(SELECT count(*) FROM credit_notes cn JOIN refunds r ON r.id=cn.refund_id WHERE r.payment_id=p.id),
		(SELECT count(*) FROM credit_ledger WHERE grant_id=g.id AND kind='refund'),
		(SELECT count(*) FROM outbox_events WHERE aggregate_id=w.id AND event_type='payment.refunded')
		FROM payments p JOIN credit_packs cp ON cp.id=p.credit_pack_id
		JOIN wallets w ON w.account_id=p.account_id AND w.product_id=cp.product_id
		JOIN credit_grants g ON g.wallet_id=w.id AND g.operation_ref='payment:'||p.id WHERE p.id=$1`, f.order.PaymentID).
		Scan(&status, &refunds, &amount, &reversed, &available, &remaining, &notes, &refundLedger, &refundOutbox); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || refunds != wantRefunds || amount != wantAmount || reversed != wantReversed || available != wantAvailable || remaining != wantAvailable || notes != wantRefunds || refundOutbox != wantRefunds {
		t.Fatalf("refund state: status=%s refunds=%d amount=%d reversed=%d available=%d remaining=%d notes=%d ledger=%d outbox=%d",
			status, refunds, amount, reversed, available, remaining, notes, refundLedger, refundOutbox)
	}
	if wantReversed == 0 && refundLedger != 0 || wantReversed > 0 && refundLedger == 0 {
		t.Fatalf("refund ledger count=%d for reversed=%d", refundLedger, wantReversed)
	}
}
