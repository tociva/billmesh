//go:build e2e

package security_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestSecurityAuthenticationHeaderRejections(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("gap-auth")
	valid := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog", nil, valid)

	cases := []struct {
		name          string
		authorization []string
	}{
		{name: "empty bearer", authorization: []string{"Bearer "}},
		{name: "unsupported scheme", authorization: []string{"Basic " + valid}},
		{name: "malformed jwt", authorization: []string{"Bearer not-a-token"}},
		{name: "malformed segments", authorization: []string{"Bearer a.b.c.d"}},
		{name: "invalid base64", authorization: []string{"Bearer %%%.%%%.%%%"}},
		{name: "unsigned algorithm", authorization: []string{"Bearer " + replaceJWTAlgorithm(t, valid, "none")}},
		{name: "hmac substitution", authorization: []string{"Bearer " + replaceJWTAlgorithm(t, valid, "HS256")}},
		{name: "oversized token", authorization: []string{"Bearer " + strings.Repeat("x", 64*1024)}},
		{name: "duplicate authorization", authorization: []string{"Bearer " + valid, "Bearer not-a-token"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, h.BaseURL+"/v1/catalog", nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range tc.authorization {
				req.Header.Add("Authorization", value)
			}
			resp, err := h.Client.Do(req)
			if err != nil {
				t.Fatalf("request rejected below application layer: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
				raw, _ := io.ReadAll(resp.Body)
				t.Fatalf("want auth rejection, got %d: %s", resp.StatusCode, raw)
			}
		})
	}
}

func replaceJWTAlgorithm(t *testing.T, raw, algorithm string) string {
	t.Helper()
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("fixture JWT has %d segments", len(parts))
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(header, &fields); err != nil {
		t.Fatal(err)
	}
	fields["alg"] = algorithm
	header, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	parts[0] = base64.RawURLEncoding.EncodeToString(header)
	return strings.Join(parts, ".")
}

func TestSecuritySignedMissingAndMalformedClaims(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("claims")
	for _, claim := range []string{"exp", "iss", "aud", "sub", "app"} {
		t.Run("missing "+claim, func(t *testing.T) {
			token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), map[string]any{"omit_claims": []string{claim}})
			h.RequireStatus(t, http.StatusUnauthorized, http.MethodGet, "/v1/catalog", nil, token)
		})
	}
	withoutOrg := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), map[string]any{"omit_claims": []string{"org_id"}})
	h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog", nil, withoutOrg)
	for _, tc := range []struct {
		claim string
		value any
	}{
		{"org_id", []string{org}}, {"app", true}, {"exp", "tomorrow"},
	} {
		t.Run("wrong type "+tc.claim, func(t *testing.T) {
			token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), map[string]any{"claim_overrides": map[string]any{tc.claim: tc.value}})
			h.RequireStatus(t, http.StatusUnauthorized, http.MethodGet, "/v1/catalog", nil, token)
		})
	}
}

func TestSecurityMissingTokenContextIsRejected(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("gap-context")

	withoutTokenUse := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), map[string]any{"omit_token_use": true})
	h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog", nil, withoutTokenUse)

	withoutOrg := h.IssueToken(t, "", "daybook", testkit.AllPermissions(), nil)
	status, raw, _ := h.JSON(t, http.MethodPost, "/v1/accounts", map[string]any{"name": "Missing Org", "external_ref": testkit.Unique("acct")}, withoutOrg)
	if status != http.StatusForbidden {
		t.Fatalf("GAP-AUTH-002: token without org context created an account bridge, got %d: %s", status, raw)
	}
}

func TestSecurityIssuedPermissionsRemainUntilTokenExpiry(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("permission-snapshot")
	subject := testkit.Unique("same-principal")
	setup := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), map[string]any{"sub": subject})
	testkit.CreateFixtureAccount(t, h, org, setup)
	old := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), map[string]any{"sub": subject, "expires_in_seconds": 3})
	newToken := h.IssueToken(t, org, "daybook", []string{"billing:read"}, map[string]any{"sub": subject})
	h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/audit", nil, old)
	h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/admin/audit", nil, newToken)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, _, _ := h.JSON(t, http.MethodGet, "/v1/admin/audit", nil, old)
		if status == http.StatusUnauthorized {
			return
		}
		if status != http.StatusOK {
			t.Fatalf("GAP-AUTH-008: old permission snapshot returned %d before expiry", status)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("GAP-AUTH-008: previously issued permissions remained usable past token expiry")
}

