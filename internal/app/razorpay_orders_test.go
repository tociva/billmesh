package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRazorpayOrderAmountUsesStoredCurrencySubunits(t *testing.T) {
	amount, currency, err := razorpayOrderAmount(49900, "INR")
	if err != nil {
		t.Fatal(err)
	}
	if amount != 49900 || currency != "INR" {
		t.Fatalf("mapped amount = %d %s, want 49900 INR", amount, currency)
	}
	for _, test := range []struct {
		amount   int64
		currency string
	}{
		{amount: 0, currency: "INR"},
		{amount: -1, currency: "INR"},
		{amount: 100, currency: "USD"},
		{amount: 100, currency: "inr"},
	} {
		if _, _, err := razorpayOrderAmount(test.amount, test.currency); err == nil {
			t.Fatalf("razorpayOrderAmount(%d, %q) unexpectedly succeeded", test.amount, test.currency)
		}
	}
}

func TestRazorpayReceiptIsProviderSafeAndDeterministic(t *testing.T) {
	operation := "credit-pack:" + uuid.NewString() + ":" + strings.Repeat("customer-key-", 20)
	first := razorpayReceipt("development", operation)
	second := razorpayReceipt("development", operation)
	if first == "" || first != second || len(first) > 40 {
		t.Fatalf("receipt = %q, repeated = %q", first, second)
	}
	for _, value := range []byte(first) {
		if value < 0x20 || value > 0x7e {
			t.Fatalf("receipt contains non-ASCII byte %x: %q", value, first)
		}
	}
	if other := razorpayReceipt("development", operation+"-other"); other == first {
		t.Fatalf("different operation produced duplicate receipt %q", first)
	}
	if production := razorpayReceipt("production", operation); production == first {
		t.Fatalf("different environment produced duplicate receipt %q", first)
	}
}

func TestCreateRazorpayOrderSendsApprovedWireContract(t *testing.T) {
	expected := razorpayOrderExpectation{
		PaymentID: uuid.New(), AccountID: uuid.New(), Amount: 49900, Currency: "INR",
		Environment: "development", OperationRef: "credit-pack:test-operation", CreatedAt: time.Now().UTC(),
	}
	expected.Receipt = razorpayReceipt(expected.Environment, expected.OperationRef)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/orders" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		key, secret, ok := r.BasicAuth()
		if !ok || key != "rzp_test_billmesh" || secret != "provider-secret" {
			t.Fatalf("unexpected Basic Auth: key=%q present=%t", key, ok)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		var request razorpayOrderRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Amount != expected.Amount || request.Currency != expected.Currency || request.Receipt != expected.Receipt {
			t.Fatalf("provider request = %+v", request)
		}
		if request.Notes["billmesh_payment_id"] != expected.PaymentID.String() ||
			request.Notes["billmesh_account_id"] != expected.AccountID.String() ||
			request.Notes["billmesh_environment"] != expected.Environment ||
			request.Notes["billmesh_operation_ref"] != expected.OperationRef {
			t.Fatalf("provider notes = %+v", request.Notes)
		}
		writeJSON(w, http.StatusOK, razorpayOrder{
			ID: "order_billmesh_test", Entity: "order", Amount: request.Amount, AmountDue: request.Amount,
			Currency: request.Currency, Receipt: request.Receipt, Status: "created", Notes: request.Notes,
			CreatedAt: time.Now().UTC().Unix(),
		})
	}))
	defer provider.Close()

	api := NewAPI(nil, nil, nil)
	api.providerBaseURL = provider.URL
	api.providerPublicKey = "rzp_test_billmesh"
	api.providerSecret = "provider-secret"
	order, err := api.createRazorpayOrderOnce(context.Background(), expected)
	if err != nil {
		t.Fatal(err)
	}
	if order.ID != "order_billmesh_test" {
		t.Fatalf("order id = %q", order.ID)
	}
}

