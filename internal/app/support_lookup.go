package app

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tociva/billmesh/internal/auth"
)

var supportProviderReferencePattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,255}$`)

type supportPaymentRecord struct {
	ID                  uuid.UUID `json:"id"`
	AccountID           uuid.UUID `json:"account_id"`
	Purpose             string    `json:"purpose"`
	Status              string    `json:"status"`
	AmountMinor         int64     `json:"amount_minor"`
	Currency            string    `json:"currency"`
	ProviderEnvironment string    `json:"provider_environment"`
	ProviderOrderID     *string   `json:"provider_order_id"`
	ProviderPaymentID   *string   `json:"provider_payment_id"`
	RequestID           *string   `json:"request_id"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type supportProviderEvent struct {
	EventID           string     `json:"event_id"`
	EventType         string     `json:"event_type"`
	ProviderOrderID   *string    `json:"provider_order_id"`
	ProviderPaymentID *string    `json:"provider_payment_id"`
	ProviderRefundID  *string    `json:"provider_refund_id"`
	RequestID         *string    `json:"request_id"`
	ProcessedAt       *time.Time `json:"processed_at"`
	CreatedAt         time.Time  `json:"created_at"`
}

type supportLookupResponse struct {
	Payment              *supportPaymentRecord         `json:"payment,omitempty"`
	ProviderEvents       []supportProviderEvent        `json:"provider_events"`
	ReconciliationIssues []reconciliationIssueResponse `json:"reconciliation_issues"`
}

func (a *API) lookupPaymentSupport(w http.ResponseWriter, r *http.Request) {
	keys := []string{"payment_id", "provider_order_id", "provider_payment_id", "event_id", "request_id"}
	selected, value := "", ""
	for _, key := range keys {
		if len(r.URL.Query()[key]) > 1 {
			writeCodedError(w, http.StatusBadRequest, "support_lookup_ambiguous", "provide each support lookup key at most once")
			return
		}
		if candidate := strings.TrimSpace(r.URL.Query().Get(key)); candidate != "" {
			if selected != "" {
				writeCodedError(w, http.StatusBadRequest, "support_lookup_ambiguous", "provide exactly one support lookup key")
				return
			}
			selected, value = key, candidate
		}
	}
	if selected == "" {
		writeCodedError(w, http.StatusBadRequest, "support_lookup_required", "provide exactly one support lookup key")
		return
	}
	if selected == "payment_id" {
		if _, err := uuid.Parse(value); err != nil {
			writeCodedError(w, http.StatusBadRequest, "support_lookup_invalid", "payment_id is invalid")
			return
		}
	} else if selected == "request_id" {
		if !validRequestID(value) {
			writeCodedError(w, http.StatusBadRequest, "support_lookup_invalid", "request_id is invalid")
			return
		}
	} else if selected == "event_id" {
		if !razorpayEventIDPattern.MatchString(value) {
			writeCodedError(w, http.StatusBadRequest, "support_lookup_invalid", "event_id is invalid")
			return
		}
	} else if !supportProviderReferencePattern.MatchString(value) {
		writeCodedError(w, http.StatusBadRequest, "support_lookup_invalid", "provider reference is invalid")
		return
	}

	paymentIDs, err := a.supportPaymentIDs(r, selected, value)
	if err != nil {
		writeDBError(w, err)
		return
	}
	if len(paymentIDs) > 1 {
		writeCodedError(w, http.StatusConflict, "support_lookup_ambiguous", "lookup matched more than one payment")
		return
	}

	response := supportLookupResponse{ProviderEvents: []supportProviderEvent{}, ReconciliationIssues: []reconciliationIssueResponse{}}
	if len(paymentIDs) == 1 {
		payment, err := a.supportPayment(r, paymentIDs[0])
		if err != nil {
			writeDBError(w, err)
			return
		}
		response.Payment = &payment
	}
	response.ProviderEvents, err = a.supportProviderEvents(r, selected, value, paymentIDs)
	if err != nil {
		writeDBError(w, err)
		return
	}
	if response.Payment == nil && len(response.ProviderEvents) == 0 {
		writeCodedError(w, http.StatusNotFound, "support_lookup_not_found", "payment or provider event not found")
		return
	}
	if response.Payment != nil {
		response.ReconciliationIssues, err = a.supportReconciliationIssues(r, response.Payment.ID)
		if err != nil {
			writeDBError(w, err)
			return
		}
	}
	if err := a.auditSupportLookup(r, selected, value, response.Payment); err != nil {
		a.log.Error("failed to audit payment support lookup", "lookup_key", selected, "error", err)
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *API) supportPaymentIDs(r *http.Request, key, value string) ([]uuid.UUID, error) {
	queries := map[string]string{
		"payment_id":          `SELECT id FROM payments WHERE id=$1::uuid`,
		"provider_order_id":   `SELECT id FROM payments WHERE provider='razorpay' AND provider_order_id=$1`,
		"provider_payment_id": `SELECT id FROM payments WHERE provider='razorpay' AND provider_payment_id=$1`,
		"event_id":            `SELECT billmesh_payment_id FROM provider_events WHERE provider='razorpay' AND provider_event_id=$1 AND billmesh_payment_id IS NOT NULL`,
		"request_id":          `SELECT id FROM payments WHERE request_id=$1 UNION SELECT billmesh_payment_id FROM provider_events WHERE request_id=$1 AND billmesh_payment_id IS NOT NULL`,
	}
	rows, err := a.pool.Query(r.Context(), queries[key], value)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]uuid.UUID, 0, 2)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (a *API) supportPayment(r *http.Request, id uuid.UUID) (supportPaymentRecord, error) {
	var payment supportPaymentRecord
	err := a.pool.QueryRow(r.Context(), `SELECT id,account_id,purpose,status,amount_minor,currency,provider_environment,
		provider_order_id,provider_payment_id,request_id,created_at,updated_at FROM payments WHERE id=$1`, id).
		Scan(&payment.ID, &payment.AccountID, &payment.Purpose, &payment.Status, &payment.AmountMinor, &payment.Currency,
			&payment.ProviderEnvironment, &payment.ProviderOrderID, &payment.ProviderPaymentID, &payment.RequestID, &payment.CreatedAt, &payment.UpdatedAt)
	return payment, err
}