func TestSecurityJSONParserRejectsAmbiguity(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("gap-parser")
	token := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, nil)

	cases := []struct {
		name        string
		body        string
		contentType string
	}{
		{name: "unknown writable field", body: `{"name":"Parser","external_ref":"` + testkit.Unique("acct") + `","admin":true}`, contentType: "application/json"},
		{name: "duplicate json key", body: `{"name":"Parser","name":"Attacker","external_ref":"` + testkit.Unique("acct") + `"}`, contentType: "application/json"},
		{name: "trailing json value", body: `{"name":"Parser","external_ref":"` + testkit.Unique("acct") + `"} {"name":"second"}`, contentType: "application/json"},
		{name: "unsupported content type", body: `{"name":"Parser","external_ref":"` + testkit.Unique("acct") + `"}`, contentType: "text/plain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := rawJSON(t, h, http.MethodPost, "/v1/accounts", []byte(tc.body), token, tc.contentType)
			if status < 400 || status >= 500 {
				t.Fatalf("want deterministic parser rejection, got %d: %s", status, raw)
			}
		})
	}
	sqlLike := `Robert'); DROP TABLE billing_accounts; --`
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{"name": sqlLike, "external_ref": testkit.Unique("sql-like")}, token)
	account := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/accounts/current", nil, token)
	if !strings.Contains(string(account), sqlLike) {
		t.Fatalf("GAP-SEC-006: SQL-like name was not stored as data: %s", account)
	}
	h.RequireStatus(t, http.StatusNotFound, http.MethodGet, "/v1/accounts/not-a-uuid", nil, token)
}

func TestSecurityAccountLinkCannotClaimForeignIdentity(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("gap-link")
	token := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, nil)
	account := testkit.CreateFixtureAccount(t, h, org, token)

	foreignOrg := testkit.Unique("foreign-org")
	for _, target := range []map[string]any{
		{"application": "daybook", "organization_id": foreignOrg, "environment": "production"},
		{"application": "taskmesh", "organization_id": org, "environment": "production"},
		{"application": "daybook", "organization_id": org, "environment": "staging"},
	} {
		status, raw, _ := h.JSON(t, http.MethodPost, "/v1/accounts/"+account+"/links", target, token)
		if status != http.StatusNotFound {
			t.Fatalf("GAP-AUTHZ-005: removed account-link route returned %d: %s", status, raw)
		}
	}
	foreign := h.IssueToken(t, foreignOrg, "daybook", testkit.AllPermissions(), nil)
	h.RequireStatus(t, http.StatusNotFound, http.MethodGet, "/v1/accounts/current", nil, foreign)
	linker := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write", "billing:link"}, nil)
	h.RequireStatus(t, http.StatusNotFound, http.MethodPost, "/v1/accounts/"+account+"/links", map[string]any{"application": "taskmesh", "organization_id": org, "environment": "production"}, linker)
}

func TestSecurityProductBoundaryForWalletsAndSubscriptions(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("gap-product")
	token := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, nil)
	account := testkit.CreateFixtureAccount(t, h, org, token)
	taskmeshToken := h.IssueToken(t, org, "taskmesh", []string{"catalogue:read"}, nil)
	taskmesh := productID(t, h, "taskmesh", taskmeshToken)

	status, raw, _ := h.JSON(t, http.MethodPost, "/v1/wallets", map[string]any{"account_id": account, "product_id": taskmesh}, token)
	if status != http.StatusForbidden {
		t.Fatalf("GAP-AUTHZ-006: daybook token created taskmesh wallet, got %d: %s", status, raw)
	}
	headers := http.Header{"Idempotency-Key": []string{testkit.Unique("foreign-transition")}}
	status, raw, _ = h.JSONWithHeaders(t, http.MethodPost, "/v1/subscription-transitions", map[string]any{"plan_id": testkit.FixturePlan(t, h, "taskmesh", "paid", taskmeshToken)}, token, headers)
	if status != http.StatusForbidden {
		t.Fatalf("GAP-AUTHZ-006: daybook token created taskmesh subscription, got %d: %s", status, raw)
	}
}

