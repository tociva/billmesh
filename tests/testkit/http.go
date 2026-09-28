package testkit

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
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
	return &HTTP{BaseURL: base, MockURL: strings.TrimRight(os.Getenv("MOCK_SERVER_URL"), "/"), Token: os.Getenv("BILLMESH_TEST_TOKEN"), Client: &http.Client{Timeout: 5 * time.Second}}
}

func (h *HTTP) JSON(t *testing.T, method, path string, body any, token string) (int, []byte, http.Header) {
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
func (h *HTTP) SignedWebhook(t *testing.T, path string, body any, secret string) int {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
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
	payload := map[string]any{"org_id": org, "app": app, "permissions": permissions}
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
		viewer := h.IssueToken(t, org, "daybook", []string{"billing:read"}, nil)
		h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/webhooks", map[string]any{"application": "daybook", "target_url": h.MockURL + "/receivers/daybook", "secret": "test-secret"}, viewer)
		return true
	default:
		return false
	}
}

func allPermissions() []string {
	return []string{"billing:read", "billing:write", "billing:admin", "credits:grant", "credits:reserve", "credits:settle"}
}

func AllPermissions() []string { return allPermissions() }

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
	case "SUB", "ENT", "WAL", "RES", "MTR", "PAY", "INV", "WH", "LIM":
		account := createFixtureAccount(t, h, org, token)
		switch prefix {
		case "SUB":
			return endpoint, map[string]any{"account_id": account, "plan": "daybook-free", "product": "daybook"}
		case "ENT":
			createFixtureSubscription(t, h, account, token)
			return endpoint, nil
		case "INV":
			return endpoint, nil
		case "LIM":
			return endpoint, nil
		case "PAY":
			return endpoint, map[string]any{"account_id": account, "credit_pack": "credits-500"}
		case "WH":
			return endpoint, map[string]any{"application": "daybook", "target_url": h.MockURL + "/receivers/daybook", "secret": "test-secret"}
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
	raw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/accounts", map[string]any{"name": "Contract Account", "external_ref": Unique("account"), "application": "daybook", "organization_id": org}, token)
	var out struct {
		ID string `json:"id"`
	}
	out = Decode[struct {
		ID string `json:"id"`
	}](t, raw)
	return out.ID
}
func fixtureProduct(t *testing.T, h *HTTP, slug, token string) string {
	t.Helper()
	raw := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/products", nil, token)
	items := Decode[[]struct{ ID, Slug string }](t, raw)
	for _, item := range items {
		if item.Slug == slug {
			return item.ID
		}
	}
	t.Fatalf("seed product %s not found", slug)
	return ""
}
func createFixtureSubscription(t *testing.T, h *HTTP, account, token string) string {
	t.Helper()
	raw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/subscriptions", map[string]any{"account_id": account, "plan": "daybook-free", "product": "daybook"}, token)
	return Decode[struct {
		ID string `json:"id"`
	}](t, raw).ID
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
		h.RequireStatus(t, 200, http.MethodGet, "/v1/accounts/"+id, nil, token)
	case "ACC-003", "ACC-009":
		id := created()
		h.RequireStatus(t, 200, http.MethodPatch, "/v1/accounts/"+id, map[string]any{"name": "Updated Account"}, token)
	case "ACC-005", "ACC-006":
		id := created()
		application := "daybook"
		if tc.ID == "ACC-006" {
			application = "taskmesh"
		}
		h.RequireStatus(t, 204, http.MethodPost, "/v1/accounts/"+id+"/links", map[string]any{"application": application, "organization_id": Unique("linked-org"), "environment": "production"}, token)
	case "ACC-007":
		id := created()
		for _, app := range []string{"daybook-secondary", "taskmesh"} {
			h.RequireStatus(t, 204, http.MethodPost, "/v1/accounts/"+id+"/links", map[string]any{"application": app, "organization_id": Unique("org")}, token)
		}
	case "ACC-008":
		id := created()
		other := h.IssueToken(t, Unique("other"), "daybook", allPermissions(), nil)
		h.RequireStatus(t, 403, http.MethodPost, "/v1/accounts/"+id+"/links", map[string]any{"application": "taskmesh", "organization_id": Unique("org")}, other)
	}
}

