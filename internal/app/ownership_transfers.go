package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tociva/billmesh/internal/auth"
	"github.com/tociva/billmesh/internal/products"
)

func (a *API) createOwnershipTransfer(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.FromContext(r.Context())
	if claims.ActorType != "service" {
		writeCodedError(w, http.StatusForbidden, "trusted_service_required", "ownership changes require a trusted application service token")
		return
	}
	if strings.TrimSpace(r.Header.Get("If-Match")) == "" {
		writeCodedError(w, http.StatusPreconditionRequired, "precondition_required", "the current billing snapshot ETag is required")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		writeCodedError(w, http.StatusBadRequest, "idempotency_key_required", "a valid Idempotency-Key header is required")
		return
	}
	var in struct {
		NewOwnerRef string `json:"new_owner_ref"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.NewOwnerRef = strings.TrimSpace(in.NewOwnerRef)
	if in.NewOwnerRef == "" || len(in.NewOwnerRef) > 512 {
		writeCodedError(w, http.StatusBadRequest, "invalid_new_owner_ref", "new_owner_ref is required")
		return
	}
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeCodedError(w, http.StatusNotFound, "billing_account_not_found", "billing account not found")
		return
	}
	var productID uuid.UUID
	var policy products.BillingPolicy
	if err := a.pool.QueryRow(r.Context(), `SELECT id,billing_policy FROM products WHERE slug=$1 AND active`, claims.App).Scan(&productID, &policy); err != nil {
		writeDBError(w, err)
		return
	}
	if policy.Customer.Scope == "organization" {
		writeCodedError(w, http.StatusConflict, "ownership_transfer_not_applicable", "organization-scoped billing does not change customer identity when organization ownership changes")
		return
	}
	if policy.Customer.OwnershipTransfer == "unsupported" {
		writeCodedError(w, http.StatusConflict, "ownership_transfer_unsupported", "ownership transfer is disabled by product policy")
		return
	}
	digest := sha256.Sum256([]byte(in.NewOwnerRef))
	requestHash := hex.EncodeToString(digest[:])
	var existingID uuid.UUID
	var existingHash string
	err = a.pool.QueryRow(r.Context(), `SELECT id,request_hash FROM account_ownership_transfers
		WHERE account_id=$1 AND product_id=$2 AND idempotency_key=$3`, accountID, productID, key).Scan(&existingID, &existingHash)
	if err == nil {
		if existingHash != requestHash {
			writeCodedError(w, http.StatusConflict, "idempotency_conflict", "idempotency key was reused with a different request")
			return
		}
		a.writeOwnershipTransfer(w, r, existingID, http.StatusOK)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeDBError(w, err)
		return
	}

	var transferID uuid.UUID
	err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ownership:"+accountID.String()+":"+productID.String()); err != nil {
			return err
		}
		err := tx.QueryRow(r.Context(), `SELECT id,request_hash FROM account_ownership_transfers
			WHERE account_id=$1 AND product_id=$2 AND idempotency_key=$3`, accountID, productID, key).Scan(&existingID, &existingHash)
		if err == nil {
			if existingHash != requestHash {
				return errIdempotencyConflict
			}
			transferID = existingID
			return errOwnershipReplay
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE account_ownership_transfers SET status='expired',updated_at=now()
			WHERE account_id=$1 AND product_id=$2 AND status IN ('authorized','requires_paid_transition') AND expires_at<=now()`, accountID, productID); err != nil {
			return err
		}
		var currentCustomerID *uuid.UUID
		if err := tx.QueryRow(r.Context(), `SELECT customer_id FROM billing_accounts WHERE id=$1 FOR UPDATE`, accountID).Scan(&currentCustomerID); err != nil {
			return err
		}
		if currentCustomerID == nil {
			return errOwnershipCustomerMissing
		}
		issuer := claims.Issuer
		if policy.Customer.Scope == "external_customer" {
			issuer = "urn:billmesh:external-customer:" + claims.App + ":" + claims.Environment
		}
		var proposedCustomerID uuid.UUID
		if err := tx.QueryRow(r.Context(), `INSERT INTO billing_customers(issuer,external_subject)
			VALUES($1,$2) ON CONFLICT(issuer,external_subject) DO UPDATE SET updated_at=now() RETURNING id`, issuer, in.NewOwnerRef).Scan(&proposedCustomerID); err != nil {
			return err
		}
		if proposedCustomerID == *currentCustomerID {
			return errOwnershipSameCustomer
		}
		status, decisionCode, requiredAction := "authorized", "owner_eligible", "none"
		if policy.Customer.OwnershipTransfer == "preauthorized_recheck" || policy.Customer.OwnershipChange == "reevaluate" {
			var hasFree bool
			if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM subscriptions
				WHERE account_id=$1 AND product_id=$2 AND billing_model='free' AND status IN ('pending','active','past_due'))`, accountID, productID).Scan(&hasFree); err != nil {
				return err
			}
			if hasFree {
				eligible, err := freePlanEligibleForCustomer(r.Context(), tx, proposedCustomerID, productID, policy.Customer.FreeAllowance)
				if err != nil {
					return err
				}
				if !eligible && policy.Customer.IneligibleOwnerAction == "require_paid_checkout" {
					status, decisionCode, requiredAction = "requires_paid_transition", "new_owner_free_allowance_exhausted", "paid_transition"
				} else if !eligible {
					status, decisionCode, requiredAction = "ineligible", "new_owner_free_allowance_exhausted", "reject"
				}
			}
		}
		revision, err := ensureBillingRevision(r.Context(), tx, accountID, productID)
		if err != nil {
			return err
		}
		if match := strings.TrimSpace(r.Header.Get("If-Match")); match == "" || match != billingETag(revision) {
			return errOwnershipRevisionChanged
		}
		transferID = uuid.New()
		_, err = tx.Exec(r.Context(), `INSERT INTO account_ownership_transfers(id,account_id,product_id,current_customer_id,proposed_customer_id,
			status,decision_code,required_action,idempotency_key,request_hash,base_revision,expires_at,created_by)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now()+interval '15 minutes',$12)`, transferID, accountID, productID,
			*currentCustomerID, proposedCustomerID, status, decisionCode, requiredAction, key, requestHash, revision, claims.Subject)
		return err
	})
	if errors.Is(err, errOwnershipRevisionChanged) {
		writeCodedError(w, http.StatusPreconditionFailed, "stale_revision", "billing state changed; refresh the billing snapshot")
		return
	}
	if errors.Is(err, errOwnershipReplay) {
		a.writeOwnershipTransfer(w, r, transferID, http.StatusOK)
		return
	}
	if errors.Is(err, errIdempotencyConflict) {
		writeCodedError(w, http.StatusConflict, "idempotency_conflict", errIdempotencyConflict.Error())
		return
	}
	if err != nil {
		if writeOwnershipTransferError(w, err) {
			return
		}
		writeDBError(w, err)
		return
	}
	a.writeOwnershipTransfer(w, r, transferID, http.StatusCreated)
}

func (a *API) getOwnershipTransfer(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeCodedError(w, http.StatusBadRequest, "invalid_ownership_transfer_id", "invalid ownership transfer id")
		return
	}
	a.writeOwnershipTransfer(w, r, id, http.StatusOK)
}

func (a *API) writeOwnershipTransfer(w http.ResponseWriter, r *http.Request, id uuid.UUID, statusCode int) {
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeCodedError(w, http.StatusNotFound, "billing_account_not_found", "billing account not found")
		return
	}
	_, _ = a.pool.Exec(r.Context(), `UPDATE account_ownership_transfers SET status='expired',updated_at=now()
		WHERE id=$1 AND account_id=$2 AND status IN ('authorized','requires_paid_transition') AND expires_at<=now()`, id, accountID)
	var out ownershipTransferResponse
	err = a.pool.QueryRow(r.Context(), `SELECT id,status,decision_code,required_action,base_revision,expires_at,confirmed_at,cancelled_at
		FROM account_ownership_transfers WHERE id=$1 AND account_id=$2`, id, accountID).Scan(&out.ID, &out.Status, &out.DecisionCode,
		&out.RequiredAction, &out.BaseRevision, &out.ExpiresAt, &out.ConfirmedAt, &out.CancelledAt)
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, statusCode, out)
}

func (a *API) confirmOwnershipTransfer(w http.ResponseWriter, r *http.Request) {
	a.finishOwnershipTransfer(w, r, true)
}

func (a *API) cancelOwnershipTransfer(w http.ResponseWriter, r *http.Request) {
	a.finishOwnershipTransfer(w, r, false)
}

func (a *API) finishOwnershipTransfer(w http.ResponseWriter, r *http.Request, confirm bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeCodedError(w, http.StatusBadRequest, "invalid_ownership_transfer_id", "invalid ownership transfer id")
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key == "" || len(key) > 200 {
		writeCodedError(w, http.StatusBadRequest, "idempotency_key_required", "a valid Idempotency-Key header is required")
		return
	}
	if confirm && strings.TrimSpace(r.Header.Get("If-Match")) == "" {
		writeCodedError(w, http.StatusPreconditionRequired, "precondition_required", "the current billing snapshot ETag is required")
		return
	}
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeCodedError(w, http.StatusNotFound, "billing_account_not_found", "billing account not found")
		return
	}
	claims, _ := auth.FromContext(r.Context())
	if claims.ActorType != "service" {
		writeCodedError(w, http.StatusForbidden, "trusted_service_required", "ownership changes require a trusted application service token")
		return
	}
	err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		var productID, currentCustomerID, proposedCustomerID uuid.UUID
		var status string
		var baseRevision int64
		var expiresAt time.Time
		if err := tx.QueryRow(r.Context(), `SELECT product_id,current_customer_id,proposed_customer_id,status,base_revision,expires_at
			FROM account_ownership_transfers WHERE id=$1 AND account_id=$2 FOR UPDATE`, id, accountID).Scan(&productID, &currentCustomerID,
			&proposedCustomerID, &status, &baseRevision, &expiresAt); err != nil {
			return err
		}
		finalStatus := "cancelled"
		if confirm {
			finalStatus = "confirmed"
		}
		if status == finalStatus {
			return nil
		}
		if expiresAt.Before(time.Now().UTC()) {
			return errOwnershipExpired
		}
		if !confirm {
			if status != "authorized" && status != "requires_paid_transition" {
				return errOwnershipNotCancellable
			}
			_, err := tx.Exec(r.Context(), `UPDATE account_ownership_transfers SET status='cancelled',cancelled_at=now(),updated_at=now() WHERE id=$1`, id)
			return err
		}
		wasRequiresPaidTransition := status == "requires_paid_transition"
		if wasRequiresPaidTransition {
			var hasFree bool
			if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM subscriptions WHERE account_id=$1 AND product_id=$2
				AND billing_model='free' AND status IN ('pending','active','past_due'))`, accountID, productID).Scan(&hasFree); err != nil {
				return err
			}
			if hasFree {
				return errOwnershipPaidTransitionRequired
			}
			status = "authorized"
		}
		if status != "authorized" {
			return errOwnershipNotAuthorized
		}
		var policy products.BillingPolicy
		if err := tx.QueryRow(r.Context(), `SELECT billing_policy FROM products WHERE id=$1 FOR SHARE`, productID).Scan(&policy); err != nil {
			return err
		}
		if policy.Customer.OwnershipTransfer == "preauthorized_recheck" || policy.Customer.OwnershipChange == "reevaluate" {
			var hasFree bool
			if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM subscriptions WHERE account_id=$1 AND product_id=$2
				AND billing_model='free' AND status IN ('pending','active','past_due'))`, accountID, productID).Scan(&hasFree); err != nil {
				return err
			}
			if hasFree {
				if _, err := tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "free-customer:"+proposedCustomerID.String()); err != nil {
					return err
				}
				eligible, err := freePlanEligibleForCustomer(r.Context(), tx, proposedCustomerID, productID, policy.Customer.FreeAllowance)
				if err != nil {
					return err
				}
				if !eligible && policy.Customer.IneligibleOwnerAction == "require_paid_checkout" {
					return errOwnershipPaidTransitionRequired
				}
				if !eligible {
					return errOwnershipFreeIneligible
				}
			}
		}
		var revision int64
		if err := tx.QueryRow(r.Context(), `SELECT revision FROM billing_state_revisions WHERE account_id=$1 AND product_id=$2 FOR UPDATE`, accountID, productID).Scan(&revision); err != nil {
			return err
		}
		if match := strings.TrimSpace(r.Header.Get("If-Match")); match == "" || match != billingETag(revision) || !wasRequiresPaidTransition && revision != baseRevision {
			return errOwnershipRevisionChanged
		}
		var linkedCustomerID *uuid.UUID
		if err := tx.QueryRow(r.Context(), `SELECT customer_id FROM billing_accounts WHERE id=$1 FOR UPDATE`, accountID).Scan(&linkedCustomerID); err != nil {
			return err
		}
		if linkedCustomerID == nil || *linkedCustomerID != currentCustomerID {
			return errOwnershipRevisionChanged
		}
		if _, err := tx.Exec(r.Context(), `UPDATE billing_accounts SET customer_id=$2,updated_at=now() WHERE id=$1`, accountID, proposedCustomerID); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO billing_account_ownership_history(account_id,product_id,previous_customer_id,new_customer_id,transfer_id,actor_subject)
			VALUES($1,$2,$3,$4,$5,$6)`, accountID, productID, currentCustomerID, proposedCustomerID, id, claims.Subject); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE account_ownership_transfers SET status='confirmed',confirmed_at=now(),updated_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE billing_state_revisions SET revision=revision+1,updated_at=now() WHERE account_id=$1 AND product_id=$2`, accountID, productID); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO audit_log(account_id,actor_subject,actor_type,action,resource_type,resource_id,after_state)
			VALUES($1,$2,$3,'billing.ownership_transfer','billing_account',$4,$5)`, accountID, claims.Subject, claims.ActorType, accountID.String(), map[string]any{"transfer_id": id}); err != nil {
			return err
		}
		return ownershipEvent(r.Context(), tx, accountID, productID, id)
	})
	if errors.Is(err, errOwnershipRevisionChanged) {
		writeCodedError(w, http.StatusPreconditionFailed, "stale_revision", "billing state changed; create a new ownership transfer")
		return
	}
	if err != nil {
		if errors.Is(err, errOwnershipExpired) {
			_, _ = a.pool.Exec(r.Context(), `UPDATE account_ownership_transfers SET status='expired',updated_at=now()
				WHERE id=$1 AND account_id=$2 AND status IN ('authorized','requires_paid_transition')`, id, accountID)
		}
		if writeOwnershipTransferError(w, err) {
			return
		}
		writeDBError(w, err)
		return
	}
	a.writeOwnershipTransfer(w, r, id, http.StatusOK)
}