func TestSecurityRejectedOperationsHaveNoFinancialSideEffects(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("gap-side-effects")
	admin := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	account := testkit.CreateFixtureAccount(t, h, org, admin)
	wallet := testkit.CreateFundedWallet(t, h, account, admin)
	before := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/wallets/"+wallet, nil, admin)

	viewer := h.IssueToken(t, org, "daybook", []string{"billing:read"}, nil)
	h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/wallets/"+wallet+"/grants", map[string]any{"source": "forbidden", "operation_ref": testkit.Unique("grant"), "amount": 50}, viewer)
	after := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/wallets/"+wallet, nil, admin)
	if string(before) != string(after) {
		t.Fatalf("GAP-AUTHZ-009: rejected wallet grant changed financial state before=%s after=%s", before, after)
	}
}

func TestSecurityPaymentWebhookRequiresTrustedCaptureFields(t *testing.T) {
	h := testkit.NewHTTP(t)
	resetMockFailure(t, h)
	org := testkit.Unique("gap-payment")
	token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	testkit.CreateFixtureAccount(t, h, org, token)
	order := testkit.CreateCreditPackOrder(t, h, "daybook", 500, token)

	cases := []struct {
		name  string
		event map[string]any
	}{
		{name: "missing amount and currency", event: map[string]any{"id": testkit.Unique("event"), "type": "payment.captured", "payment_id": testkit.Unique("payment"), "order_id": order.Checkout.ClientConfig.OrderID, "status": "captured"}},
		{name: "zero amount", event: map[string]any{"id": testkit.Unique("event"), "type": "payment.captured", "payment_id": testkit.Unique("payment"), "order_id": order.Checkout.ClientConfig.OrderID, "status": "captured", "amount_minor": 0, "currency": order.Checkout.ClientConfig.Currency}},
		{name: "mismatched amount", event: map[string]any{"id": testkit.Unique("event"), "type": "payment.captured", "payment_id": testkit.Unique("payment"), "order_id": order.Checkout.ClientConfig.OrderID, "status": "captured", "amount_minor": order.Checkout.ClientConfig.AmountMinor + 1, "currency": order.Checkout.ClientConfig.Currency}},
		{name: "missing currency", event: map[string]any{"id": testkit.Unique("event"), "type": "payment.captured", "payment_id": testkit.Unique("payment"), "order_id": order.Checkout.ClientConfig.OrderID, "status": "captured", "amount_minor": order.Checkout.ClientConfig.AmountMinor}},
		{name: "mismatched currency", event: map[string]any{"id": testkit.Unique("event"), "type": "payment.captured", "payment_id": testkit.Unique("payment"), "order_id": order.Checkout.ClientConfig.OrderID, "status": "captured", "amount_minor": order.Checkout.ClientConfig.AmountMinor, "currency": "USD"}},
		{name: "missing payment reference", event: map[string]any{"id": testkit.Unique("event"), "type": "payment.captured", "order_id": order.Checkout.ClientConfig.OrderID, "status": "captured", "amount_minor": order.Checkout.ClientConfig.AmountMinor, "currency": order.Checkout.ClientConfig.Currency}},
		{name: "unknown order", event: map[string]any{"id": testkit.Unique("event"), "type": "payment.captured", "payment_id": testkit.Unique("payment"), "order_id": testkit.Unique("order"), "status": "captured", "amount_minor": order.Checkout.ClientConfig.AmountMinor, "currency": order.Checkout.ClientConfig.Currency}},
		{name: "mismatched payment status", event: map[string]any{"id": testkit.Unique("event"), "type": "payment.captured", "payment_id": testkit.Unique("payment"), "order_id": order.Checkout.ClientConfig.OrderID, "status": "authorized", "amount_minor": order.Checkout.ClientConfig.AmountMinor, "currency": order.Checkout.ClientConfig.Currency}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if status := h.SignedWebhook(t, "/v1/payments/webhook", tc.event, "test-webhook-secret"); status != http.StatusBadRequest {
				t.Fatalf("GAP-SEC-005: want 400 for untrusted capture fields, got %d", status)
			}
		})
	}
	raw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/payments", nil, token)
	if strings.Contains(string(raw), `"status":"captured"`) {
		t.Fatalf("GAP-SEC-005: rejected capture variant changed payment state: %s", raw)
	}
}

