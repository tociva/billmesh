//go:build e2e

package security_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tociva/billmesh/tests/testkit"
)

func TestDelegationProfileChangesRefreshWithoutRestart(t *testing.T) {
	ownerURL := os.Getenv("BILLMESH_E2E_DATABASE_URL")
	if ownerURL == "" {
		t.Fatal("BILLMESH_E2E_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	authorizer := "daybook-billmesh-authorizer-test"
	actor := testkit.Unique("daybook-dynamic-billing")
	_, err = pool.Exec(ctx, `INSERT INTO delegation_client_profiles(
		authorizer_client_id,actor_client_id,scope,client_type,actor_type,application,environment
	) VALUES($1,$2,'billmesh.billing','billing','user','daybook','production')`, authorizer, actor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM delegation_client_profiles
			WHERE authorizer_client_id=$1 AND actor_client_id=$2`, authorizer, actor)
	})

	h := testkit.NewHTTP(t)
	token := h.IssueToken(t, testkit.Unique("dynamic-profile"), "daybook", []string{"billing:read"}, map[string]any{
		"authorizer_client_id": authorizer,
		"client_id":            actor,
	})
	waitForStatus(t, h, token, http.StatusNotFound)

	_, err = pool.Exec(ctx, `UPDATE delegation_client_profiles SET enabled=false
		WHERE authorizer_client_id=$1 AND actor_client_id=$2`, authorizer, actor)
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, h, token, http.StatusUnauthorized)
}

func waitForStatus(t *testing.T, h *testkit.HTTP, token string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var status int
	var body []byte
	for time.Now().Before(deadline) {
		status, body, _ = h.JSON(t, http.MethodGet, "/v1/accounts/current", nil, token)
		if status == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("GET /v1/accounts/current did not reach status %d; last status %d: %s", want, status, body)
}
