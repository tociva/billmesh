//go:build e2e

package payments_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

type providerOrder struct {
	ID       string            `json:"id"`
	Entity   string            `json:"entity"`
	Amount   int64             `json:"amount"`
	Currency string            `json:"currency"`
	Receipt  string            `json:"receipt"`
	Status   string            `json:"status"`
	Notes    map[string]string `json:"notes"`
}

type paymentCheckout struct {
	PaymentID string `json:"payment_id"`
	Credits   int64  `json:"credits"`
	Checkout  struct {
		ClientConfig struct {
			PublicKey   string `json:"public_key"`
			OrderID     string `json:"order_id"`
			AmountMinor int64  `json:"amount_minor"`
			Currency    string `json:"currency"`
		} `json:"client_config"`
	} `json:"checkout"`
}

func TestRazorpayCreditPackOrderUsesServerValuesAndProviderContract(t *testing.T) {
	h := testkit.NewHTTP(t)
	setRazorpayBehavior(t, h, map[string]any{})
	t.Cleanup(func() { setRazorpayBehavior(t, h, map[string]any{}) })
	org := testkit.Unique("order-contract")
	token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	accountID := testkit.CreateFixtureAccount(t, h, org, token)
	packID := testkit.FixtureCreditPack(t, h, "daybook", 500, token)

	pool := e2ePool(t)
	var expectedPrice, expectedCredits int64
	var expectedCurrency string
	if err := pool.QueryRow(context.Background(), `SELECT price_minor,credits,currency FROM credit_packs WHERE id=$1`, packID).
		Scan(&expectedPrice, &expectedCredits, &expectedCurrency); err != nil {
		t.Fatal(err)
	}
	key := testkit.Unique("order-contract")
	raw := h.RequireStatusWithHeaders(t, http.StatusCreated, http.MethodPost, "/v1/payments/orders",
		map[string]any{"credit_pack_id": packID}, token, http.Header{"Idempotency-Key": []string{key}})
	checkout := testkit.Decode[paymentCheckout](t, raw)
	if checkout.Credits != expectedCredits || checkout.Checkout.ClientConfig.AmountMinor != expectedPrice || checkout.Checkout.ClientConfig.Currency != expectedCurrency {
		t.Fatalf("checkout did not use stored pack values: %s", raw)
	}
	if checkout.Checkout.ClientConfig.PublicKey != "rzp_test_billmesh" || strings.Contains(string(raw), "test-provider-secret") || strings.Contains(string(raw), "test-webhook-secret") {
		t.Fatalf("checkout exposed incorrect credential material: %s", raw)
	}

	order := findProviderOrder(t, h, checkout.Checkout.ClientConfig.OrderID)
	operationRef := "credit-pack:" + accountID + ":" + key
	if order.Entity != "order" || order.Amount != expectedPrice || order.Currency != expectedCurrency || order.Status != "created" {
		t.Fatalf("provider order = %+v", order)
	}
	if order.Receipt == "" || len(order.Receipt) > 40 || order.Notes["billmesh_payment_id"] != checkout.PaymentID ||
		order.Notes["billmesh_account_id"] != accountID || order.Notes["billmesh_environment"] != "test" ||
		order.Notes["billmesh_operation_ref"] != operationRef {
		t.Fatalf("provider correlation fields = receipt=%q notes=%+v", order.Receipt, order.Notes)
	}
}