func TestSecuritySharedWebhookURLUsesTenantSecret(t *testing.T) {
	h := testkit.NewHTTP(t)
	if h.MockURL == "" {
		t.Fatal("MOCK_SERVER_URL is required")
	}
	before := listReceivedWebhooks(t, h)
	target := h.MockURL + "/receivers/shared"

	orgA := testkit.Unique("gap-webhook-a")
	tokenA := h.IssueToken(t, orgA, "daybook", testkit.AllPermissions(), nil)
	accountA := testkit.CreateFixtureAccount(t, h, orgA, tokenA)
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{"target_url": target, "secret": "tenant-a-secret-0123456789abcdef"}, tokenA)
	subscriptionA := testkit.CreateFixtureSubscription(t, h, accountA, tokenA)

	orgB := testkit.Unique("gap-webhook-b")
	tokenB := h.IssueToken(t, orgB, "daybook", testkit.AllPermissions(), nil)
	accountB := testkit.CreateFixtureAccount(t, h, orgB, tokenB)
	h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{"target_url": target, "secret": "tenant-b-secret-0123456789abcdef"}, tokenB)
	subscriptionB := testkit.CreateFixtureSubscription(t, h, accountB, tokenB)

	deliveries := waitForWebhookDeliveries(t, h, len(before)+4)
	newDeliveries := deliveries[len(before):]
	foundA := false
	foundB := false
	eventB := ""
	for _, delivery := range newDeliveries {
		if delivery.App != "shared" || delivery.EventType != "subscription.transition_completed" {
			continue
		}
		if strings.Contains(delivery.RawBody, subscriptionA) {
			if delivery.Signature != outgoingWebhookSignature(delivery.Timestamp, delivery.RawBody, "tenant-a-secret-0123456789abcdef") {
				t.Fatalf("GAP-SEC-002: tenant A event used the wrong secret: %+v", delivery)
			}
			foundA = true
		}
		if strings.Contains(delivery.RawBody, subscriptionB) {
			if delivery.Signature != outgoingWebhookSignature(delivery.Timestamp, delivery.RawBody, "tenant-b-secret-0123456789abcdef") {
				t.Fatalf("GAP-SEC-002: tenant B event used the wrong secret: %+v", delivery)
			}
			foundB = true
			eventB = delivery.EventID
		}
	}
	if !foundA || !foundB {
		t.Fatalf("GAP-SEC-002: shared webhook URL did not produce tenant-specific signatures: foundA=%v foundB=%v deliveries=%+v", foundA, foundB, newDeliveries)
	}
	pool, err := pgxpool.New(context.Background(), os.Getenv("BILLMESH_E2E_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var deliveryID string
	if err := pool.QueryRow(context.Background(), `SELECT id FROM webhook_deliveries WHERE event_id=$1`, eventB).Scan(&deliveryID); err != nil {
		t.Fatal(err)
	}
	prior := len(deliveries)
	h.RequireStatus(t, http.StatusNoContent, http.MethodPost, "/v1/admin/webhooks/"+deliveryID+"/replay", nil, tokenB)
	retried := waitForWebhookDeliveries(t, h, prior+1)
	last := retried[len(retried)-1]
	if last.EventID != eventB || last.Signature != outgoingWebhookSignature(last.Timestamp, last.RawBody, "tenant-b-secret-0123456789abcdef") {
		t.Fatalf("GAP-SEC-002: replay used wrong tenant secret: %+v", last)
	}
}

func rawJSON(t *testing.T, h *testkit.HTTP, method, path string, raw []byte, token, contentType string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, h.BaseURL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := h.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

func productID(t *testing.T, h *testkit.HTTP, slug, token string) string {
	t.Helper()
	return testkit.FixtureProduct(t, h, slug, token)
}

type receivedWebhook struct {
	App       string `json:"app"`
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	Signature string `json:"signature"`
	Timestamp string `json:"timestamp"`
	RawBody   string `json:"raw_body"`
}

func listReceivedWebhooks(t *testing.T, h *testkit.HTTP) []receivedWebhook {
	t.Helper()
	resp, err := h.Client.Get(h.MockURL + "/test/webhooks")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("list fake webhooks: status %d: %s", resp.StatusCode, raw)
	}
	var deliveries []receivedWebhook
	if err := json.NewDecoder(resp.Body).Decode(&deliveries); err != nil {
		t.Fatal(err)
	}
	return deliveries
}

func waitForWebhookDeliveries(t *testing.T, h *testkit.HTTP, want int) []receivedWebhook {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var deliveries []receivedWebhook
	for time.Now().Before(deadline) {
		deliveries = listReceivedWebhooks(t, h)
		if len(deliveries) >= want {
			return deliveries
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d webhook deliveries, got %d", want, len(deliveries))
	return nil
}

func webhookSignature(rawBody, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(rawBody))
	return hex.EncodeToString(mac.Sum(nil))
}

func outgoingWebhookSignature(timestamp, rawBody, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "." + rawBody))
	return hex.EncodeToString(mac.Sum(nil))
}
