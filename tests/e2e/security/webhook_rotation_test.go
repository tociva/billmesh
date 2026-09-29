//go:build e2e

package security_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestSecurityOutgoingWebhookSecretRotation(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("rotation")
	token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	account := testkit.CreateFixtureAccount(t, h, org, token)
	wallet := testkit.CreateFundedWallet(t, h, account, token)
	target := h.MockURL + "/receivers/rotation"
	register := func(secret string) {
		t.Helper()
		h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{"application": "daybook", "target_url": target, "secret": secret}, token)
	}
	deliver := func(secret string) receivedWebhook {
		t.Helper()
		ref := testkit.Unique("rotation-event")
		h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/wallets/"+wallet+"/grants", map[string]any{"source": "rotation", "operation_ref": ref, "amount": 1}, token)
		deadline := time.Now().Add(7 * time.Second)
		for time.Now().Before(deadline) {
			for _, item := range listReceivedWebhooks(t, h) {
				if item.App != "rotation" || !strings.Contains(item.RawBody, ref) {
					continue
				}
				if item.Signature != webhookSignature(item.RawBody, secret) {
					t.Fatalf("WH-021: delivery used wrong signing secret: %+v", item)
				}
				return item
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("WH-021: no signed delivery for %s", ref)
		return receivedWebhook{}
	}
	register("old-secret")
	oldDelivery := deliver("old-secret")
	register("new-secret")
	newDelivery := deliver("new-secret")
	if newDelivery.Signature == webhookSignature(newDelivery.RawBody, "old-secret") || oldDelivery.Signature == webhookSignature(oldDelivery.RawBody, "new-secret") {
		t.Fatal("WH-021: retired and current secrets were not distinct")
	}
}

func TestSecurityOutgoingWebhookMissingSecretDoesNotSendUnsigned(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("missing-webhook-secret")
	token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	account := testkit.CreateFixtureAccount(t, h, org, token)
	wallet := testkit.CreateFundedWallet(t, h, account, token)
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{"application": "daybook", "target_url": h.MockURL + "/receivers/missing-secret", "secret": "temporary-secret"}, token)
	url := os.Getenv("BILLMESH_E2E_DATABASE_URL")
	if url == "" {
		t.Fatal("BILLMESH_E2E_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), `UPDATE webhook_endpoints SET secret='' WHERE account_id=$1`, account); err != nil {
		t.Fatal(err)
	}
	ref := testkit.Unique("unsigned-delivery")
	h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/wallets/"+wallet+"/grants", map[string]any{"source": "test", "operation_ref": ref, "amount": 1}, token)
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		var status, lastError string
		err := pool.QueryRow(context.Background(), `SELECT d.status,COALESCE(d.last_error,'') FROM webhook_deliveries d JOIN outbox_events e ON e.id=d.event_id WHERE e.aggregate_id=$1 AND e.payload->>'operation_ref'=$2 ORDER BY e.sequence DESC LIMIT 1`, wallet, ref).Scan(&status, &lastError)
		if err == nil && status == "failed" {
			if !strings.Contains(lastError, "signing endpoint is unavailable") {
				t.Fatalf("GAP-SEC-003: missing signing secret failed for another reason: %s", lastError)
			}
			for _, delivery := range listReceivedWebhooks(t, h) {
				if strings.Contains(delivery.RawBody, ref) {
					t.Fatal("GAP-SEC-003: unsigned delivery reached the receiver")
				}
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("GAP-SEC-003: missing secret did not fail the delivery")
}