func (a *API) supportProviderEvents(r *http.Request, key, value string, paymentIDs []uuid.UUID) ([]supportProviderEvent, error) {
	query := `SELECT provider_event_id,event_type,provider_order_id,provider_payment_id,provider_refund_id,request_id,processed_at,created_at
		FROM provider_events WHERE provider='razorpay' AND `
	var argument any = value
	switch key {
	case "event_id":
		query += `provider_event_id=$1`
	case "request_id":
		query += `request_id=$1`
	default:
		if len(paymentIDs) == 0 {
			return []supportProviderEvent{}, nil
		}
		query += `billmesh_payment_id=$1`
		argument = paymentIDs[0]
	}
	query += ` ORDER BY created_at,id LIMIT 100`
	rows, err := a.pool.Query(r.Context(), query, argument)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]supportProviderEvent, 0)
	for rows.Next() {
		var event supportProviderEvent
		if err := rows.Scan(&event.EventID, &event.EventType, &event.ProviderOrderID, &event.ProviderPaymentID, &event.ProviderRefundID,
			&event.RequestID, &event.ProcessedAt, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (a *API) supportReconciliationIssues(r *http.Request, paymentID uuid.UUID) ([]reconciliationIssueResponse, error) {
	rows, err := a.pool.Query(r.Context(), `SELECT id,provider,provider_environment,account_id,payment_id,issue_type,status,
		provider_order_id,provider_payment_id,provider_refund_id,summary,details,first_detected_at,last_detected_at,
		acknowledged_at,resolved_at,resolution_note,version FROM reconciliation_issues WHERE payment_id=$1 ORDER BY last_detected_at DESC,id LIMIT 100`, paymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	issues := make([]reconciliationIssueResponse, 0)
	for rows.Next() {
		var issue reconciliationIssueResponse
		if err := rows.Scan(&issue.ID, &issue.Provider, &issue.Environment, &issue.AccountID, &issue.PaymentID, &issue.IssueType,
			&issue.Status, &issue.ProviderOrderID, &issue.ProviderPaymentID, &issue.ProviderRefundID, &issue.Summary, &issue.Details,
			&issue.FirstDetectedAt, &issue.LastDetectedAt, &issue.AcknowledgedAt, &issue.ResolvedAt, &issue.ResolutionNote, &issue.Version); err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}

func (a *API) auditSupportLookup(r *http.Request, key, value string, payment *supportPaymentRecord) error {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		return errors.New("support lookup claims are missing")
	}
	resourceID := key + ":" + value
	var accountID any
	if payment != nil {
		resourceID = payment.ID.String()
		accountID = payment.AccountID
	}
	actorType := "user"
	if strings.HasPrefix(claims.Subject, "service:") {
		actorType = "service"
	}
	_, err := a.pool.Exec(r.Context(), `INSERT INTO audit_log(account_id,actor_subject,actor_type,action,resource_type,resource_id,reason,request_id)
		VALUES($1,$2,$3,'payment.support.lookup','payment',$4,$5,$6)`, accountID, claims.Subject, actorType, resourceID, key,
		r.Header.Get("X-Request-ID"))
	return err
}
