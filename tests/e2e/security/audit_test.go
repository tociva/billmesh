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

func TestSecurityAdjustmentAndAuditCommitTogether(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("audit-atomic")
	admin := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), map[string]any{"sub": "audit-test-admin"})
	account := testkit.CreateFixtureAccount(t, h, org, admin)
	wallet := testkit.CreateFundedWallet(t, h, account, admin)
	url := os.Getenv("BILLMESH_E2E_DATABASE_URL")
	if url == "" {
		t.Fatal("BILLMESH_E2E_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	var before, ledgerBefore int64
	if err := pool.QueryRow(ctx, `SELECT available FROM wallets WHERE id=$1`, wallet).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_ledger WHERE wallet_id=$1`, wallet).Scan(&ledgerBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE OR REPLACE FUNCTION security_test_reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.reason LIKE 'security-audit-failure-%' THEN RAISE EXCEPTION 'injected audit failure'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER IF EXISTS security_test_reject_audit ON audit_log`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER security_test_reject_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION security_test_reject_audit()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS security_test_reject_audit ON audit_log`)
		_, _ = pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS security_test_reject_audit()`)
	}()
	reason := testkit.Unique("security-audit-failure")
	status, errorBody, _ := h.JSON(t, http.MethodPost, "/v1/admin/adjustments", map[string]any{"wallet_id": wallet, "amount": 7, "reason": reason}, admin)
	if status != http.StatusInternalServerError {
		t.Fatalf("GAP-SEC-009: injected audit failure returned %d", status)
	}
	if strings.Contains(string(errorBody), "injected audit failure") || strings.Contains(string(errorBody), reason) {
		t.Fatalf("GAP-SEC-008: database failure exposed internal details: %s", errorBody)
	}
	var after, ledgerAfter, grantCount int64
	if err := pool.QueryRow(ctx, `SELECT available FROM wallets WHERE id=$1`, wallet).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_ledger WHERE wallet_id=$1`, wallet).Scan(&ledgerAfter); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_grants WHERE wallet_id=$1 AND source='admin'`, wallet).Scan(&grantCount); err != nil {
		t.Fatal(err)
	}
	if after != before || ledgerAfter != ledgerBefore || grantCount != 0 {
		t.Fatalf("GAP-SEC-009: audit failure left financial changes: balance %d->%d ledger %d->%d grants %d", before, after, ledgerBefore, ledgerAfter, grantCount)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER security_test_reject_audit ON audit_log`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP FUNCTION security_test_reject_audit()`); err != nil {
		t.Fatal(err)
	}
	goodReason := testkit.Unique("security-audit-success")
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/adjustments", map[string]any{"wallet_id": wallet, "amount": 7, "reason": goodReason}, admin)
	var auditID, actor, action, resourceType, resourceID string
	if err := pool.QueryRow(ctx, `SELECT id,actor_subject,action,resource_type,resource_id FROM audit_log WHERE reason=$1`, goodReason).Scan(&auditID, &actor, &action, &resourceType, &resourceID); err != nil {
		t.Fatal(err)
	}
	if actor != "audit-test-admin" || action != "credit.adjust" || resourceType != "wallet" || resourceID != wallet {
		t.Fatalf("GAP-SEC-009: incomplete audit entry: %q %q %q %q", actor, action, resourceType, resourceID)
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET action='tampered' WHERE id=$1`, auditID); err == nil {
		t.Fatal("GAP-SEC-009: a real adjustment audit could be modified")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_log WHERE id=$1`, auditID); err == nil {
		t.Fatal("GAP-SEC-009: a real adjustment audit could be deleted")
	}
	var ledgerID string
	if err := pool.QueryRow(ctx, `SELECT id FROM credit_ledger WHERE wallet_id=$1 ORDER BY created_at DESC LIMIT 1`, wallet).Scan(&ledgerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE credit_ledger SET available_delta=999 WHERE id=$1`, ledgerID); err == nil {
		t.Fatal("GAP-SEC-009: a real adjustment ledger entry could be modified")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM credit_ledger WHERE id=$1`, ledgerID); err == nil {
		t.Fatal("GAP-SEC-009: a real adjustment ledger entry could be deleted")
	}
	if err := pool.QueryRow(ctx, `SELECT available FROM wallets WHERE id=$1`, wallet).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before+7 || !strings.Contains(string(h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/audit", nil, admin)), goodReason) {
		t.Fatalf("GAP-SEC-009: successful adjustment and audit were not visible")
	}
}
