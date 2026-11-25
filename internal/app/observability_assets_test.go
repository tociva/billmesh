package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRazorpayOperationalAssetsCoverRequiredDashboardsAlertsAndRunbooks(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve source path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))

	dashboardRaw, err := os.ReadFile(filepath.Join(root, "deploy", "observability", "razorpay-dashboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dashboard struct {
		Title  string `json:"title"`
		Panels []struct {
			Title string `json:"title"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(dashboardRaw, &dashboard); err != nil {
		t.Fatal(err)
	}
	panelTitles := make([]string, 0, len(dashboard.Panels))
	for _, panel := range dashboard.Panels {
		panelTitles = append(panelTitles, panel.Title)
	}
	joinedPanels := strings.ToLower(strings.Join(panelTitles, " "))
	for _, required := range []string{"checkout", "webhook", "reconciliation", "unfulfilled", "refund", "subscription"} {
		if !strings.Contains(joinedPanels, required) {
			t.Fatalf("dashboard lacks %s panel: %v", required, panelTitles)
		}
	}

	alertsRaw, err := os.ReadFile(filepath.Join(root, "deploy", "observability", "razorpay-alerts.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var alerts map[string]any
	if err := yaml.Unmarshal(alertsRaw, &alerts); err != nil {
		t.Fatal(err)
	}
	alertsText := string(alertsRaw)
	for _, required := range []string{
		"BillmeshRazorpayProviderFailures", "BillmeshRazorpayInvalidSignatureSpike",
		"BillmeshRazorpayWebhookProcessingFailures", "BillmeshRazorpayWebhookAcknowledgementSlow",
		"BillmeshRazorpayReconciliationBacklog", "BillmeshRazorpayCapturedUnfulfilled", "BillmeshRazorpayLedgerMismatch",
	} {
		if !strings.Contains(alertsText, required) {
			t.Fatalf("alert %s is missing", required)
		}
	}

	operations, err := os.ReadFile(filepath.Join(root, "docs", "runbooks", "razorpay-operations.md"))
	if err != nil {
		t.Fatal(err)
	}
	operationsText := strings.ToLower(string(operations))
	for _, required := range []string{"webhook disablement", "delayed or failed delivery", "provider outage", "ambiguous order creation", "missed capture", "duplicate charge", "refund or ledger mismatch", "reconciliation failure"} {
		if !strings.Contains(operationsText, required) {
			t.Fatalf("operations runbook lacks %q", required)
		}
	}
	rotation, err := os.ReadFile(filepath.Join(root, "docs", "runbooks", "razorpay-key-rotation.md"))
	if err != nil || !strings.Contains(strings.ToLower(string(rotation)), "zero-downtime") {
		t.Fatalf("rotation runbook is incomplete: %v", err)
	}
}