var (
	errOwnershipRevisionChanged        = errors.New("ownership transfer revision changed")
	errOwnershipReplay                 = errors.New("ownership transfer idempotent replay")
	errOwnershipCustomerMissing        = errors.New("billing account has no customer identity")
	errOwnershipSameCustomer           = errors.New("new owner already owns the billing account")
	errOwnershipExpired                = errors.New("ownership transfer expired")
	errOwnershipNotCancellable         = errors.New("ownership transfer cannot be cancelled")
	errOwnershipPaidTransitionRequired = errors.New("required paid transition has not completed")
	errOwnershipNotAuthorized          = errors.New("ownership transfer is not authorized")
	errOwnershipFreeIneligible         = errors.New("new owner is not eligible for the current free plan")
)

func writeOwnershipTransferError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, errOwnershipCustomerMissing):
		writeCodedError(w, http.StatusConflict, "billing_customer_missing", "billing account has no customer identity")
	case errors.Is(err, errOwnershipSameCustomer):
		writeCodedError(w, http.StatusConflict, "owner_unchanged", "new owner already owns the billing account")
	case errors.Is(err, errOwnershipExpired):
		writeCodedError(w, http.StatusConflict, "ownership_transfer_expired", "ownership transfer expired")
	case errors.Is(err, errOwnershipNotCancellable):
		writeCodedError(w, http.StatusConflict, "ownership_transfer_not_cancellable", "ownership transfer cannot be cancelled")
	case errors.Is(err, errOwnershipPaidTransitionRequired):
		writeCodedError(w, http.StatusConflict, "paid_transition_required", "the current free subscription must be changed to a paid plan before ownership can transfer")
	case errors.Is(err, errOwnershipNotAuthorized):
		writeCodedError(w, http.StatusConflict, "ownership_transfer_not_authorized", "ownership transfer is not authorized")
	case errors.Is(err, errOwnershipFreeIneligible):
		writeCodedError(w, http.StatusConflict, "new_owner_free_allowance_exhausted", "new owner is not eligible for the current free plan")
	default:
		return false
	}
	return true
}

