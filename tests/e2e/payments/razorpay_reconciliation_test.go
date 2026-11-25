//go:build e2e

package payments_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/internal/app"
	"github.com/tociva/billmesh/internal/config"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestRazorpayReconciliationRecoversExactCaptureWithoutWebhook(t *testing.T) {
	h, pool, _, _, order := captureOrderFixture(t, "reconcile-no-webhook")
	config := order.Checkout.ClientConfig
	addRazorpayPayment(t, h, map[string]any{
		"id": testkit.Unique("pay_failed_reconciliation"), "order_id": config.OrderID, "status": "failed",
		"amount": config.AmountMinor, "currency": config.Currency,
	})
	providerPaymentID := testkit.Unique("pay_recovered")
	addRazorpayPayment(t, h, map[string]any{
		"id": providerPaymentID, "order_id": config.OrderID, "status": "captured",
		"amount": config.AmountMinor, "currency": config.Currency,
	})
	makePaymentReconciliationDue(t, pool, order.PaymentID)

	waitForReconciliation(t, func() bool {
		var status, outcome string
		var storedProviderPaymentID *string
		var attempts int
		err := pool.QueryRow(context.Background(), `SELECT status,COALESCE(reconciliation_outcome,''),
			provider_payment_id,reconciliation_attempts FROM payments WHERE id=$1`, order.PaymentID).
			Scan(&status, &outcome, &storedProviderPaymentID, &attempts)
		return err == nil && status == "captured" && outcome == "recovered" && storedProviderPaymentID != nil &&
			*storedProviderPaymentID == providerPaymentID && attempts >= 1
	})
	assertCapturedOnce(t, pool, config.OrderID)
	var attemptRows int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM reconciliation_attempts
		WHERE payment_id=$1 AND outcome='recovered' AND provider_payment_id=$2 AND completed_at IS NOT NULL`,
		order.PaymentID, providerPaymentID).Scan(&attemptRows); err != nil || attemptRows != 1 {
		t.Fatalf("reconciliation audit rows=%d err=%v", attemptRows, err)
	}
}

func TestRazorpayReconciliationRejectsMismatchAndProvidesAuditedOperatorWorkflow(t *testing.T) {
	h, pool, _, _, order := captureOrderFixture(t, "reconcile-mismatch")
	config := order.Checkout.ClientConfig
	providerPaymentID := testkit.Unique("pay_mismatched_reconciliation")
	addRazorpayPayment(t, h, map[string]any{
		"id": providerPaymentID, "order_id": config.OrderID, "status": "captured",
		"amount": config.AmountMinor + 1, "currency": config.Currency,
	})
	makePaymentReconciliationDue(t, pool, order.PaymentID)

	var issueID string
	var version int
	waitForReconciliation(t, func() bool {
		return pool.QueryRow(context.Background(), `SELECT id::text,version FROM reconciliation_issues
			WHERE payment_id=$1 AND issue_type='capture_mismatch' AND status='open'`, order.PaymentID).
			Scan(&issueID, &version) == nil
	})
	assertUnfulfilledPayment(t, pool, order.PaymentID, "unused-reconciliation-event", false)

	status, raw, _ := h.JSON(t, http.MethodGet, "/v1/admin/reconciliation/issues?status=open", nil, h.Token)
	if status != http.StatusOK || !bytes.Contains(raw, []byte(issueID)) {
		t.Fatalf("operator list returned %d: %s", status, raw)
	}
	status, raw, _ = h.JSON(t, http.MethodPost, "/v1/admin/reconciliation/issues/"+issueID+"/actions", map[string]any{
		"action": "resolve", "note": "Verified provider mismatch; refund workflow opened.", "version": version,
	}, h.Token)
	if status != http.StatusOK || !bytes.Contains(raw, []byte(`"status":"resolved"`)) {
		t.Fatalf("operator resolution returned %d: %s", status, raw)
	}
	var audits int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE resource_type='reconciliation_issue'
		AND resource_id=$1 AND action='reconciliation.issue.resolve'`, issueID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("operator audit rows=%d err=%v", audits, err)
	}
}

