//go:build e2e

package journeys_test

import (
	"net/http"
	"testing"

	"github.com/tociva/billmesh/tests/testkit"
)

type ownershipTransfer struct {
	ID             string  `json:"id"`
	Status         string  `json:"status"`
	DecisionCode   string  `json:"decision_code"`
	RequiredAction string  `json:"required_action"`
	BaseRevision   int64   `json:"base_revision"`
	ConfirmedAt    *string `json:"confirmed_at"`
	CancelledAt    *string `json:"cancelled_at"`
}

func TestAccountOwnershipTransferLifecycle(t *testing.T) {
	h := testkit.NewHTTP(t)
	admin := h.IssueToken(t, testkit.Unique("ownership-admin"), "daybook", testkit.AllPermissions(), nil)
	policy := testkit.Decode[struct {
		Defaults map[string]any `json:"defaults"`
	}](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/product-policy-metadata", nil, admin)).Defaults
	policy["customer"].(map[string]any)["ownership_transfer"] = "retain"

	product := testkit.Decode[struct {
		Slug string `json:"slug"`
	}](t, h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/products", map[string]any{
		"slug": testkit.Unique("ownership-product"), "name": "Ownership Product", "billing_policy": policy,
	}, admin))

	org := testkit.Unique("ownership-org")
	owner := h.IssueToken(t, org, product.Slug, []string{"billing:read", "billing:write"}, map[string]any{"sub": "user:original-owner"})
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{
		"name": "Ownership Account", "external_ref": testkit.Unique("ownership-account"),
	}, owner)
	service := h.IssueToken(t, org, product.Slug, []string{"billing:read", "billing:ownership"}, map[string]any{
		"client_id": "billmesh-global-admin-test", "sub": "service:ownership", "actor_type": "service",
	})

	status, _, snapshotHeaders := h.JSON(t, http.MethodGet, "/v1/billing-snapshot", nil, service)
	if status != http.StatusOK || snapshotHeaders.Get("ETag") == "" {
		t.Fatalf("ownership snapshot returned status %d without an ETag", status)
	}
	etag := snapshotHeaders.Get("ETag")
	createTransfer := func(ownerRef string) ownershipTransfer {
		t.Helper()
		raw := h.RequireStatusWithHeaders(t, http.StatusCreated, http.MethodPost, "/v1/account-ownership-transfers", map[string]any{
			"new_owner_ref": ownerRef,
		}, service, http.Header{
			"If-Match":        []string{etag},
			"Idempotency-Key": []string{testkit.Unique("ownership-create")},
		})
		transfer := testkit.Decode[ownershipTransfer](t, raw)
		if transfer.ID == "" || transfer.Status != "authorized" || transfer.DecisionCode != "owner_eligible" || transfer.RequiredAction != "none" || transfer.BaseRevision < 1 {
			t.Fatalf("unexpected ownership-transfer decision: %+v", transfer)
		}
		return transfer
	}

	cancelledCandidate := createTransfer("owner:" + testkit.Unique("cancelled"))
	loaded := testkit.Decode[ownershipTransfer](t, h.RequireStatus(t, http.StatusOK, http.MethodGet,
		"/v1/account-ownership-transfers/"+cancelledCandidate.ID, nil, service))
	if loaded.ID != cancelledCandidate.ID || loaded.Status != "authorized" {
		t.Fatalf("ownership-transfer lookup returned the wrong transfer: %+v", loaded)
	}
	cancelled := testkit.Decode[ownershipTransfer](t, h.RequireStatusWithHeaders(t, http.StatusOK, http.MethodPost,
		"/v1/account-ownership-transfers/"+cancelledCandidate.ID+"/cancel", nil, service,
		http.Header{"Idempotency-Key": []string{testkit.Unique("ownership-cancel")}}))
	if cancelled.Status != "cancelled" || cancelled.CancelledAt == nil {
		t.Fatalf("ownership transfer was not cancelled: %+v", cancelled)
	}

	confirmedCandidate := createTransfer("owner:" + testkit.Unique("confirmed"))
	confirmed := testkit.Decode[ownershipTransfer](t, h.RequireStatusWithHeaders(t, http.StatusOK, http.MethodPost,
		"/v1/account-ownership-transfers/"+confirmedCandidate.ID+"/confirm", nil, service, http.Header{
			"If-Match":        []string{etag},
			"Idempotency-Key": []string{testkit.Unique("ownership-confirm")},
		}))
	if confirmed.Status != "confirmed" || confirmed.ConfirmedAt == nil {
		t.Fatalf("ownership transfer was not confirmed: %+v", confirmed)
	}
}
