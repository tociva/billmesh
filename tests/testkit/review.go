package testkit

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
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
		for _, target := range []string{"http://127.0.0.1/hook", "http://[::1]/hook", "http://169.254.169.254/latest/meta-data"} {
			t.Run(url.QueryEscape(target), func(t *testing.T) {
				requireReviewStatus(t, h, tc.ID, http.StatusBadRequest, http.MethodPost, "/v1/webhooks", map[string]any{"application": "daybook", "target_url": target, "secret": "secret"}, token)
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
		h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/accounts/"+first, nil, otherToken)
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
	account := CreateFixtureAccount(t, h, org, token)
	orderRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/payments/orders", map[string]any{"account_id": account, "credit_pack": "credits-500"}, token)
	order := Decode[struct {
		PaymentID string `json:"payment_id"`
		Order     struct {
			ID       string `json:"id"`
			Amount   int64  `json:"amount"`
			Currency string `json:"currency"`
		} `json:"order"`
	}](t, orderRaw)
	event := map[string]any{"id": Unique("event"), "type": "payment.captured", "payment_id": Unique("payment"), "order_id": order.Order.ID, "status": "captured", "amount_minor": order.Order.Amount}
	switch id {
	case "PAY-021":
		event["amount_minor"] = order.Order.Amount + 1
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
		secondRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/payments/orders", map[string]any{"account_id": account, "credit_pack": "credits-500", "idempotency_key": order.PaymentID}, token)
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
		refund := map[string]any{"id": Unique("refund-event"), "type": "refund.processed", "payment_id": event["payment_id"], "refund_id": Unique("refund"), "amount_minor": order.Order.Amount}
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
		requireReviewStatus(t, h, id, http.StatusServiceUnavailable, http.MethodPost, "/v1/payments/orders", map[string]any{"account_id": account, "credit_pack": "credits-500"}, token)
	case "PAY-027":
		event["type"] = "payment.unsupported"
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
		requireReviewStatus(t, h, id, http.StatusPaymentRequired, http.MethodPost, "/v1/subscriptions/"+subscription+"/change-plan", map[string]any{"plan": "daybook-pro", "payment_status": "failed"}, token)
		after := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/entitlements", nil, token)
		if string(before) != string(after) {
			t.Fatalf("%s: failed upgrade changed entitlements", id)
		}
	case "SUB-023", "SUB-028":
		h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/subscriptions/"+subscription+"/cancel", map[string]any{"immediate": true}, token)
		requireReviewStatus(t, h, id, http.StatusConflict, http.MethodPost, "/v1/subscriptions/"+subscription+"/renew", map[string]any{"operation_ref": Unique("late-renewal")}, token)
	case "SUB-027":
		wallet := CreateFundedWallet(t, h, account, token)
		before := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/wallets/"+wallet, nil, token)
		requireReviewStatus(t, h, id, http.StatusBadRequest, http.MethodPost, "/v1/subscriptions/"+subscription+"/change-plan", map[string]any{"plan": "unknown"}, token)
		after := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/wallets/"+wallet, nil, token)
		if string(before) != string(after) {
			t.Fatalf("%s: rejected plan change modified purchased credits", id)
		}
	}
}

func exerciseReviewWebhook(t *testing.T, h *HTTP, id, org, token string) {
	t.Helper()
	CreateFixtureAccount(t, h, org, token)
	target := h.MockURL + "/receivers/daybook"
	switch id {
	case "WH-021":
		requireReviewStatus(t, h, id, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{"application": "daybook", "target_url": target, "secret": "current", "previous_secret": "retired"}, token)
	case "WH-022":
		h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{"application": "daybook", "target_url": target, "secret": "tenant-secret"}, token)
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
		h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{"application": "daybook", "target_url": target, "secret": "retry-secret"}, token)
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
		req.Header.Set("Last-Event-ID", "9223372036854775807")
	case "LIVE-014":
		expired := h.IssueToken(t, org, "daybook", AllPermissions(), map[string]any{"expires_in_seconds": -1})
		req.Header.Set("Authorization", "Bearer "+expired)
	case "LIVE-015":
		req.Header.Set("Last-Event-ID", "1")
	case "LIVE-016":
		req.Header.Set("X-Forwarded-Proto", "https")
	}
	client := *h.Client
	client.Timeout = 1500 * time.Millisecond
	resp, err := client.Do(req)
	if id == "LIVE-014" {
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("%s: want 401, got %d", id, resp.StatusCode)
			}
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
	if id != "LIVE-015" && resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: want 200, got %d", id, resp.StatusCode)
	}
}
