package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPaymentMetricsExposePrometheusContractWithoutHighCardinalityLabels(t *testing.T) {
	metrics := newPaymentMetrics()
	metrics.orderCreate("attempt", 0)
	metrics.orderCreate("success", 125*time.Millisecond)
	metrics.webhook("received")
	metrics.webhook("signature_failure")
	metrics.webhookProcessing("success", 25*time.Millisecond)
	metrics.webhookAcknowledgement("success", 30*time.Millisecond)
	metrics.paymentLifecycle("captured")
	metrics.providerCall("create_order", &http.Response{StatusCode: http.StatusTooManyRequests}, nil, 50*time.Millisecond)
	metrics.providerCall("fetch_payment", nil, context.DeadlineExceeded, 5*time.Second)
	metrics.set("billmesh_razorpay_reconciliation_backlog", nil, 3)

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	metrics.handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("metrics response = %d %q", response.Code, response.Header().Get("Content-Type"))
	}
	body := response.Body.String()
	for _, expected := range []string{
		`billmesh_razorpay_order_create_total{outcome="attempt"} 1`,
		`billmesh_razorpay_order_create_duration_seconds_bucket{outcome="success",le="0.25"} 1`,
		`billmesh_razorpay_webhook_requests_total{outcome="signature_failure"} 1`,
		`billmesh_razorpay_provider_api_requests_total{operation="create_order",outcome="rate_limited",status_class="4xx"} 1`,
		`billmesh_razorpay_provider_api_requests_total{operation="fetch_payment",outcome="timeout",status_class="none"} 1`,
		`billmesh_razorpay_reconciliation_backlog 3`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metric %q missing from:\n%s", expected, body)
		}
	}
	for _, forbiddenLabel := range []string{"account_id=", "customer_id=", "payment_id=", "provider_order_id=", "event_id=", "request_id="} {
		if strings.Contains(body, forbiddenLabel) {
			t.Fatalf("high-cardinality label %q found in metrics", forbiddenLabel)
		}
	}
}

func TestPaymentMetricsEndpointRejectsNonScrapeRequests(t *testing.T) {
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/metrics", nil),
		httptest.NewRequest(http.MethodGet, "/", nil),
	} {
		response := httptest.NewRecorder()
		newPaymentMetrics().handler().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("%s %s returned %d: %s", request.Method, request.URL.Path, response.Code, body)
		}
	}
}