func TestRazorpayReconciliationRetriesAfterRestartAndWorkersConverge(t *testing.T) {
	h, pool, _, _, order := captureOrderFixture(t, "reconcile-restart")
	config := order.Checkout.ClientConfig
	providerPaymentID := testkit.Unique("pay_restart_recovery")
	addRazorpayPayment(t, h, map[string]any{
		"id": providerPaymentID, "order_id": config.OrderID, "status": "captured",
		"amount": config.AmountMinor, "currency": config.Currency,
	})
	setRazorpayBehavior(t, h, map[string]any{"status": http.StatusInternalServerError})
	t.Cleanup(func() { setRazorpayBehavior(t, h, map[string]any{}) })
	makePaymentReconciliationDue(t, pool, order.PaymentID)
	waitForReconciliation(t, func() bool {
		var outcome string
		var next *time.Time
		return pool.QueryRow(context.Background(), `SELECT COALESCE(reconciliation_outcome,''),reconciliation_next_attempt_at
			FROM payments WHERE id=$1`, order.PaymentID).Scan(&outcome, &next) == nil && outcome == "retry_scheduled" && next != nil
	})

	setRazorpayBehavior(t, h, map[string]any{})
	if _, err := pool.Exec(context.Background(), `UPDATE payments SET reconciliation_next_attempt_at=now() WHERE id=$1`, order.PaymentID); err != nil {
		t.Fatal(err)
	}
	var environment string
	if err := pool.QueryRow(context.Background(), `SELECT provider_environment FROM payments WHERE id=$1`, order.PaymentID).Scan(&environment); err != nil {
		t.Fatal(err)
	}
	provider := configpkg(h.MockURL, environment)
	first := app.NewWorker(pool, h.Client, time.Hour, nil)
	second := app.NewWorker(pool, h.Client, time.Hour, nil)
	first.ConfigureRazorpay(provider)
	second.ConfigureRazorpay(provider)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = first.Run(ctx) }()
	go func() { _ = second.Run(ctx) }()
	waitForReconciliation(t, func() bool {
		var status string
		return pool.QueryRow(context.Background(), `SELECT status FROM payments WHERE id=$1`, order.PaymentID).Scan(&status) == nil && status == "captured"
	})
	cancel()
	assertCapturedOnce(t, pool, config.OrderID)
}

func TestRazorpayPermanentAuthenticationFailureRequiresOperatorRetry(t *testing.T) {
	h, pool, _, _, order := captureOrderFixture(t, "reconcile-authentication")
	config := order.Checkout.ClientConfig
	providerPaymentID := testkit.Unique("pay_authentication_recovery")
	addRazorpayPayment(t, h, map[string]any{
		"id": providerPaymentID, "order_id": config.OrderID, "status": "captured",
		"amount": config.AmountMinor, "currency": config.Currency,
	})
	setRazorpayBehavior(t, h, map[string]any{"status": http.StatusUnauthorized})
	t.Cleanup(func() { setRazorpayBehavior(t, h, map[string]any{}) })
	makePaymentReconciliationDue(t, pool, order.PaymentID)
	var issueID string
	var version int
	waitForReconciliation(t, func() bool {
		var outcome string
		var next *time.Time
		err := pool.QueryRow(context.Background(), `SELECT COALESCE(reconciliation_outcome,''),reconciliation_next_attempt_at
			FROM payments WHERE id=$1`, order.PaymentID).Scan(&outcome, &next)
		if err != nil || outcome != "attention_required" || next != nil {
			return false
		}
		return pool.QueryRow(context.Background(), `SELECT id::text,version FROM reconciliation_issues
			WHERE payment_id=$1 AND issue_type='provider_authentication' AND status='open'`, order.PaymentID).
			Scan(&issueID, &version) == nil
	})

	setRazorpayBehavior(t, h, map[string]any{})
	status, raw, _ := h.JSON(t, http.MethodPost, "/v1/admin/reconciliation/issues/"+issueID+"/actions", map[string]any{
		"action": "retry", "note": "Provider credentials were corrected and verified.", "version": version,
	}, h.Token)
	if status != http.StatusOK {
		t.Fatalf("operator retry returned %d: %s", status, raw)
	}
	waitForReconciliation(t, func() bool {
		var paymentStatus string
		return pool.QueryRow(context.Background(), `SELECT status FROM payments WHERE id=$1`, order.PaymentID).Scan(&paymentStatus) == nil && paymentStatus == "captured"
	})
	assertCapturedOnce(t, pool, config.OrderID)
}

