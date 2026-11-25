//go:build e2e

package payments_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestRazorpayAuthorizedPaymentDoesNotFulfil(t *testing.T) {
	h, pool, _, _, order := captureOrderFixture(t, "authorized-only")
	config := order.Checkout.ClientConfig
	eventID := testkit.Unique("evt_authorized")
	raw := []byte(fmt.Sprintf(`{"entity":"event","account_id":"%s","event":"payment.authorized","payload":{"payment":{"entity":{"id":"%s","entity":"payment","order_id":"%s","status":"authorized","amount":%d,"currency":"%s"}}}}`,
		testkit.RazorpayTestAccountID, testkit.Unique("pay_authorized"), config.OrderID, config.AmountMinor, config.Currency))
	if status := h.RazorpayWebhook(t, "/v1/payments/webhook", testkit.RazorpayWebhookFixture{EventID: eventID, Raw: raw}, "test-webhook-secret"); status != http.StatusNoContent {
		t.Fatalf("signed payment.authorized returned %d", status)
	}
	assertUnfulfilledPayment(t, pool, order.PaymentID, eventID, true)
}

func TestRazorpayCaptureRejectsCrossEnvironmentOrder(t *testing.T) {
	h, pool, _, _, order := captureOrderFixture(t, "cross-environment")
	if _, err := pool.Exec(context.Background(), `UPDATE payments SET provider_environment='production' WHERE id=$1`, order.PaymentID); err != nil {
		t.Fatal(err)
	}
	config := order.Checkout.ClientConfig
	event := testkit.RazorpayPaymentCaptured(testkit.Unique("evt_cross_environment"), config.OrderID, testkit.Unique("pay_cross_environment"), config.AmountMinor, config.Currency)
	if status := h.RazorpayWebhook(t, "/v1/payments/webhook", event, "test-webhook-secret"); status != http.StatusBadRequest {
		t.Fatalf("cross-environment capture returned %d", status)
	}
	assertUnfulfilledPayment(t, pool, order.PaymentID, event.EventID, false)
}

func TestRazorpayCaptureCommitsCompleteAuthoritativeState(t *testing.T) {
	h, pool, accountID, productID, order := captureOrderFixture(t, "atomic-capture")
	var beforeRevision int64
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE((SELECT revision FROM billing_state_revisions WHERE account_id=$1 AND product_id=$2),0)`, accountID, productID).Scan(&beforeRevision); err != nil {
		t.Fatal(err)
	}
	var beforeAvailable int64
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE((SELECT available FROM wallets WHERE account_id=$1 AND product_id=$2),0)`, accountID, productID).Scan(&beforeAvailable); err != nil {
		t.Fatal(err)
	}
	paymentID := testkit.Unique("pay_atomic")
	eventID := testkit.Unique("evt_atomic")
	sendCapture(t, h, order, paymentID, eventID)

	var status, storedPaymentID string
	var credits, available, grants, ledger, invoices, outbox, providerEvents int64
	var afterRevision, eventRevision int64
	err := pool.QueryRow(context.Background(), `SELECT p.status,p.provider_payment_id,p.credits,w.available,
		(SELECT count(*) FROM credit_grants g WHERE g.operation_ref='payment:'||p.id),
		(SELECT count(*) FROM credit_ledger l WHERE l.operation_ref='grant:payment:'||p.id),
		(SELECT count(*) FROM invoices i WHERE i.payment_id=p.id),
		(SELECT count(*) FROM outbox_events e WHERE e.aggregate_id=w.id AND e.event_type='payment.succeeded'),
		(SELECT count(*) FROM provider_events pe WHERE pe.provider_event_id=$2 AND pe.processed_at IS NOT NULL),
		br.revision,
		COALESCE((SELECT max(e.billing_revision) FROM outbox_events e WHERE e.aggregate_id=w.id AND e.event_type='payment.succeeded'),0)
		FROM payments p
		JOIN credit_packs cp ON cp.id=p.credit_pack_id
		JOIN wallets w ON w.account_id=p.account_id AND w.product_id=cp.product_id
		JOIN billing_state_revisions br ON br.account_id=p.account_id AND br.product_id=cp.product_id
		WHERE p.id=$1`, order.PaymentID, eventID).Scan(&status, &storedPaymentID, &credits, &available, &grants, &ledger, &invoices, &outbox, &providerEvents, &afterRevision, &eventRevision)
	if err != nil {
		t.Fatal(err)
	}
	if status != "captured" || storedPaymentID != paymentID || available != beforeAvailable+credits || grants != 1 || ledger != 1 || invoices != 1 || outbox != 1 || providerEvents != 1 {
		t.Fatalf("partial capture state: status=%s payment=%s available=%d credits=%d grants=%d ledger=%d invoices=%d outbox=%d events=%d",
			status, storedPaymentID, available, credits, grants, ledger, invoices, outbox, providerEvents)
	}
	if afterRevision <= beforeRevision || eventRevision != afterRevision {
		t.Fatalf("authoritative revision did not advance atomically: before=%d after=%d event=%d", beforeRevision, afterRevision, eventRevision)
	}
}

