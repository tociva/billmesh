package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFetchCapturedProviderPaymentSelectsOnlyExactCapture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/orders/order_1/payments" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		writeTestJSON(w, `{"entity":"collection","count":3,"items":[
			{"id":"pay_failed","entity":"payment","order_id":"order_1","status":"failed","amount":500,"currency":"INR"},
			{"id":"pay_authorized","entity":"payment","order_id":"order_1","status":"authorized","amount":500,"currency":"INR"},
			{"id":"pay_captured","entity":"payment","order_id":"order_1","status":"captured","amount":500,"currency":"INR"}
		]}`)
	}))
	defer server.Close()
	worker := reconciliationTestWorker(server)

	paymentID, found, err := worker.fetchCapturedProviderPayment(context.Background(), "order_1", 500, "INR")
	if err != nil {
		t.Fatal(err)
	}
	if !found || paymentID != "pay_captured" {
		t.Fatalf("capture = %q, %t", paymentID, found)
	}
}

func TestFetchCapturedProviderPaymentRejectsEveryMismatch(t *testing.T) {
	tests := []struct {
		name, payment string
	}{
		{"order", `{"id":"pay_1","entity":"payment","order_id":"order_other","status":"captured","amount":500,"currency":"INR"}`},
		{"amount", `{"id":"pay_1","entity":"payment","order_id":"order_1","status":"captured","amount":501,"currency":"INR"}`},
		{"currency", `{"id":"pay_1","entity":"payment","order_id":"order_1","status":"captured","amount":500,"currency":"USD"}`},
		{"entity", `{"id":"pay_1","entity":"refund","order_id":"order_1","status":"captured","amount":500,"currency":"INR"}`},
		{"missing id", `{"id":"","entity":"payment","order_id":"order_1","status":"captured","amount":500,"currency":"INR"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeTestJSON(w, `{"entity":"collection","count":1,"items":[`+test.payment+`]}`)
			}))
			defer server.Close()
			_, _, err := reconciliationTestWorker(server).fetchCapturedProviderPayment(context.Background(), "order_1", 500, "INR")
			failure := classifyProviderReconciliationError(err)
			if failure.code != "captured_payment_mismatch" || !failure.permanent {
				t.Fatalf("failure = %#v", failure)
			}
		})
	}
}

func TestProviderReconciliationHTTPFailuresAreClassified(t *testing.T) {
	tests := []struct {
		status    int
		code      string
		permanent bool
	}{
		{http.StatusUnauthorized, "authentication", true},
		{http.StatusNotFound, "order_not_found", true},
		{http.StatusTooManyRequests, "provider_transient", false},
		{http.StatusInternalServerError, "provider_transient", false},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "provider failure with secret-looking text", test.status)
			}))
			defer server.Close()
			_, _, err := reconciliationTestWorker(server).fetchCapturedProviderPayment(context.Background(), "order_1", 500, "INR")
			failure := classifyProviderReconciliationError(err)
			if failure.code != test.code || failure.permanent != test.permanent {
				t.Fatalf("failure = %#v", failure)
			}
			if failure.Error() == "provider failure with secret-looking text" {
				t.Fatal("provider response body leaked into the error")
			}
		})
	}
}

func TestProviderReconciliationRejectsMalformedPartialAndTrailingJSON(t *testing.T) {
	responses := []string{
		`{"entity":`,
		`{"entity":"collection","count":1,"items":[{"id":"pay_partial"}]}`,
		`{"entity":"collection","count":0,"items":[]} {}`,
		`{"entity":"collection","count":2,"items":[]}`,
	}
	for _, body := range responses {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeTestJSON(w, body)
		}))
		_, _, err := reconciliationTestWorker(server).fetchCapturedProviderPayment(context.Background(), "order_1", 500, "INR")
		server.Close()
		if err == nil {
			t.Fatalf("response unexpectedly accepted: %s", body)
		}
	}
}

func TestProviderReconciliationTimeoutIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		writeTestJSON(w, `{"entity":"collection","count":0,"items":[]}`)
	}))
	defer server.Close()
	worker := reconciliationTestWorker(server)
	worker.client.Timeout = time.Millisecond

	_, _, err := worker.fetchCapturedProviderPayment(context.Background(), "order_1", 500, "INR")
	failure := classifyProviderReconciliationError(err)
	if failure.code != "transport" || failure.permanent {
		t.Fatalf("failure = %#v", failure)
	}
}

func TestReconciliationBackoffIsBoundedDeterministicAndJittered(t *testing.T) {
	paymentID := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	previous := time.Duration(0)
	for attempt := 1; attempt <= 20; attempt++ {
		delay := reconciliationBackoff(attempt, paymentID)
		if delay != reconciliationBackoff(attempt, paymentID) {
			t.Fatal("backoff jitter is not deterministic for one payment attempt")
		}
		base := time.Minute * time.Duration(1<<min(attempt-1, 8))
		if delay < base*80/100 || delay > base*120/100 {
			t.Fatalf("attempt %d delay %s outside jitter bound around %s", attempt, delay, base)
		}
		if attempt <= 9 && delay <= previous/2 {
			t.Fatalf("attempt %d did not increase backoff: %s after %s", attempt, delay, previous)
		}
		previous = delay
	}
}

func TestReconciliationOrderValidationAllowsProviderLifecycleChangesOnly(t *testing.T) {
	expected := razorpayOrderExpectation{
		PaymentID: uuid.MustParse("00000000-0000-4000-8000-000000000001"),
		AccountID: uuid.MustParse("00000000-0000-4000-8000-000000000002"),
		Amount:    500, Currency: "INR", Receipt: "bm_receipt", Environment: "development", OperationRef: "purchase:1",
	}
	order := razorpayOrder{
		ID: "order_1", Entity: "order", Amount: 500, AmountPaid: 500, AmountDue: 0, Currency: "INR",
		Receipt: "bm_receipt", Status: "paid", Attempts: 2, Notes: expected.notes(), CreatedAt: 1,
	}
	if err := validateReconciliationOrder(order, expected); err != nil {
		t.Fatal(err)
	}
	order.Currency = "USD"
	if err := validateReconciliationOrder(order, expected); err == nil {
		t.Fatal("immutable currency mismatch was accepted")
	}
}

func TestOperatorResolutionNoteValidation(t *testing.T) {
	if _, ok := safeResolutionNote("no"); ok {
		t.Fatal("short resolution note was accepted")
	}
	if note, ok := safeResolutionNote("  verified in provider dashboard  "); !ok || note != "verified in provider dashboard" {
		t.Fatalf("valid note = %q, %t", note, ok)
	}
}

func reconciliationTestWorker(server *httptest.Server) *Worker {
	return &Worker{
		client: server.Client(), providerBaseURL: server.URL,
		providerKey: "key", providerSecret: "secret", providerEnvironment: "test",
	}
}

func writeTestJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}
