package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	testRazorpayKeyID     = "rzp_test_billmesh"
	testRazorpayKeySecret = "test-provider-secret"
)

func TestRazorpayOrderValidationAndPersistence(t *testing.T) {
	server := httptest.NewServer(newHandler(newFakeServerState()))
	defer server.Close()

	valid := map[string]any{
		"amount": 50000, "currency": "INR", "receipt": "bm_01JTESTPAYMENT000000000000001",
		"notes": map[string]string{"billmesh_operation": "payment-test"},
	}
	response := postRazorpayOrder(t, server.URL, testRazorpayKeyID, testRazorpayKeySecret, valid, "application/json")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("valid order: want 200, got %d: %s", response.StatusCode, readBody(t, response))
	}
	var order fakeRazorpayOrder
	decodeResponse(t, response, &order)
	if order.ID == "" || order.Entity != "order" || order.Status != "created" || order.Amount != 50000 || order.Currency != "INR" || order.Receipt != valid["receipt"] {
		t.Fatalf("unexpected order response: %+v", order)
	}

	response = getRequest(t, server.URL+"/test/razorpay/orders?receipt="+order.Receipt, testRazorpayKeyID, testRazorpayKeySecret)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("fetch persisted order: want 200, got %d: %s", response.StatusCode, readBody(t, response))
	}
	var fetched struct {
		Items []fakeRazorpayOrder `json:"items"`
	}
	decodeResponse(t, response, &fetched)
	if len(fetched.Items) != 1 || fetched.Items[0].ID != order.ID {
		t.Fatalf("persisted orders = %+v, want %s", fetched.Items, order.ID)
	}

	response = postRazorpayOrder(t, server.URL, testRazorpayKeyID, testRazorpayKeySecret, valid, "application/json")
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate receipt: want 400, got %d: %s", response.StatusCode, readBody(t, response))
	}
}

func TestRazorpayOrderRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name, key, secret, contentType string
		body                           map[string]any
		want                           int
	}{
		{name: "missing auth", contentType: "application/json", body: map[string]any{"amount": 100, "currency": "INR", "receipt": "receipt-1"}, want: http.StatusUnauthorized},
		{name: "wrong secret", key: testRazorpayKeyID, secret: "wrong", contentType: "application/json", body: map[string]any{"amount": 100, "currency": "INR", "receipt": "receipt-2"}, want: http.StatusUnauthorized},
		{name: "wrong content type", key: testRazorpayKeyID, secret: testRazorpayKeySecret, contentType: "text/plain", body: map[string]any{"amount": 100, "currency": "INR", "receipt": "receipt-3"}, want: http.StatusUnsupportedMediaType},
		{name: "zero amount", key: testRazorpayKeyID, secret: testRazorpayKeySecret, contentType: "application/json", body: map[string]any{"amount": 0, "currency": "INR", "receipt": "receipt-4"}, want: http.StatusBadRequest},
		{name: "invalid currency", key: testRazorpayKeyID, secret: testRazorpayKeySecret, contentType: "application/json", body: map[string]any{"amount": 100, "currency": "inr", "receipt": "receipt-5"}, want: http.StatusBadRequest},
		{name: "missing receipt", key: testRazorpayKeyID, secret: testRazorpayKeySecret, contentType: "application/json", body: map[string]any{"amount": 100, "currency": "INR"}, want: http.StatusBadRequest},
		{name: "long receipt", key: testRazorpayKeyID, secret: testRazorpayKeySecret, contentType: "application/json", body: map[string]any{"amount": 100, "currency": "INR", "receipt": strings.Repeat("r", 41)}, want: http.StatusBadRequest},
		{name: "non ascii receipt", key: testRazorpayKeyID, secret: testRazorpayKeySecret, contentType: "application/json", body: map[string]any{"amount": 100, "currency": "INR", "receipt": "receipt-💳"}, want: http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(newHandler(newFakeServerState()))
			defer server.Close()
			response := postRazorpayOrder(t, server.URL, test.key, test.secret, test.body, test.contentType)
			if response.StatusCode != test.want {
				t.Fatalf("want %d, got %d: %s", test.want, response.StatusCode, readBody(t, response))
			}
			response.Body.Close()
		})
	}
}

