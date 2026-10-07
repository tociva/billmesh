package testkit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

type HTTP struct {
	BaseURL, MockURL, Token string
	Client                  *http.Client
}

func NewHTTP(t *testing.T) *HTTP {
	t.Helper()
	base := strings.TrimRight(os.Getenv("BILLMESH_BASE_URL"), "/")
	if base == "" {
		t.Fatal("BILLMESH_BASE_URL is required")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	if trace := os.Getenv("BILLMESH_ROUTE_TRACE_FILE"); trace != "" {
		parsed, err := url.Parse(base)
		if err != nil {
			t.Fatal(err)
		}
		client.Transport = &routeTraceTransport{baseHost: parsed.Host, traceFile: trace, next: http.DefaultTransport}
	}
	return &HTTP{BaseURL: base, MockURL: strings.TrimRight(os.Getenv("MOCK_SERVER_URL"), "/"), Token: os.Getenv("BILLMESH_TEST_TOKEN"), Client: client}
}

type routeTraceTransport struct {
	baseHost, traceFile string
	next                http.RoundTripper
}

func (t *routeTraceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err == nil && req.URL.Host == t.baseHost {
		file, openErr := os.OpenFile(t.traceFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if openErr == nil {
			_, _ = fmt.Fprintf(file, "%s %d %s\n", req.Method, resp.StatusCode, req.URL.Path)
			_ = file.Close()
		}
	}
	return resp, err
}

func (h *HTTP) JSON(t *testing.T, method, path string, body any, token string) (int, []byte, http.Header) {
	return h.JSONWithHeaders(t, method, path, body, token, nil)
}

func (h *HTTP) JSONWithHeaders(t *testing.T, method, path string, body any, token string, headers http.Header) (int, []byte, http.Header) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, h.BaseURL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for name, values := range headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp.StatusCode, raw, resp.Header
}

func (h *HTTP) RequireStatus(t *testing.T, want int, method, path string, body any, token string) []byte {
	t.Helper()
	status, raw, _ := h.JSON(t, method, path, body, token)
	if status != want {
		t.Fatalf("%s %s: want status %d, got %d: %s", method, path, want, status, raw)
	}
	return raw
}

func (h *HTTP) RequireStatusWithHeaders(t *testing.T, want int, method, path string, body any, token string, headers http.Header) []byte {
	t.Helper()
	status, raw, _ := h.JSONWithHeaders(t, method, path, body, token, headers)
	if status != want {
		t.Fatalf("%s %s: want status %d, got %d: %s", method, path, want, status, raw)
	}
	return raw
}
func (h *HTTP) SignedWebhook(t *testing.T, path string, body any, secret string) int {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return h.SignedWebhookRaw(t, path, raw, secret)
}

func (h *HTTP) SignedWebhookRaw(t *testing.T, path string, raw []byte, secret string) int {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)
	req, err := http.NewRequest(http.MethodPost, h.BaseURL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Razorpay-Signature", hex.EncodeToString(mac.Sum(nil)))
	resp, err := h.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func (h *HTTP) IssueToken(t *testing.T, org, app string, permissions []string, overrides map[string]any) string {
	t.Helper()
	if h.MockURL == "" {
		t.Fatal("MOCK_SERVER_URL is required")
	}
	payload := map[string]any{"org_id": org, "app": app, "environment": "production", "actor_type": "user", "permissions": permissions}
	if org != "" {
		// Keep independent test organizations from accidentally sharing the fake
		// issuer's default subject. Tests for customer-level behavior pass an
		// explicit subject and therefore still exercise shared ownership.
		payload["sub"] = "test:" + org
	}
	for k, v := range overrides {
		payload[k] = v
	}
	raw, _ := json.Marshal(payload)
	resp, err := h.Client.Post(h.MockURL+"/test/token", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("issue token status %d: %s", resp.StatusCode, data)
	}
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.AccessToken == "" {
		t.Fatal("fake issuer returned an empty token")
	}
	return result.AccessToken
}

func tamperJWTPayload(t *testing.T, raw string, patch map[string]any) string {
	t.Helper()
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("test issuer returned malformed token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode token payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal token payload: %v", err)
	}
	for key, value := range patch {
		claims[key] = value
	}
	payload, err = json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal tampered token payload: %v", err)
	}
	parts[1] = base64.RawURLEncoding.EncodeToString(payload)
	return strings.Join(parts, ".")
}

func Decode[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode response: %v: %s", err, raw)
	}
	return value
}
func Unique(prefix string) string { return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()) }

type Endpoint struct{ Method, Path string }

