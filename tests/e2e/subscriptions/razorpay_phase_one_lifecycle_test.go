//go:build e2e

package subscriptions_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestPhaseOnePaidTransitionUsesOneTimeOrderWithoutRecurringMandate(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("one-time-order-boundary")
	token := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, nil)
	h.Token = token
	testkit.CreateFixtureAccount(t, h, org, token)
	planID := testkit.FixturePlan(t, h, "daybook", "paid", token)
	beforeOrderCalls := phaseOneProviderOrderCallCount(t, h)

	status, raw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/subscription-transitions", map[string]any{"plan_id": planID}, token,
		http.Header{"Idempotency-Key": []string{testkit.Unique("one-time-order-transition")}})
	if status != http.StatusCreated {
		t.Fatalf("create paid transition returned %d: %s", status, raw)
	}
	transition := testkit.Decode[transitionResponse](t, raw)
	if transition.Status != "requires_payment" || transition.Checkout == nil || transition.Checkout.ClientConfig.OrderID == "" {
		t.Fatalf("phase-one transition did not return a Razorpay Order checkout: %s", raw)
	}
	afterOrders := phaseOneProviderOrders(t, h)
	afterOrderCalls := phaseOneProviderOrderCallCount(t, h)
	if afterOrderCalls != beforeOrderCalls+1 {
		t.Fatalf("provider order calls changed from %d to %d", beforeOrderCalls, afterOrderCalls)
	}
	var matched bool
	for _, order := range afterOrders {
		if order.ID == transition.Checkout.ClientConfig.OrderID {
			matched = order.Entity == "order" && order.Notes["billmesh_environment"] == "test" && order.Notes["billmesh_payment_id"] != ""
		}
	}
	if !matched {
		t.Fatalf("checkout order %s was not a Billmesh one-time Razorpay Order", transition.Checkout.ClientConfig.OrderID)
	}

	pool := subscriptionE2EPool(t)
	var paymentPurpose, paymentStatus, providerOrderID, renewal string
	var recurringMandate bool
	if err := pool.QueryRow(context.Background(), `SELECT p.purpose,p.status,p.provider_order_id,
		t.billing_policy #>> '{lifecycle,renewal}',(t.billing_policy #>> '{checkout,recurring_mandate}')::boolean
		FROM subscription_transitions t JOIN payments p ON p.transition_id=t.id WHERE t.id=$1`, transition.ID).
		Scan(&paymentPurpose, &paymentStatus, &providerOrderID, &renewal, &recurringMandate); err != nil {
		t.Fatal(err)
	}
	if paymentPurpose != "subscription" || paymentStatus != "created" || providerOrderID != transition.Checkout.ClientConfig.OrderID || renewal != "manual" || recurringMandate {
		t.Fatalf("phase-one boundary: purpose=%s status=%s order=%s renewal=%s recurring_mandate=%t",
			paymentPurpose, paymentStatus, providerOrderID, renewal, recurringMandate)
	}
	var recurringProviderColumns int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns
		WHERE table_schema='billmesh' AND column_name IN ('provider_customer_id','provider_subscription_id','provider_mandate_id')`).Scan(&recurringProviderColumns); err != nil {
		t.Fatal(err)
	}
	if recurringProviderColumns != 0 {
		t.Fatalf("phase-one schema exposes %d unapproved recurring-provider identifiers", recurringProviderColumns)
	}
}

func TestPaidSubscriptionWithoutRenewalMovesThroughGraceToExpiry(t *testing.T) {
	h, pool, token, subscriptionID := activePhaseOnePaidSubscription(t, "manual-renewal-expiry")
	beforeOrderCalls := phaseOneProviderOrderCallCount(t, h)
	var beforePayments, beforeInvoices int
	if err := pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM payments p JOIN subscription_transitions st ON st.id=p.transition_id WHERE st.subscription_id=$1),
		(SELECT count(*) FROM invoices WHERE subscription_id=$1)`, subscriptionID).Scan(&beforePayments, &beforeInvoices); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE subscriptions SET current_period_end=now()-interval '1 minute' WHERE id=$1`, subscriptionID); err != nil {
		t.Fatal(err)
	}
	waitForSubscriptionStatus(t, pool, subscriptionID, "past_due")

	var graceEnd *time.Time
	var pastDueEvents, renewalEvents, payments, invoices int
	if err := pool.QueryRow(context.Background(), `SELECT grace_period_end,
		(SELECT count(*) FROM outbox_events WHERE aggregate_id=s.id AND event_type='subscription.past_due'),
		(SELECT count(*) FROM outbox_events WHERE aggregate_id=s.id AND event_type='subscription.renewed'),
		(SELECT count(*) FROM payments p JOIN subscription_transitions st ON st.id=p.transition_id WHERE st.subscription_id=s.id),
		(SELECT count(*) FROM invoices WHERE subscription_id=s.id)
		FROM subscriptions s WHERE s.id=$1`, subscriptionID).Scan(&graceEnd, &pastDueEvents, &renewalEvents, &payments, &invoices); err != nil {
		t.Fatal(err)
	}
	afterOrderCalls := phaseOneProviderOrderCallCount(t, h)
	if graceEnd == nil || !graceEnd.After(time.Now().UTC()) || pastDueEvents != 1 || renewalEvents != 0 || payments != beforePayments || invoices != beforeInvoices || afterOrderCalls != beforeOrderCalls {
		t.Fatalf("missing-payment boundary: grace=%v past_due_events=%d renewal_events=%d payments=%d/%d invoices=%d/%d orders=%d/%d",
			graceEnd, pastDueEvents, renewalEvents, payments, beforePayments, invoices, beforeInvoices, afterOrderCalls, beforeOrderCalls)
	}

	snapshot := phaseOneSnapshot(t, h, token)
	if snapshot.Subscription == nil || snapshot.Subscription.Status != "past_due" || snapshot.Subscription.GracePeriodEnd == nil || len(snapshot.Entitlements) == 0 {
		t.Fatalf("snapshot did not preserve grace-period access: %+v", snapshot)
	}
	if snapshot.Subscription.BillingPolicy.Lifecycle.Renewal != "manual" || snapshot.Subscription.BillingPolicy.Checkout.RecurringMandate {
		t.Fatalf("snapshot claimed recurring collection: %+v", snapshot.Subscription.BillingPolicy)
	}

	if _, err := pool.Exec(context.Background(), `UPDATE subscriptions SET grace_period_end=now()-interval '1 minute' WHERE id=$1`, subscriptionID); err != nil {
		t.Fatal(err)
	}
	waitForSubscriptionStatus(t, pool, subscriptionID, "expired")
	expired := phaseOneSnapshot(t, h, token)
	if expired.Subscription == nil || expired.Subscription.Status != "expired" || expired.Subscription.GracePeriodEnd != nil || len(expired.Entitlements) != 0 {
		t.Fatalf("expired snapshot retained paid access: %+v", expired)
	}
	var expiredEvents int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='subscription.expired'`, subscriptionID).Scan(&expiredEvents); err != nil || expiredEvents != 1 {
		t.Fatalf("subscription.expired events=%d err=%v", expiredEvents, err)
	}
}

