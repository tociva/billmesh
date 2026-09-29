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
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{"name": "Journey One", "external_ref": testkit.Unique("j1"), "application": "daybook", "organization_id": org}, token)
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/subscriptions", map[string]any{"product": "daybook", "plan": "daybook-paid", "payment_status": "verified"}, token)
	h.RequireStatus(t, http.StatusNoContent, http.MethodPost, "/v1/account-links", map[string]any{"application": "taskmesh", "organization_id": testkit.Unique("journey-1-taskmesh")}, token)
}
func TestJourney2ExhaustionAndTopUp(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("journey-2")
	token := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write", "credits:reserve", "credits:settle"}, nil)
	accountRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{"name": "Journey Two", "external_ref": testkit.Unique("j2"), "application": "daybook", "organization_id": org}, token)
	account := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, accountRaw).ID
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/subscriptions", map[string]any{"account_id": account, "product": "daybook", "plan": "daybook-free"}, token)
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/executions/authorize", map[string]any{"credits": 100, "execution_id": testkit.Unique("exhaust")}, token)
	h.RequireStatus(t, http.StatusConflict, http.MethodPost, "/v1/executions/authorize", map[string]any{"credits": 1}, token)
	orderRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack": "credits-500"}, token)
	order := testkit.Decode[struct {
		Order struct {
			ID       string `json:"id"`
			Amount   int64  `json:"amount"`
			Currency string `json:"currency"`
		} `json:"order"`
	}](t, orderRaw)
	require.NotEmpty(t, order.Order.ID)
	require.Equal(t, http.StatusNoContent, h.SignedWebhook(t, "/v1/payments/webhook", map[string]any{"id": testkit.Unique("event"), "type": "payment.captured", "payment_id": testkit.Unique("payment"), "order_id": order.Order.ID, "status": "captured", "amount_minor": order.Order.Amount, "currency": order.Order.Currency}, "test-webhook-secret"))
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/executions/authorize", map[string]any{"credits": 1}, token)
}
func TestJourney3IndependentTaskmeshSubscription(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("journey-3")
	token := h.IssueToken(t, org, "taskmesh", []string{"billing:read", "billing:write", "billing:link", "credits:reserve"}, nil)
	accountRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{"name": "Journey Three", "external_ref": testkit.Unique("j3"), "application": "taskmesh", "organization_id": org}, token)
	account := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, accountRaw).ID
	h.RequireStatus(t, http.StatusNoContent, http.MethodPost, "/v1/accounts/"+account+"/links", map[string]any{"application": "daybook", "organization_id": testkit.Unique("journey-3-daybook")}, token)
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/subscriptions", map[string]any{"account_id": account, "product": "daybook", "plan": "daybook-free"}, token)
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/subscriptions", map[string]any{"account_id": account, "product": "taskmesh", "plan": "professional", "payment_status": "verified"}, token)
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/executions/authorize", map[string]any{"context": "standalone", "credits": 10}, token)
	h.RequireStatus(t, http.StatusNoContent, http.MethodPost, "/v1/subscriptions/current/cancel", map[string]any{"immediate": true}, token)
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/executions/authorize", map[string]any{"context": "daybook", "credits": 10}, token)
}
func TestJourney4ConcurrentExecution(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("journey-4")
	token := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write", "credits:grant", "credits:reserve", "credits:settle"}, nil)
	accountRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{"name": "Journey Four", "external_ref": testkit.Unique("j4"), "application": "daybook", "organization_id": org}, token)
	account := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, accountRaw).ID
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/subscriptions", map[string]any{"account_id": account, "product": "daybook", "plan": "daybook-free"}, token)
	var statuses [2]int
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i], _, _ = h.JSON(t, http.MethodPost, "/v1/executions/authorize", map[string]any{"execution_id": testkit.Unique("concurrent"), "credits": 80}, token)
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