func TestRazorpayPaidTransitionUsesStoredPlanPrice(t *testing.T) {
	h := testkit.NewHTTP(t)
	setRazorpayBehavior(t, h, map[string]any{})
	org := testkit.Unique("transition-order")
	token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	accountID := testkit.CreateFixtureAccount(t, h, org, token)
	catalogue := h.RequireStatus(t, http.StatusOK, http.MethodGet, "/v1/catalog?product=daybook", nil, testkit.CatalogueToken(t, h, "daybook"))
	var decoded struct {
		Plans []struct {
			ID           string `json:"id"`
			BillingModel string `json:"billing_model"`
		} `json:"plans"`
	}
	decoded = testkit.Decode[struct {
		Plans []struct {
			ID           string `json:"id"`
			BillingModel string `json:"billing_model"`
		} `json:"plans"`
	}](t, catalogue)
	var planID string
	for _, plan := range decoded.Plans {
		if plan.BillingModel == "paid" {
			planID = plan.ID
			break
		}
	}
	if planID == "" {
		t.Fatal("paid plan fixture is missing")
	}
	pool := e2ePool(t)
	var price int64
	var currency string
	if err := pool.QueryRow(context.Background(), `SELECT price_minor,currency FROM plans WHERE id=$1`, planID).Scan(&price, &currency); err != nil {
		t.Fatal(err)
	}
	key := testkit.Unique("transition-order")
	raw := h.RequireStatusWithHeaders(t, http.StatusCreated, http.MethodPost, "/v1/subscription-transitions",
		map[string]any{"plan_id": planID}, token, http.Header{"Idempotency-Key": []string{key}})
	var transition struct {
		ID       string `json:"id"`
		Checkout struct {
			ClientConfig struct {
				OrderID     string `json:"order_id"`
				AmountMinor int64  `json:"amount_minor"`
				Currency    string `json:"currency"`
			} `json:"client_config"`
		} `json:"checkout"`
	}
	transition = testkit.Decode[struct {
		ID       string `json:"id"`
		Checkout struct {
			ClientConfig struct {
				OrderID     string `json:"order_id"`
				AmountMinor int64  `json:"amount_minor"`
				Currency    string `json:"currency"`
			} `json:"client_config"`
		} `json:"checkout"`
	}](t, raw)
	if transition.Checkout.ClientConfig.AmountMinor != price || transition.Checkout.ClientConfig.Currency != currency {
		t.Fatalf("transition checkout did not use stored plan price: %s", raw)
	}
	order := findProviderOrder(t, h, transition.Checkout.ClientConfig.OrderID)
	if order.Amount != price || order.Currency != currency || order.Notes["billmesh_account_id"] != accountID ||
		order.Notes["billmesh_operation_ref"] != "transition:"+transition.ID {
		t.Fatalf("transition provider order = %+v", order)
	}
}

