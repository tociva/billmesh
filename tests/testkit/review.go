package testkit

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var reviewE2EIDs = map[string]struct{}{
	"INST-001": {}, "INST-002": {}, "INST-003": {}, "INST-004": {}, "INST-005": {},
	"RES-024": {}, "MTR-019": {}, "MTR-021": {}, "MTR-023": {}, "PAY-021": {},
	"PAY-022": {}, "PAY-023": {}, "PAY-025": {}, "SUB-022": {}, "SUB-023": {},
	"SUB-027": {}, "SUB-028": {}, "WH-020": {}, "WH-021": {}, "WH-022": {},
	"WH-023": {}, "WH-024": {}, "LIVE-013": {}, "LIVE-014": {}, "LIVE-015": {},
	"LIVE-016": {}, "MTR-024": {}, "MTR-026": {}, "LIM-012": {}, "PAY-026": {},
	"PAY-027": {}, "TEST-002": {}, "TEST-004": {},
}

var reviewOtherIDs = map[string]struct{}{
	"AUTH-017": {}, "AUTH-018": {}, "AUTH-019": {},
	"RES-022": {}, "RES-023": {}, "RES-025": {}, "RES-026": {}, "RES-027": {},
	"WAL-019": {}, "WAL-020": {}, "WAL-021": {}, "WAL-022": {}, "WAL-023": {}, "WAL-024": {},
	"MTR-018": {}, "MTR-020": {}, "MTR-022": {}, "MTR-025": {},
	"PAY-024": {}, "SUB-021": {}, "SUB-024": {}, "SUB-025": {}, "SUB-026": {},
	"WH-019": {}, "ACC-010": {}, "INV-009": {}, "INV-010": {}, "LIM-011": {},
	"WRK-011": {}, "WRK-012": {}, "FND-011": {}, "FND-012": {},
	"TEST-001": {}, "TEST-003": {}, "PERF-013": {}, "LIVE-017": {},
}

func IsReviewE2ECase(id string) bool {
	_, ok := reviewE2EIDs[id]
	return ok
}

func IsReviewCaseImplemented(id string) bool {
	if IsReviewE2ECase(id) {
		return true
	}
	_, ok := reviewOtherIDs[id]
	return ok
}