func ExerciseEndpointContract(t *testing.T, tc PlanCase, endpoint Endpoint) {
	t.Helper()
	if ExerciseReviewE2ECase(t, tc) {
		return
	}
	h := NewHTTP(t)
	description := strings.ToLower(tc.Description)
	org := strings.ToLower(tc.ID) + "-" + Unique("org")
	token := h.Token
	if token == "" && h.MockURL != "" {
		token = h.IssueToken(t, org, "daybook", allPermissions(), nil)
	}
	if strings.Contains(description, "without authentication") || strings.Contains(description, "unauthenticated") {
		token = ""
	}
	if exerciseSpecificRejection(t, h, tc, org, token) {
		return
	}
	negative := strings.HasPrefix(description, "reject") || strings.HasPrefix(description, "prevent") || strings.Contains(description, "unauthorized") || strings.Contains(description, "insufficient")
	body := any(map[string]any{})
	prefix := strings.Split(tc.ID, "-")[0]
	if !negative {
		switch prefix {
		case "SUB":
			account := createFixtureAccount(t, h, org, token)
			createFixtureSubscription(t, h, account, token)
			return
		case "PAY":
			createFixtureAccount(t, h, org, token)
			CreateCreditPackOrder(t, h, "daybook", 500, token)
			return
		case "WH":
			createFixtureAccount(t, h, org, token)
			h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{
				"target_url": h.MockURL + "/receivers/daybook", "secret": "test-secret-0123456789abcdef012345",
			}, token)
			return
		}
		endpoint, body = preparePositiveCase(t, h, prefix, endpoint, org, token)
	}
	status, raw, _ := h.JSON(t, endpoint.Method, endpoint.Path, body, token)
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		t.Fatalf("%s is not implemented: %s %s returned %d", tc.ID, endpoint.Method, endpoint.Path, status)
	}
	unauthenticated := strings.Contains(description, "without authentication") || strings.Contains(description, "unauthenticated")
	if unauthenticated {
		if status != http.StatusUnauthorized {
			t.Fatalf("%s: want 401, got %d: %s", tc.ID, status, raw)
		}
		return
	}
	if negative {
		if status < 400 || status >= 500 {
			t.Fatalf("%s: want a client rejection, got %d: %s", tc.ID, status, raw)
		}
		return
	}
	if status < 200 || status >= 300 {
		t.Fatalf("%s: want success, got %d: %s", tc.ID, status, raw)
	}
}

func exerciseSpecificRejection(t *testing.T, h *HTTP, tc PlanCase, org, token string) bool {
	t.Helper()
	switch tc.ID {
	case "SUB-004":
		account := createFixtureAccount(t, h, org, token)
		createFixtureSubscription(t, h, account, token)
		planID := FixturePlan(t, h, "daybook", "free", token)
		h.RequireStatusWithHeaders(t, http.StatusConflict, http.MethodPost, "/v1/subscription-transitions", map[string]any{"plan_id": planID}, token,
			http.Header{"Idempotency-Key": []string{Unique("duplicate-subscription")}})
		return true
	case "PAY-005", "PAY-006", "PAY-007":
		exercisePaymentWebhookSignatureCase(t, h, tc.ID, org, token)
		return true
	case "ENT-004":
		account := createFixtureAccount(t, h, org, token)
		createFixtureSubscription(t, h, account, token)
		h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/entitlements/check?feature=standalone_workflow", nil, token)
		return true
	case "ENT-009":
		account := createFixtureAccount(t, h, org, token)
		createFixtureSubscription(t, h, account, token)
		h.RequireStatus(t, http.StatusConflict, http.MethodPost, "/v1/executions/authorize", map[string]any{"context": "standalone", "execution_id": Unique("standalone"), "credits": 1}, token)
		return true
	case "LIM-007":
		account := createFixtureAccount(t, h, org, token)
		createFixtureSubscription(t, h, account, token)
		h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/executions/authorize", map[string]any{"execution_id": Unique("exhaust"), "credits": 100}, token)
		h.RequireStatus(t, http.StatusConflict, http.MethodPost, "/v1/executions/authorize", map[string]any{"execution_id": Unique("rejected"), "credits": 1}, token)
		return true
	case "WH-002":
		createFixtureAccount(t, h, org, token)
		catalogue := CatalogueToken(t, h, "daybook")
		h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/webhooks", map[string]any{
			"target_url": h.MockURL + "/receivers/daybook", "secret": "test-secret-0123456789abcdef012345",
		}, catalogue)
		return true
	default:
		return false
	}
}

func exercisePaymentWebhookSignatureCase(t *testing.T, h *HTTP, id, org, token string) {
	t.Helper()
	createFixtureAccount(t, h, org, token)
	order := CreateCreditPackOrder(t, h, "daybook", 500, token)
	config := order.Checkout.ClientConfig
	event := map[string]any{"id": Unique("payment-event"), "type": "payment.captured", "payment_id": Unique("payment"), "order_id": config.OrderID, "status": "captured", "amount_minor": config.AmountMinor, "currency": config.Currency}

	switch id {
	case "PAY-005":
		if status := h.SignedWebhook(t, "/v1/payments/webhook", event, "test-webhook-secret"); status != http.StatusNoContent {
			t.Fatalf("%s: want 204 for a valid signed payment webhook, got %d", id, status)
		}
		raw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/payments", nil, token)
		if !strings.Contains(string(raw), order.PaymentID) || !strings.Contains(string(raw), `"status":"captured"`) {
			t.Fatalf("%s: valid webhook did not capture payment %s: %s", id, order.PaymentID, raw)
		}
	case "PAY-006":
		if status := h.SignedWebhook(t, "/v1/payments/webhook", event, "wrong-secret"); status != http.StatusUnauthorized {
			t.Fatalf("%s: want 401 for an invalid payment webhook signature, got %d", id, status)
		}
		raw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/payments", nil, token)
		if strings.Contains(string(raw), `"status":"captured"`) {
			t.Fatalf("%s: invalid webhook signature changed payment state: %s", id, raw)
		}
	case "PAY-007":
		if status := h.SignedWebhookRaw(t, "/v1/payments/webhook", []byte(`{"id":`), "test-webhook-secret"); status != http.StatusBadRequest {
			t.Fatalf("%s: want 400 for a malformed signed payment webhook body, got %d", id, status)
		}
		raw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/payments", nil, token)
		if strings.Contains(string(raw), `"status":"captured"`) {
			t.Fatalf("%s: malformed webhook body changed payment state: %s", id, raw)
		}
	}
}