func TestRazorpayProviderBehaviorControls(t *testing.T) {
	server := httptest.NewServer(newHandler(newFakeServerState()))
	defer server.Close()

	for _, status := range []int{400, 401, 409, 429, 500, 503} {
		setRazorpayBehavior(t, server.URL, map[string]any{"status": status})
		response := postRazorpayOrder(t, server.URL, testRazorpayKeyID, testRazorpayKeySecret,
			map[string]any{"amount": 100, "currency": "INR", "receipt": "status-" + http.StatusText(status)}, "application/json")
		if response.StatusCode != status {
			t.Fatalf("configured status: want %d, got %d", status, response.StatusCode)
		}
		response.Body.Close()
	}

	setRazorpayBehavior(t, server.URL, map[string]any{"malformed_json": true})
	response := postRazorpayOrder(t, server.URL, testRazorpayKeyID, testRazorpayKeySecret,
		map[string]any{"amount": 100, "currency": "INR", "receipt": "malformed-response"}, "application/json")
	raw := readBody(t, response)
	if response.StatusCode != http.StatusOK || json.Valid(raw) {
		t.Fatalf("malformed response: status=%d body=%q", response.StatusCode, raw)
	}

	setRazorpayBehavior(t, server.URL, map[string]any{"missing_order_id": true})
	response = postRazorpayOrder(t, server.URL, testRazorpayKeyID, testRazorpayKeySecret,
		map[string]any{"amount": 100, "currency": "INR", "receipt": "missing-id"}, "application/json")
	var order map[string]any
	decodeResponse(t, response, &order)
	if _, exists := order["id"]; exists {
		t.Fatalf("missing-order-id response unexpectedly contained id: %+v", order)
	}

	setRazorpayBehavior(t, server.URL, map[string]any{"delay_ms": 25})
	started := time.Now()
	response = postRazorpayOrder(t, server.URL, testRazorpayKeyID, testRazorpayKeySecret,
		map[string]any{"amount": 100, "currency": "INR", "receipt": "delayed-response"}, "application/json")
	response.Body.Close()
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond {
		t.Fatalf("configured delay was not applied: %s", elapsed)
	}
}