func ExerciseReviewE2ECase(t *testing.T, tc PlanCase) bool {
	t.Helper()
	if !IsReviewE2ECase(tc.ID) {
		return false
	}
	h := NewHTTP(t)
	org := strings.ToLower(tc.ID) + "-" + Unique("org")
	token := h.IssueToken(t, org, "daybook", AllPermissions(), nil)

	switch tc.ID {
	case "INST-001":
		requireReviewStatus(t, h, tc.ID, http.StatusForbidden, http.MethodPost, "/v1/executions/authorize", map[string]any{"installation_id": "forged-installation", "execution_id": Unique("execution"), "credits": 1}, token)
	case "INST-002", "INST-003", "INST-005":
		account := CreateFixtureAccount(t, h, org, token)
		path := "/v1/accounts/" + account + "/installations"
		raw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, path, map[string]any{"application": "taskmesh", "organization_id": org}, token)
		installation := Decode[struct {
			ID string `json:"id"`
		}](t, raw).ID
		if installation == "" {
			t.Fatalf("%s: installation creation returned no id", tc.ID)
		}
		if tc.ID == "INST-003" {
			other := h.IssueToken(t, Unique("other-org"), "daybook", AllPermissions(), nil)
			requireReviewStatus(t, h, tc.ID, http.StatusForbidden, http.MethodPost, "/v1/executions/authorize", map[string]any{"installation_id": installation, "execution_id": Unique("execution"), "credits": 1}, other)
			return true
		}
		if tc.ID == "INST-005" {
			requireReviewStatus(t, h, tc.ID, http.StatusCreated, http.MethodPost, "/v1/executions/authorize", map[string]any{"installation_id": installation, "execution_id": Unique("execution"), "credits": 1}, token)
		}
		h.RequireStatus(t, http.StatusNoContent, http.MethodPost, "/v1/installations/"+installation+"/revoke", nil, token)
		want := http.StatusForbidden
		path = "/v1/executions/authorize"
		body := any(map[string]any{"installation_id": installation, "execution_id": Unique("execution"), "credits": 1})
		if tc.ID == "INST-005" {
			want = http.StatusOK
			path = "/v1/installations/" + installation + "/settle-active"
			body = map[string]any{"actual": 1}
		}
		requireReviewStatus(t, h, tc.ID, want, http.MethodPost, path, body, token)
	case "INST-004":
		account := CreateFixtureAccount(t, h, org, token)
		wallet := CreateFundedWallet(t, h, account, token)
		requireReviewStatus(t, h, tc.ID, http.StatusBadRequest, http.MethodPost, "/v1/executions/authorize", map[string]any{"installation_id": Unique("installation"), "wallet_id": wallet, "execution_id": Unique("execution"), "credits": 1}, token)
	case "RES-024":
		account := CreateFixtureAccount(t, h, org, token)
		wallet := CreateFundedWallet(t, h, account, token)
		reservation := ReserveFixture(t, h, wallet, token)
		requireReviewStatus(t, h, tc.ID, http.StatusConflict, http.MethodPost, "/v1/reservations/"+reservation+"/settle", map[string]any{"actual": 11}, token)
	case "MTR-019":
		account := CreateFixtureAccount(t, h, org, token)
		wallet := CreateFundedWallet(t, h, account, token)
		reservation := ReserveFixture(t, h, wallet, token)
		h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/reservations/"+reservation+"/settle", map[string]any{"actual": 10}, token)
		requireReviewStatus(t, h, tc.ID, http.StatusConflict, http.MethodPost, "/v1/usage-events", usageBody(reservation, Unique("usage"), 10), token)
	case "MTR-021":
		account := CreateFixtureAccount(t, h, org, token)
		wallet := CreateFundedWallet(t, h, account, token)
		reservation := ReserveFixture(t, h, wallet, token)
		eventID := Unique("atomic")
		requireReviewStatus(t, h, tc.ID, http.StatusBadRequest, http.MethodPost, "/v1/usage-events", []any{usageBody(reservation, eventID, 1), usageBody(reservation, Unique("invalid"), -1)}, token)
		raw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/usage-events", nil, token)
		if strings.Contains(string(raw), eventID) {
			t.Fatalf("%s: valid event was committed from a rejected batch", tc.ID)
		}
	case "MTR-023":
		account := CreateFixtureAccount(t, h, org, token)
		wallet := CreateFundedWallet(t, h, account, token)
		reservation := ReserveFixture(t, h, wallet, token)
		other := h.IssueToken(t, Unique("other-org"), "daybook", AllPermissions(), nil)
		requireReviewStatus(t, h, tc.ID, http.StatusForbidden, http.MethodPost, "/v1/usage-events", usageBody(reservation, Unique("usage"), 1), other)
	case "MTR-024":
		account := CreateFixtureAccount(t, h, org, token)
		wallet := CreateFundedWallet(t, h, account, token)
		reservation := ReserveFixture(t, h, wallet, token)
		body := usageBody(reservation, Unique("future"), 1)
		body["occurred_at"] = time.Now().UTC().Add(24 * time.Hour)
		requireReviewStatus(t, h, tc.ID, http.StatusBadRequest, http.MethodPost, "/v1/usage-events", body, token)
	case "MTR-026":
		account := CreateFixtureAccount(t, h, org, token)
		wallet := CreateFundedWallet(t, h, account, token)
		reservation := ReserveFixture(t, h, wallet, token)
		body := usageBody(reservation, Unique("metadata"), 1)
		body["metadata"] = map[string]any{"oversized": strings.Repeat("x", 1<<20)}
		requireReviewStatus(t, h, tc.ID, http.StatusBadRequest, http.MethodPost, "/v1/usage-events", body, token)
	case "PAY-021", "PAY-022", "PAY-023", "PAY-025", "PAY-026", "PAY-027":
		exerciseReviewPayment(t, h, tc.ID, org, token)
	case "SUB-022", "SUB-023", "SUB-027", "SUB-028":
		exerciseReviewSubscription(t, h, tc.ID, org, token)
	case "WH-020":
		CreateFixtureAccount(t, h, org, token)
		for _, target := range []string{
			"http://127.0.0.1/hook",
			"http://[::1]/hook",
			"http://10.0.0.10/hook",
			"http://172.16.0.10/hook",
			"http://192.168.1.10/hook",
			"http://169.254.169.254/latest/meta-data",
			"http://0.0.0.0/hook",
			"http://[fe80::1]/hook",
			"http://localhost/hook",
			"http://postgres:5432/hook",
		} {
			t.Run(url.QueryEscape(target), func(t *testing.T) {
				requireReviewStatus(t, h, tc.ID, http.StatusBadRequest, http.MethodPost, "/v1/webhooks", map[string]any{
					"target_url": target, "secret": "ssrf-test-secret-0123456789abcdef012345",
				}, token)
			})
		}
	case "WH-021", "WH-022", "WH-023", "WH-024":
		exerciseReviewWebhook(t, h, tc.ID, org, token)
	case "LIVE-013", "LIVE-014", "LIVE-015", "LIVE-016":
		exerciseReviewSSE(t, h, tc.ID, org, token)
	case "LIM-012":
		account := CreateFixtureAccount(t, h, org, token)
		CreateFixtureSubscription(t, h, account, token)
		h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/executions/authorize", map[string]any{"execution_id": Unique("long-running"), "credits": 100}, token)
		requireReviewStatus(t, h, tc.ID, http.StatusConflict, http.MethodPost, "/v1/executions/authorize", map[string]any{"execution_id": Unique("continuation"), "credits": 1}, token)
	case "TEST-002":
		if strings.TrimRight(h.BaseURL, "/") == strings.TrimRight(h.MockURL, "/") {
			t.Fatalf("%s: BILLMESH_BASE_URL points to the fake external-service server", tc.ID)
		}
		h.RequireStatus(t, http.StatusOK, http.MethodGet, "/readyz", nil, "")
	case "TEST-004":
		first := CreateFixtureAccount(t, h, org, token)
		otherOrg := Unique("isolated-org")
		otherToken := h.IssueToken(t, otherOrg, "daybook", AllPermissions(), nil)
		second := CreateFixtureAccount(t, h, otherOrg, otherToken)
		if first == second {
			t.Fatalf("%s: independent fixtures reused account %s", tc.ID, first)
		}
		current := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/accounts/current", nil, otherToken)
		if strings.Contains(string(current), first) {
			t.Fatalf("%s: first fixture account leaked into second tenant: %s", tc.ID, current)
		}
	}
	return true
}

