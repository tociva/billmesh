//go:build e2e

package security_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestSecurityWebhookDeliveryRejectsPublicToPrivateRedirect(t *testing.T) {
	h := testkit.NewHTTP(t)
	readHits := func() int {
		t.Helper()
		resp, err := h.Client.Get(h.MockURL + "/test/ssrf-hits")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result struct {
			Hits int `json:"hits"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result.Hits
	}
	beforeHits := readHits()
	org := testkit.Unique("redirect-ssrf")
	token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	account := testkit.CreateFixtureAccount(t, h, org, token)
	wallet := testkit.CreateFundedWallet(t, h, account, token)
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{
		"target_url": h.MockURL + "/receivers/redirect-private", "secret": "redirect-secret-0123456789abcdef012345",
	}, token)
	ref := testkit.Unique("redirect-event")
	h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/wallets/"+wallet+"/grants", map[string]any{"source": "test", "operation_ref": ref, "amount": 1}, token)
	url := os.Getenv("BILLMESH_E2E_DATABASE_URL")
	if url == "" {
		t.Fatal("BILLMESH_E2E_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		var status, lastError string
		err := pool.QueryRow(context.Background(), `SELECT d.status,COALESCE(d.last_error,'') FROM webhook_deliveries d JOIN outbox_events e ON e.id=d.event_id WHERE e.aggregate_id=$1 AND e.payload->>'operation_ref'=$2 ORDER BY e.sequence DESC LIMIT 1`, wallet, ref).Scan(&status, &lastError)
		if err == nil && status == "failed" {
			if !strings.Contains(lastError, "prohibited address") {
				t.Fatalf("GAP-SEC-001: redirect failure was not an address rejection: %s", lastError)
			}
			if hits := readHits(); hits != beforeHits {
				t.Fatalf("GAP-SEC-001: private redirect was contacted: hits %d->%d", beforeHits, hits)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("GAP-SEC-001: no failed delivery after private redirect")
}