func TestRazorpayConnectionDropAfterOrderCommit(t *testing.T) {
	server := httptest.NewServer(newHandler(newFakeServerState()))
	defer server.Close()
	setRazorpayBehavior(t, server.URL, map[string]any{"drop_after_commit": true})

	raw, err := json.Marshal(map[string]any{"amount": 100, "currency": "INR", "receipt": "lost-response"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/orders", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth(testRazorpayKeyID, testRazorpayKeySecret)
	request.Header.Set("Content-Type", "application/json")
	response, requestErr := http.DefaultClient.Do(request)
	if response != nil {
		response.Body.Close()
	}
	if requestErr == nil {
		t.Fatal("connection-drop behavior unexpectedly returned a complete response")
	}

	response = getRequest(t, server.URL+"/test/razorpay/orders?receipt=lost-response", testRazorpayKeyID, testRazorpayKeySecret)
	var fetched struct {
		Items []fakeRazorpayOrder `json:"items"`
	}
	decodeResponse(t, response, &fetched)
	if len(fetched.Items) != 1 || fetched.Items[0].Receipt != "lost-response" {
		t.Fatalf("committed order was not recoverable after lost response: %+v", fetched.Items)
	}
}

func TestRazorpayOrderPaymentsSupportsMultipleAttempts(t *testing.T) {
	server := httptest.NewServer(newHandler(newFakeServerState()))
	defer server.Close()

	response := postRazorpayOrder(t, server.URL, testRazorpayKeyID, testRazorpayKeySecret,
		map[string]any{"amount": 2500, "currency": "INR", "receipt": "multi-attempt"}, "application/json")
	var order fakeRazorpayOrder
	decodeResponse(t, response, &order)

	addRazorpayPayment(t, server.URL, fakeRazorpayPayment{ID: "pay_failed", OrderID: order.ID, Status: "failed", Amount: 2500, Currency: "INR"})
	addRazorpayPayment(t, server.URL, fakeRazorpayPayment{ID: "pay_captured", OrderID: order.ID, Status: "captured", Amount: 2500, Currency: "INR"})

	response = getRequest(t, server.URL+"/v1/orders/"+order.ID+"/payments", testRazorpayKeyID, testRazorpayKeySecret)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("order payments: want 200, got %d: %s", response.StatusCode, readBody(t, response))
	}
	var collection struct {
		Entity string                `json:"entity"`
		Count  int                   `json:"count"`
		Items  []fakeRazorpayPayment `json:"items"`
	}
	decodeResponse(t, response, &collection)
	if collection.Entity != "collection" || collection.Count != 2 || len(collection.Items) != 2 || collection.Items[1].ID != "pay_captured" {
		t.Fatalf("unexpected payment collection: %+v", collection)
	}
}

func TestRazorpayPaymentRefundsUseProviderCollectionContract(t *testing.T) {
	server := httptest.NewServer(newHandler(newFakeServerState()))
	defer server.Close()
	response := postRazorpayOrder(t, server.URL, testRazorpayKeyID, testRazorpayKeySecret,
		map[string]any{"amount": 2500, "currency": "INR", "receipt": "refund-collection"}, "application/json")
	var order fakeRazorpayOrder
	decodeResponse(t, response, &order)
	addRazorpayPayment(t, server.URL, fakeRazorpayPayment{ID: "pay_refunded", OrderID: order.ID, Status: "captured", Amount: 2500, Currency: "INR"})
	postTestControl(t, server.URL+"/test/razorpay/refunds", fakeRazorpayRefund{
		ID: "rfnd_processed", PaymentID: "pay_refunded", Status: "processed", Amount: 2500, Currency: "INR",
	})

	response = getRequest(t, server.URL+"/v1/payments/pay_refunded/refunds?count=100", testRazorpayKeyID, testRazorpayKeySecret)
	var collection struct {
		Entity string               `json:"entity"`
		Count  int                  `json:"count"`
		Items  []fakeRazorpayRefund `json:"items"`
	}
	decodeResponse(t, response, &collection)
	if collection.Entity != "collection" || collection.Count != 1 || collection.Items[0].Entity != "refund" ||
		collection.Items[0].ID != "rfnd_processed" || collection.Items[0].CreatedAt == 0 {
		t.Fatalf("unexpected refund collection: %+v", collection)
	}
}

func TestRazorpayReadAPIsRequireBasicAuth(t *testing.T) {
	server := httptest.NewServer(newHandler(newFakeServerState()))
	defer server.Close()

	for _, path := range []string{
		"/v1/orders",
		"/v1/orders/order_missing/payments",
		"/v1/payments/pay_missing",
		"/v1/payments/pay_missing/refunds",
	} {
		t.Run(path, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d: %s", response.StatusCode, readBody(t, response))
			}
		})
	}
}

func TestRazorpayReadAPIsUseProviderWireContractsAndPagination(t *testing.T) {
	server := httptest.NewServer(newHandler(newFakeServerState()))
	defer server.Close()

	var orders []fakeRazorpayOrder
	for index, receipt := range []string{"inventory-first", "inventory-second"} {
		response := postRazorpayOrder(t, server.URL, testRazorpayKeyID, testRazorpayKeySecret,
			map[string]any{"amount": int64(2500 + index), "currency": "INR", "receipt": receipt}, "application/json")
		var order fakeRazorpayOrder
		decodeResponse(t, response, &order)
		orders = append(orders, order)
	}
	order := orders[0]
	addRazorpayPayment(t, server.URL, fakeRazorpayPayment{
		ID: "pay_provider_fetch", OrderID: order.ID, Status: "captured", Amount: order.Amount, Currency: order.Currency,
	})
	for index := 1; index <= 2; index++ {
		postTestControl(t, server.URL+"/test/razorpay/refunds", fakeRazorpayRefund{
			ID: "rfnd_provider_" + strconv.Itoa(index), PaymentID: "pay_provider_fetch", Status: "processed",
			Amount: int64(index * 100), Currency: "INR", CreatedAt: int64(index),
		})
	}

	response := getRequest(t, server.URL+"/v1/orders?count=1&skip=1", testRazorpayKeyID, testRazorpayKeySecret)
	var orderPage struct {
		Entity string              `json:"entity"`
		Count  int                 `json:"count"`
		Items  []fakeRazorpayOrder `json:"items"`
	}
	decodeResponse(t, response, &orderPage)
	if orderPage.Entity != "collection" || orderPage.Count != 1 || len(orderPage.Items) != 1 {
		t.Fatalf("unexpected order page: %+v", orderPage)
	}

	response = getRequest(t, server.URL+"/v1/payments/pay_provider_fetch", testRazorpayKeyID, testRazorpayKeySecret)
	var payment fakeRazorpayPayment
	decodeResponse(t, response, &payment)
	if payment.Entity != "payment" || payment.ID != "pay_provider_fetch" || payment.OrderID != order.ID || payment.Status != "captured" {
		t.Fatalf("unexpected payment response: %+v", payment)
	}

	response = getRequest(t, server.URL+"/v1/payments/pay_provider_fetch/refunds?count=1&skip=1", testRazorpayKeyID, testRazorpayKeySecret)
	var refundPage struct {
		Entity string               `json:"entity"`
		Count  int                  `json:"count"`
		Items  []fakeRazorpayRefund `json:"items"`
	}
	decodeResponse(t, response, &refundPage)
	if refundPage.Entity != "collection" || refundPage.Count != 1 || len(refundPage.Items) != 1 || refundPage.Items[0].ID != "rfnd_provider_2" {
		t.Fatalf("unexpected refund page: %+v", refundPage)
	}
}

