//go:build e2e

package journeys_test

import (
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestJourney1DaybookWithoutTaskmeshPremium(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("journey-1")
	token := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write", "billing:link", "credits:reserve"}, nil)
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{"name": "Journey One", "external_ref": testkit.Unique("j1")}, token)
	testkit.ActivatePaidSubscription(t, h, "daybook", "daybook-paid", token)
	taskmesh := h.IssueToken(t, testkit.Unique("journey-1-taskmesh"), "taskmesh", []string{"catalogue:read"}, nil)
	h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog?product=taskmesh", nil, taskmesh)
}

func TestDaybookAccountCreateIsRecoverable(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("daybook-account-recovery")
	token := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, nil)
	request := map[string]any{"name": "Recoverable Account", "external_ref": "daybook:production:" + org}
	created := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", request, token))
	repeated := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/accounts", request, token))
	linked := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/accounts/current", nil, token))
	require.Equal(t, created.ID, repeated.ID)
	require.Equal(t, created.ID, linked.ID)
}

func TestDaybookCatalogueExposesOrganizationLimits(t *testing.T) {
	h := testkit.NewHTTP(t)
	token := testkit.CatalogueToken(t, h, "daybook")
	plans := testkit.Decode[struct {
		Plans []struct {
			Name         string         `json:"name"`
			Entitlements map[string]any `json:"entitlements"`
		} `json:"plans"`
	}](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog?product=daybook", nil, token)).Plans
	want := map[string][3]float64{
		"Daybook Free":         {1, 1, 0},
		"Daybook Basic":        {1, 1, 0},
		"Daybook Professional": {3, 6, 1},
	}
	for _, plan := range plans {
		limits, ok := want[plan.Name]
		if !ok {
			continue
		}
		require.Equal(t, limits[0], plan.Entitlements["branches"])
		require.Equal(t, limits[1], plan.Entitlements["users"])
		require.Equal(t, limits[2], plan.Entitlements["serviceusers"])
		delete(want, plan.Name)
	}
	require.Empty(t, want)
}
func TestJourney2ExhaustionAndTopUp(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("journey-2")
	token := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write", "credits:reserve", "credits:settle"}, nil)
	runtime := testkit.RuntimeToken(t, h, org, "daybook")
	accountRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{"name": "Journey Two", "external_ref": testkit.Unique("j2")}, token)
	account := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, accountRaw).ID
	testkit.CreateFixtureSubscription(t, h, account, token)
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/executions/authorize", map[string]any{"credits": 100, "execution_id": testkit.Unique("exhaust")}, runtime)
	h.RequireStatus(t, http.StatusConflict, http.MethodPost, "/v1/executions/authorize", map[string]any{"credits": 1, "execution_id": testkit.Unique("blocked")}, runtime)
	order := testkit.CreateCreditPackOrder(t, h, "daybook", 500, token)
	config := order.Checkout.ClientConfig
	require.NotEmpty(t, config.OrderID)
	require.Equal(t, http.StatusNoContent, h.SignedWebhook(t, "/v1/payments/webhook", map[string]any{"id": testkit.Unique("event"), "type": "payment.captured", "payment_id": testkit.Unique("payment"), "order_id": config.OrderID, "status": "captured", "amount_minor": config.AmountMinor, "currency": config.Currency}, "test-webhook-secret"))
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/executions/authorize", map[string]any{"credits": 1, "execution_id": testkit.Unique("top-up")}, runtime)
}
func TestJourney3IndependentTaskmeshSubscription(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("journey-3")
	token := h.IssueToken(t, org, "taskmesh", []string{"billing:read", "billing:write", "billing:link", "credits:reserve"}, nil)
	runtime := testkit.RuntimeToken(t, h, org, "taskmesh")
	accountRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{"name": "Journey Three", "external_ref": testkit.Unique("j3")}, token)
	account := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, accountRaw).ID
	testkit.ActivatePaidSubscription(t, h, "taskmesh", "professional", token)
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/executions/authorize", map[string]any{"context": "standalone", "execution_id": testkit.Unique("standalone"), "credits": 10}, runtime)
	h.RequireStatusWithHeaders(t, http.StatusOK, http.MethodPost, "/v1/subscriptions/current/cancellation", map[string]any{"effective": "immediate"}, token,
		http.Header{"Idempotency-Key": []string{testkit.Unique("taskmesh-cancel")}})
	_ = account
}
func TestJourney4ConcurrentExecution(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("journey-4")
	token := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, nil)
	runtime := testkit.RuntimeToken(t, h, org, "daybook")
	accountRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{"name": "Journey Four", "external_ref": testkit.Unique("j4")}, token)
	account := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, accountRaw).ID
	testkit.CreateFixtureSubscription(t, h, account, token)
	var statuses [2]int
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i], _, _ = h.JSON(t, http.MethodPost, "/v1/executions/authorize", map[string]any{"execution_id": testkit.Unique("concurrent"), "credits": 80}, runtime)
		}()
	}
	wg.Wait()
	successes := 0
	for _, status := range statuses {
		if status == http.StatusCreated {
			successes++
		}
	}
	require.Equal(t, 1, successes)
}