func TestRazorpayOrderIdempotencyConflictAndConcurrency(t *testing.T) {
	h := testkit.NewHTTP(t)
	setRazorpayBehavior(t, h, map[string]any{})
	org := testkit.Unique("order-idempotency")
	token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	accountID := testkit.CreateFixtureAccount(t, h, org, token)
	packID := testkit.FixtureCreditPack(t, h, "daybook", 500, token)
	productID := testkit.FixtureProduct(t, h, "daybook", token)
	otherPackRaw := h.RequireStatus(t, http.StatusCreated, http.MethodPost, "/v1/admin/credit-packs", map[string]any{
		"product_id": productID, "slug": testkit.Unique("idempotency-pack"), "name": "Idempotency Pack",
		"credits": 501, "price_minor": 50000, "currency": "INR",
	}, token)
	otherPackID := testkit.Decode[struct {
		ID string `json:"id"`
	}](t, otherPackRaw).ID
	if otherPackID == "" {
		t.Fatalf("created credit pack omitted id: %s", otherPackRaw)
	}
	key := testkit.Unique("same-order")
	headers := http.Header{"Idempotency-Key": []string{key}}
	firstRaw := h.RequireStatusWithHeaders(t, http.StatusCreated, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack_id": packID}, token, headers)
	first := testkit.Decode[paymentCheckout](t, firstRaw)
	secondRaw := h.RequireStatusWithHeaders(t, http.StatusOK, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack_id": packID}, token, headers)
	second := testkit.Decode[paymentCheckout](t, secondRaw)
	if first.PaymentID != second.PaymentID || first.Checkout.ClientConfig.OrderID != second.Checkout.ClientConfig.OrderID {
		t.Fatalf("idempotent replay changed identifiers: first=%+v second=%+v", first, second)
	}
	h.RequireStatusWithHeaders(t, http.StatusConflict, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack_id": otherPackID}, token, headers)

	concurrentKey := testkit.Unique("concurrent-order")
	const requests = 8
	results := make(chan paymentCheckout, requests)
	errors := make(chan string, requests)
	var group sync.WaitGroup
	for index := 0; index < requests; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			status, raw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack_id": packID}, token,
				http.Header{"Idempotency-Key": []string{concurrentKey}})
			if status != http.StatusCreated && status != http.StatusOK {
				errors <- string(raw)
				return
			}
			results <- testkit.Decode[paymentCheckout](t, raw)
		}()
	}
	group.Wait()
	close(results)
	close(errors)
	for failure := range errors {
		t.Fatalf("concurrent checkout failed: %s", failure)
	}
	var paymentID, orderID string
	for result := range results {
		if paymentID == "" {
			paymentID, orderID = result.PaymentID, result.Checkout.ClientConfig.OrderID
		}
		if result.PaymentID != paymentID || result.Checkout.ClientConfig.OrderID != orderID {
			t.Fatalf("concurrent checkout produced multiple orders: %+v", result)
		}
	}
	pool := e2ePool(t)
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM payments WHERE operation_ref=$1`, "credit-pack:"+accountID+":"+concurrentKey).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("concurrent checkout persisted %d payments", count)
	}
	providerMatches := 0
	for _, order := range listProviderOrders(t, h) {
		if order.Notes["billmesh_operation_ref"] == "credit-pack:"+accountID+":"+concurrentKey {
			providerMatches++
		}
	}
	if providerMatches != 1 {
		t.Fatalf("concurrent checkout created %d usable provider orders", providerMatches)
	}
}

func TestRazorpayOrderRejectsInvalidResponsesAndRecoversOnRetry(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("invalid-order")
	token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	testkit.CreateFixtureAccount(t, h, org, token)
	packID := testkit.FixtureCreditPack(t, h, "daybook", 500, token)
	behaviors := map[string]map[string]any{
		"missing id":          {"missing_order_id": true},
		"malformed JSON":      {"malformed_json": true},
		"mismatched entity":   {"mismatched_entity": true},
		"mismatched amount":   {"mismatched_amount": true},
		"mismatched currency": {"mismatched_currency": true},
		"mismatched receipt":  {"mismatched_receipt": true},
		"mismatched notes":    {"mismatched_notes": true},
	}
	for name, behavior := range behaviors {
		t.Run(name, func(t *testing.T) {
			setRazorpayBehavior(t, h, behavior)
			key := testkit.Unique("invalid-response")
			headers := http.Header{"Idempotency-Key": []string{key}}
			status, raw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack_id": packID}, token, headers)
			if status != http.StatusBadGateway {
				t.Fatalf("invalid response returned %d: %s", status, raw)
			}
			if strings.Contains(string(raw), "test-provider-secret") {
				t.Fatalf("invalid response exposed provider credentials: %s", raw)
			}
			setRazorpayBehavior(t, h, map[string]any{})
			recovered := h.RequireStatusWithHeaders(t, http.StatusOK, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack_id": packID}, token, headers)
			if testkit.Decode[paymentCheckout](t, recovered).Checkout.ClientConfig.OrderID == "" {
				t.Fatalf("retry did not recover committed provider order: %s", recovered)
			}
		})
	}
}

func TestRazorpayOrderProviderFailuresAndLostResponse(t *testing.T) {
	h := testkit.NewHTTP(t)
	org := testkit.Unique("provider-failures")
	token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	testkit.CreateFixtureAccount(t, h, org, token)
	packID := testkit.FixtureCreditPack(t, h, "daybook", 500, token)
	for _, test := range []struct {
		status int
		want   int
	}{
		{status: 400, want: http.StatusBadGateway},
		{status: 401, want: http.StatusBadGateway},
		{status: 409, want: http.StatusServiceUnavailable},
		{status: 429, want: http.StatusServiceUnavailable},
		{status: 500, want: http.StatusServiceUnavailable},
		{status: 503, want: http.StatusServiceUnavailable},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			setRazorpayBehavior(t, h, map[string]any{"status": test.status})
			status, raw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack_id": packID}, token,
				http.Header{"Idempotency-Key": []string{testkit.Unique("provider-status")}})
			if status != test.want {
				t.Fatalf("provider status %d returned %d: %s", test.status, status, raw)
			}
		})
	}

	setRazorpayBehavior(t, h, map[string]any{"drop_after_commit": true})
	lostResponse := h.RequireStatusWithHeaders(t, http.StatusCreated, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack_id": packID}, token,
		http.Header{"Idempotency-Key": []string{testkit.Unique("lost-response")}})
	if testkit.Decode[paymentCheckout](t, lostResponse).Checkout.ClientConfig.OrderID == "" {
		t.Fatalf("lost provider response was not recovered: %s", lostResponse)
	}
	setRazorpayBehavior(t, h, map[string]any{})
}

func TestRazorpayOrderRecoversWhenLocalAttachFailsAfterProviderCommit(t *testing.T) {
	h := testkit.NewHTTP(t)
	setRazorpayBehavior(t, h, map[string]any{})
	org := testkit.Unique("local-attach-failure")
	token := h.IssueToken(t, org, "daybook", testkit.AllPermissions(), nil)
	accountID := testkit.CreateFixtureAccount(t, h, org, token)
	packID := testkit.FixtureCreditPack(t, h, "daybook", 500, token)
	key := testkit.Unique("local-attach-failure")
	operationRef := "credit-pack:" + accountID + ":" + key
	pool := e2ePool(t)
	cleanup := `
		DROP TRIGGER IF EXISTS test_razorpay_attach_failure ON billmesh.payments;
		DROP FUNCTION IF EXISTS billmesh.test_razorpay_attach_failure();
		DROP SEQUENCE IF EXISTS billmesh.test_razorpay_attach_failure_once;`
	if _, err := pool.Exec(context.Background(), cleanup); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), cleanup) })
	setup := fmt.Sprintf(`
		CREATE SEQUENCE billmesh.test_razorpay_attach_failure_once START WITH 1;
		CREATE FUNCTION billmesh.test_razorpay_attach_failure() RETURNS trigger
		LANGUAGE plpgsql SECURITY DEFINER SET search_path = billmesh, pg_temp AS $body$
		BEGIN
		  IF NEW.operation_ref = TG_ARGV[0] AND OLD.provider_order_id IS NULL AND NEW.provider_order_id IS NOT NULL
		     AND nextval('billmesh.test_razorpay_attach_failure_once') = 1 THEN
		    RAISE EXCEPTION 'injected provider-order attach failure';
		  END IF;
		  RETURN NEW;
		END;
		$body$;
		CREATE TRIGGER test_razorpay_attach_failure BEFORE UPDATE ON billmesh.payments
		FOR EACH ROW EXECUTE FUNCTION billmesh.test_razorpay_attach_failure('%s');`, strings.ReplaceAll(operationRef, "'", "''"))
	if _, err := pool.Exec(context.Background(), setup); err != nil {
		t.Fatal(err)
	}
	headers := http.Header{"Idempotency-Key": []string{key}}
	status, raw, _ := h.JSONWithHeaders(t, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack_id": packID}, token, headers)
	if status != http.StatusInternalServerError {
		t.Fatalf("injected local failure returned %d: %s", status, raw)
	}
	recovered := h.RequireStatusWithHeaders(t, http.StatusOK, http.MethodPost, "/v1/payments/orders", map[string]any{"credit_pack_id": packID}, token, headers)
	checkout := testkit.Decode[paymentCheckout](t, recovered)
	if checkout.Checkout.ClientConfig.OrderID == "" {
		t.Fatalf("retry did not recover the committed provider order: %s", recovered)
	}
	orders := listProviderOrders(t, h)
	matches := 0
	for _, order := range orders {
		if order.Notes["billmesh_operation_ref"] == operationRef {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("local attach retry left %d usable provider orders", matches)
	}
}

func e2ePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("BILLMESH_E2E_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("BILLMESH_E2E_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func setRazorpayBehavior(t *testing.T, h *testkit.HTTP, behavior map[string]any) {
	t.Helper()
	raw, err := json.Marshal(behavior)
	if err != nil {
		t.Fatal(err)
	}
	response, err := h.Client.Post(h.MockURL+"/test/razorpay/behavior", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("set Razorpay behavior: status=%d body=%s", response.StatusCode, body)
	}
}

func findProviderOrder(t *testing.T, h *testkit.HTTP, orderID string) providerOrder {
	t.Helper()
	for _, order := range listProviderOrders(t, h) {
		if order.ID == orderID {
			return order
		}
	}
	t.Fatalf("provider order %s was not found", orderID)
	return providerOrder{}
}

func listProviderOrders(t *testing.T, h *testkit.HTTP) []providerOrder {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, h.MockURL+"/test/razorpay/orders?count=100", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth("rzp_test_billmesh", "test-provider-secret")
	response, err := h.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var collection struct {
		Items []providerOrder `json:"items"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&collection) != nil {
		t.Fatalf("list provider orders returned %d", response.StatusCode)
	}
	return collection.Items
}