func TestRazorpayReadAPIsHonorProviderFailureControls(t *testing.T) {
	server := httptest.NewServer(newHandler(newFakeServerState()))
	defer server.Close()
	response := postRazorpayOrder(t, server.URL, testRazorpayKeyID, testRazorpayKeySecret,
		map[string]any{"amount": 2500, "currency": "INR", "receipt": "read-failures"}, "application/json")
	var order fakeRazorpayOrder
	decodeResponse(t, response, &order)
	addRazorpayPayment(t, server.URL, fakeRazorpayPayment{
		ID: "pay_read_failures", OrderID: order.ID, Status: "captured", Amount: order.Amount, Currency: order.Currency,
	})
	paths := []string{
		"/v1/orders",
		"/v1/orders/" + order.ID + "/payments",
		"/v1/payments/pay_read_failures",
		"/v1/payments/pay_read_failures/refunds",
	}

	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusConflict,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	} {
		setRazorpayBehavior(t, server.URL, map[string]any{"status": status})
		for _, path := range paths {
			t.Run(http.StatusText(status)+path, func(t *testing.T) {
				response := getRequest(t, server.URL+path, testRazorpayKeyID, testRazorpayKeySecret)
				if response.StatusCode != status {
					t.Fatalf("want %d, got %d: %s", status, response.StatusCode, readBody(t, response))
				}
				response.Body.Close()
			})
		}
	}

	setRazorpayBehavior(t, server.URL, map[string]any{"malformed_json": true})
	for _, path := range paths {
		t.Run("malformed"+path, func(t *testing.T) {
			response := getRequest(t, server.URL+path, testRazorpayKeyID, testRazorpayKeySecret)
			raw := readBody(t, response)
			if response.StatusCode != http.StatusOK || json.Valid(raw) {
				t.Fatalf("want malformed 200 JSON, got status=%d body=%q", response.StatusCode, raw)
			}
		})
	}

	setRazorpayBehavior(t, server.URL, map[string]any{"delay_ms": 25})
	for _, path := range paths {
		t.Run("delay"+path, func(t *testing.T) {
			started := time.Now()
			response := getRequest(t, server.URL+path, testRazorpayKeyID, testRazorpayKeySecret)
			response.Body.Close()
			if elapsed := time.Since(started); elapsed < 20*time.Millisecond {
				t.Fatalf("configured delay was not applied: %s", elapsed)
			}
		})
	}
}

func postRazorpayOrder(t *testing.T, baseURL, key, secret string, body map[string]any, contentType string) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/v1/orders", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" || secret != "" {
		request.SetBasicAuth(key, secret)
	}
	request.Header.Set("Content-Type", contentType)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func setRazorpayBehavior(t *testing.T, baseURL string, behavior map[string]any) {
	t.Helper()
	postTestControl(t, baseURL+"/test/razorpay/behavior", behavior)
}

func addRazorpayPayment(t *testing.T, baseURL string, payment fakeRazorpayPayment) {
	t.Helper()
	postTestControl(t, baseURL+"/test/razorpay/payments", payment)
}

func postTestControl(t *testing.T, url string, body any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("POST %s: want 204, got %d: %s", url, response.StatusCode, readBody(t, response))
	}
}

func getRequest(t *testing.T, url, key, secret string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth(key, secret)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func readBody(t *testing.T, response *http.Response) []byte {
	t.Helper()
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