func TestValidateRazorpayOrderRejectsMismatchedResponse(t *testing.T) {
	expected := razorpayOrderExpectation{
		PaymentID: uuid.New(), AccountID: uuid.New(), Amount: 49900, Currency: "INR",
		Environment: "development", OperationRef: "credit-pack:test-operation",
	}
	expected.Receipt = razorpayReceipt(expected.Environment, expected.OperationRef)
	valid := razorpayOrder{
		ID: "order_billmesh_test", Entity: "order", Amount: expected.Amount, AmountDue: expected.Amount,
		Currency: expected.Currency, Receipt: expected.Receipt, Status: "created", Notes: expected.notes(), CreatedAt: time.Now().Unix(),
	}
	if err := validateRazorpayOrder(valid, expected); err != nil {
		t.Fatalf("valid order rejected: %v", err)
	}

	tests := map[string]func(*razorpayOrder){
		"missing id":        func(order *razorpayOrder) { order.ID = "" },
		"wrong entity":      func(order *razorpayOrder) { order.Entity = "payment" },
		"wrong amount":      func(order *razorpayOrder) { order.Amount++ },
		"wrong amount due":  func(order *razorpayOrder) { order.AmountDue-- },
		"wrong currency":    func(order *razorpayOrder) { order.Currency = "USD" },
		"wrong receipt":     func(order *razorpayOrder) { order.Receipt += "x" },
		"wrong status":      func(order *razorpayOrder) { order.Status = "paid" },
		"wrong notes":       func(order *razorpayOrder) { order.Notes["billmesh_account_id"] = uuid.NewString() },
		"wrong environment": func(order *razorpayOrder) { order.Notes["billmesh_environment"] = "production" },
		"missing timestamp": func(order *razorpayOrder) { order.CreatedAt = 0 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			candidate.Notes = map[string]string{}
			for key, value := range valid.Notes {
				candidate.Notes[key] = value
			}
			mutate(&candidate)
			if err := validateRazorpayOrder(candidate, expected); err == nil {
				t.Fatalf("mismatched order unexpectedly accepted: %+v", candidate)
			}
		})
	}
}

func TestCreateOrRecoverRazorpayOrderAfterProviderTimeout(t *testing.T) {
	expected := razorpayOrderExpectation{
		PaymentID: uuid.New(), AccountID: uuid.New(), Amount: 49900, Currency: "INR",
		Environment: "development", OperationRef: "credit-pack:timeout", CreatedAt: time.Now().UTC(),
	}
	expected.Receipt = razorpayReceipt(expected.Environment, expected.OperationRef)
	var mutex sync.Mutex
	var committed *razorpayOrder
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			mutex.Lock()
			alreadyCommitted := committed != nil
			mutex.Unlock()
			if alreadyCommitted {
				http.Error(w, "duplicate receipt", http.StatusBadRequest)
				return
			}
			time.Sleep(25 * time.Millisecond)
			order := razorpayOrder{
				ID: "order_timeout_recovered", Entity: "order", Amount: expected.Amount, AmountDue: expected.Amount,
				Currency: expected.Currency, Receipt: expected.Receipt, Status: "created", Notes: expected.notes(), CreatedAt: time.Now().Unix(),
			}
			mutex.Lock()
			committed = &order
			mutex.Unlock()
			writeJSON(w, http.StatusOK, order)
		case http.MethodGet:
			mutex.Lock()
			items := []razorpayOrder{}
			if committed != nil {
				items = append(items, *committed)
			}
			mutex.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"entity": "collection", "count": len(items), "items": items})
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	}))
	defer provider.Close()

	api := NewAPI(nil, nil, nil)
	api.providerBaseURL = provider.URL
	api.providerPublicKey = "rzp_test_billmesh"
	api.providerSecret = "provider-secret"
	api.client = &http.Client{Timeout: 10 * time.Millisecond}
	order, err := api.createOrRecoverRazorpayOrder(context.Background(), expected)
	if err != nil {
		t.Fatal(err)
	}
	if order.ID != "order_timeout_recovered" {
		t.Fatalf("recovered order id = %q", order.ID)
	}
}
