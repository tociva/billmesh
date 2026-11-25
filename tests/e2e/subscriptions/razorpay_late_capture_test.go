//go:build e2e

package subscriptions_test

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestRazorpayLateCaptureIsRetainedWithoutActivatingCancelledOrExpiredTransition(t *testing.T) {
	for _, terminal := range []string{"cancelled", "expired"} {
		t.Run(terminal, func(t *testing.T) {
			h := testkit.NewHTTP(t)
			org := testkit.Unique("late-capture-" + terminal)
			token := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, nil)
			testkit.CreateFixtureAccount(t, h, org, token)
			planID := testkit.FixturePlan(t, h, "daybook", "paid", token)
			status, raw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/subscription-transitions", map[string]any{
				"plan_id": planID,
			}, token, http.Header{"Idempotency-Key": []string{testkit.Unique("late-transition")}})
			if status != http.StatusCreated {
				t.Fatalf("create paid transition returned %d: %s", status, raw)
			}
			transition := testkit.Decode[transitionResponse](t, raw)
			if transition.Checkout == nil {
				t.Fatalf("paid transition omitted checkout: %s", raw)
			}

			// Transition creation and any browser-side success claim cannot activate billing.
			before := testkit.Decode[snapshotResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/billing-snapshot?product=daybook", nil, token))
			if before.Subscription != nil || before.PendingTransition == nil {
				t.Fatalf("success URL configuration changed authoritative state: %+v", before)
			}

			pool := subscriptionE2EPool(t)
			if terminal == "cancelled" {
				h.RequireStatusWithHeaders(t, http.StatusOK, http.MethodPost, "/v1/subscription-transitions/"+transition.ID+"/cancel", nil, token,
					http.Header{"Idempotency-Key": []string{testkit.Unique("cancel-late")}})
			} else {
				if _, err := pool.Exec(context.Background(), `UPDATE subscription_transitions SET checkout_expires_at=now()-interval '1 minute' WHERE id=$1`, transition.ID); err != nil {
					t.Fatal(err)
				}
			}

			config := transition.Checkout.ClientConfig
			providerPaymentID := testkit.Unique("pay_late")
			eventID := testkit.Unique("evt_late")
			event := testkit.RazorpayPaymentCaptured(eventID, config.OrderID, providerPaymentID, config.AmountMinor, config.Currency)
			if status := h.RazorpayWebhook(t, "/v1/payments/webhook", event, "test-webhook-secret"); status != http.StatusNoContent {
				t.Fatalf("late capture returned %d", status)
			}

			var transitionStatus, paymentStatus, storedPaymentID, reason string
			var subscriptions, lateEvents, providerEvents int
			err := pool.QueryRow(context.Background(), `SELECT t.status,p.status,p.provider_payment_id,
				(SELECT count(*) FROM subscriptions s WHERE s.account_id=t.account_id AND s.product_id=t.product_id),
				(SELECT count(*) FROM outbox_events e WHERE e.aggregate_type='payment' AND e.aggregate_id=p.id AND e.event_type='payment.late_capture'),
				COALESCE((SELECT e.payload->>'reason' FROM outbox_events e WHERE e.aggregate_type='payment' AND e.aggregate_id=p.id AND e.event_type='payment.late_capture' ORDER BY e.created_at DESC LIMIT 1),''),
				(SELECT count(*) FROM provider_events pe WHERE pe.provider_event_id=$2 AND pe.processed_at IS NOT NULL)
				FROM subscription_transitions t JOIN payments p ON p.transition_id=t.id WHERE t.id=$1`, transition.ID, eventID).
				Scan(&transitionStatus, &paymentStatus, &storedPaymentID, &subscriptions, &lateEvents, &reason, &providerEvents)
			if err != nil {
				t.Fatal(err)
			}
			if transitionStatus != terminal || paymentStatus != "captured" || storedPaymentID != providerPaymentID || subscriptions != 0 || lateEvents != 1 || reason == "" || providerEvents != 1 {
				t.Fatalf("late capture was not retained safely: transition=%s payment=%s provider_payment=%s subscriptions=%d events=%d reason=%q provider_events=%d",
					transitionStatus, paymentStatus, storedPaymentID, subscriptions, lateEvents, reason, providerEvents)
			}

			after := testkit.Decode[snapshotResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/billing-snapshot?product=daybook", nil, token))
			if after.Subscription != nil {
				t.Fatalf("late capture activated paid subscription: %+v", after)
			}
		})
	}
}

