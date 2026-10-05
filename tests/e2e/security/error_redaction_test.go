//go:build e2e

package security_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestSecurityErrorsAndEventsDoNotExposeSecrets(t *testing.T) {
	h := testkit.NewHTTP(t)
	resetMockFailure(t, h)
	t.Cleanup(func() { resetMockFailure(t, h) })
	secret := testkit.Unique("private-signing-secret")
	org := testkit.Unique("redaction")
	token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	account := testkit.CreateFixtureAccount(t, h, org, token)
	wallet := testkit.CreateFundedWallet(t, h, account, token)
	check := func(label string, body []byte) {
		t.Helper()
		if strings.Contains(string(body), secret) || strings.Contains(string(body), token) || strings.Contains(string(body), "testpassword") {
			t.Fatalf("GAP-SEC-008: %s exposed a credential: %s", label, body)
		}
	}
	status, body, _ := h.JSON(t, http.MethodGet, "/v1/catalog", nil, secret)
	if status != http.StatusUnauthorized {
		t.Fatalf("GAP-SEC-008: invalid authentication returned %d", status)
	}
	check("authentication response", body)
	status, body, _ = h.JSON(t, http.MethodPost, "/v1/accounts", map[string]any{"name": "Invalid", "external_ref": testkit.Unique("invalid"), "secret": secret}, token)
	if status != http.StatusBadRequest {
		t.Fatalf("GAP-SEC-008: validation returned %d", status)
	}
	check("validation response", body)
	otherOrg := testkit.Unique("redaction-foreign")
	otherToken := h.IssueToken(t, otherOrg, "daybook", testkit.AllPermissions(), nil)
	otherAccount := testkit.CreateFixtureAccount(t, h, otherOrg, otherToken)
	status, body, _ = h.JSON(t, http.MethodGet, "/v1/accounts/"+otherAccount, nil, token)
	if status != http.StatusNotFound || strings.Contains(string(body), otherAccount) {
		t.Fatalf("GAP-SEC-008: foreign account error exposed data: %d %s", status, body)
	}
	check("authorization response", body)
	providerFailure := []byte(`{"status":503}`)
	resp, err := h.Client.Post(h.MockURL+"/test/failure", "application/json", bytes.NewReader(providerFailure))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	status, body, _ = h.JSONWithHeaders(t, http.MethodPost, "/v1/payments/orders", map[string]any{
		"credit_pack_id": testkit.FixtureCreditPack(t, h, "daybook", 500, token),
	}, token, http.Header{"Idempotency-Key": []string{testkit.Unique("redaction-provider")}})
	if status != http.StatusServiceUnavailable {
		t.Fatalf("GAP-SEC-008: provider failure returned %d: %s", status, body)
	}
	check("provider response", body)
	resetMockFailure(t, h)
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{"target_url": h.MockURL + "/receivers/redaction", "secret": secret}, token)
	resp, err = h.Client.Post(h.MockURL+"/test/failure", "application/json", bytes.NewReader([]byte(`{"status":500}`)))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	ref := testkit.Unique("redaction-event")
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
		var lastError string
		var eventPayload []byte
		err := pool.QueryRow(context.Background(), `SELECT COALESCE(d.last_error,''),e.payload FROM webhook_deliveries d JOIN outbox_events e ON e.id=d.event_id WHERE e.aggregate_id=$1 AND e.payload->>'operation_ref'=$2 AND d.status='failed' ORDER BY e.sequence DESC LIMIT 1`, wallet, ref).Scan(&lastError, &eventPayload)
		if err == nil {
			check("delivery diagnostic", []byte(lastError))
			check("business event payload", eventPayload)
			if !strings.Contains(lastError, "status 500") {
				t.Fatalf("GAP-SEC-008: delivery diagnostic lost safe status: %s", lastError)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("GAP-SEC-008: no failed delivery to inspect")
}