func TestScheduledCancellationWinsAtPeriodEndWithoutRenewalAttempt(t *testing.T) {
	h, pool, token, subscriptionID := activePhaseOnePaidSubscription(t, "period-end-cancellation")
	beforeOrderCalls := phaseOneProviderOrderCallCount(t, h)
	var beforePayments int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM payments p JOIN subscription_transitions st ON st.id=p.transition_id WHERE st.subscription_id=$1`, subscriptionID).Scan(&beforePayments); err != nil {
		t.Fatal(err)
	}
	status, raw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/subscriptions/current/cancellation", map[string]any{"effective": "period_end"}, token,
		http.Header{"Idempotency-Key": []string{testkit.Unique("period-end-cancel")}})
	if status != http.StatusOK {
		t.Fatalf("schedule cancellation returned %d: %s", status, raw)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE subscriptions SET current_period_end=now()-interval '1 minute' WHERE id=$1`, subscriptionID); err != nil {
		t.Fatal(err)
	}
	waitForSubscriptionStatus(t, pool, subscriptionID, "cancelled")

	var graceEnd *time.Time
	var pastDueEvents, cancelledEvents, payments int
	if err := pool.QueryRow(context.Background(), `SELECT grace_period_end,
		(SELECT count(*) FROM outbox_events WHERE aggregate_id=s.id AND event_type='subscription.past_due'),
		(SELECT count(*) FROM outbox_events WHERE aggregate_id=s.id AND event_type='subscription.cancelled'),
		(SELECT count(*) FROM payments p JOIN subscription_transitions st ON st.id=p.transition_id WHERE st.subscription_id=s.id)
		FROM subscriptions s WHERE s.id=$1`, subscriptionID).Scan(&graceEnd, &pastDueEvents, &cancelledEvents, &payments); err != nil {
		t.Fatal(err)
	}
	afterOrderCalls := phaseOneProviderOrderCallCount(t, h)
	if graceEnd != nil || pastDueEvents != 0 || cancelledEvents != 1 || payments != beforePayments || afterOrderCalls != beforeOrderCalls {
		t.Fatalf("period-end cancellation: grace=%v past_due=%d cancelled=%d payments=%d/%d orders=%d/%d",
			graceEnd, pastDueEvents, cancelledEvents, payments, beforePayments, afterOrderCalls, beforeOrderCalls)
	}
	snapshot := phaseOneSnapshot(t, h, token)
	if snapshot.Subscription == nil || snapshot.Subscription.Status != "cancelled" || len(snapshot.Entitlements) != 0 {
		t.Fatalf("cancelled snapshot retained paid access: %+v", snapshot)
	}
}

