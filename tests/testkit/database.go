package testkit

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Database(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Fatal("DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func Truncate(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `TRUNCATE audit_log,invoice_lines,invoices,webhook_deliveries,outbox_events,provider_events,payments,usage_events,credit_ledger,reservation_allocations,reservations,credit_grants,wallets,subscriptions,plans,products,account_links,billing_accounts CASCADE`)
	if err != nil {
		t.Fatalf("truncate database: %v", err)
	}
}

func ExerciseDatabaseContract(t *testing.T, tc PlanCase, table string) {
	t.Helper()
	pool := Database(t)
	switch tc.ID {
	case "WAL-018":
		exerciseLedgerTamperResistance(t, pool)
		return
	case "INV-008":
		exerciseFinalizedInvoiceTamperResistance(t, pool)
		return
	case "ADM-009":
		exerciseAuditTamperResistance(t, pool)
		return
	}
	var schema string
	if err := pool.QueryRow(context.Background(), `SELECT CASE
		WHEN to_regclass($1) IS NOT NULL THEN current_schema()
		WHEN to_regclass('public.'||$1) IS NOT NULL THEN 'public'
		ELSE '' END`, table).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if schema == "" {
		t.Fatalf("%s requires table %q", tc.ID, table)
	}
	text := strings.ToLower(tc.Description)
	if strings.Contains(text, "duplicate") || strings.Contains(text, "deduplicate") {
		var count int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_indexes WHERE schemaname=$2 AND tablename=$1 AND indexdef ILIKE '%UNIQUE%'`, table, schema).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatalf("%s requires a database uniqueness guarantee on %s", tc.ID, table)
		}
	}
}

func exerciseLedgerTamperResistance(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	accountID := insertDatabaseAccount(t, pool)
	productID := insertDatabaseProduct(t, pool)
	var walletID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO wallets(account_id,product_id,available,reserved) VALUES($1,$2,100,0) RETURNING id`, accountID, productID).Scan(&walletID); err != nil {
		t.Fatalf("insert wallet fixture: %v", err)
	}
	var grantID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO credit_grants(wallet_id,source,operation_ref,amount,remaining) VALUES($1,'test',$2,100,100) RETURNING id`, walletID, Unique("grant")).Scan(&grantID); err != nil {
		t.Fatalf("insert grant fixture: %v", err)
	}
	var ledgerID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO credit_ledger(wallet_id,grant_id,operation_ref,kind,available_delta,reserved_delta) VALUES($1,$2,$3,'grant',100,0) RETURNING id`, walletID, grantID, Unique("ledger")).Scan(&ledgerID); err != nil {
		t.Fatalf("insert ledger fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE credit_ledger SET available_delta=999 WHERE id=$1`, ledgerID); err == nil {
		t.Fatalf("WAL-018: credit ledger update succeeded; financial ledger rows must be tamper-resistant")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM credit_ledger WHERE id=$1`, ledgerID); err == nil {
		t.Fatalf("WAL-018: credit ledger delete succeeded; financial ledger rows must be tamper-resistant")
	}
}

func exerciseFinalizedInvoiceTamperResistance(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	accountID := insertDatabaseAccount(t, pool)
	var invoiceID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO invoices(account_id,billing_operation_ref,invoice_number,status,currency,subtotal_minor,total_minor,finalized_at,paid_at) VALUES($1,$2,$3,'paid','INR',100,100,now(),now()) RETURNING id`, accountID, Unique("invoice-op"), Unique("INV")).Scan(&invoiceID); err != nil {
		t.Fatalf("insert finalized invoice fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE invoices SET total_minor=0, subtotal_minor=0 WHERE id=$1`, invoiceID); err == nil {
		t.Fatalf("INV-008: finalized invoice update succeeded")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM invoices WHERE id=$1`, invoiceID); err == nil {
		t.Fatalf("INV-008: finalized invoice delete succeeded")
	}
	var stillExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM invoices WHERE id=$1 AND total_minor=100)`, invoiceID).Scan(&stillExists); err != nil || !stillExists {
		t.Fatalf("INV-008: finalized invoice history was not preserved, exists=%v err=%v", stillExists, err)
	}
}

func exerciseAuditTamperResistance(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	accountID := insertDatabaseAccount(t, pool)
	var auditID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO audit_log(account_id,actor_subject,actor_type,action,resource_type,resource_id,reason,after_state) VALUES($1,'user-1','user','credit.adjust','wallet',$2,'test',$3) RETURNING id`, accountID, uuid.NewString(), map[string]any{"amount": 25}).Scan(&auditID); err != nil {
		t.Fatalf("insert audit fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET action='tampered' WHERE id=$1`, auditID); err == nil {
		t.Fatalf("ADM-009: audit log update succeeded")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_log WHERE id=$1`, auditID); err == nil {
		t.Fatalf("ADM-009: audit log delete succeeded")
	}
	var actor, actorType, action, resourceType, resourceID string
	var createdAt any
	if err := pool.QueryRow(ctx, `SELECT actor_subject,actor_type,action,resource_type,resource_id,created_at FROM audit_log WHERE id=$1`, auditID).Scan(&actor, &actorType, &action, &resourceType, &resourceID, &createdAt); err != nil {
		t.Fatalf("ADM-009: read audit fixture: %v", err)
	}
	if actor == "" || actorType == "" || action == "" || resourceType == "" || resourceID == "" || createdAt == nil {
		t.Fatalf("ADM-009: audit entry is missing required actor/resource/action details")
	}
}

func insertDatabaseAccount(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `INSERT INTO billing_accounts(name,external_ref) VALUES($1,$2) RETURNING id`, "Security Fixture", Unique("acct")).Scan(&id)
	if err != nil {
		t.Fatalf("insert account fixture: %v", err)
	}
	return id
}

func insertDatabaseProduct(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `INSERT INTO products(slug,name) VALUES($1,'Security Product') ON CONFLICT(slug) DO UPDATE SET name=excluded.name RETURNING id`, Unique("product")).Scan(&id)
	if err != nil && err != pgx.ErrNoRows {
		t.Fatalf("insert product fixture: %v", err)
	}
	if id == uuid.Nil {
		err = pool.QueryRow(context.Background(), `SELECT id FROM products WHERE slug='daybook'`).Scan(&id)
		if err != nil {
			t.Fatalf("select fallback product fixture: %v", err)
		}
	}
	return id
}
