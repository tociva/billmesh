package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	reconciliationClaimLease  = 5 * time.Minute
	reconciliationMaxAttempts = 8
	providerInventoryInterval = 5 * time.Minute
	financeInterval           = 24 * time.Hour
)

type providerReconciliationError struct {
	code      string
	permanent bool
	status    int
	cause     error
}

func (failure *providerReconciliationError) Error() string {
	if failure.status != 0 {
		return fmt.Sprintf("provider reconciliation %s (status %d)", failure.code, failure.status)
	}
	return "provider reconciliation " + failure.code
}

func (failure *providerReconciliationError) Unwrap() error { return failure.cause }

type providerPayment struct {
	ID       string `json:"id"`
	Entity   string `json:"entity"`
	OrderID  string `json:"order_id"`
	Status   string `json:"status"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

type providerRefund struct {
	ID        string `json:"id"`
	Entity    string `json:"entity"`
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	CreatedAt int64  `json:"created_at"`
}

type reconciliationCandidate struct {
	id, accountID         uuid.UUID
	attemptID             uuid.UUID
	orderID, currency     string
	amount                int64
	attempt, retrySeconds int
}

func (w *Worker) reconcileProviderPayments(ctx context.Context) error {
	if !w.providerConfigured() {
		return nil
	}
	items, err := w.claimReconciliationCandidates(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		paymentID, found, fetchErr := w.fetchCapturedProviderPayment(ctx, item.orderID, item.amount, item.currency)
		if fetchErr != nil {
			if err := w.recordReconciliationFailure(ctx, item, fetchErr); err != nil {
				return err
			}
			continue
		}
		if !found {
			if err := w.finishReconciliation(ctx, item, "no_capture", "", "", time.Now().UTC().Add(time.Duration(item.retrySeconds)*time.Second)); err != nil {
				return err
			}
			if w.metrics != nil {
				w.metrics.paymentLifecycle("unreconciled")
			}
			continue
		}
		if err := w.applyReconciledCapture(ctx, item, paymentID); err != nil {
			if errors.Is(err, errConflictingProviderEvent) {
				if finishErr := w.recordReconciliationAttention(ctx, item, "capture_mismatch", paymentID, "provider payment conflicts with the stored payment"); finishErr != nil {
					return finishErr
				}
				continue
			}
			return err
		}
		if w.metrics != nil {
			w.metrics.paymentLifecycle("captured")
		}
	}
	return nil
}

func (w *Worker) providerConfigured() bool {
	return w.providerBaseURL != "" && w.providerKey != "" && w.providerSecret != ""
}

func (w *Worker) claimReconciliationCandidates(ctx context.Context) ([]reconciliationCandidate, error) {
	items := make([]reconciliationCandidate, 0, 100)
	err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `WITH candidates AS (
			SELECT pay.id,COALESCE((pr.billing_policy->'projection'->>'reconciliation_seconds')::integer,300) retry_seconds
			FROM payments pay
			LEFT JOIN credit_packs cp ON cp.id=pay.credit_pack_id
			LEFT JOIN subscription_transitions st ON st.id=pay.transition_id
			JOIN products pr ON pr.id=COALESCE(cp.product_id,st.product_id)
			WHERE pay.provider='razorpay' AND pay.status IN ('created','authorized')
			AND pay.provider_environment=$1 AND pay.provider_order_id IS NOT NULL
			AND pay.reconciliation_outcome IS DISTINCT FROM 'attention_required'
			AND COALESCE(pay.reconciliation_next_attempt_at,
				pay.last_reconciled_at + COALESCE((pr.billing_policy->'projection'->>'reconciliation_seconds')::integer,300) * interval '1 second',
				pay.created_at + COALESCE((pr.billing_policy->'projection'->>'reconciliation_seconds')::integer,300) * interval '1 second') <= now()
			ORDER BY COALESCE(pay.reconciliation_next_attempt_at,pay.last_reconciled_at,pay.created_at),pay.id
			FOR UPDATE OF pay SKIP LOCKED LIMIT 100
		), claimed AS (
			UPDATE payments pay SET last_reconciled_at=now(),reconciliation_attempts=pay.reconciliation_attempts+1,
				reconciliation_outcome='in_progress',reconciliation_error_code=NULL,
				reconciliation_next_attempt_at=now()+($2::bigint * interval '1 second')
			FROM candidates c WHERE pay.id=c.id
			RETURNING pay.id,pay.account_id,pay.provider_order_id,pay.amount_minor,pay.currency,
				pay.reconciliation_attempts,c.retry_seconds
		)
		SELECT id,account_id,provider_order_id,amount_minor,currency,reconciliation_attempts,retry_seconds FROM claimed`,
			w.providerEnvironment, int64(reconciliationClaimLease/time.Second))
		if err != nil {
			return err
		}
		for rows.Next() {
			var item reconciliationCandidate
			if err := rows.Scan(&item.id, &item.accountID, &item.orderID, &item.amount, &item.currency, &item.attempt, &item.retrySeconds); err != nil {
				rows.Close()
				return err
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for index := range items {
			item := &items[index]
			if err := tx.QueryRow(ctx, `INSERT INTO reconciliation_attempts(payment_id,outcome,provider_order_id)
				VALUES($1,'in_progress',$2) RETURNING id`, item.id, item.orderID).Scan(&item.attemptID); err != nil {
				return err
			}
		}
		return nil
	})
	return items, err
}

func (w *Worker) applyReconciledCapture(ctx context.Context, item reconciliationCandidate, providerPaymentID string) error {
	return pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		providerEventID := "reconciliation:" + providerPaymentID
		payload, err := json.Marshal(map[string]any{
			"payment_id": providerPaymentID, "order_id": item.orderID, "status": "captured",
			"amount": item.amount, "currency": item.currency,
		})
		if err != nil {
			return err
		}
		var eventID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO provider_events(provider,provider_event_id,event_type,payload,billmesh_payment_id,provider_order_id,provider_payment_id)
			VALUES('razorpay',$1,'payment.reconciled',$2,$3,$4,$5) ON CONFLICT(provider,provider_event_id) DO UPDATE
			SET provider_event_id=excluded.provider_event_id RETURNING id`, providerEventID, payload, item.id, item.orderID, providerPaymentID).Scan(&eventID); err != nil {
			return err
		}
		if err := capturePayment(ctx, tx, w.providerEnvironment, item.orderID, providerPaymentID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE provider_events SET processed_at=COALESCE(processed_at,now()) WHERE id=$1`, eventID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payments SET reconciliation_outcome='recovered',reconciliation_next_attempt_at=NULL,
			reconciliation_error_code=NULL,reconciliation_provider_payment_id=$2 WHERE id=$1`, item.id, providerPaymentID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE reconciliation_attempts SET outcome='recovered',provider_payment_id=$2,completed_at=now() WHERE id=$1`, item.attemptID, providerPaymentID)
		return err
	})
}

func (w *Worker) finishReconciliation(ctx context.Context, item reconciliationCandidate, outcome, code, providerPaymentID string, next time.Time) error {
	return pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		var nextValue any
		if !next.IsZero() {
			nextValue = next
		}
		if _, err := tx.Exec(ctx, `UPDATE payments SET reconciliation_outcome=$2,reconciliation_error_code=NULLIF($3,''),
			reconciliation_provider_payment_id=NULLIF($4,''),reconciliation_next_attempt_at=$5 WHERE id=$1`,
			item.id, outcome, code, providerPaymentID, nextValue); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE reconciliation_attempts SET outcome=$2,error_code=NULLIF($3,''),
			provider_payment_id=NULLIF($4,''),completed_at=now() WHERE id=$1`, item.attemptID, outcome, code, providerPaymentID)
		return err
	})
}