type phaseOneOrder struct {
	ID     string            `json:"id"`
	Entity string            `json:"entity"`
	Notes  map[string]string `json:"notes"`
}

type phaseOneSnapshotResponse struct {
	Entitlements map[string]any `json:"entitlements"`
	Subscription *struct {
		Status         string     `json:"status"`
		GracePeriodEnd *time.Time `json:"grace_period_end"`
		BillingPolicy  struct {
			Lifecycle struct {
				Renewal string `json:"renewal"`
			} `json:"lifecycle"`
			Checkout struct {
				RecurringMandate bool `json:"recurring_mandate"`
			} `json:"checkout"`
		} `json:"billing_policy"`
	} `json:"subscription"`
}

func phaseOneProviderOrders(t *testing.T, h *testkit.HTTP) []phaseOneOrder {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, h.MockURL+"/test/razorpay/orders?count=100", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth("rzp_test_billmesh", "test-provider-secret")
	response, err := h.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var collection struct {
		Items []phaseOneOrder `json:"items"`
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list provider orders returned %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&collection); err != nil {
		t.Fatal(err)
	}
	return collection.Items
}

func phaseOneProviderOrderCallCount(t *testing.T, h *testkit.HTTP) int {
	t.Helper()
	response, err := h.Client.Get(h.MockURL + "/test/order-count")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result struct {
		Count int `json:"count"`
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("provider order-call count returned %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result.Count
}

func activePhaseOnePaidSubscription(t *testing.T, prefix string) (*testkit.HTTP, *pgxpool.Pool, string, string) {
	t.Helper()
	h := testkit.NewHTTP(t)
	org := testkit.Unique(prefix)
	token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	h.Token = token
	testkit.CreateFixtureAccount(t, h, org, token)
	subscriptionID := testkit.ActivatePaidSubscription(t, h, "daybook", "daybook-paid", token)
	return h, subscriptionE2EPool(t), token, subscriptionID
}

func waitForSubscriptionStatus(t *testing.T, pool *pgxpool.Pool, subscriptionID, want string) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		if err := pool.QueryRow(context.Background(), `SELECT status FROM subscriptions WHERE id=$1`, subscriptionID).Scan(&status); err == nil && status == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	var status string
	_ = pool.QueryRow(context.Background(), `SELECT status FROM subscriptions WHERE id=$1`, subscriptionID).Scan(&status)
	t.Fatalf("subscription status=%s, want %s", status, want)
}

func phaseOneSnapshot(t *testing.T, h *testkit.HTTP, token string) phaseOneSnapshotResponse {
	t.Helper()
	raw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/billing-snapshot?product=daybook", nil, token)
	return testkit.Decode[phaseOneSnapshotResponse](t, raw)
}