func usageBody(reservation, event string, quantity int64) map[string]any {
	return map[string]any{"event_id": event, "reservation_id": reservation, "meter": "workflow.execution", "quantity": quantity, "application": "daybook"}
}

func requireReviewStatus(t *testing.T, h *HTTP, id string, want int, method, path string, body any, token string) {
	t.Helper()
	status, raw, _ := h.JSON(t, method, path, body, token)
	if status != want {
		t.Fatalf("%s: %s %s: want status %d, got %d: %s", id, method, path, want, status, raw)
	}
}

func exerciseReviewPayment(t *testing.T, h *HTTP, id, org, token string) {
	t.Helper()
	CreateFixtureAccount(t, h, org, token)
	packID := FixtureCreditPack(t, h, "daybook", 500, token)
	key := Unique("review-payment")
	orderRaw := h.RequireStatusWithHeaders(t, http.StatusCreated, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack_id": packID}, token,
		http.Header{"Idempotency-Key": []string{key}})
	order := Decode[PaymentOrderFixture](t, orderRaw)
	config := order.Checkout.ClientConfig
	event := map[string]any{"id": Unique("event"), "type": "payment.captured", "payment_id": Unique("payment"), "order_id": config.OrderID, "status": "captured", "amount_minor": config.AmountMinor, "currency": config.Currency}
	switch id {
	case "PAY-021":
		event["amount_minor"] = config.AmountMinor + 1
		event["currency"] = "USD"
		if status := h.SignedWebhook(t, "/v1/payments/webhook", event, "test-webhook-secret"); status != http.StatusBadRequest {
			t.Fatalf("%s: want 400 for mismatched capture, got %d", id, status)
		}
	case "PAY-022":
		if status := h.SignedWebhook(t, "/v1/payments/webhook", event, "test-webhook-secret"); status != http.StatusNoContent {
			t.Fatalf("%s: capture status %d", id, status)
		}
		otherOrg := Unique("other-org")
		otherToken := h.IssueToken(t, otherOrg, "daybook", AllPermissions(), nil)
		CreateFixtureAccount(t, h, otherOrg, otherToken)
		raw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/payments", nil, otherToken)
		if strings.Contains(string(raw), order.PaymentID) {
			t.Fatalf("%s: payment leaked into another account", id)
		}
	case "PAY-023":
		secondRaw := h.RequireStatusWithHeaders(t, http.StatusOK, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack_id": packID}, token,
			http.Header{"Idempotency-Key": []string{key}})
		second := Decode[struct {
			PaymentID string `json:"payment_id"`
		}](t, secondRaw)
		if second.PaymentID != order.PaymentID {
			t.Fatalf("%s: checkout retry created payment %s after %s", id, second.PaymentID, order.PaymentID)
		}
	case "PAY-025":
		if status := h.SignedWebhook(t, "/v1/payments/webhook", event, "test-webhook-secret"); status != http.StatusNoContent {
			t.Fatalf("%s: capture status %d", id, status)
		}
		refund := map[string]any{"id": Unique("refund-event"), "type": "refund.processed", "payment_id": event["payment_id"], "refund_id": Unique("refund"), "amount_minor": config.AmountMinor}
		if status := h.SignedWebhook(t, "/v1/payments/webhook", refund, "test-webhook-secret"); status != http.StatusNoContent {
			t.Fatalf("%s: refund status %d", id, status)
		}
		event["id"] = Unique("late-capture")
		if status := h.SignedWebhook(t, "/v1/payments/webhook", event, "test-webhook-secret"); status != http.StatusNoContent {
			t.Fatalf("%s: late capture status %d", id, status)
		}
		raw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/payments", nil, token)
		if !strings.Contains(string(raw), `"status":"refunded"`) {
			t.Fatalf("%s: late capture changed refunded payment: %s", id, raw)
		}
	case "PAY-026":
		if h.MockURL == "" {
			t.Fatal("MOCK_SERVER_URL is required")
		}
		resp, err := h.Client.Post(h.MockURL+"/test/failure", "application/json", strings.NewReader(`{"status":429}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		t.Cleanup(func() {
			_, _ = h.Client.Post(h.MockURL+"/test/failure", "application/json", strings.NewReader(`{"status":0}`))
		})
		status, raw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack_id": packID}, token,
			http.Header{"Idempotency-Key": []string{Unique("provider-outage")}})
		if status != http.StatusServiceUnavailable {
			t.Fatalf("%s: want 503, got %d: %s", id, status, raw)
		}
	case "PAY-027":
		event["order_id"] = Unique("unknown-order")
		if status := h.SignedWebhook(t, "/v1/payments/webhook", event, "test-webhook-secret"); status != http.StatusBadRequest {
			t.Fatalf("%s: want 400 for unsupported operation, got %d", id, status)
		}
	}
}

func exerciseReviewSubscription(t *testing.T, h *HTTP, id, org, token string) {
	t.Helper()
	account := CreateFixtureAccount(t, h, org, token)
	subscription := CreateFixtureSubscription(t, h, account, token)
	switch id {
	case "SUB-022":
		before := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/entitlements", nil, token)
		paid := FixturePlan(t, h, "daybook", "paid", token)
		raw := h.RequireStatusWithHeaders(t, http.StatusCreated, http.MethodPost, "/v1/subscription-transitions", map[string]any{"plan_id": paid}, token,
			http.Header{"Idempotency-Key": []string{Unique("failed-upgrade")}})
		transition := Decode[struct {
			Checkout struct {
				ClientConfig struct {
					OrderID string `json:"order_id"`
				} `json:"client_config"`
			} `json:"checkout"`
		}](t, raw)
		if status := h.SignedWebhook(t, "/v1/payments/webhook", map[string]any{
			"id": Unique("failed-payment"), "type": "payment.failed", "payment_id": Unique("provider-payment"),
			"order_id": transition.Checkout.ClientConfig.OrderID, "status": "failed",
		}, "test-webhook-secret"); status != http.StatusNoContent {
			t.Fatalf("%s: failed payment webhook returned %d", id, status)
		}
		after := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/entitlements", nil, token)
		if string(before) != string(after) {
			t.Fatalf("%s: failed upgrade changed entitlements", id)
		}
	case "SUB-023", "SUB-028":
		h.RequireStatusWithHeaders(t, http.StatusOK, http.MethodPost, "/v1/subscriptions/current/cancellation", map[string]any{"effective": "immediate"}, token,
			http.Header{"Idempotency-Key": []string{Unique("cancel")}})
		snapshot := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/billing-snapshot", nil, token)
		if !strings.Contains(string(snapshot), `"status":"cancelled"`) {
			t.Fatalf("%s: cancellation was not preserved: %s", id, snapshot)
		}
	case "SUB-027":
		wallet := CreateFundedWallet(t, h, account, token)
		before := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/wallets/"+wallet, nil, token)
		h.RequireStatusWithHeaders(t, http.StatusBadRequest, http.MethodPost, "/v1/subscription-transitions", map[string]any{"plan_id": "00000000-0000-0000-0000-000000000001"}, token,
			http.Header{"Idempotency-Key": []string{Unique("unknown-plan")}})
		after := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/wallets/"+wallet, nil, token)
		if string(before) != string(after) {
			t.Fatalf("%s: rejected plan change modified purchased credits", id)
		}
	}
	_ = subscription
}

func exerciseReviewWebhook(t *testing.T, h *HTTP, id, org, token string) {
	t.Helper()
	CreateFixtureAccount(t, h, org, token)
	target := h.MockURL + "/receivers/daybook"
	switch id {
	case "WH-021":
		raw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{
			"target_url": target, "secret": "current-secret-0123456789abcdef012345",
		}, token)
		endpoint := Decode[struct {
			ID string `json:"id"`
		}](t, raw).ID
		h.RequireStatus(t, http.StatusNoContent, http.MethodPost, "/v1/webhooks/"+endpoint+"/rotate-secret", map[string]any{
			"secret": "replacement-secret-0123456789abcdef", "grace_seconds": 60,
		}, token)
	case "WH-022":
		h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{"target_url": target, "secret": "tenant-secret-0123456789abcdef012345"}, token)
		otherOrg := Unique("other-org")
		other := h.IssueToken(t, otherOrg, "daybook", AllPermissions(), nil)
		CreateFixtureAccount(t, h, otherOrg, other)
		raw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/webhooks", nil, other)
		if strings.Contains(string(raw), target) {
			t.Fatalf("%s: another tenant can see webhook destination", id)
		}
	case "WH-023", "WH-024":
		status := 429
		if id == "WH-024" {
			status = http.StatusGone
		}
		resp, err := h.Client.Post(h.MockURL+"/test/failure", "application/json", strings.NewReader(fmt.Sprintf(`{"status":%d}`, status)))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		t.Cleanup(func() {
			reset, err := h.Client.Post(h.MockURL+"/test/failure", "application/json", strings.NewReader(`{"status":0}`))
			if err == nil {
				reset.Body.Close()
			}
		})
		h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{"target_url": target, "secret": "retry-secret-0123456789abcdef012345"}, token)
	}
}

func exerciseReviewSSE(t *testing.T, h *HTTP, id, org, token string) {
	t.Helper()
	CreateFixtureAccount(t, h, org, token)
	req, err := http.NewRequest(http.MethodGet, h.BaseURL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	switch id {
	case "LIVE-013":
		otherOrg := Unique("foreign-sse")
		other := h.IssueToken(t, otherOrg, "daybook", AllPermissions(), nil)
		otherAccount := CreateFixtureAccount(t, h, otherOrg, other)
		foreignSubscription := CreateFixtureSubscription(t, h, otherAccount, other)
		pool, err := pgxpool.New(context.Background(), os.Getenv("BILLMESH_E2E_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		var foreignSequence int64
		if err := pool.QueryRow(context.Background(), `SELECT sequence FROM outbox_events WHERE aggregate_type='subscription' AND aggregate_id=$1 ORDER BY sequence DESC LIMIT 1`, foreignSubscription).Scan(&foreignSequence); err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Last-Event-ID", fmt.Sprint(foreignSequence))
	case "LIVE-014":
		shortLived := h.IssueToken(t, org, "daybook", AllPermissions(), map[string]any{"expires_in_seconds": 3})
		req.Header.Set("Authorization", "Bearer "+shortLived)
	case "LIVE-015":
		req.Header.Set("Last-Event-ID", "9223372036854775807")
	case "LIVE-016":
		req.Header.Set("X-Forwarded-Proto", "https")
	}
	client := *h.Client
	client.Timeout = 1500 * time.Millisecond
	if id == "LIVE-014" {
		client.Timeout = 6 * time.Second
	}
	resp, err := client.Do(req)
	if id == "LIVE-014" {
		if err != nil {
			t.Fatalf("%s: valid SSE connection failed: %v", id, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: want initial 200, got %d", id, resp.StatusCode)
		}
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Fatalf("%s: stream did not close after token expiry: %v", id, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("%s: connect SSE: %v", id, err)
	}
	defer resp.Body.Close()
	if id == "LIVE-015" && resp.StatusCode != http.StatusGone {
		t.Fatalf("%s: want 410 resynchronization response, got %d", id, resp.StatusCode)
	}
	if id == "LIVE-013" && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("%s: want 403 for foreign event cursor, got %d", id, resp.StatusCode)
	}
	if id != "LIVE-013" && id != "LIVE-015" && resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: want 200, got %d", id, resp.StatusCode)
	}
}