func (w *Worker) recordReconciliationFailure(ctx context.Context, item reconciliationCandidate, cause error) error {
	failure := classifyProviderReconciliationError(cause)
	if failure.permanent || item.attempt >= reconciliationMaxAttempts {
		issueType := "provider_response_invalid"
		if failure.code == "authentication" {
			issueType = "provider_authentication"
		} else if failure.code == "order_not_found" {
			issueType = "provider_order_missing"
		} else if failure.code == "captured_payment_mismatch" {
			issueType = "capture_mismatch"
		}
		return w.recordReconciliationAttention(ctx, item, issueType, "", failure.Error())
	}
	delay := reconciliationBackoff(item.attempt, item.id)
	w.log.Warn("provider order reconciliation retry scheduled", "payment_id", item.id, "order_id", item.orderID, "error_code", failure.code, "attempt", item.attempt)
	return w.finishReconciliation(ctx, item, "retry_scheduled", failure.code, "", time.Now().UTC().Add(delay))
}

func (w *Worker) recordReconciliationAttention(ctx context.Context, item reconciliationCandidate, issueType, providerPaymentID, summary string) error {
	w.log.Error("provider reconciliation requires attention", "payment_id", item.id, "order_id", item.orderID, "issue_type", issueType)
	if w.metrics != nil {
		w.metrics.paymentLifecycle("mismatched")
	}
	return pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE payments SET reconciliation_outcome='attention_required',
			reconciliation_error_code=$2,reconciliation_provider_payment_id=NULLIF($3,''),reconciliation_next_attempt_at=NULL WHERE id=$1`,
			item.id, issueType, providerPaymentID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE reconciliation_attempts SET outcome='attention_required',error_code=$2,
			provider_payment_id=NULLIF($3,''),completed_at=now() WHERE id=$1`, item.attemptID, issueType, providerPaymentID); err != nil {
			return err
		}
		return upsertReconciliationIssue(ctx, tx, reconciliationIssue{
			Environment: w.providerEnvironment, AccountID: &item.accountID, PaymentID: &item.id,
			Type: issueType, OrderID: item.orderID, PaymentRef: providerPaymentID, Summary: summary,
		})
	})
}