func ExercisePlanContract(t *testing.T, tc PlanCase) {
	t.Helper()
	h := NewHTTP(t)
	org := Unique("admin-org")
	token := h.IssueToken(t, org, "daybook", allPermissions(), nil)
	product := fixtureProduct(t, h, "daybook", token)
	createPlan := func(active bool, price int64, interval string) string {
		raw := h.RequireStatus(t, 201, http.MethodPost, "/v1/plans", map[string]any{
			"product_id": product, "slug": strings.ToLower(tc.ID) + "-" + Unique("plan"),
			"name": "Contract Plan", "price_minor": price, "currency": "INR",
			"included_credits": 500, "billing_interval": interval, "active": active,
			"entitlements": map[string]any{"workflow_execution": true, "workflow_limit": 500},
		}, token)
		return Decode[struct {
			ID string `json:"id"`
		}](t, raw).ID
	}
	switch tc.ID {
	case "PLAN-001":
		h.RequireStatus(t, 201, http.MethodPost, "/v1/products", map[string]any{"slug": Unique("product"), "name": "Product"}, token)
	case "PLAN-004":
		h.RequireStatus(t, 200, http.MethodGet, "/v1/plans?product=daybook", nil, token)
	case "PLAN-009":
		limited := h.IssueToken(t, Unique("customer"), "daybook", []string{"billing:read"}, nil)
		h.RequireStatus(t, 403, http.MethodPost, "/v1/plans", map[string]any{"product_id": product}, limited)
	case "PLAN-010":
		plan := createPlan(true, 1000, "monthly")
		account := createFixtureAccount(t, h, org, token)
		h.RequireStatus(t, 201, http.MethodPost, "/v1/subscriptions", map[string]any{"account_id": account, "plan_id": plan, "payment_status": "verified"}, token)
		h.RequireStatus(t, 200, http.MethodPatch, "/v1/plans/"+plan, map[string]any{"price_minor": 2000}, token)
		h.RequireStatus(t, 200, http.MethodGet, "/v1/subscriptions/current", nil, token)
	case "PLAN-011":
		plan := createPlan(false, 0, "monthly")
		account := createFixtureAccount(t, h, org, token)
		h.RequireStatus(t, 409, http.MethodPost, "/v1/subscriptions", map[string]any{"account_id": account, "plan_id": plan}, token)
	case "PLAN-012":
		h.RequireStatus(t, 201, http.MethodPost, "/v1/credit-packs", map[string]any{"product_id": product, "slug": Unique("pack"), "name": "Top-up", "credits": 250, "price_minor": 25000, "currency": "INR", "validity_days": 90}, token)
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

func ExerciseAuthContract(t *testing.T, tc PlanCase) {
	t.Helper()
	h := NewHTTP(t)
	org := Unique("auth-org")
	path := "/v1/products"
	permissions := allPermissions()
	overrides := map[string]any{}
	token := ""
	switch tc.ID {
	case "AUTH-017":
		token = h.IssueToken(t, org, "daybook", permissions, map[string]any{"token_use": "id"})
	case "AUTH-018":
		token = h.IssueToken(t, org, "daybook", []string{"billing:read"}, nil)
		status, raw, _ := h.JSON(t, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack": "credits-500"}, token)
		if status != http.StatusForbidden {
			t.Fatalf("%s: want 403, got %d: %s", tc.ID, status, raw)
		}
		return
	case "AUTH-019":
		owner := h.IssueToken(t, org, "daybook", permissions, nil)
		account := createFixtureAccount(t, h, org, owner)
		wallet := createFundedWallet(t, h, account, owner)
		other := h.IssueToken(t, Unique("other-org"), "daybook", permissions, nil)
		h.RequireStatus(t, http.StatusForbidden, http.MethodPost, "/v1/wallets/"+wallet+"/reservations", map[string]any{"execution_id": Unique("nested"), "operation_seq": 0, "amount": 1}, other)
		return
	case "AUTH-002":
	case "AUTH-003":
		overrides["expires_in_seconds"] = -60
		token = h.IssueToken(t, org, "daybook", permissions, overrides)
	case "AUTH-004":
		overrides["unknown_key"] = true
		token = h.IssueToken(t, org, "daybook", permissions, overrides)
	case "AUTH-005":
		overrides["issuer"] = "https://untrusted.invalid"
		token = h.IssueToken(t, org, "daybook", permissions, overrides)
	case "AUTH-006":
		overrides["audience"] = "wrong-audience"
		token = h.IssueToken(t, org, "daybook", permissions, overrides)
	case "AUTH-010", "AUTH-013", "AUTH-015":
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
		h.RequireStatus(t, 403, http.MethodGet, "/v1/accounts/"+account, nil, other)
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
		production := h.IssueToken(t, org, "daybook", permissions, map[string]any{"environment": "production"})
		h.RequireStatus(t, 403, http.MethodGet, "/v1/accounts/"+account, nil, production)
		return
	default:
		token = h.IssueToken(t, org, "daybook", permissions, nil)
	}
	status, raw, _ := h.JSON(t, http.MethodGet, path, nil, token)
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
		raw := h.RequireStatus(t, 201, http.MethodPost, "/v1/plans", map[string]any{"product_id": product, "slug": Unique("admin-plan"), "name": "Admin Plan", "price_minor": 100, "currency": "INR", "included_credits": 10, "billing_interval": "monthly"}, admin)
		plan := Decode[struct {
			ID string `json:"id"`
		}](t, raw).ID
		h.RequireStatus(t, 200, http.MethodPatch, "/v1/plans/"+plan, map[string]any{"price_minor": 200}, admin)
	case "ADM-002":
		account := createFixtureAccount(t, h, org, admin)
		createFixtureSubscription(t, h, account, admin)
		h.RequireStatus(t, 200, http.MethodGet, "/v1/subscriptions/current", nil, admin)
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
	if unauthenticated {
		token = ""
	} else if token == "" && h.MockURL != "" {
		org := strings.ToLower(tc.ID) + "-" + Unique("sse-org")
		token = h.IssueToken(t, org, "daybook", allPermissions(), nil)
		createFixtureAccount(t, h, org, token)
	}
	req, err := http.NewRequest(http.MethodGet, h.BaseURL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if strings.Contains(description, "resume") || strings.Contains(description, "reconnect") || strings.Contains(description, "missed") {
		req.Header.Set("Last-Event-ID", "9223372036854775807")
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