func freePlanEligibleForCustomer(ctx context.Context, q billingQueryRower, customerID, productID uuid.UUID, allowance int) (bool, error) {
	if allowance <= 0 {
		return false, nil
	}
	var eligible bool
	err := q.QueryRow(ctx, `SELECT count(*) < $3 FROM billing_accounts a JOIN subscriptions s ON s.account_id=a.id
		WHERE a.customer_id=$1 AND s.product_id=$2 AND s.billing_model='free' AND s.status IN ('pending','active','past_due')`,
		customerID, productID, allowance).Scan(&eligible)
	return eligible, err
}

func ownershipEvent(ctx context.Context, tx pgx.Tx, accountID, productID, transferID uuid.UUID) error {
	payload, err := json.Marshal(map[string]any{"transfer_id": transferID, "snapshot_url": "/v1/billing-snapshot"})
	if err != nil {
		return err
	}
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM billing_state_revisions WHERE account_id=$1 AND product_id=$2`, accountID, productID).Scan(&revision); err != nil {
		return err
	}
	var eventID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,payload,account_id,product_id,billing_revision,schema_version)
		VALUES('account', $1, 'billing.ownership_transferred', $2, $1, $3, $4, '2') RETURNING id`, accountID, payload, productID, revision).Scan(&eventID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO webhook_deliveries(event_id,target_url,endpoint_id)
		SELECT $1,target_url,id FROM webhook_endpoints WHERE account_id=$2 AND active ON CONFLICT DO NOTHING`, eventID, accountID)
	return err
}
