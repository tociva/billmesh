//go:build e2e

package security_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestSecurityTenantAdministratorCannotAdjustOrReplayForeignResources(t *testing.T) {
	h := testkit.NewHTTP(t)
	orgA := testkit.Unique("tenant-admin-a")
	adminA := h.IssueToken(t, orgA, "daybook", testkit.AllPermissions(), map[string]any{"sub": "tenant-admin-a"})
	accountA := testkit.CreateFixtureAccount(t, h, orgA, adminA)
	walletA := testkit.CreateFundedWallet(t, h, accountA, adminA)
	orgB := testkit.Unique("tenant-admin-b")
	adminB := h.IssueToken(t, orgB, "daybook", testkit.AllPermissions(), map[string]any{"sub": "tenant-admin-b"})
	accountB := testkit.CreateFixtureAccount(t, h, orgB, adminB)
	walletB := testkit.CreateFundedWallet(t, h, accountB, adminB)
	h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/admin/adjustments", map[string]any{"wallet_id": walletB, "amount": 5, "reason": "foreign"}, adminA)
	reasonB := testkit.Unique("private-audit")
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/adjustments", map[string]any{"wallet_id": walletB, "amount": 1, "reason": reasonB}, adminB)
	auditA := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/audit", nil, adminA)
	if strings.Contains(string(auditA), reasonB) || !strings.Contains(string(auditA), "credit.adjust.denied") {
		t.Fatalf("GAP-AUTHZ-004: foreign audit data leaked: %s", auditA)
	}
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{"target_url": h.MockURL + "/receivers/tenant-a", "secret": "tenant-a-secret-0123456789abcdef"}, adminA)
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{"target_url": h.MockURL + "/receivers/tenant-b", "secret": "tenant-b-secret-0123456789abcdef"}, adminB)
	h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/wallets/"+walletA+"/grants", map[string]any{"source": "test", "operation_ref": testkit.Unique("event-a"), "amount": 1}, adminA)
	h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/wallets/"+walletB+"/grants", map[string]any{"source": "test", "operation_ref": testkit.Unique("event-b"), "amount": 1}, adminB)
	url := os.Getenv("BILLMESH_E2E_DATABASE_URL")
	if url == "" {
		t.Fatal("BILLMESH_E2E_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var actor, action, resourceType, resourceID, denialReason, auditAccount string
	if err := pool.QueryRow(context.Background(), `SELECT actor_subject,action,resource_type,resource_id,reason,account_id FROM audit_log WHERE actor_subject='tenant-admin-a' AND action='credit.adjust.denied' ORDER BY created_at DESC LIMIT 1`).Scan(&actor, &action, &resourceType, &resourceID, &denialReason, &auditAccount); err != nil {
		t.Fatal(err)
	}
	if actor != "tenant-admin-a" || action != "credit.adjust.denied" || resourceType != "wallet" || resourceID != walletB || denialReason != "wallet_not_accessible" || auditAccount != accountA {
		t.Fatalf("GAP-SEC-009: foreign adjustment denial audit is incomplete or scoped incorrectly: %q %q %q %q %q %q", actor, action, resourceType, resourceID, denialReason, auditAccount)
	}
	var deliveryB string
	if err := pool.QueryRow(context.Background(), `SELECT d.id FROM webhook_deliveries d JOIN webhook_endpoints ep ON ep.id=d.endpoint_id WHERE ep.account_id=$1 ORDER BY d.next_attempt_at DESC LIMIT 1`, accountB).Scan(&deliveryB); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE webhook_deliveries SET status='failed',next_attempt_at=now()+interval '1 hour' WHERE id=$1`, deliveryB); err != nil {
		t.Fatal(err)
	}
	for _, restricted := range []struct {
		name        string
		permissions []string
		subject     string
	}{
		{"viewer", []string{"billing:read"}, "viewer"},
		{"runtime", []string{"credits:reserve"}, "service:runtime"},
		{"service", []string{"billing:read"}, "service:worker"},
	} {
		t.Run(restricted.name+" privileged routes", func(t *testing.T) {
			token := h.IssueToken(t, orgB, "daybook", restricted.permissions, map[string]any{"sub": restricted.subject})
			h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/admin/audit", nil, token)
			h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/admin/adjustments", map[string]any{"wallet_id": walletB, "amount": 1, "reason": "forbidden"}, token)
			h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/admin/webhooks/"+deliveryB+"/replay", nil, token)
			var count int
			if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE account_id=$1 AND actor_subject=$2 AND reason='missing_permission' AND action IN ('audit.read.denied','credit.adjust.denied','webhook.replay.denied')`, accountB, restricted.subject).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 3 {
				t.Fatalf("GAP-SEC-009: want three denied privileged-action audits, got %d", count)
			}
		})
	}
	h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/admin/webhooks/"+deliveryB+"/replay", nil, adminA)
	var replayAuditAccount, replayReason string
	if err := pool.QueryRow(context.Background(), `SELECT account_id,reason FROM audit_log WHERE actor_subject='tenant-admin-a' AND action='webhook.replay.denied' AND resource_id=$1 ORDER BY created_at DESC LIMIT 1`, deliveryB).Scan(&replayAuditAccount, &replayReason); err != nil {
		t.Fatal(err)
	}
	if replayAuditAccount != accountA || replayReason != "delivery_not_accessible" {
		t.Fatalf("GAP-SEC-009: foreign replay denial audit was scoped incorrectly: account=%q reason=%q", replayAuditAccount, replayReason)
	}
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM webhook_deliveries WHERE id=$1`, deliveryB).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("foreign replay changed delivery state to %q", status)
	}
	h.RequireStatus(t, http.StatusNoContent, http.MethodPost, "/v1/admin/webhooks/"+deliveryB+"/replay", nil, adminB)
}