func allPermissions() []string {
	return []string{"catalogue:read", "billing:read", "billing:write", "billing:ownership", "billing:link", "billing:admin", "credits:grant", "credits:reserve", "credits:settle"}
}

func AllPermissions() []string { return allPermissions() }

// CatalogueToken returns the fixed catalogue client for seeded products. Tests
// that create products dynamically use the test-only global admin registration
// because client registrations are intentionally immutable at API runtime.
func CatalogueToken(t *testing.T, h *HTTP, product string) string {
	t.Helper()
	clientID := product + "-catalogue-test"
	if product != "daybook" && product != "taskmesh" {
		clientID = "billmesh-global-admin-test"
	}
	return h.IssueToken(t, "", product, nil, map[string]any{
		"client_id": clientID, "sub": "service:" + product + "-catalogue", "actor_type": "service",
	})
}

// RuntimeToken returns the product-scoped runtime client used for reservation,
// settlement, and usage-ingestion APIs.
func RuntimeToken(t *testing.T, h *HTTP, org, product string) string {
	t.Helper()
	return h.IssueToken(t, org, product, nil, map[string]any{
		"client_id": product + "-runtime-test", "sub": "service:" + product + "-runtime", "actor_type": "service",
	})
}

func CreateFixtureAccount(t *testing.T, h *HTTP, org, token string) string {
	t.Helper()
	return createFixtureAccount(t, h, org, token)
}

func FixtureProduct(t *testing.T, h *HTTP, slug, token string) string {
	t.Helper()
	return fixtureProduct(t, h, slug, token)
}

func CreateFixtureSubscription(t *testing.T, h *HTTP, account, token string) string {
	t.Helper()
	return createFixtureSubscription(t, h, account, token)
}

// ActivatePaidSubscription exercises the public paid path: resolve the plan,
// create a server-priced transition, and confirm it with a signed provider event.
func ActivatePaidSubscription(t *testing.T, h *HTTP, product, planSlug, token string) string {
	t.Helper()
	catalogueToken := CatalogueToken(t, h, product)
	plans := Decode[struct {
		Plans []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			BillingModel string `json:"billing_model"`
		} `json:"plans"`
	}](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog?product="+url.QueryEscape(product), nil, catalogueToken)).Plans
	var planID string
	wanted := strings.ToLower(strings.ReplaceAll(planSlug, "-", " "))
	for _, plan := range plans {
		name := strings.ToLower(plan.Name)
		if plan.BillingModel == "paid" && (plan.ID == planSlug || name == wanted || strings.Contains(name, wanted)) {
			planID = plan.ID
			break
		}
	}
	if planID == "" {
		t.Fatalf("paid plan %s/%s was not found", product, planSlug)
	}
	status, raw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/subscription-transitions", map[string]any{"plan_id": planID}, token,
		http.Header{"Idempotency-Key": []string{Unique("paid-transition")}})
	if status != http.StatusCreated {
		t.Fatalf("create paid transition: got %d: %s", status, raw)
	}
	transition := Decode[struct {
		ID       string `json:"id"`
		Checkout struct {
			ClientConfig struct {
				OrderID     string `json:"order_id"`
				AmountMinor int64  `json:"amount_minor"`
				Currency    string `json:"currency"`
			} `json:"client_config"`
		} `json:"checkout"`
	}](t, raw)
	config := transition.Checkout.ClientConfig
	if config.OrderID == "" {
		t.Fatalf("paid transition did not return checkout: %s", raw)
	}
	if status := h.SignedWebhook(t, "/v1/payments/webhook", map[string]any{
		"id": Unique("event"), "type": "payment.captured", "payment_id": Unique("payment"), "order_id": config.OrderID,
		"status": "captured", "amount_minor": config.AmountMinor, "currency": config.Currency,
	}, "test-webhook-secret"); status != http.StatusNoContent {
		t.Fatalf("capture paid transition: got %d", status)
	}
	completed := Decode[struct {
		SubscriptionID string `json:"subscription_id"`
		Status         string `json:"status"`
	}](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/subscription-transitions/"+transition.ID, nil, token))
	if completed.Status != "completed" || completed.SubscriptionID == "" {
		t.Fatalf("paid transition did not complete: %+v", completed)
	}
	return completed.SubscriptionID
}

type PaymentOrderFixture struct {
	PaymentID string `json:"payment_id"`
	Checkout  struct {
		ClientConfig struct {
			OrderID     string `json:"order_id"`
			AmountMinor int64  `json:"amount_minor"`
			Currency    string `json:"currency"`
		} `json:"client_config"`
	} `json:"checkout"`
}

func CreateCreditPackOrder(t *testing.T, h *HTTP, product string, credits int64, token string) PaymentOrderFixture {
	t.Helper()
	raw := h.RequireStatusWithHeaders(t, http.StatusCreated, http.MethodPost, "/v1/payments/orders",
		map[string]any{"credit_pack_id": FixtureCreditPack(t, h, product, credits, token)}, token,
		http.Header{"Idempotency-Key": []string{Unique("payment-order")}})
	order := Decode[PaymentOrderFixture](t, raw)
	if order.PaymentID == "" || order.Checkout.ClientConfig.OrderID == "" {
		t.Fatalf("payment order omitted identifiers: %s", raw)
	}
	return order
}

