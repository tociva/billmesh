package testkit

import (
	"context"
	"os"
	"strings"
	"testing"

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
	var exists bool
	if err := pool.QueryRow(context.Background(), `SELECT to_regclass('public.'||$1) IS NOT NULL`, table).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatalf("%s requires table %q", tc.ID, table)
	}
	text := strings.ToLower(tc.Description)
	if strings.Contains(text, "duplicate") || strings.Contains(text, "deduplicate") {
		var count int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_indexes WHERE schemaname='public' AND tablename=$1 AND indexdef ILIKE '%UNIQUE%'`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatalf("%s requires a database uniqueness guarantee on %s", tc.ID, table)
		}
	}
}
