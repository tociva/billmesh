package app

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tociva/billmesh/internal/auth"
)

var (
	errReconciliationIssueResolved     = errors.New("reconciliation issue is resolved")
	errReconciliationIssueNotRetryable = errors.New("reconciliation issue is not retryable")
)

type reconciliationIssueResponse struct {
	ID                uuid.UUID      `json:"id"`
	Provider          string         `json:"provider"`
	Environment       string         `json:"provider_environment"`
	AccountID         *uuid.UUID     `json:"account_id"`
	PaymentID         *uuid.UUID     `json:"payment_id"`
	IssueType         string         `json:"issue_type"`
	Status            string         `json:"status"`
	ProviderOrderID   *string        `json:"provider_order_id"`
	ProviderPaymentID *string        `json:"provider_payment_id"`
	ProviderRefundID  *string        `json:"provider_refund_id"`
	Summary           string         `json:"summary"`
	Details           map[string]any `json:"details"`
	FirstDetectedAt   time.Time      `json:"first_detected_at"`
	LastDetectedAt    time.Time      `json:"last_detected_at"`
	AcknowledgedAt    *time.Time     `json:"acknowledged_at"`
	ResolvedAt        *time.Time     `json:"resolved_at"`
	ResolutionNote    *string        `json:"resolution_note"`
	Version           int            `json:"version"`
}

func (a *API) listReconciliationIssues(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = "open"
	}
	if status != "open" && status != "acknowledged" && status != "resolved" && status != "all" {
		writeCodedError(w, http.StatusBadRequest, "reconciliation_status_invalid", "status must be open, acknowledged, resolved, or all")
		return
	}
	rows, err := a.pool.Query(r.Context(), `SELECT id,provider,provider_environment,account_id,payment_id,issue_type,status,
		provider_order_id,provider_payment_id,provider_refund_id,summary,details,first_detected_at,last_detected_at,
		acknowledged_at,resolved_at,resolution_note,version FROM reconciliation_issues
		WHERE ($1='all' OR status=$1) ORDER BY last_detected_at DESC,id LIMIT $2`, status, parseLimit(r))
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	items := make([]reconciliationIssueResponse, 0)
	for rows.Next() {
		var item reconciliationIssueResponse
		if err := rows.Scan(&item.ID, &item.Provider, &item.Environment, &item.AccountID, &item.PaymentID, &item.IssueType,
			&item.Status, &item.ProviderOrderID, &item.ProviderPaymentID, &item.ProviderRefundID, &item.Summary, &item.Details,
			&item.FirstDetectedAt, &item.LastDetectedAt, &item.AcknowledgedAt, &item.ResolvedAt, &item.ResolutionNote, &item.Version); err != nil {
			writeDBError(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (a *API) actOnReconciliationIssue(w http.ResponseWriter, r *http.Request) {
	issueID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeCodedError(w, http.StatusBadRequest, "reconciliation_issue_id_invalid", "reconciliation issue id is invalid")
		return
	}
	var input struct {
		Action  string `json:"action"`
		Note    string `json:"note"`
		Version int    `json:"version"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Action = strings.TrimSpace(input.Action)
	note, noteValid := safeResolutionNote(input.Note)
	if input.Version < 1 || (input.Action != "acknowledge" && input.Action != "retry" && input.Action != "resolve") {
		writeCodedError(w, http.StatusBadRequest, "reconciliation_action_invalid", "action and a positive version are required")
		return
	}
	if (input.Action == "resolve" || input.Action == "retry") && !noteValid {
		writeCodedError(w, http.StatusBadRequest, "reconciliation_note_invalid", "a note between 3 and 1000 characters is required")
		return
	}
	claims, _ := auth.FromContext(r.Context())
	var response reconciliationIssueResponse
	err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		var paymentID *uuid.UUID
		var accountID *uuid.UUID
		var status string
		if err := tx.QueryRow(r.Context(), `SELECT payment_id,account_id,status FROM reconciliation_issues
			WHERE id=$1 AND version=$2 FOR UPDATE`, issueID, input.Version).Scan(&paymentID, &accountID, &status); err != nil {
			return err
		}
		if status == "resolved" {
			return errReconciliationIssueResolved
		}
		switch input.Action {
		case "acknowledge":
			_, err = tx.Exec(r.Context(), `UPDATE reconciliation_issues SET status='acknowledged',acknowledged_at=COALESCE(acknowledged_at,now()),
				version=version+1 WHERE id=$1`, issueID)
		case "retry":
			if paymentID == nil {
				return errReconciliationIssueNotRetryable
			}
			tag, updateErr := tx.Exec(r.Context(), `UPDATE payments SET reconciliation_outcome='retry_scheduled',reconciliation_error_code=NULL,
				reconciliation_next_attempt_at=now() WHERE id=$1 AND status IN ('created','authorized')`, paymentID)
			if updateErr != nil {
				return updateErr
			}
			if tag.RowsAffected() != 1 {
				return errReconciliationIssueNotRetryable
			}
			_, err = tx.Exec(r.Context(), `UPDATE reconciliation_issues SET status='acknowledged',acknowledged_at=COALESCE(acknowledged_at,now()),
				resolution_note=$2,version=version+1 WHERE id=$1`, issueID, note)
		case "resolve":
			_, err = tx.Exec(r.Context(), `UPDATE reconciliation_issues SET status='resolved',resolved_at=now(),resolution_note=$2,
				version=version+1 WHERE id=$1`, issueID, note)
		}
		if err != nil {
			return err
		}
		actorType := "user"
		if strings.HasPrefix(claims.Subject, "service:") {
			actorType = "service"
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO audit_log(account_id,actor_subject,actor_type,action,resource_type,resource_id,reason,after_state)
			VALUES($1,$2,$3,$4,'reconciliation_issue',$5,$6,$7)`, accountID, claims.Subject, actorType,
			"reconciliation.issue."+input.Action, issueID.String(), note, map[string]any{"version": input.Version + 1}); err != nil {
			return err
		}
		return tx.QueryRow(r.Context(), `SELECT id,provider,provider_environment,account_id,payment_id,issue_type,status,
			provider_order_id,provider_payment_id,provider_refund_id,summary,details,first_detected_at,last_detected_at,
			acknowledged_at,resolved_at,resolution_note,version FROM reconciliation_issues WHERE id=$1`, issueID).
			Scan(&response.ID, &response.Provider, &response.Environment, &response.AccountID, &response.PaymentID, &response.IssueType,
				&response.Status, &response.ProviderOrderID, &response.ProviderPaymentID, &response.ProviderRefundID, &response.Summary,
				&response.Details, &response.FirstDetectedAt, &response.LastDetectedAt, &response.AcknowledgedAt, &response.ResolvedAt,
				&response.ResolutionNote, &response.Version)
	})
	if err != nil {
		switch err {
		case pgx.ErrNoRows:
			writeCodedError(w, http.StatusConflict, "reconciliation_issue_stale", "issue was not found or its version changed")
		case errReconciliationIssueResolved:
			writeCodedError(w, http.StatusConflict, "reconciliation_issue_resolved", "resolved issues cannot be changed")
		case errReconciliationIssueNotRetryable:
			writeCodedError(w, http.StatusConflict, "reconciliation_issue_not_retryable", "issue has no retryable Billmesh payment")
		default:
			writeDBError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, response)
}