func reconciliationBackoff(attempt int, paymentID uuid.UUID) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	exponent := min(attempt-1, 8)
	base := time.Minute * time.Duration(1<<exponent)
	digest := sha256.Sum256(append(paymentID[:], byte(attempt)))
	jitter := int64(binary.BigEndian.Uint16(digest[:2])%41) - 20
	return base + time.Duration(int64(base)*jitter/100)
}

func classifyProviderReconciliationError(err error) *providerReconciliationError {
	var failure *providerReconciliationError
	if errors.As(err, &failure) {
		return failure
	}
	return &providerReconciliationError{code: "transport", cause: err}
}

func providerStatusError(status int) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &providerReconciliationError{code: "authentication", permanent: true, status: status}
	case status == http.StatusNotFound:
		return &providerReconciliationError{code: "order_not_found", permanent: true, status: status}
	case status == http.StatusTooManyRequests || status >= 500:
		return &providerReconciliationError{code: "provider_transient", status: status}
	default:
		return &providerReconciliationError{code: "provider_rejected", permanent: true, status: status}
	}
}

func (w *Worker) fetchCapturedProviderPayment(ctx context.Context, orderID string, expectedAmount int64, expectedCurrency string) (string, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, w.providerBaseURL+"/v1/orders/"+url.PathEscape(orderID)+"/payments", nil)
	if err != nil {
		return "", false, err
	}
	request.SetBasicAuth(w.providerKey, w.providerSecret)
	response, err := w.doRazorpayRequest("list_order_payments", request)
	if err != nil {
		return "", false, &providerReconciliationError{code: "transport", cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
		return "", false, providerStatusError(response.StatusCode)
	}
	var collection struct {
		Entity string            `json:"entity"`
		Count  int               `json:"count"`
		Items  []providerPayment `json:"items"`
	}
	if err := decodeProviderCollection(response.Body, &collection); err != nil {
		return "", false, &providerReconciliationError{code: "invalid_response", cause: err}
	}
	if collection.Entity != "collection" || collection.Count != len(collection.Items) {
		return "", false, &providerReconciliationError{code: "invalid_response", cause: errors.New("invalid payment collection")}
	}
	var captured string
	for _, payment := range collection.Items {
		if !validProviderPaymentStatus(payment.Status) {
			return "", false, &providerReconciliationError{code: "invalid_response", cause: errors.New("provider payment collection contains an incomplete item")}
		}
		if payment.Status == "captured" {
			if payment.ID == "" || payment.Entity != "payment" || payment.OrderID != orderID ||
				payment.Amount != expectedAmount || payment.Currency != expectedCurrency {
				return "", false, &providerReconciliationError{code: "captured_payment_mismatch", permanent: true}
			}
			if captured != "" && captured != payment.ID {
				return "", false, &providerReconciliationError{code: "multiple_captured_payments", permanent: true}
			}
			captured = payment.ID
			continue
		}
		if payment.ID == "" || payment.Entity != "payment" || payment.OrderID != orderID ||
			payment.Amount <= 0 || len(payment.Currency) != 3 {
			return "", false, &providerReconciliationError{code: "invalid_response", cause: errors.New("provider payment collection contains an incomplete item")}
		}
	}
	return captured, captured != "", nil
}

