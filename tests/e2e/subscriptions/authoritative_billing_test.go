//go:build e2e

package subscriptions_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tociva/billmesh/tests/testkit"
)

type catalogueResponse struct {
	Revision int64 `json:"revision"`
	Plans    []struct {
		ID           string `json:"id"`
		BillingModel string `json:"billing_model"`
		PriceMinor   int64  `json:"price_minor"`
		Currency     string `json:"currency"`
	} `json:"plans"`
}

type transitionResponse struct {
	ID             string `json:"id"`
	SubscriptionID string `json:"subscription_id"`
	Status         string `json:"status"`
	Checkout       *struct {
		OrderID     string `json:"order_id"`
		AmountMinor int64  `json:"amount_minor"`
		Currency    string `json:"currency"`
	} `json:"checkout"`
}

type snapshotResponse struct {
	Revision          int64 `json:"revision"`
	PendingTransition *struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"pending_transition"`
	Subscription *struct {
		ID                string `json:"id"`
		Status            string `json:"status"`
		CancelAtPeriodEnd bool   `json:"cancel_at_period_end"`
		EffectivePlan     struct {
			ID           string `json:"id"`
			BillingModel string `json:"billing_model"`
		} `json:"effective_plan"`
	} `json:"subscription"`
}

func TestAuthoritativePaidTransitionSnapshotAndCancellation(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("authoritative-paid")
	token := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, map[string]any{"sub": testkit.Unique("paid-customer")})
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{
		"name": "Authoritative Paid", "application": "daybook", "organization_id": org,
	}, token)

	status, catalogueRaw, catalogueHeaders := h.JSON(t, http.MethodGet, "/v1/catalog?product=daybook", nil, token)
	if status != http.StatusOK {
		t.Fatalf("catalogue returned %d: %s", status, catalogueRaw)
	}
	if catalogueHeaders.Get("ETag") == "" {
		t.Fatal("catalogue did not return an ETag")
	}
	if strings.Contains(string(catalogueRaw), `"slug":"daybook-paid"`) {
		t.Fatalf("catalogue exposed a plan slug instead of an opaque plan id: %s", catalogueRaw)
	}
	catalogue := testkit.Decode[catalogueResponse](t, catalogueRaw)
	var paidPlan string
	for _, plan := range catalogue.Plans {
		if plan.BillingModel == "paid" && plan.PriceMinor > 0 {
			paidPlan = plan.ID
			break
		}
	}
	if paidPlan == "" {
		t.Fatal("catalogue does not contain an available paid plan")
	}

	// A caller assertion is not payment verification.
	h.RequireStatus(t, http.StatusPaymentRequired, http.MethodPost, "/v1/subscriptions", map[string]any{
		"plan_id": paidPlan, "payment_status": "verified",
	}, token)

	key := testkit.Unique("paid-transition")
	headers := http.Header{"Idempotency-Key": []string{key}}
	status, transitionRaw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/subscription-transitions", map[string]any{"plan_id": paidPlan}, token, headers)
	if status != http.StatusCreated {
		t.Fatalf("create transition returned %d: %s", status, transitionRaw)
	}
	transition := testkit.Decode[transitionResponse](t, transitionRaw)
	if transition.Status != "requires_payment" || transition.Checkout == nil || transition.Checkout.OrderID == "" {
		t.Fatalf("paid transition did not create a server-priced checkout: %s", transitionRaw)
	}

	status, replayRaw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/subscription-transitions", map[string]any{"plan_id": paidPlan}, token, headers)
	if status != http.StatusOK || testkit.Decode[transitionResponse](t, replayRaw).ID != transition.ID {
		t.Fatalf("idempotent replay returned %d: %s", status, replayRaw)
	}

	status, snapshotRaw, snapshotHeaders := h.JSON(t, http.MethodGet, "/v1/billing-snapshot?product=daybook", nil, token)
	if status != http.StatusOK {
		t.Fatalf("snapshot returned %d: %s", status, snapshotRaw)
	}
	snapshot := testkit.Decode[snapshotResponse](t, snapshotRaw)
	if snapshot.Subscription != nil || snapshot.PendingTransition == nil || snapshot.PendingTransition.ID != transition.ID {
		t.Fatalf("snapshot did not expose the pending transition without granting a subscription: %s", snapshotRaw)
	}
	oldETag := snapshotHeaders.Get("ETag")
	status, _, _ = h.JSONWithHeaders(t, http.MethodGet, "/v1/billing-snapshot?product=daybook", nil, token, http.Header{"If-None-Match": []string{oldETag}})
	if status != http.StatusNotModified {
		t.Fatalf("conditional snapshot returned %d, want 304", status)
	}

	if status := h.SignedWebhook(t, "/v1/payments/webhook", map[string]any{
		"id": testkit.Unique("paid-event"), "type": "payment.captured", "payment_id": testkit.Unique("provider-payment"),
		"order_id": transition.Checkout.OrderID, "status": "captured", "amount_minor": transition.Checkout.AmountMinor,
		"currency": transition.Checkout.Currency,
	}, "test-webhook-secret"); status != http.StatusNoContent {
		t.Fatalf("verified payment capture returned %d", status)
	}

	completedRaw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/subscription-transitions/"+transition.ID, nil, token)
	completed := testkit.Decode[transitionResponse](t, completedRaw)
	if completed.Status != "completed" || completed.SubscriptionID == "" {
		t.Fatalf("verified capture did not complete the transition: %s", completedRaw)
	}
	status, activeRaw, activeHeaders := h.JSONWithHeaders(t, http.MethodGet, "/v1/billing-snapshot?product=daybook", nil, token, http.Header{"If-None-Match": []string{oldETag}})
	if status != http.StatusOK || activeHeaders.Get("ETag") == oldETag {
		t.Fatalf("capture did not advance snapshot revision: status=%d body=%s", status, activeRaw)
	}
	active := testkit.Decode[snapshotResponse](t, activeRaw)
	if active.Subscription == nil || active.Subscription.Status != "active" || active.Subscription.EffectivePlan.BillingModel != "paid" {
		t.Fatalf("snapshot does not contain the verified paid subscription: %s", activeRaw)
	}

	badReq, err := http.NewRequest(http.MethodPost, h.BaseURL+"/v1/subscriptions/current/cancellation", strings.NewReader(`{"effective":`))
	if err != nil {
		t.Fatal(err)
	}
	badReq.Header.Set("Authorization", "Bearer "+token)
	badReq.Header.Set("Content-Type", "application/json")
	badReq.Header.Set("Idempotency-Key", testkit.Unique("bad-cancel"))
	badResp, err := h.Client.Do(badReq)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, badResp.Body)
	_ = badResp.Body.Close()
	if badResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed cancellation returned %d", badResp.StatusCode)
	}

	cancelKey := testkit.Unique("cancel")
	cancelHeaders := http.Header{"Idempotency-Key": []string{cancelKey}}
	status, cancelRaw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/subscriptions/current/cancellation", map[string]any{"effective": "period_end"}, token, cancelHeaders)
	if status != http.StatusOK {
		t.Fatalf("schedule cancellation returned %d: %s", status, cancelRaw)
	}
	status, duplicateCancelRaw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/subscriptions/current/cancellation", map[string]any{"effective": "period_end"}, token, cancelHeaders)
	if status != http.StatusOK || !strings.Contains(string(duplicateCancelRaw), `"duplicate":true`) {
		t.Fatalf("cancellation replay returned %d: %s", status, duplicateCancelRaw)
	}
	status, conflictRaw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/subscriptions/current/cancellation", map[string]any{"effective": "immediate"}, token, cancelHeaders)
	if status != http.StatusConflict {
		t.Fatalf("conflicting cancellation key returned %d: %s", status, conflictRaw)
	}
}

func TestFreeEligibilityIsCustomerScoped(t *testing.T) {
	h := testkit.NewHTTP(t)
	sharedSubject := testkit.Unique("free-customer")
	var freePlan string
	for index := 0; index < 2; index++ {
		org := testkit.Unique("free-org")
		token := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, map[string]any{"sub": sharedSubject})
		h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{
			"name": "Free Eligibility", "application": "daybook", "organization_id": org,
		}, token)
		if freePlan == "" {
			catalogue := testkit.Decode[catalogueResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog?product=daybook", nil, token))
			for _, plan := range catalogue.Plans {
				if plan.BillingModel == "free" {
					freePlan = plan.ID
					break
				}
			}
		}
		if freePlan == "" {
			t.Fatal("catalogue does not contain an available free plan")
		}
		status, raw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/subscription-transitions", map[string]any{"plan_id": freePlan}, token,
			http.Header{"Idempotency-Key": []string{testkit.Unique("free-transition")}})
		if index == 0 && status != http.StatusCreated {
			t.Fatalf("first free activation returned %d: %s", status, raw)
		}
		if index == 1 && status != http.StatusConflict {
			t.Fatalf("second free activation for the same customer returned %d: %s", status, raw)
		}
	}
}

func TestTransitionCancellationAndWebhookManagement(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("transition-cancel")
	token := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, nil)
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{
		"name": "Transition Cancellation", "application": "daybook", "organization_id": org,
	}, token)
	h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/credit-packs?product=daybook", nil, token)

	catalogue := testkit.Decode[catalogueResponse](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog?product=daybook", nil, token))
	var paidPlan string
	for _, plan := range catalogue.Plans {
		if plan.BillingModel == "paid" {
			paidPlan = plan.ID
			break
		}
	}
	if paidPlan == "" {
		t.Fatal("catalogue does not contain a paid plan")
	}
	status, staleRaw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/subscription-transitions", map[string]any{"plan_id": paidPlan}, token,
		http.Header{"Idempotency-Key": []string{testkit.Unique("stale")}, "If-Match": []string{`"billing-0"`}})
	if status != http.StatusPreconditionFailed {
		t.Fatalf("stale transition precondition returned %d: %s", status, staleRaw)
	}
	status, transitionRaw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/subscription-transitions", map[string]any{"plan_id": paidPlan}, token,
		http.Header{"Idempotency-Key": []string{testkit.Unique("cancel-transition")}})
	if status != http.StatusCreated {
		t.Fatalf("create cancellable transition returned %d: %s", status, transitionRaw)
	}
	transition := testkit.Decode[transitionResponse](t, transitionRaw)
	cancelledRaw := h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/subscription-transitions/"+transition.ID+"/cancel", nil, token)
	if testkit.Decode[transitionResponse](t, cancelledRaw).Status != "cancelled" {
		t.Fatalf("transition was not cancelled: %s", cancelledRaw)
	}

	webhookRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{
		"application": "daybook", "target_url": h.MockURL + "/receivers/daybook", "secret": "initial-secret-123", "api_version": "2",
	}, token)
	webhookID := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, webhookRaw).ID
	h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/webhooks/"+webhookID, map[string]any{
		"event_types": []string{"subscription.transition_completed"}, "active": true,
	}, token)
	h.RequireStatus(t, http.StatusNoContent, http.MethodPost, "/v1/webhooks/"+webhookID+"/rotate-secret", map[string]any{
		"secret": "rotated-secret-456", "grace_seconds": 60,
	}, token)
	h.RequireStatus(t, http.StatusNoContent, http.MethodDelete, "/v1/webhooks/"+webhookID, nil, token)
}