func TestRazorpayCaptureRollsBackAtEveryFulfilmentStep(t *testing.T) {
	scenarios := []struct {
		name, table, action string
		condition           func(paymentID, accountID string) string
	}{
		{name: "provider event", table: "provider_events", action: "INSERT", condition: func(_, _ string) string { return "NEW.provider_event_id LIKE 'evt_rollback%'" }},
		{name: "payment", table: "payments", action: "UPDATE", condition: func(paymentID, _ string) string { return fmt.Sprintf("NEW.id = '%s'::uuid", paymentID) }},
		{name: "wallet creation", table: "wallets", action: "INSERT", condition: func(_, accountID string) string { return fmt.Sprintf("NEW.account_id = '%s'::uuid", accountID) }},
		{name: "credit grant", table: "credit_grants", action: "INSERT", condition: walletOwnerCondition},
		{name: "wallet balance", table: "wallets", action: "UPDATE", condition: func(_, accountID string) string {
			return fmt.Sprintf("NEW.account_id = '%s'::uuid AND NEW.available <> OLD.available", accountID)
		}},
		{name: "ledger", table: "credit_ledger", action: "INSERT", condition: walletOwnerCondition},
		{name: "invoice", table: "invoices", action: "INSERT", condition: func(paymentID, _ string) string { return fmt.Sprintf("NEW.payment_id = '%s'::uuid", paymentID) }},
		{name: "billing revision", table: "billing_state_revisions", action: "UPDATE", condition: func(_, accountID string) string { return fmt.Sprintf("NEW.account_id = '%s'::uuid", accountID) }},
		{name: "outbox", table: "outbox_events", action: "INSERT", condition: func(_, accountID string) string { return fmt.Sprintf("NEW.account_id = '%s'::uuid", accountID) }},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			h, pool, accountID, productID, order := captureOrderFixture(t, "rollback-"+strings.ReplaceAll(scenario.name, " ", "-"))
			if _, err := pool.Exec(context.Background(), `INSERT INTO billing_state_revisions(account_id,product_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, accountID, productID); err != nil {
				t.Fatal(err)
			}
			installCaptureFailureTrigger(t, pool, scenario.table, scenario.action, scenario.condition(order.PaymentID, accountID))
			eventID := testkit.Unique("evt_rollback")
			config := order.Checkout.ClientConfig
			event := testkit.RazorpayPaymentCaptured(eventID, config.OrderID, testkit.Unique("pay_rollback"), config.AmountMinor, config.Currency)
			status := h.RazorpayWebhook(t, "/v1/payments/webhook", event, "test-webhook-secret")
			removeCaptureFailureTrigger(t, pool, scenario.table)
			if status < 500 {
				t.Fatalf("injected %s failure returned %d", scenario.name, status)
			}
			assertUnfulfilledPayment(t, pool, order.PaymentID, eventID, false)
		})
	}
}

func TestRazorpayWebhookRacesProviderReconciliationWithoutDoubleFulfilment(t *testing.T) {
	h, pool, _, _, order := captureOrderFixture(t, "reconciliation-race")
	setRazorpayBehavior(t, h, map[string]any{"delay_ms": 750})
	t.Cleanup(func() { setRazorpayBehavior(t, h, map[string]any{}) })
	config := order.Checkout.ClientConfig
	paymentID := testkit.Unique("pay_reconciliation_race")
	addRazorpayPayment(t, h, map[string]any{
		"id": paymentID, "order_id": config.OrderID, "status": "captured",
		"amount": config.AmountMinor, "currency": config.Currency,
	})
	if _, err := pool.Exec(context.Background(), `UPDATE payments SET last_reconciled_at=now()-interval '1 hour',created_at=now()-interval '1 hour' WHERE id=$1`, order.PaymentID); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	workerStarted := false
	for time.Now().Before(deadline) {
		var recentlyReconciled bool
		if err := pool.QueryRow(context.Background(), `SELECT COALESCE(last_reconciled_at > now()-interval '2 seconds',false) FROM payments WHERE id=$1`, order.PaymentID).Scan(&recentlyReconciled); err != nil {
			t.Fatal(err)
		}
		if recentlyReconciled {
			workerStarted = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !workerStarted {
		t.Fatal("worker did not start provider reconciliation")
	}
	sendCapture(t, h, order, paymentID, testkit.Unique("evt_racing_webhook"))
	setRazorpayBehavior(t, h, map[string]any{})

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var reconciled int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM provider_events WHERE provider_event_id=$1`, "reconciliation:"+paymentID).Scan(&reconciled); err != nil {
			t.Fatal(err)
		}
		if reconciled == 1 {
			assertCapturedOnce(t, pool, config.OrderID)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("reconciliation did not finish after the racing webhook")
}

func captureOrderFixture(t *testing.T, prefix string) (*testkit.HTTP, *pgxpool.Pool, string, string, testkit.PaymentOrderFixture) {
	t.Helper()
	h := testkit.NewHTTP(t)
	org := testkit.Unique(prefix)
	token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	accountID := testkit.CreateFixtureAccount(t, h, org, token)
	order := testkit.CreateCreditPackOrder(t, h, "daybook", 500, token)
	pool := e2ePool(t)
	var productID string
	if err := pool.QueryRow(context.Background(), `SELECT cp.product_id FROM payments p JOIN credit_packs cp ON cp.id=p.credit_pack_id WHERE p.id=$1`, order.PaymentID).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	return h, pool, accountID, productID, order
}

func assertUnfulfilledPayment(t *testing.T, pool *pgxpool.Pool, paymentID, eventID string, eventPersisted bool) {
	t.Helper()
	var status string
	var providerPaymentID *string
	if err := pool.QueryRow(context.Background(), `SELECT status,provider_payment_id FROM payments WHERE id=$1`, paymentID).Scan(&status, &providerPaymentID); err != nil {
		t.Fatal(err)
	}
	var grants, ledger, invoices, events int
	if err := pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM credit_grants WHERE operation_ref='payment:'||$1::text),
		(SELECT count(*) FROM credit_ledger WHERE operation_ref='grant:payment:'||$1::text),
		(SELECT count(*) FROM invoices WHERE payment_id=$1::uuid),
		(SELECT count(*) FROM provider_events WHERE provider_event_id=$2)`, paymentID, eventID).Scan(&grants, &ledger, &invoices, &events); err != nil {
		t.Fatal(err)
	}
	wantEvents := 0
	if eventPersisted {
		wantEvents = 1
	}
	if status != "created" || providerPaymentID != nil || grants != 0 || ledger != 0 || invoices != 0 || events != wantEvents {
		t.Fatalf("unexpected unfulfilled state: status=%s provider_payment=%v grants=%d ledger=%d invoices=%d events=%d want_events=%d",
			status, providerPaymentID, grants, ledger, invoices, events, wantEvents)
	}
}

func walletOwnerCondition(_, accountID string) string {
	return fmt.Sprintf("EXISTS(SELECT 1 FROM billmesh.wallets w WHERE w.id=NEW.wallet_id AND w.account_id='%s'::uuid)", accountID)
}

func installCaptureFailureTrigger(t *testing.T, pool *pgxpool.Pool, table, action, condition string) {
	t.Helper()
	removeCaptureFailureTrigger(t, pool, table)
	statement := fmt.Sprintf(`
		CREATE FUNCTION billmesh.test_capture_failure() RETURNS trigger
		LANGUAGE plpgsql SECURITY DEFINER SET search_path=billmesh,pg_temp AS $body$
		BEGIN
		  IF %s THEN RAISE EXCEPTION 'injected capture failure'; END IF;
		  RETURN NEW;
		END;
		$body$;
		CREATE TRIGGER test_capture_failure BEFORE %s ON billmesh.%s
		FOR EACH ROW EXECUTE FUNCTION billmesh.test_capture_failure();`, condition, action, table)
	if _, err := pool.Exec(context.Background(), statement); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeCaptureFailureTrigger(t, pool, table) })
}

func removeCaptureFailureTrigger(t *testing.T, pool *pgxpool.Pool, table string) {
	t.Helper()
	statement := fmt.Sprintf(`DROP TRIGGER IF EXISTS test_capture_failure ON billmesh.%s; DROP FUNCTION IF EXISTS billmesh.test_capture_failure();`, table)
	if _, err := pool.Exec(context.Background(), statement); err != nil {
		t.Fatal(err)
	}
}

func addRazorpayPayment(t *testing.T, h *testkit.HTTP, payment map[string]any) {
	t.Helper()
	raw, err := json.Marshal(payment)
	if err != nil {
		t.Fatal(err)
	}
	response, err := h.Client.Post(h.MockURL+"/test/razorpay/payments", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("add Razorpay payment returned %d: %s", response.StatusCode, body)
	}
}