func validProviderPaymentStatus(status string) bool {
	return status == "created" || status == "authorized" || status == "captured" || status == "failed"
}

func decodeProviderCollection(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("provider response contains trailing JSON")
	}
	return nil
}

type reconciliationIssue struct {
	Environment               string
	AccountID, PaymentID      *uuid.UUID
	Type, OrderID, PaymentRef string
	RefundRef, Summary        string
	Details                   map[string]any
}

func upsertReconciliationIssue(ctx context.Context, tx pgx.Tx, issue reconciliationIssue) error {
	if issue.Details == nil {
		issue.Details = map[string]any{}
	}
	_, err := tx.Exec(ctx, `INSERT INTO reconciliation_issues(provider,provider_environment,account_id,payment_id,issue_type,
		provider_order_id,provider_payment_id,provider_refund_id,summary,details)
		VALUES('razorpay',$1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),$8,$9)
		ON CONFLICT (provider,provider_environment,issue_type,COALESCE(payment_id,'00000000-0000-0000-0000-000000000000'::uuid),
			COALESCE(provider_order_id,''),COALESCE(provider_payment_id,''),COALESCE(provider_refund_id,'')) WHERE status<>'resolved'
		DO UPDATE SET last_detected_at=now(),summary=excluded.summary,details=excluded.details,version=reconciliation_issues.version+1`,
		issue.Environment, issue.AccountID, issue.PaymentID, issue.Type, issue.OrderID, issue.PaymentRef, issue.RefundRef, issue.Summary, issue.Details)
	return err
}

