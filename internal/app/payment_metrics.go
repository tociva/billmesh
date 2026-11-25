package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var paymentMetricDefinitions = map[string]struct {
	help string
	kind string
}{
	"billmesh_razorpay_order_create_total":                       {"Razorpay order-create attempts and terminal outcomes.", "counter"},
	"billmesh_razorpay_order_create_duration_seconds":            {"Razorpay order-create latency by terminal outcome.", "histogram"},
	"billmesh_razorpay_webhook_requests_total":                   {"Razorpay webhook receipts and processing outcomes.", "counter"},
	"billmesh_razorpay_webhook_processing_duration_seconds":      {"Razorpay webhook processing latency after signature verification.", "histogram"},
	"billmesh_razorpay_webhook_acknowledgement_duration_seconds": {"End-to-end Razorpay webhook acknowledgement latency.", "histogram"},
	"billmesh_razorpay_payment_lifecycle_total":                  {"Durable Razorpay payment lifecycle outcomes observed by this process.", "counter"},
	"billmesh_razorpay_provider_api_requests_total":              {"Razorpay API calls by operation, status class, and bounded outcome.", "counter"},
	"billmesh_razorpay_provider_api_duration_seconds":            {"Razorpay API latency by operation and bounded outcome.", "histogram"},
	"billmesh_razorpay_payment_records":                          {"Current Razorpay payment records by state.", "gauge"},
	"billmesh_razorpay_lifecycle_records":                        {"Durable Razorpay lifecycle records by bounded event type.", "gauge"},
	"billmesh_razorpay_reconciliation_backlog":                   {"Current Razorpay payments awaiting reconciliation.", "gauge"},
	"billmesh_razorpay_reconciliation_issues":                    {"Current open Razorpay reconciliation issues by bounded type.", "gauge"},
	"billmesh_razorpay_captured_unfulfilled":                     {"Captured Razorpay payments without a matching invoice or ledger fulfilment.", "gauge"},
	"billmesh_subscription_transitions":                          {"Current subscription transitions by state.", "gauge"},
	"billmesh_razorpay_raw_payloads_pending_purge":               {"Raw Razorpay webhook payloads older than the configured retention period.", "gauge"},
}

var paymentHistogramBuckets = []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type metricLabels map[string]string

type metricSeries struct {
	labels string
	value  float64
}

type metricHistogram struct {
	labels  string
	buckets []uint64
	count   uint64
	sum     float64
}

type paymentMetrics struct {
	mu         sync.RWMutex
	counters   map[string]metricSeries
	gauges     map[string]metricSeries
	histograms map[string]*metricHistogram
}

func newPaymentMetrics() *paymentMetrics {
	return &paymentMetrics{
		counters: make(map[string]metricSeries), gauges: make(map[string]metricSeries),
		histograms: make(map[string]*metricHistogram),
	}
}

func (m *paymentMetrics) increment(name string, labels metricLabels) {
	labelText := prometheusLabels(labels)
	key := name + "\x00" + labelText
	m.mu.Lock()
	series := m.counters[key]
	series.labels = labelText
	series.value++
	m.counters[key] = series
	m.mu.Unlock()
}

func (m *paymentMetrics) set(name string, labels metricLabels, value float64) {
	labelText := prometheusLabels(labels)
	key := name + "\x00" + labelText
	m.mu.Lock()
	m.gauges[key] = metricSeries{labels: labelText, value: value}
	m.mu.Unlock()
}

func (m *paymentMetrics) observe(name string, labels metricLabels, duration time.Duration) {
	labelText := prometheusLabels(labels)
	key := name + "\x00" + labelText
	value := duration.Seconds()
	m.mu.Lock()
	histogram := m.histograms[key]
	if histogram == nil {
		histogram = &metricHistogram{labels: labelText, buckets: make([]uint64, len(paymentHistogramBuckets))}
		m.histograms[key] = histogram
	}
	for index, boundary := range paymentHistogramBuckets {
		if value <= boundary {
			histogram.buckets[index]++
		}
	}
	histogram.count++
	histogram.sum += value
	m.mu.Unlock()
}

func (m *paymentMetrics) orderCreate(outcome string, duration time.Duration) {
	m.increment("billmesh_razorpay_order_create_total", metricLabels{"outcome": outcome})
	if outcome != "attempt" {
		m.observe("billmesh_razorpay_order_create_duration_seconds", metricLabels{"outcome": outcome}, duration)
	}
}

func (m *paymentMetrics) webhook(outcome string) {
	m.increment("billmesh_razorpay_webhook_requests_total", metricLabels{"outcome": outcome})
}