func TestRazorpayDuplicateCaptureCompletesSubscriptionExactlyOnce(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("duplicate-subscription-capture")
	token := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, nil)
	testkit.CreateFixtureAccount(t, h, org, token)
	planID := testkit.FixturePlan(t, h, "daybook", "paid", token)
	status, raw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/subscription-transitions", map[string]any{"plan_id": planID}, token,
		http.Header{"Idempotency-Key": []string{testkit.Unique("duplicate-capture-transition")}})
	if status != http.StatusCreated {
		t.Fatalf("create paid transition returned %d: %s", status, raw)
	}
	transition := testkit.Decode[transitionResponse](t, raw)
	if transition.Checkout == nil {
		t.Fatalf("paid transition omitted checkout: %s", raw)
	}
	config := transition.Checkout.ClientConfig
	paymentID := testkit.Unique("pay_duplicate_subscription")
	first := testkit.RazorpayPaymentCaptured(testkit.Unique("evt_duplicate_first"), config.OrderID, paymentID, config.AmountMinor, config.Currency)
	if status := h.RazorpayWebhook(t, "/v1/payments/webhook", first, "test-webhook-secret"); status != http.StatusNoContent {
		t.Fatalf("first capture returned %d", status)
	}
	if status := h.RazorpayWebhook(t, "/v1/payments/webhook", first, "test-webhook-secret"); status != http.StatusNoContent {
		t.Fatalf("same-event replay returned %d", status)
	}
	second := testkit.RazorpayPaymentCaptured(testkit.Unique("evt_duplicate_second"), config.OrderID, paymentID, config.AmountMinor, config.Currency)
	if status := h.RazorpayWebhook(t, "/v1/payments/webhook", second, "test-webhook-secret"); status != http.StatusNoContent {
		t.Fatalf("second-event replay returned %d", status)
	}

	pool := subscriptionE2EPool(t)
	var transitionStatus, paymentStatus string
	var subscriptions, history, invoices, completedEvents, succeededEvents int
	err := pool.QueryRow(context.Background(), `SELECT t.status,p.status,
		(SELECT count(*) FROM subscriptions s WHERE s.id=t.subscription_id),
		(SELECT count(*) FROM subscription_history h WHERE h.operation_ref='transition:'||t.id||':payment:'||$2),
		(SELECT count(*) FROM invoices i WHERE i.payment_id=p.id),
		(SELECT count(*) FROM outbox_events e WHERE e.aggregate_type='subscription' AND e.aggregate_id=t.subscription_id AND e.event_type='subscription.transition_completed'),
		(SELECT count(*) FROM outbox_events e WHERE e.aggregate_type='payment' AND e.aggregate_id=p.id AND e.event_type='payment.succeeded')
		FROM subscription_transitions t JOIN payments p ON p.transition_id=t.id WHERE t.id=$1`, transition.ID, paymentID).
		Scan(&transitionStatus, &paymentStatus, &subscriptions, &history, &invoices, &completedEvents, &succeededEvents)
	if err != nil {
		t.Fatal(err)
	}
	if transitionStatus != "completed" || paymentStatus != "captured" || subscriptions != 1 || history != 1 || invoices != 1 || completedEvents != 1 || succeededEvents != 1 {
		t.Fatalf("duplicate capture changed subscription result: transition=%s payment=%s subscriptions=%d history=%d invoices=%d completed_events=%d succeeded_events=%d",
			transitionStatus, paymentStatus, subscriptions, history, invoices, completedEvents, succeededEvents)
	}
}

func subscriptionE2EPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), os.Getenv("BILLMESH_E2E_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