func (w *Worker) reconcileProviderInventory(ctx context.Context) error {
	if !w.providerConfigured() {
		return nil
	}
	claimed := false
	err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO provider_reconciliation_checkpoints(provider,provider_environment)
			VALUES('razorpay',$1) ON CONFLICT DO NOTHING`, w.providerEnvironment); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `UPDATE provider_reconciliation_checkpoints SET last_inventory_at=now(),
			last_inventory_outcome='in_progress',updated_at=now() WHERE provider='razorpay' AND provider_environment=$1
			AND COALESCE(last_inventory_at,'epoch') <= now()-($2::bigint * interval '1 second') RETURNING true`,
			w.providerEnvironment, int64(providerInventoryInterval/time.Second)).Scan(&claimed)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil || !claimed {
		return err
	}
	orders, err := w.fetchProviderOrders(ctx, time.Now().UTC().Add(-24*time.Hour), time.Now().UTC().Add(time.Minute))
	if err != nil {
		_, _ = w.pool.Exec(ctx, `UPDATE provider_reconciliation_checkpoints SET last_inventory_outcome='failed',updated_at=now()
			WHERE provider='razorpay' AND provider_environment=$1`, w.providerEnvironment)
		failure := classifyProviderReconciliationError(err)
		if failure.permanent {
			issueType := "provider_response_invalid"
			if failure.code == "authentication" {
				issueType = "provider_authentication"
			}
			issueErr := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
				return upsertReconciliationIssue(ctx, tx, reconciliationIssue{
					Environment: w.providerEnvironment, Type: issueType,
					Summary: "provider inventory reconciliation requires operator attention",
				})
			})
			if issueErr != nil {
				return errors.Join(err, issueErr)
			}
		}
		return err
	}
	for _, order := range orders {
		if err := w.reconcileProviderOrder(ctx, order); err != nil {
			return err
		}
	}
	_, err = w.pool.Exec(ctx, `UPDATE provider_reconciliation_checkpoints SET last_inventory_outcome='completed',updated_at=now()
		WHERE provider='razorpay' AND provider_environment=$1`, w.providerEnvironment)
	return err
}

func (w *Worker) fetchProviderOrders(ctx context.Context, from, to time.Time) ([]razorpayOrder, error) {
	orders := make([]razorpayOrder, 0)
	for page := 0; page < razorpayRecoveryPages; page++ {
		query := url.Values{
			"from": {strconv.FormatInt(from.Unix(), 10)}, "to": {strconv.FormatInt(to.Unix(), 10)},
			"count": {strconv.Itoa(razorpayRecoveryCount)}, "skip": {strconv.Itoa(page * razorpayRecoveryCount)},
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, w.providerBaseURL+"/v1/orders?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		request.SetBasicAuth(w.providerKey, w.providerSecret)
		response, err := w.doRazorpayRequest("list_orders", request)
		if err != nil {
			return nil, &providerReconciliationError{code: "transport", cause: err}
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			response.Body.Close()
			return nil, providerStatusError(response.StatusCode)
		}
		var collection struct {
			Entity string          `json:"entity"`
			Count  int             `json:"count"`
			Items  []razorpayOrder `json:"items"`
		}
		decodeErr := decodeProviderCollection(response.Body, &collection)
		response.Body.Close()
		if decodeErr != nil || collection.Entity != "collection" || collection.Count != len(collection.Items) {
			return nil, &providerReconciliationError{code: "invalid_response", cause: decodeErr}
		}
		orders = append(orders, collection.Items...)
		if len(collection.Items) < razorpayRecoveryCount {
			break
		}
	}
	return orders, nil
}

func (w *Worker) reconcileProviderOrder(ctx context.Context, order razorpayOrder) error {
	paymentID, parseErr := uuid.Parse(order.Notes["billmesh_payment_id"])
	if parseErr != nil {
		return w.recordOrphanOrder(ctx, order, nil, "provider order has no usable Billmesh payment reference")
	}
	var accountID uuid.UUID
	var storedOrderID *string
	var receipt, environment, operation, currency string
	var amount int64
	err := w.pool.QueryRow(ctx, `SELECT account_id,provider_order_id,provider_receipt,provider_environment,operation_ref,amount_minor,currency
		FROM payments WHERE id=$1 AND provider='razorpay'`, paymentID).
		Scan(&accountID, &storedOrderID, &receipt, &environment, &operation, &amount, &currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return w.recordOrphanOrder(ctx, order, nil, "provider order references a missing Billmesh payment")
	}
	if err != nil {
		return err
	}
	expected := razorpayOrderExpectation{
		PaymentID: paymentID, AccountID: accountID, Amount: amount, Currency: currency,
		Receipt: receipt, Environment: environment, OperationRef: operation,
	}
	if validationErr := validateReconciliationOrder(order, expected); validationErr != nil {
		return pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
			return upsertReconciliationIssue(ctx, tx, reconciliationIssue{
				Environment: w.providerEnvironment, AccountID: &accountID, PaymentID: &paymentID,
				Type: "orphan_order", OrderID: order.ID, Summary: "provider order does not match its Billmesh payment",
			})
		})
	}
	if storedOrderID == nil {
		_, err = w.pool.Exec(ctx, `UPDATE payments SET provider_order_id=$2,failure_reason=NULL,reconciliation_outcome='order_recovered',
			reconciliation_next_attempt_at=now(),updated_at=now() WHERE id=$1 AND provider_order_id IS NULL`, paymentID, order.ID)
		return err
	}
	if *storedOrderID != order.ID {
		return w.recordOrphanOrder(ctx, order, &paymentID, "provider order conflicts with the stored Billmesh order")
	}
	return nil
}

func validateReconciliationOrder(order razorpayOrder, expected razorpayOrderExpectation) error {
	if !strings.HasPrefix(order.ID, "order_") || order.Entity != "order" || order.Amount != expected.Amount ||
		order.Currency != expected.Currency || order.Receipt != expected.Receipt || order.CreatedAt <= 0 {
		return errors.New("provider order immutable fields do not match the Billmesh operation")
	}
	if order.Status != "created" && order.Status != "attempted" && order.Status != "paid" {
		return errors.New("provider order has an unsupported status")
	}
	for key, value := range expected.notes() {
		if order.Notes[key] != value {
			return fmt.Errorf("provider order note %s does not match", key)
		}
	}
	return nil
}

func (w *Worker) recordOrphanOrder(ctx context.Context, order razorpayOrder, paymentID *uuid.UUID, summary string) error {
	paymentRef, captured, fetchErr := w.fetchCapturedProviderPayment(ctx, order.ID, order.Amount, order.Currency)
	if fetchErr != nil {
		return fetchErr
	}
	issueType := "orphan_order"
	if captured {
		issueType = "captured_payment_missing"
	}
	return pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		return upsertReconciliationIssue(ctx, tx, reconciliationIssue{
			Environment: w.providerEnvironment, PaymentID: paymentID, Type: issueType,
			OrderID: order.ID, PaymentRef: paymentRef, Summary: summary,
		})
	})
}

func (w *Worker) reconcileFinancialRecords(ctx context.Context) error {
	if !w.providerConfigured() {
		return nil
	}
	rows, err := w.pool.Query(ctx, `SELECT id,account_id,provider_order_id,provider_payment_id,purpose,amount_minor,currency
		FROM payments WHERE provider='razorpay' AND provider_environment=$1 AND status IN ('captured','refunded')
		AND provider_order_id IS NOT NULL AND provider_payment_id IS NOT NULL
		AND COALESCE(finance_reconciled_at,'epoch') <= now()-($2::bigint * interval '1 second')
		ORDER BY COALESCE(finance_reconciled_at,'epoch'),id LIMIT 100`, w.providerEnvironment, int64(financeInterval/time.Second))
	if err != nil {
		return err
	}
	type financialCandidate struct {
		id, accountID                uuid.UUID
		orderID, paymentRef, purpose string
		amount                       int64
		currency                     string
	}
	items := make([]financialCandidate, 0, 100)
	for rows.Next() {
		var item financialCandidate
		if err := rows.Scan(&item.id, &item.accountID, &item.orderID, &item.paymentRef, &item.purpose, &item.amount, &item.currency); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range items {
		if err := w.reconcileFinancialRecord(ctx, item.id, item.accountID, item.orderID, item.paymentRef, item.purpose, item.amount, item.currency); err != nil {
			w.log.Warn("financial reconciliation failed", "payment_id", item.id, "error", err)
		}
	}
	return nil
}

func (w *Worker) reconcileFinancialRecord(ctx context.Context, paymentID, accountID uuid.UUID, orderID, paymentRef, purpose string, amount int64, currency string) error {
	provider, err := w.fetchProviderPayment(ctx, paymentRef)
	if err != nil {
		_, _ = w.pool.Exec(ctx, `UPDATE payments SET finance_reconciled_at=now(),finance_reconciliation_outcome='provider_unavailable' WHERE id=$1`, paymentID)
		return err
	}
	if provider.ID != paymentRef || provider.Entity != "payment" || provider.OrderID != orderID || provider.Amount != amount || provider.Currency != currency || provider.Status != "captured" {
		return w.recordFinanceIssue(ctx, paymentID, accountID, orderID, paymentRef, "capture_mismatch", "captured provider payment does not match Billmesh")
	}
	var invoiceCount, fulfillmentCount, ledgerCount int
	if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE payment_id=$1 AND status IN ('paid','refunded')
		AND total_minor=$2 AND currency=$3`, paymentID, amount, currency).Scan(&invoiceCount); err != nil {
		return err
	}
	if purpose == "subscription" {
		err = w.pool.QueryRow(ctx, `SELECT count(*) FROM subscription_transitions WHERE payment_id=$1 AND status IN ('completed','processing')`, paymentID).Scan(&fulfillmentCount)
		ledgerCount = 1
	} else {
		err = w.pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM credit_grants WHERE operation_ref=$1),
			(SELECT count(*) FROM credit_ledger WHERE operation_ref=$2)`,
			"payment:"+paymentID.String(), "grant:payment:"+paymentID.String()).Scan(&fulfillmentCount, &ledgerCount)
	}
	if err != nil {
		return err
	}
	if invoiceCount != 1 {
		return w.recordFinanceIssue(ctx, paymentID, accountID, orderID, paymentRef, "invoice_mismatch", "captured payment does not have exactly one matching invoice")
	}
	if fulfillmentCount != 1 || ledgerCount != 1 {
		return w.recordFinanceIssue(ctx, paymentID, accountID, orderID, paymentRef, "ledger_mismatch", "captured payment is absent from the fulfilled ledger")
	}
	refunds, err := w.fetchProviderRefunds(ctx, paymentRef)
	if err != nil {
		return err
	}
	providerRefunds := make(map[string]providerRefund, len(refunds))
	for _, refund := range refunds {
		if refund.Status != "processed" {
			continue
		}
		providerRefunds[refund.ID] = refund
		var matches int
		if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM refunds r JOIN credit_notes cn ON cn.refund_id=r.id
			WHERE r.payment_id=$1 AND r.provider_refund_id=$2 AND r.amount_minor=$3 AND cn.amount_minor=$3 AND cn.currency=$4`,
			paymentID, refund.ID, refund.Amount, refund.Currency).Scan(&matches); err != nil {
			return err
		}
		if matches != 1 {
			return w.recordFinanceIssueWithRefund(ctx, paymentID, accountID, orderID, paymentRef, refund.ID, "refund_mismatch", "provider refund does not match one Billmesh refund and credit note")
		}
	}
	rows, err := w.pool.Query(ctx, `SELECT provider_refund_id,amount_minor FROM refunds WHERE payment_id=$1`, paymentID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var refundID string
		var refundAmount int64
		if err := rows.Scan(&refundID, &refundAmount); err != nil {
			return err
		}
		providerRefund, found := providerRefunds[refundID]
		if !found || providerRefund.Amount != refundAmount || providerRefund.Currency != currency {
			rows.Close()
			return w.recordFinanceIssueWithRefund(ctx, paymentID, accountID, orderID, paymentRef, refundID, "refund_mismatch", "Billmesh refund is absent or different in the provider records")
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = w.pool.Exec(ctx, `UPDATE payments SET finance_reconciled_at=now(),finance_reconciliation_outcome='matched' WHERE id=$1`, paymentID)
	return err
}

func (w *Worker) fetchProviderPayment(ctx context.Context, paymentID string) (providerPayment, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, w.providerBaseURL+"/v1/payments/"+url.PathEscape(paymentID), nil)
	if err != nil {
		return providerPayment{}, err
	}
	request.SetBasicAuth(w.providerKey, w.providerSecret)
	response, err := w.doRazorpayRequest("fetch_payment", request)
	if err != nil {
		return providerPayment{}, &providerReconciliationError{code: "transport", cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerPayment{}, providerStatusError(response.StatusCode)
	}
	var payment providerPayment
	if err := decodeProviderCollection(response.Body, &payment); err != nil {
		return providerPayment{}, &providerReconciliationError{code: "invalid_response", cause: err}
	}
	return payment, nil
}

func (w *Worker) fetchProviderRefunds(ctx context.Context, paymentID string) ([]providerRefund, error) {
	refunds := make([]providerRefund, 0)
	for page := 0; page < razorpayRecoveryPages; page++ {
		endpoint := w.providerBaseURL + "/v1/payments/" + url.PathEscape(paymentID) + "/refunds?count=100&skip=" + strconv.Itoa(page*100)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		request.SetBasicAuth(w.providerKey, w.providerSecret)
		response, err := w.doRazorpayRequest("list_refunds", request)
		if err != nil {
			return nil, &providerReconciliationError{code: "transport", cause: err}
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			response.Body.Close()
			return nil, providerStatusError(response.StatusCode)
		}
		var collection struct {
			Entity string           `json:"entity"`
			Count  int              `json:"count"`
			Items  []providerRefund `json:"items"`
		}
		decodeErr := decodeProviderCollection(response.Body, &collection)
		response.Body.Close()
		if decodeErr != nil || collection.Entity != "collection" || collection.Count != len(collection.Items) {
			return nil, &providerReconciliationError{code: "invalid_response", cause: decodeErr}
		}
		for _, refund := range collection.Items {
			if refund.ID == "" || refund.Entity != "refund" || refund.PaymentID != paymentID || refund.Amount <= 0 || len(refund.Currency) != 3 {
				return nil, &providerReconciliationError{code: "invalid_response", cause: errors.New("invalid refund collection")}
			}
		}
		refunds = append(refunds, collection.Items...)
		if len(collection.Items) < 100 {
			break
		}
	}
	return refunds, nil
}

func (w *Worker) recordFinanceIssue(ctx context.Context, paymentID, accountID uuid.UUID, orderID, paymentRef, issueType, summary string) error {
	return w.recordFinanceIssueWithRefund(ctx, paymentID, accountID, orderID, paymentRef, "", issueType, summary)
}

func (w *Worker) recordFinanceIssueWithRefund(ctx context.Context, paymentID, accountID uuid.UUID, orderID, paymentRef, refundRef, issueType, summary string) error {
	return pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE payments SET finance_reconciled_at=now(),finance_reconciliation_outcome='mismatch' WHERE id=$1`, paymentID); err != nil {
			return err
		}
		return upsertReconciliationIssue(ctx, tx, reconciliationIssue{
			Environment: w.providerEnvironment, AccountID: &accountID, PaymentID: &paymentID,
			Type: issueType, OrderID: orderID, PaymentRef: paymentRef, RefundRef: refundRef, Summary: summary,
		})
	})
}

func safeResolutionNote(value string) (string, bool) {
	value = strings.TrimSpace(value)
	return value, len(value) >= 3 && len(value) <= 1000
}