func (m *paymentMetrics) webhookProcessing(outcome string, duration time.Duration) {
	m.observe("billmesh_razorpay_webhook_processing_duration_seconds", metricLabels{"outcome": outcome}, duration)
}

func (m *paymentMetrics) webhookAcknowledgement(outcome string, duration time.Duration) {
	m.observe("billmesh_razorpay_webhook_acknowledgement_duration_seconds", metricLabels{"outcome": outcome}, duration)
}

func (m *paymentMetrics) paymentLifecycle(outcome string) {
	m.increment("billmesh_razorpay_payment_lifecycle_total", metricLabels{"outcome": outcome})
}

func (m *paymentMetrics) providerCall(operation string, response *http.Response, err error, duration time.Duration) {
	statusClass, outcome := "none", "transport_error"
	if err != nil {
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() {
			outcome = "timeout"
		}
	} else if response != nil {
		statusClass = strconv.Itoa(response.StatusCode/100) + "xx"
		switch {
		case response.StatusCode == http.StatusTooManyRequests:
			outcome = "rate_limited"
		case response.StatusCode >= 200 && response.StatusCode < 300:
			outcome = "success"
		case response.StatusCode >= 500:
			outcome = "provider_error"
		default:
			outcome = "rejected"
		}
	}
	labels := metricLabels{"operation": operation, "outcome": outcome, "status_class": statusClass}
	m.increment("billmesh_razorpay_provider_api_requests_total", labels)
	m.observe("billmesh_razorpay_provider_api_duration_seconds", metricLabels{"operation": operation, "outcome": outcome}, duration)
}

func (a *API) doRazorpayRequest(operation string, request *http.Request) (*http.Response, error) {
	started := time.Now()
	response, err := a.client.Do(request)
	if a.metrics != nil {
		a.metrics.providerCall(operation, response, err, time.Since(started))
	}
	return response, err
}

func (w *Worker) doRazorpayRequest(operation string, request *http.Request) (*http.Response, error) {
	started := time.Now()
	response, err := w.client.Do(request)
	if w.metrics != nil {
		w.metrics.providerCall(operation, response, err, time.Since(started))
	}
	return response, err
}

func (m *paymentMetrics) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(m.render()))
	})
}

func (m *paymentMetrics) render() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(paymentMetricDefinitions))
	for name := range paymentMetricDefinitions {
		names = append(names, name)
	}
	sort.Strings(names)
	var output strings.Builder
	for _, name := range names {
		definition := paymentMetricDefinitions[name]
		fmt.Fprintf(&output, "# HELP %s %s\n# TYPE %s %s\n", name, definition.help, name, definition.kind)
		keys := make([]string, 0)
		for key := range m.counters {
			if strings.HasPrefix(key, name+"\x00") {
				keys = append(keys, key)
			}
		}
		for key := range m.gauges {
			if strings.HasPrefix(key, name+"\x00") {
				keys = append(keys, key)
			}
		}
		for key := range m.histograms {
			if strings.HasPrefix(key, name+"\x00") {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			if series, ok := m.counters[key]; ok {
				fmt.Fprintf(&output, "%s%s %s\n", name, series.labels, strconv.FormatFloat(series.value, 'f', -1, 64))
				continue
			}
			if series, ok := m.gauges[key]; ok {
				fmt.Fprintf(&output, "%s%s %s\n", name, series.labels, strconv.FormatFloat(series.value, 'f', -1, 64))
				continue
			}
			histogram := m.histograms[key]
			for index, boundary := range paymentHistogramBuckets {
				fmt.Fprintf(&output, "%s_bucket%s %d\n", name, prometheusAdditionalLabel(histogram.labels, "le", strconv.FormatFloat(boundary, 'f', -1, 64)), histogram.buckets[index])
			}
			fmt.Fprintf(&output, "%s_bucket%s %d\n", name, prometheusAdditionalLabel(histogram.labels, "le", "+Inf"), histogram.count)
			fmt.Fprintf(&output, "%s_sum%s %s\n", name, histogram.labels, strconv.FormatFloat(histogram.sum, 'f', -1, 64))
			fmt.Fprintf(&output, "%s_count%s %d\n", name, histogram.labels, histogram.count)
		}
	}
	return output.String()
}

func prometheusLabels(labels metricLabels) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"").Replace(labels[key])
		parts = append(parts, key+"=\""+value+"\"")
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func prometheusAdditionalLabel(existing, key, value string) string {
	addition := key + "=\"" + value + "\""
	if existing == "" {
		return "{" + addition + "}"
	}
	return strings.TrimSuffix(existing, "}") + "," + addition + "}"
}