func CreateFundedWallet(t *testing.T, h *HTTP, account, token string) string {
	t.Helper()
	return createFundedWallet(t, h, account, token)
}

func ReserveFixture(t *testing.T, h *HTTP, wallet, token string) string {
	t.Helper()
	return reserveFixture(t, h, wallet, token)
}
func preparePositiveCase(t *testing.T, h *HTTP, prefix string, endpoint Endpoint, org, token string) (Endpoint, any) {
	t.Helper()
	switch prefix {
	case "ENT", "WAL", "RES", "MTR", "INV", "LIM":
		account := createFixtureAccount(t, h, org, token)
		switch prefix {
		case "ENT":
			createFixtureSubscription(t, h, account, token)
			return endpoint, nil
		case "INV":
			return endpoint, nil
		case "LIM":
			return endpoint, nil
		case "WAL":
			return endpoint, map[string]any{"account_id": account, "product_id": fixtureProduct(t, h, "daybook", token)}
		case "RES":
			wallet := createFundedWallet(t, h, account, token)
			endpoint.Path = "/v1/wallets/" + wallet + "/reservations"
			return endpoint, map[string]any{"execution_id": Unique("execution"), "operation_seq": 0, "amount": 10}
		case "MTR":
			wallet := createFundedWallet(t, h, account, token)
			reservation := reserveFixture(t, h, wallet, token)
			return endpoint, map[string]any{"event_id": Unique("usage"), "reservation_id": reservation, "meter": "workflow.execution", "quantity": 1, "application": "daybook"}
		}
	}
	return endpoint, nil
}
func createFixtureAccount(t *testing.T, h *HTTP, org, token string) string {
	t.Helper()
	raw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{"name": "Contract Account", "external_ref": Unique("account")}, token)
	var out struct {
		ID string `json:"id"`
	}
	out = Decode[struct {
		ID string `json:"id"`
	}](t, raw)
	return out.ID
}
func fixtureProduct(t *testing.T, h *HTTP, slug, _ string) string {
	t.Helper()
	raw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog?product="+url.QueryEscape(slug), nil, CatalogueToken(t, h, slug))
	product := Decode[struct {
		Product struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"product"`
	}](t, raw).Product
	if product.ID == "" || product.Slug != slug {
		t.Fatalf("seed product %s not found: %s", slug, raw)
	}
	return product.ID
}

func FixtureCreditPack(t *testing.T, h *HTTP, product string, credits int64, _ string) string {
	t.Helper()
	raw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog?product="+url.QueryEscape(product), nil, CatalogueToken(t, h, product))
	packs := Decode[struct {
		CreditPacks []struct {
			ID      string `json:"id"`
			Credits int64  `json:"credits"`
		} `json:"credit_packs"`
	}](t, raw).CreditPacks
	for _, pack := range packs {
		if pack.Credits == credits {
			return pack.ID
		}
	}
	t.Fatalf("credit pack with %d credits not found for %s", credits, product)
	return ""
}

func FixturePlan(t *testing.T, h *HTTP, product, billingModel string, _ string) string {
	t.Helper()
	raw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog?product="+url.QueryEscape(product), nil, CatalogueToken(t, h, product))
	plans := Decode[struct {
		Plans []struct {
			ID           string `json:"id"`
			BillingModel string `json:"billing_model"`
		} `json:"plans"`
	}](t, raw).Plans
	for _, plan := range plans {
		if plan.BillingModel == billingModel {
			return plan.ID
		}
	}
	t.Fatalf("%s plan not found for %s", billingModel, product)
	return ""
}
func createFixtureSubscription(t *testing.T, h *HTTP, account, token string) string {
	t.Helper()
	_ = account
	catalogue := Decode[struct {
		Plans []struct {
			ID           string `json:"id"`
			BillingModel string `json:"billing_model"`
		} `json:"plans"`
	}](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog?product=daybook", nil, CatalogueToken(t, h, "daybook")))
	var planID string
	for _, plan := range catalogue.Plans {
		if plan.BillingModel == "free" {
			planID = plan.ID
			break
		}
	}
	if planID == "" {
		t.Fatal("seed free plan not found")
	}
	headers := make(http.Header)
	headers.Set("Idempotency-Key", Unique("subscription"))
	status, raw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/subscription-transitions", map[string]any{"plan_id": planID}, token, headers)
	if status != http.StatusCreated {
		t.Fatalf("create subscription transition: want %d, got %d: %s", http.StatusCreated, status, raw)
	}
	result := Decode[struct {
		SubscriptionID *string `json:"subscription_id"`
	}](t, raw)
	if result.SubscriptionID == nil {
		t.Fatal("free transition did not return a subscription_id")
	}
	return *result.SubscriptionID
}
func createFundedWallet(t *testing.T, h *HTTP, account, token string) string {
	t.Helper()
	raw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/wallets", map[string]any{"account_id": account, "product_id": fixtureProduct(t, h, "daybook", token)}, token)
	wallet := Decode[struct{ ID string }](t, raw).ID
	h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/wallets/"+wallet+"/grants", map[string]any{"source": "test", "operation_ref": Unique("grant"), "amount": 100}, token)
	return wallet
}
func reserveFixture(t *testing.T, h *HTTP, wallet, token string) string {
	t.Helper()
	raw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/wallets/"+wallet+"/reservations", map[string]any{"execution_id": Unique("execution"), "operation_seq": 0, "amount": 10}, token)
	return Decode[struct{ ID string }](t, raw).ID
}

func ExerciseAccountContract(t *testing.T, tc PlanCase) {
	t.Helper()
	h := NewHTTP(t)
	org := strings.ToLower(tc.ID) + "-" + Unique("org")
	token := h.IssueToken(t, org, "daybook", allPermissions(), nil)
	created := func() string { return createFixtureAccount(t, h, org, token) }
	switch tc.ID {
	case "ACC-001":
		created()
	case "ACC-002":
		id := created()
		current := Decode[struct {
			ID string `json:"id"`
		}](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/accounts/current", nil, token))
		if current.ID != id {
			t.Fatalf("%s: current account = %s, want %s", tc.ID, current.ID, id)
		}
	case "ACC-003":
		id := created()
		h.RequireStatus(t, http.StatusNotFound, http.MethodPatch, "/v1/accounts/"+id, map[string]any{"name": "Updated Account"}, token)
	case "ACC-005":
		created()
		h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/accounts/current", nil, token)
	case "ACC-006":
		taskmesh := h.IssueToken(t, org, "taskmesh", allPermissions(), nil)
		createFixtureAccount(t, h, org, taskmesh)
		h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/accounts/current", nil, taskmesh)
	case "ACC-007":
		created()
		taskmesh := h.IssueToken(t, org, "taskmesh", allPermissions(), map[string]any{"sub": "customer:" + org})
		createFixtureAccount(t, h, org, taskmesh)
		h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog?product=daybook", nil, token)
		h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog?product=taskmesh", nil, taskmesh)
	case "ACC-008":
		id := created()
		other := h.IssueToken(t, Unique("other"), "daybook", allPermissions(), nil)
		h.RequireStatus(t, http.StatusNotFound, http.MethodPost, "/v1/accounts/"+id+"/links", map[string]any{"application": "taskmesh", "organization_id": Unique("org")}, other)
	case "ACC-009":
		id := created()
		createFixtureSubscription(t, h, id, token)
		repeated := Decode[struct {
			ID string `json:"id"`
		}](t, h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/accounts", map[string]any{"name": "Ignored retry", "external_ref": Unique("retry")}, token))
		if repeated.ID != id {
			t.Fatalf("%s: account retry changed id from %s to %s", tc.ID, id, repeated.ID)
		}
		h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/billing-snapshot", nil, token)
	}
}

func ExercisePlanContract(t *testing.T, tc PlanCase) {
	t.Helper()
	h := NewHTTP(t)
	org := Unique("admin-org")
	token := h.IssueToken(t, org, "daybook", allPermissions(), nil)
	product := fixtureProduct(t, h, "daybook", token)
	createPlan := func(active bool, price int64, interval string) string {
		model := "free"
		if price > 0 {
			model = "paid"
		}
		raw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/products/"+product+"/plans", map[string]any{
			"slug": strings.ToLower(tc.ID) + "-" + Unique("plan"),
			"name": "Contract Plan", "price_minor": price, "currency": "INR",
			"included_credits": 500, "billing_interval": interval, "billing_model": model, "active": active,
			"entitlements": daybookEntitlements(),
		}, token)
		return Decode[struct {
			ID string `json:"id"`
		}](t, raw).ID
	}
	switch tc.ID {
	case "PLAN-001":
		h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/products", map[string]any{"slug": Unique("product"), "name": "Product"}, token)
	case "PLAN-004":
		h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog?product=daybook", nil, token)
	case "PLAN-009":
		limited := h.IssueToken(t, Unique("customer"), "daybook", []string{"billing:read"}, nil)
		h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/admin/products/"+product+"/plans", map[string]any{"slug": Unique("forbidden"), "name": "Forbidden"}, limited)
	case "PLAN-010":
		plan := createPlan(true, 1000, "monthly")
		createFixtureAccount(t, h, org, token)
		ActivatePaidSubscription(t, h, "daybook", plan, token)
		detail := Decode[struct {
			Version int64 `json:"version"`
		}](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/plans/"+plan, nil, token))
		h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/plans/"+plan, map[string]any{"price_minor": 2000, "version": detail.Version}, token)
		h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/billing-snapshot", nil, token)
	case "PLAN-011":
		plan := createPlan(false, 0, "monthly")
		createFixtureAccount(t, h, org, token)
		h.RequireStatusWithHeaders(t, http.StatusBadRequest, http.MethodPost, "/v1/subscription-transitions", map[string]any{"plan_id": plan}, token,
			http.Header{"Idempotency-Key": []string{Unique("inactive-plan")}})
	case "PLAN-012":
		h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/credit-packs", map[string]any{"product_id": product, "slug": Unique("pack"), "name": "Top-up", "credits": 250, "price_minor": 25000, "currency": "INR", "validity_days": 90}, token)
	default:
		interval := "monthly"
		price := int64(0)
		if tc.ID == "PLAN-003" {
			interval = "annual"
			price = 1000
		}
		createPlan(true, price, interval)
	}
}

func daybookEntitlements() map[string]any {
	return map[string]any{
		"branches": 1, "users": 1, "serviceusers": 0,
		"workflow_execution": true, "standalone_workflow": false,
	}
}

func ExerciseAuthContract(t *testing.T, tc PlanCase) {
	t.Helper()
	h := NewHTTP(t)
	org := Unique("auth-org")
	path := "/v1/catalog"
	permissions := allPermissions()
	overrides := map[string]any{}
	token := ""
	switch tc.ID {
	case "AUTH-017":
		token = h.IssueToken(t, org, "daybook", permissions, map[string]any{"token_use": "id", "audience": "billmesh-browser-client"})
	case "AUTH-018":
		token = h.IssueToken(t, org, "daybook", nil, map[string]any{"client_id": "daybook-catalogue-test"})
		status, raw, _ := h.JSON(t, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack_id": "00000000-0000-0000-0000-000000000001"}, token)
		if status != http.StatusForbidden {
			t.Fatalf("%s: want 403, got %d: %s", tc.ID, status, raw)
		}
		return
	case "AUTH-019":
		owner := h.IssueToken(t, org, "daybook", permissions, nil)
		account := createFixtureAccount(t, h, org, owner)
		wallet := createFundedWallet(t, h, account, owner)
		subscription := createFixtureSubscription(t, h, account, owner)
		reservation := reserveFixture(t, h, wallet, owner)
		raw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts/"+account+"/installations", map[string]any{"application": "taskmesh", "organization_id": org}, owner)
		installation := Decode[struct {
			ID string `json:"id"`
		}](t, raw).ID
		other := h.IssueToken(t, Unique("other-org"), "daybook", permissions, nil)
		h.RequireStatus(t, http.StatusNotFound, http.MethodGet, "/v1/accounts/current", nil, other)
		h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/wallets/"+wallet, nil, other)
		h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/wallets/"+wallet+"/reservations", map[string]any{"execution_id": Unique("nested"), "operation_seq": 0, "amount": 1}, other)
		h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/reservations/"+reservation+"/settle", map[string]any{"actual": 1}, other)
		_ = subscription
		h.RequireStatusWithHeaders(t, http.StatusNotFound, http.MethodPost, "/v1/subscriptions/current/cancellation", map[string]any{"effective": "immediate"}, other,
			http.Header{"Idempotency-Key": []string{Unique("foreign-cancel")}})
		h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/installations/"+installation+"/revoke", nil, other)
		return
	case "AUTH-002":
	case "AUTH-003":
		overrides["expires_in_seconds"] = -60
		token = h.IssueToken(t, org, "daybook", permissions, overrides)
	case "AUTH-004":
		unknown := h.IssueToken(t, org, "daybook", permissions, map[string]any{"unknown_key": true})
		h.RequireStatus(t, http.StatusUnauthorized, http.MethodGet, path, nil, unknown)
		token = tamperJWTPayload(t, h.IssueToken(t, org, "daybook", []string{"billing:read"}, nil), map[string]any{"client_id": "billmesh-global-admin-test"})
	case "AUTH-005":
		overrides["issuer"] = "https://untrusted.invalid"
		token = h.IssueToken(t, org, "daybook", permissions, overrides)
	case "AUTH-006":
		overrides["audience"] = "wrong-audience"
		token = h.IssueToken(t, org, "daybook", permissions, overrides)
	case "AUTH-009":
		service := h.IssueToken(t, "", "daybook", []string{"catalogue:read"}, map[string]any{"sub": "service:catalogue"})
		h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog", nil, service)
		h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/admin/audit", nil, service)
		owner := h.IssueToken(t, org, "daybook", allPermissions(), nil)
		account := createFixtureAccount(t, h, org, owner)
		wallet := createFundedWallet(t, h, account, owner)
		runtime := h.IssueToken(t, org, "daybook", []string{"credits:reserve", "credits:settle"}, map[string]any{"sub": "service:runtime"})
		reservationRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/wallets/"+wallet+"/reservations", map[string]any{"execution_id": Unique("service-execution"), "amount": 1}, runtime)
		reservation := Decode[struct {
			ID string `json:"id"`
		}](t, reservationRaw).ID
		h.RequireStatus(t, http.StatusOK, http.MethodPost, "/v1/reservations/"+reservation+"/settle", map[string]any{"actual": 1}, runtime)
		return
	case "AUTH-010":
		service := h.IssueToken(t, org, "daybook", []string{"credits:reserve"}, map[string]any{"sub": "service:runtime"})
		account := createFixtureAccount(t, h, org, h.IssueToken(t, org, "daybook", permissions, nil))
		wallet := createFundedWallet(t, h, account, h.IssueToken(t, org, "daybook", permissions, nil))
		h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack_id": "00000000-0000-0000-0000-000000000001"}, service)
		h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/admin/adjustments", map[string]any{"wallet_id": wallet, "amount": 1, "reason": "forbidden"}, service)
		return
	case "AUTH-013":
		token = h.IssueToken(t, org, "daybook", []string{"billing:read"}, nil)
		path = "/v1/admin/audit"
	case "AUTH-008":
		oldToken := h.IssueToken(t, org, "daybook", permissions, nil)
		h.RequireStatus(t, 200, http.MethodGet, path, nil, oldToken)
		resp, err := h.Client.Post(h.MockURL+"/test/rotate-key", "application/json", bytes.NewReader(nil))
		if err != nil {
			t.Fatalf("rotate signing key: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("rotate signing key: status %d", resp.StatusCode)
		}
		token = h.IssueToken(t, org, "daybook", permissions, nil)
	case "AUTH-011":
		owner := h.IssueToken(t, org, "daybook", permissions, nil)
		account := createFixtureAccount(t, h, org, owner)
		other := h.IssueToken(t, Unique("other-org"), "daybook", permissions, nil)
		_ = account
		h.RequireStatus(t, http.StatusNotFound, http.MethodGet, "/v1/accounts/current", nil, other)
		return
	case "AUTH-012":
		owner := h.IssueToken(t, org, "daybook", permissions, nil)
		account := createFixtureAccount(t, h, org, owner)
		wallet := createFundedWallet(t, h, account, owner)
		other := h.IssueToken(t, Unique("other-org"), "daybook", permissions, nil)
		h.RequireStatus(t, 403, http.MethodPost, "/v1/wallets/"+wallet+"/grants", map[string]any{"source": "test", "operation_ref": Unique("grant"), "amount": 1}, other)
		return
	case "AUTH-016":
		staging := h.IssueToken(t, org, "daybook", permissions, map[string]any{"environment": "staging"})
		account := createFixtureAccount(t, h, org, staging)
		wallet := createFundedWallet(t, h, account, staging)
		subscription := createFixtureSubscription(t, h, account, staging)
		production := h.IssueToken(t, org, "daybook", permissions, map[string]any{"environment": "production"})
		h.RequireStatus(t, http.StatusNotFound, http.MethodGet, "/v1/accounts/current", nil, production)
		h.RequireStatus(t, 403, http.MethodGet, "/v1/wallets/"+wallet, nil, production)
		h.RequireStatus(t, 403, http.MethodPost, "/v1/wallets/"+wallet+"/grants", map[string]any{"source": "test", "operation_ref": Unique("cross-env"), "amount": 1}, production)
		_ = subscription
		h.RequireStatusWithHeaders(t, http.StatusNotFound, http.MethodPost, "/v1/subscriptions/current/cancellation", map[string]any{"effective": "immediate"}, production,
			http.Header{"Idempotency-Key": []string{Unique("cross-env-cancel")}})
		h.RequireStatus(t, http.StatusNotFound, http.MethodPost, "/v1/webhooks", map[string]any{"target_url": h.MockURL + "/receivers/daybook", "secret": "cross-environment-secret-0123456789"}, production)
		for _, list := range []string{"/v1/payments", "/v1/invoices", "/v1/webhooks", "/v1/usage-events", "/v1/admin/audit"} {
			h.RequireStatus(t, http.StatusNotFound, http.MethodGet, list, nil, production)
		}
		h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/events", nil, production)
		withoutLinkPermission := h.IssueToken(t, org, "daybook", []string{"billing:write", "billing:read"}, map[string]any{"environment": "staging"})
		h.RequireStatus(t, http.StatusNotFound, http.MethodPost, "/v1/accounts/"+account+"/links", map[string]any{}, withoutLinkPermission)
		raw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/accounts/current", nil, staging)
		if !strings.Contains(string(raw), account) {
			t.Fatalf("%s: staging owner could not read its own account: %s", tc.ID, raw)
		}
		return
	case "AUTH-014":
		billing := h.IssueToken(t, org, "daybook", []string{"billing:read", "billing:write"}, nil)
		account := createFixtureAccount(t, h, org, billing)
		h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/accounts/current", nil, billing)
		h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/payments", nil, billing)
		h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/invoices", nil, billing)
		h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/catalog", nil, billing)
		h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/executions/authorize", map[string]any{"execution_id": Unique("wrong-client"), "credits": 1}, billing)
		h.RequireStatus(t, http.StatusForbidden, http.MethodGet, "/v1/admin/audit", nil, billing)
		_ = account
		return
	case "AUTH-015":
		admin := h.IssueToken(t, org, "daybook", permissions, nil)
		account := createFixtureAccount(t, h, org, admin)
		product := fixtureProduct(t, h, "daybook", admin)
		subscription := createFixtureSubscription(t, h, account, admin)
		runtime := h.IssueToken(t, org, "daybook", []string{"credits:reserve", "credits:settle"}, nil)
		h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/admin/products/"+product+"/plans", map[string]any{"slug": Unique("runtime-plan"), "name": "Runtime Plan"}, runtime)
		h.RequireStatusWithHeaders(t, http.StatusForbidden, http.MethodPost, "/v1/subscription-transitions", map[string]any{"plan_id": FixturePlan(t, h, "daybook", "paid", admin)}, runtime,
			http.Header{"Idempotency-Key": []string{Unique("runtime-transition")}})
		h.RequireStatusWithHeaders(t, http.StatusForbidden, http.MethodPost, "/v1/subscriptions/current/cancellation", map[string]any{"effective": "immediate"}, runtime,
			http.Header{"Idempotency-Key": []string{Unique("runtime-cancel")}})
		_ = subscription
		return
	default:
		token = h.IssueToken(t, org, "daybook", permissions, nil)
	}
	status, raw, _ := h.JSON(t, http.MethodGet, path, nil, token)
	if tc.ID == "AUTH-008" {
		deadline := time.Now().Add(2 * time.Second)
		for status == http.StatusUnauthorized && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
			status, raw, _ = h.JSON(t, http.MethodGet, path, nil, token)
		}
	}
	want := 200
	if tc.ID == "AUTH-002" || tc.ID == "AUTH-003" || tc.ID == "AUTH-004" || tc.ID == "AUTH-005" || tc.ID == "AUTH-006" || tc.ID == "AUTH-017" {
		want = 401
	}
	if tc.ID == "AUTH-010" || tc.ID == "AUTH-013" || tc.ID == "AUTH-015" {
		want = 403
	}
	if status != want {
		t.Fatalf("%s: want %d, got %d: %s", tc.ID, want, status, raw)
	}
}

func ExerciseAdminContract(t *testing.T, tc PlanCase) {
	t.Helper()
	h := NewHTTP(t)
	org := Unique("admin-org")
	admin := h.IssueToken(t, org, "daybook", allPermissions(), nil)
	switch tc.ID {
	case "ADM-001":
		product := fixtureProduct(t, h, "daybook", admin)
		raw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/products/"+product+"/plans", map[string]any{
			"slug": Unique("admin-plan"), "name": "Admin Plan", "billing_model": "paid", "price_minor": 100,
			"currency": "INR", "included_credits": 10, "billing_interval": "monthly", "entitlements": daybookEntitlements(),
		}, admin)
		plan := Decode[struct {
			ID string `json:"id"`
		}](t, raw).ID
		detail := Decode[struct {
			Version int64 `json:"version"`
		}](t, h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/admin/plans/"+plan, nil, admin))
		h.RequireStatus(t, http.StatusOK, http.MethodPatch, "/v1/admin/plans/"+plan, map[string]any{"price_minor": 200, "version": detail.Version}, admin)
	case "ADM-002":
		account := createFixtureAccount(t, h, org, admin)
		createFixtureSubscription(t, h, account, admin)
		h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/billing-snapshot", nil, admin)
	case "ADM-003", "ADM-004":
		account := createFixtureAccount(t, h, org, admin)
		wallet := createFundedWallet(t, h, account, admin)
		reason := "support correction"
		want := http.StatusCreated
		if tc.ID == "ADM-004" {
			reason = ""
			want = http.StatusBadRequest
		}
		h.RequireStatus(t, want, http.MethodPost, "/v1/admin/adjustments", map[string]any{"wallet_id": wallet, "amount": 25, "reason": reason}, admin)
	case "ADM-006":
		createFixtureAccount(t, h, org, admin)
		h.RequireStatus(t, 200, http.MethodGet, "/v1/payments", nil, admin)
	case "ADM-007":
		createFixtureAccount(t, h, org, admin)
		h.RequireStatus(t, 200, http.MethodGet, "/v1/webhooks", nil, admin)
	case "ADM-008":
		viewer := h.IssueToken(t, org, "daybook", []string{"billing:read"}, nil)
		h.RequireStatus(t, 403, http.MethodPost, "/v1/admin/webhooks/00000000-0000-0000-0000-000000000001/replay", nil, viewer)
	case "ADM-010":
		viewer := h.IssueToken(t, org, "daybook", []string{"billing:read"}, nil)
		h.RequireStatus(t, 403, http.MethodPost, "/v1/admin/adjustments", map[string]any{"wallet_id": "00000000-0000-0000-0000-000000000001", "amount": 1, "reason": "forbidden"}, viewer)
	default:
		createFixtureAccount(t, h, org, admin)
		h.RequireStatus(t, 200, http.MethodGet, "/v1/admin/audit", nil, admin)
	}
}

func ExerciseSSEContract(t *testing.T, tc PlanCase) {
	t.Helper()
	if ExerciseReviewE2ECase(t, tc) {
		return
	}
	h := NewHTTP(t)
	description := strings.ToLower(tc.Description)
	unauthenticated := strings.Contains(description, "unauthenticated")
	token := h.Token
	account := ""
	if unauthenticated {
		token = ""
	} else if token == "" && h.MockURL != "" {
		org := strings.ToLower(tc.ID) + "-" + Unique("sse-org")
		token = h.IssueToken(t, org, "daybook", allPermissions(), nil)
		account = createFixtureAccount(t, h, org, token)
	}
	req, err := http.NewRequest(http.MethodGet, h.BaseURL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if strings.Contains(description, "resume") || strings.Contains(description, "reconnect") || strings.Contains(description, "missed") {
		if account == "" {
			t.Fatal("SSE replay case needs a fixture account")
		}
		subscription := createFixtureSubscription(t, h, account, token)
		pool, err := pgxpool.New(context.Background(), os.Getenv("BILLMESH_E2E_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		var sequence int64
		if err := pool.QueryRow(context.Background(), `SELECT sequence FROM outbox_events WHERE aggregate_type='subscription' AND aggregate_id=$1 ORDER BY sequence DESC LIMIT 1`, subscription).Scan(&sequence); err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Last-Event-ID", fmt.Sprint(sequence))
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		t.Fatalf("%s: connect SSE: %v", tc.ID, err)
	}
	resp.Body.Close()
	if token == "" {
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: want 401, got %d", tc.ID, resp.StatusCode)
		}
		return
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: want 200, got %d", tc.ID, resp.StatusCode)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("%s: unexpected content type %q", tc.ID, resp.Header.Get("Content-Type"))
	}
}
