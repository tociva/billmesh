//go:build e2e

package security_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestSecurityRuntimeRoleCannotAlterFinancialHistoryOrSchema(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("runtime-role")
	token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), map[string]any{"sub": "runtime-role-admin"})
	account := testkit.CreateFixtureAccount(t, h, org, token)
	wallet := testkit.CreateFundedWallet(t, h, account, token)
	reason := testkit.Unique("runtime-role-adjustment")
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/adjustments", map[string]any{"wallet_id": wallet, "amount": 3, "reason": reason}, token)

	ownerURL, runtimeURL := os.Getenv("BILLMESH_E2E_DATABASE_URL"), os.Getenv("BILLMESH_RUNTIME_DATABASE_URL")
	if ownerURL == "" || runtimeURL == "" {
		t.Fatal("owner and runtime database URLs are required")
	}
	owner, err := pgxpool.New(context.Background(), ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	runtime, err := pgxpool.New(context.Background(), runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	ctx := context.Background()
	var currentUser string
	var superuser, createRole, createDB bool
	if err := runtime.QueryRow(ctx, `SELECT current_user,rolsuper,rolcreaterole,rolcreatedb FROM pg_roles WHERE rolname=current_user`).Scan(&currentUser, &superuser, &createRole, &createDB); err != nil {
		t.Fatal(err)
	}
	if currentUser != "billmesh_runtime" || superuser || createRole || createDB {
		t.Fatalf("API role is not restricted: user=%s superuser=%v create_role=%v create_db=%v", currentUser, superuser, createRole, createDB)
	}
	expectSQLState := func(label, state, query string, args ...any) {
		t.Helper()
		_, err := runtime.Exec(ctx, query, args...)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != state {
			t.Fatalf("%s: want PostgreSQL state %s, got %v", label, state, err)
		}
	}
	expectSQLState("create table", "42501", `CREATE TABLE security_runtime_schema_probe(id integer)`)
	expectSQLState("disable audit trigger", "42501", `ALTER TABLE audit_log DISABLE TRIGGER audit_log_prevent_update_delete`)

	var delegationProfiles int
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM delegation_client_profiles WHERE enabled`).Scan(&delegationProfiles); err != nil {
		t.Fatalf("runtime role cannot read delegation profiles: %v", err)
	}
	if delegationProfiles == 0 {
		t.Fatal("expected seeded delegation profiles")
	}
	expectSQLState("insert delegation profile", "42501", `INSERT INTO delegation_client_profiles(authorizer_client_id,actor_client_id,scope,client_type,actor_type,application,environment) VALUES('unauthorized-authorizer','unauthorized-actor','billmesh.billing','billing','user','daybook','production')`)
	expectSQLState("update delegation profile", "42501", `UPDATE delegation_client_profiles SET enabled=false WHERE authorizer_client_id='daybook-billmesh-authorizer-test' AND actor_client_id='daybook-billing-test'`)
	expectSQLState("delete delegation profile", "42501", `DELETE FROM delegation_client_profiles WHERE authorizer_client_id='daybook-billmesh-authorizer-test' AND actor_client_id='daybook-billing-test'`)
	expectSQLState("read delegation profile audit", "42501", `SELECT count(*) FROM delegation_client_profile_events`)

	var ledgerID, auditID, invoiceID string
	if err := owner.QueryRow(ctx, `SELECT id FROM credit_ledger WHERE wallet_id=$1 AND operation_ref LIKE 'grant:admin:%' ORDER BY created_at DESC LIMIT 1`, wallet).Scan(&ledgerID); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `SELECT id FROM audit_log WHERE reason=$1`, reason).Scan(&auditID); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `INSERT INTO invoices(account_id,billing_operation_ref,invoice_number,status,currency,subtotal_minor,total_minor,finalized_at,paid_at) VALUES($1,$2,$3,'paid','INR',100,100,now(),now()) RETURNING id`, account, testkit.Unique("runtime-invoice-op"), testkit.Unique("runtime-invoice")).Scan(&invoiceID); err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]string{
		"ledger update":  `UPDATE credit_ledger SET available_delta=999 WHERE id=$1`,
		"ledger delete":  `DELETE FROM credit_ledger WHERE id=$1`,
		"invoice update": `UPDATE invoices SET total_minor=101,subtotal_minor=101 WHERE id=$1`,
		"invoice delete": `DELETE FROM invoices WHERE id=$1`,
		"audit update":   `UPDATE audit_log SET action='tampered' WHERE id=$1`,
		"audit delete":   `DELETE FROM audit_log WHERE id=$1`,
	} {
		id := ledgerID
		if name == "invoice update" || name == "invoice delete" {
			id = invoiceID
		} else if name == "audit update" || name == "audit delete" {
			id = auditID
		}
		expectSQLState(name, "P0001", query, id)
	}
	var ledgerExists, invoiceExists, auditExists bool
	if err := owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM credit_ledger WHERE id=$1), EXISTS(SELECT 1 FROM invoices WHERE id=$2 AND total_minor=100), EXISTS(SELECT 1 FROM audit_log WHERE id=$3 AND action='credit.adjust')`, ledgerID, invoiceID, auditID).Scan(&ledgerExists, &invoiceExists, &auditExists); err != nil {
		t.Fatal(err)
	}
	if !ledgerExists || !invoiceExists || !auditExists {
		t.Fatalf("financial history changed: ledger=%v invoice=%v audit=%v", ledgerExists, invoiceExists, auditExists)
	}
}