func TestRazorpayReconciliationLeavesAlreadyFulfilledPaymentUnchanged(t *testing.T) {
	h, pool, _, _, order := captureOrderFixture(t, "reconcile-already-fulfilled")
	config := order.Checkout.ClientConfig
	providerPaymentID := testkit.Unique("pay_already_fulfilled")
	addRazorpayPayment(t, h, map[string]any{
		"id": providerPaymentID, "order_id": config.OrderID, "status": "captured",
		"amount": config.AmountMinor, "currency": config.Currency,
	})
	sendCapture(t, h, order, providerPaymentID, testkit.Unique("evt_already_fulfilled"))
	if _, err := pool.Exec(context.Background(), `UPDATE payments SET reconciliation_outcome='retry_scheduled',
		reconciliation_next_attempt_at=now(),last_reconciled_at=now()-interval '1 hour' WHERE id=$1`, order.PaymentID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	assertCapturedOnce(t, pool, config.OrderID)
	var reconciliationEvents int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM provider_events
		WHERE provider_event_id=$1`, "reconciliation:"+providerPaymentID).Scan(&reconciliationEvents); err != nil {
		t.Fatal(err)
	}
	if reconciliationEvents != 0 {
		t.Fatalf("already fulfilled payment was reconciled again %d times", reconciliationEvents)
	}
}

func TestRazorpayInventoryDetectsOrphanedCapturedPayment(t *testing.T) {
	h := testkit.NewHTTP(t)
	pool := e2ePool(t)
	missingPaymentID := "00000000-0000-4000-8000-" + fmt.Sprintf("%012d", time.Now().UnixNano()%1_000_000_000_000)
	orderID := createProviderOrder(t, h, map[string]any{
		"amount": 8800, "currency": "INR", "receipt": testkit.Unique("orphan"),
		"notes": map[string]string{
			"billmesh_payment_id": missingPaymentID, "billmesh_account_id": "00000000-0000-4000-8000-000000000002",
			"billmesh_environment": "test", "billmesh_operation_ref": "orphan:payment",
		},
	})
	providerPaymentID := testkit.Unique("pay_orphan")
	addRazorpayPayment(t, h, map[string]any{
		"id": providerPaymentID, "order_id": orderID, "status": "captured", "amount": 8800, "currency": "INR",
	})
	if _, err := pool.Exec(context.Background(), `INSERT INTO provider_reconciliation_checkpoints(provider,provider_environment,last_inventory_at)
		VALUES('razorpay','test',now()-interval '1 hour') ON CONFLICT(provider,provider_environment)
		DO UPDATE SET last_inventory_at=excluded.last_inventory_at`); err != nil {
		t.Fatal(err)
	}
	waitForReconciliation(t, func() bool {
		var count int
		return pool.QueryRow(context.Background(), `SELECT count(*) FROM reconciliation_issues WHERE issue_type='captured_payment_missing'
			AND provider_order_id=$1 AND provider_payment_id=$2 AND status='open'`, orderID, providerPaymentID).Scan(&count) == nil && count == 1
	})
}

func TestRazorpayInventoryReattachesAmbiguousLocalOrder(t *testing.T) {
	_, pool, _, _, order := captureOrderFixture(t, "inventory-order-recovery")
	providerOrderID := order.Checkout.ClientConfig.OrderID
	if _, err := pool.Exec(context.Background(), `UPDATE payments SET provider_order_id=NULL,failure_reason='provider_unavailable'
		WHERE id=$1`, order.PaymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO provider_reconciliation_checkpoints(provider,provider_environment,last_inventory_at)
		VALUES('razorpay','test',now()-interval '1 hour') ON CONFLICT(provider,provider_environment)
		DO UPDATE SET last_inventory_at=excluded.last_inventory_at`); err != nil {
		t.Fatal(err)
	}
	waitForReconciliation(t, func() bool {
		var storedOrderID *string
		var failureReason *string
		return pool.QueryRow(context.Background(), `SELECT provider_order_id,failure_reason FROM payments WHERE id=$1`, order.PaymentID).
			Scan(&storedOrderID, &failureReason) == nil && storedOrderID != nil && *storedOrderID == providerOrderID && failureReason == nil
	})
}

