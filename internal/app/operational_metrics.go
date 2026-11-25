package app

import (
	"context"
	"time"
)

const operationalMetricRefreshInterval = 15 * time.Second

func (w *Worker) RunOperationalMaintenance(ctx context.Context) error {
	if err := w.purgeExpiredRazorpayPayloads(ctx); err != nil {
		return err
	}
	return w.refreshOperationalMetrics(ctx)
}

func (w *Worker) refreshOperationalMetricsIfDue(ctx context.Context) error {
	if !w.lastMetricRefresh.IsZero() && time.Since(w.lastMetricRefresh) < operationalMetricRefreshInterval {
		return nil
	}
	if err := w.refreshOperationalMetrics(ctx); err != nil {
		return err
	}
	w.lastMetricRefresh = time.Now()
	return nil
}

func (w *Worker) refreshOperationalMetrics(ctx context.Context) error {
	paymentStates := []string{"created", "authorized", "captured", "failed", "refunded"}
	for _, state := range paymentStates {
		w.metrics.set("billmesh_razorpay_payment_records", metricLabels{"state": state}, 0)
	}
	rows, err := w.pool.Query(ctx, `SELECT status::text,count(*) FROM payments WHERE provider='razorpay' AND provider_environment=$1 GROUP BY status`, w.providerEnvironment)
	if err != nil {
		return err
	}
	for rows.Next() {
		var state string
		var count int64
		if err := rows.Scan(&state, &count); err != nil {
			rows.Close()
			return err
		}
		w.metrics.set("billmesh_razorpay_payment_records", metricLabels{"state": state}, float64(count))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	var count int64
	if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE provider='razorpay' AND provider_environment=$1
		AND status IN ('created','authorized') AND provider_order_id IS NOT NULL
		AND reconciliation_outcome IS DISTINCT FROM 'attention_required'`, w.providerEnvironment).Scan(&count); err != nil {
		return err
	}
	w.metrics.set("billmesh_razorpay_reconciliation_backlog", nil, float64(count))

	issueTypes := []string{"provider_authentication", "provider_order_missing", "provider_response_invalid", "orphan_order", "captured_payment_missing", "capture_mismatch", "invoice_mismatch", "ledger_mismatch", "refund_mismatch"}
	for _, issueType := range issueTypes {
		w.metrics.set("billmesh_razorpay_reconciliation_issues", metricLabels{"type": issueType}, 0)
	}
	rows, err = w.pool.Query(ctx, `SELECT issue_type,count(*) FROM reconciliation_issues WHERE provider='razorpay' AND provider_environment=$1 AND status<>'resolved' GROUP BY issue_type`, w.providerEnvironment)
	if err != nil {
		return err
	}
	for rows.Next() {
		var issueType string
		if err := rows.Scan(&issueType, &count); err != nil {
			rows.Close()
			return err
		}
		w.metrics.set("billmesh_razorpay_reconciliation_issues", metricLabels{"type": issueType}, float64(count))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM payments p WHERE p.provider='razorpay' AND p.provider_environment=$1 AND p.status='captured' AND (
		NOT EXISTS(SELECT 1 FROM invoices i WHERE i.payment_id=p.id) OR
		(p.credit_pack_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM credit_grants g WHERE g.operation_ref='payment:'||p.id::text)) OR
		(p.transition_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM subscription_transitions t WHERE t.id=p.transition_id AND t.status='completed'))
	)`, w.providerEnvironment).Scan(&count); err != nil {
		return err
	}
	w.metrics.set("billmesh_razorpay_captured_unfulfilled", nil, float64(count))

	transitionStates := []string{"requires_payment", "processing", "completed", "failed", "expired", "cancelled"}
	for _, state := range transitionStates {
		w.metrics.set("billmesh_subscription_transitions", metricLabels{"state": state}, 0)
	}
	rows, err = w.pool.Query(ctx, `SELECT status,count(*) FROM subscription_transitions GROUP BY status`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var state string
		if err := rows.Scan(&state, &count); err != nil {
			rows.Close()
			return err
		}
		w.metrics.set("billmesh_subscription_transitions", metricLabels{"state": state}, float64(count))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type='payment.late_capture'`).Scan(&count); err != nil {
		return err
	}
	w.metrics.set("billmesh_razorpay_lifecycle_records", metricLabels{"event": "late_captured"}, float64(count))
	return nil
}

func (w *Worker) purgeExpiredRazorpayPayloads(ctx context.Context) error {
	if w.rawPayloadRetention <= 0 || !w.lastRetentionCleanup.IsZero() && time.Since(w.lastRetentionCleanup) < time.Hour {
		return nil
	}
	cutoff := time.Now().UTC().Add(-w.rawPayloadRetention)
	var pending int64
	if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM provider_events WHERE provider='razorpay' AND raw_payload IS NOT NULL AND created_at<$1`, cutoff).Scan(&pending); err != nil {
		return err
	}
	w.metrics.set("billmesh_razorpay_raw_payloads_pending_purge", nil, float64(pending))
	if _, err := w.pool.Exec(ctx, `UPDATE provider_events SET raw_payload=NULL,
		payload=jsonb_strip_nulls(jsonb_build_object('retained',false,'provider_order_id',provider_order_id,
		'provider_payment_id',provider_payment_id,'provider_refund_id',provider_refund_id))
		WHERE provider='razorpay' AND raw_payload IS NOT NULL AND created_at<$1`, cutoff); err != nil {
		return err
	}
	w.metrics.set("billmesh_razorpay_raw_payloads_pending_purge", nil, 0)
	w.lastRetentionCleanup = time.Now()
	return nil
}