func TestRazorpayFinanceReconciliationFindsLedgerAndRefundMismatches(t *testing.T) {
	t.Run("captured payment absent from fulfilled ledger", func(t *testing.T) {
		h, pool, accountID, _, order := captureOrderFixture(t, "finance-ledger-mismatch")
		config := order.Checkout.ClientConfig
		providerPaymentID := testkit.Unique("pay_missing_ledger")
		addRazorpayPayment(t, h, map[string]any{
			"id": providerPaymentID, "order_id": config.OrderID, "status": "captured",
			"amount": config.AmountMinor, "currency": config.Currency,
		})
		err := pgx.BeginFunc(context.Background(), pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(context.Background(), `UPDATE payments SET status='captured',provider_payment_id=$2,
				finance_reconciled_at=NULL WHERE id=$1`, order.PaymentID, providerPaymentID); err != nil {
				return err
			}
			_, err := tx.Exec(context.Background(), `INSERT INTO invoices(account_id,payment_id,billing_operation_ref,invoice_number,status,
				currency,subtotal_minor,total_minor,finalized_at,paid_at) VALUES($1,$2,$3,$4,'paid',$5,$6,$6,now(),now())`,
				accountID, order.PaymentID, "payment:"+order.PaymentID, "INV-TEST-"+order.PaymentID[:8], config.Currency, config.AmountMinor)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		waitForReconciliation(t, func() bool {
			var count int
			return pool.QueryRow(context.Background(), `SELECT count(*) FROM reconciliation_issues
				WHERE payment_id=$1 AND issue_type='ledger_mismatch' AND status='open'`, order.PaymentID).Scan(&count) == nil && count == 1
		})
	})

	t.Run("processed provider refund absent from Billmesh", func(t *testing.T) {
		h, pool, _, _, order := captureOrderFixture(t, "finance-refund-mismatch")
		config := order.Checkout.ClientConfig
		providerPaymentID := testkit.Unique("pay_refund_mismatch")
		addRazorpayPayment(t, h, map[string]any{
			"id": providerPaymentID, "order_id": config.OrderID, "status": "captured",
			"amount": config.AmountMinor, "currency": config.Currency,
		})
		sendCapture(t, h, order, providerPaymentID, testkit.Unique("evt_finance_capture"))
		providerRefundID := testkit.Unique("rfnd_missing_local")
		postMockControl(t, h, "/test/razorpay/refunds", map[string]any{
			"id": providerRefundID, "payment_id": providerPaymentID, "status": "processed",
			"amount": config.AmountMinor, "currency": config.Currency,
		})
		if _, err := pool.Exec(context.Background(), `UPDATE payments SET finance_reconciled_at=NULL WHERE id=$1`, order.PaymentID); err != nil {
			t.Fatal(err)
		}
		waitForReconciliation(t, func() bool {
			var count int
			return pool.QueryRow(context.Background(), `SELECT count(*) FROM reconciliation_issues
				WHERE payment_id=$1 AND issue_type='refund_mismatch' AND provider_refund_id=$2 AND status='open'`,
				order.PaymentID, providerRefundID).Scan(&count) == nil && count == 1
		})
	})

	t.Run("Billmesh refund absent from provider", func(t *testing.T) {
		h, pool, _, _, order := captureOrderFixture(t, "finance-local-refund-mismatch")
		config := order.Checkout.ClientConfig
		providerPaymentID := testkit.Unique("pay_local_refund_mismatch")
		addRazorpayPayment(t, h, map[string]any{
			"id": providerPaymentID, "order_id": config.OrderID, "status": "captured",
			"amount": config.AmountMinor, "currency": config.Currency,
		})
		sendCapture(t, h, order, providerPaymentID, testkit.Unique("evt_local_refund_capture"))
		localRefundID := testkit.Unique("rfnd_local_only")
		err := pgx.BeginFunc(context.Background(), pool, func(tx pgx.Tx) error {
			var refundID string
			if err := tx.QueryRow(context.Background(), `INSERT INTO refunds(payment_id,provider_refund_id,amount_minor,credits_reversed)
				VALUES($1,$2,$3,0) RETURNING id::text`, order.PaymentID, localRefundID, config.AmountMinor).Scan(&refundID); err != nil {
				return err
			}
			if _, err := tx.Exec(context.Background(), `INSERT INTO credit_notes(invoice_id,refund_id,credit_note_number,currency,amount_minor)
				SELECT id,$2,$3,$4,$5 FROM invoices WHERE payment_id=$1`, order.PaymentID, refundID,
				"CN-TEST-"+order.PaymentID[:8], config.Currency, config.AmountMinor); err != nil {
				return err
			}
			_, err := tx.Exec(context.Background(), `UPDATE payments SET finance_reconciled_at=NULL WHERE id=$1`, order.PaymentID)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		waitForReconciliation(t, func() bool {
			var count int
			return pool.QueryRow(context.Background(), `SELECT count(*) FROM reconciliation_issues
				WHERE payment_id=$1 AND issue_type='refund_mismatch' AND provider_refund_id=$2 AND status='open'`,
				order.PaymentID, localRefundID).Scan(&count) == nil && count == 1
		})
	})
}

func configpkg(baseURL, environment string) config.RazorpayConfig {
	return config.RazorpayConfig{
		Enabled: true, BaseURL: baseURL, KeyID: "rzp_test_billmesh", KeySecret: "test-provider-secret",
		Environment: environment,
	}
}

func makePaymentReconciliationDue(t *testing.T, pool *pgxpool.Pool, paymentID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE payments SET created_at=now()-interval '1 hour',
		last_reconciled_at=now()-interval '1 hour',reconciliation_next_attempt_at=now(),reconciliation_outcome=NULL WHERE id=$1`, paymentID); err != nil {
		t.Fatal(err)
	}
}

func waitForReconciliation(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("reconciliation condition was not reached")
}

func createProviderOrder(t *testing.T, h *testkit.HTTP, body map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, h.MockURL+"/v1/orders", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth("rzp_test_billmesh", "test-provider-secret")
	response, err := h.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("create provider order returned %d: %s", response.StatusCode, raw)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || result.ID == "" {
		t.Fatalf("decode provider order: id=%q err=%v", result.ID, err)
	}
	return result.ID
}

func postMockControl(t *testing.T, h *testkit.HTTP, path string, body map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	response, err := h.Client.Post(h.MockURL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("mock control %s returned %d: %s", path, response.StatusCode, raw)
	}
}
