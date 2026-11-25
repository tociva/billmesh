package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	razorpayWebhookBodyLimit int64 = 1 << 20
	razorpayWebhookMaxDepth        = 64
)

var (
	razorpayEventIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	razorpaySignaturePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type razorpayWebhookEnvelope struct {
	Entity    string                 `json:"entity"`
	AccountID string                 `json:"account_id"`
	Event     string                 `json:"event"`
	Contains  []string               `json:"contains"`
	Payload   razorpayWebhookPayload `json:"payload"`
	CreatedAt int64                  `json:"created_at"`
}

type razorpayWebhookPayload struct {
	Payment razorpayPaymentPayload `json:"payment"`
	Order   razorpayOrderPayload   `json:"order"`
	Refund  razorpayRefundPayload  `json:"refund"`
}

type razorpayPaymentPayload struct {
	Entity razorpayPaymentEntity `json:"entity"`
}

type razorpayOrderPayload struct {
	Entity razorpayOrderEntity `json:"entity"`
}

type razorpayRefundPayload struct {
	Entity razorpayRefundEntity `json:"entity"`
}

type razorpayPaymentEntity struct {
	ID       string `json:"id"`
	Entity   string `json:"entity"`
	OrderID  string `json:"order_id"`
	Status   string `json:"status"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

type razorpayOrderEntity struct {
	ID       string `json:"id"`
	Entity   string `json:"entity"`
	Status   string `json:"status"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

type razorpayRefundEntity struct {
	ID        string `json:"id"`
	Entity    string `json:"entity"`
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
}

type normalizedRazorpayEvent struct {
	ID        string
	Type      string
	AccountID string
	PaymentID string
	OrderID   string
	Status    string
	RefundID  string
	Amount    int64
	Currency  string
	Supported bool
}

func (a *API) paymentWebhook(w http.ResponseWriter, r *http.Request) {
	release, admitted := a.admitWebhook(w, r)
	if !admitted {
		return
	}
	defer release()

	eventID, eventIDPresent := singleHeaderValue(r.Header, "X-Razorpay-Event-Id")
	if !eventIDPresent {
		writeError(w, http.StatusBadRequest, "Razorpay event ID header must occur exactly once")
		return
	}
	if !razorpayEventIDPattern.MatchString(eventID) {
		writeError(w, http.StatusBadRequest, "invalid Razorpay event ID")
		return
	}
	signature, signaturePresent := singleHeaderValue(r.Header, "X-Razorpay-Signature")
	if !signaturePresent {
		if len(r.Header.Values("X-Razorpay-Signature")) > 1 {
			writeError(w, http.StatusBadRequest, "Razorpay signature header must occur exactly once")
		} else {
			writeError(w, http.StatusUnauthorized, "invalid webhook signature")
		}
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, razorpayWebhookBodyLimit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "webhook body exceeds limit")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid webhook body")
		return
	}
	if a.webhookSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "webhook secret is not configured")
		return
	}
	if !verifyRazorpayWebhookSignature(raw, signature, a.webhookSecret, a.webhookPreviousSecret, a.webhookPreviousSecretExpiresAt, time.Now().UTC()) {
		writeError(w, http.StatusUnauthorized, "invalid webhook signature")
		return
	}

	event, err := parseRazorpayWebhook(eventID, raw, a.providerAccountID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if event.Supported {
		if err := a.validateRazorpayEvent(r.Context(), event); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := a.persistAndApplyRazorpayEvent(r.Context(), event, raw); err != nil {
		if errors.Is(err, errConflictingProviderEvent) {
			writeError(w, http.StatusConflict, "conflicting provider event")
			return
		}
		if errors.Is(err, errRefundMismatch) {
			if issueErr := a.recordRefundMismatch(r.Context(), event); issueErr != nil {
				a.log.Error("failed to record refund reconciliation issue", "event_id", event.ID, "refund_id", event.RefundID, "error", issueErr)
			}
			writeCodedError(w, http.StatusConflict, "refund_mismatch", "refund conflicts with the captured payment")
			return
		}
		writeDBError(w, err)
		return
	}
	a.logRazorpayWebhook(w.Header().Get("X-Request-ID"), event)
	writeNoContent(w)
}

func (a *API) logRazorpayWebhook(requestID string, event normalizedRazorpayEvent) {
	a.log.Info("processed Razorpay webhook",
		"request_id", requestID,
		"event_id", event.ID,
		"event_type", event.Type,
		"provider_account_id", event.AccountID,
		"provider_payment_id", event.PaymentID,
		"provider_order_id", event.OrderID,
		"provider_refund_id", event.RefundID,
		"supported", event.Supported)
}

func singleHeaderValue(header http.Header, name string) (string, bool) {
	values := header.Values(name)
	if len(values) != 1 {
		return "", false
	}
	return values[0], true
}

func verifyRazorpayWebhookSignature(raw []byte, signature, current, previous string, previousExpiresAt *time.Time, now time.Time) bool {
	if current == "" || !razorpaySignaturePattern.MatchString(signature) {
		return false
	}
	provided, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	validFor := func(secret string) bool {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(raw)
		return secureHMACEqual(mac.Sum(nil), provided)
	}
	if validFor(current) {
		return true
	}
	return previous != "" && previousExpiresAt != nil && now.Before(*previousExpiresAt) && validFor(previous)
}

func parseRazorpayWebhook(eventID string, raw []byte, expectedAccountID string) (normalizedRazorpayEvent, error) {
	if !utf8.Valid(raw) {
		return normalizedRazorpayEvent{}, errors.New("webhook payload is not valid UTF-8")
	}
	if err := validateJSONDepth(raw, razorpayWebhookMaxDepth); err != nil {
		return normalizedRazorpayEvent{}, err
	}
	var envelope razorpayWebhookEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return normalizedRazorpayEvent{}, errors.New("malformed webhook payload")
	}
	if envelope.Entity != "event" || envelope.Event == "" || envelope.AccountID == "" {
		return normalizedRazorpayEvent{}, errors.New("invalid Razorpay event envelope")
	}
	if expectedAccountID != "" && envelope.AccountID != expectedAccountID {
		return normalizedRazorpayEvent{}, errors.New("unexpected Razorpay merchant account")
	}

	event := normalizedRazorpayEvent{ID: eventID, Type: envelope.Event, AccountID: envelope.AccountID}
	switch envelope.Event {
	case "payment.captured", "payment.failed":
		payment := envelope.Payload.Payment.Entity
		if payment.Entity != "payment" || payment.ID == "" || payment.OrderID == "" || payment.Status == "" {
			return normalizedRazorpayEvent{}, errors.New("incomplete Razorpay payment entity")
		}
		event.PaymentID, event.OrderID, event.Status = payment.ID, payment.OrderID, payment.Status
		event.Amount, event.Currency, event.Supported = payment.Amount, payment.Currency, true
	case "order.paid":
		payment, order := envelope.Payload.Payment.Entity, envelope.Payload.Order.Entity
		if payment.Entity != "payment" || order.Entity != "order" || payment.ID == "" || order.ID == "" || payment.OrderID != order.ID || order.Status != "paid" {
			return normalizedRazorpayEvent{}, errors.New("incomplete or contradictory Razorpay order entity")
		}
		event.PaymentID, event.OrderID, event.Status = payment.ID, order.ID, order.Status
		event.Amount, event.Currency, event.Supported = payment.Amount, payment.Currency, true
	case "refund.processed":
		refund := envelope.Payload.Refund.Entity
		if refund.Entity != "refund" || refund.ID == "" || refund.PaymentID == "" || refund.Status != "processed" || refund.Amount <= 0 || refund.Currency == "" {
			return normalizedRazorpayEvent{}, errors.New("incomplete Razorpay refund entity")
		}
		event.PaymentID, event.RefundID, event.Status = refund.PaymentID, refund.ID, refund.Status
		event.Amount, event.Currency, event.Supported = refund.Amount, refund.Currency, true
	default:
		// Valid signed events outside Billmesh's financial allowlist are durably
		// recorded and acknowledged so Razorpay does not retry them indefinitely.
	}
	return event, nil
}

func validateJSONDepth(raw []byte, maximum int) error {
	depth := 0
	inString := false
	escaped := false
	for _, character := range raw {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > maximum {
				return errors.New("webhook JSON nesting exceeds limit")
			}
		case '}', ']':
			depth--
			if depth < 0 {
				return errors.New("malformed webhook payload")
			}
		}
	}
	if inString || depth != 0 {
		return errors.New("malformed webhook payload")
	}
	return nil
}

func (a *API) validateRazorpayEvent(ctx context.Context, event normalizedRazorpayEvent) error {
	switch event.Type {
	case "payment.captured":
		if event.Status != "captured" || event.Amount <= 0 || event.Currency == "" {
			return errors.New("incomplete or contradictory capture")
		}
	case "order.paid":
		if event.Status != "paid" || event.Amount <= 0 || event.Currency == "" {
			return errors.New("incomplete or contradictory capture")
		}
	case "payment.failed":
		if event.Status != "failed" {
			return errors.New("contradictory payment failure")
		}
		var exists bool
		if err := a.pool.QueryRow(ctx, `SELECT true FROM payments WHERE provider='razorpay' AND provider_order_id=$1 AND provider_environment=$2`, event.OrderID, a.providerEnvironment).Scan(&exists); err != nil || !exists {
			return errors.New("unknown payment order")
		}
		return nil
	case "refund.processed":
		return nil
	}
	var expectedAmount int64
	var expectedCurrency string
	if err := a.pool.QueryRow(ctx, `SELECT amount_minor,currency FROM payments WHERE provider='razorpay' AND provider_order_id=$1 AND provider_environment=$2`, event.OrderID, a.providerEnvironment).Scan(&expectedAmount, &expectedCurrency); err != nil {
		return errors.New("unknown payment order")
	}
	if event.Amount != expectedAmount || event.Currency != expectedCurrency {
		return errors.New("captured amount or currency does not match the order")
	}
	return nil
}

func (a *API) persistAndApplyRazorpayEvent(ctx context.Context, event normalizedRazorpayEvent, raw []byte) error {
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	return pgx.BeginFunc(ctx, a.pool, func(tx pgx.Tx) error {
		var inserted uuid.UUID
		err := tx.QueryRow(ctx, `INSERT INTO provider_events(provider,provider_event_id,event_type,payload,raw_payload,payload_sha256,provider_account_id)
			VALUES('razorpay',$1,$2,$3::jsonb,$4,$5,$6)
			ON CONFLICT(provider,provider_event_id) DO NOTHING RETURNING id`, event.ID, event.Type, string(raw), raw, digest, event.AccountID).Scan(&inserted)
		if errors.Is(err, pgx.ErrNoRows) {
			var identical bool
			if err := tx.QueryRow(ctx, `SELECT raw_payload=$2 AND event_type=$3 AND provider_account_id=$4
				FROM provider_events WHERE provider='razorpay' AND provider_event_id=$1`, event.ID, raw, event.Type, event.AccountID).Scan(&identical); err != nil {
				return err
			}
			if !identical {
				return errConflictingProviderEvent
			}
			return nil
		}
		if err != nil {
			return err
		}

		switch event.Type {
		case "payment.captured", "order.paid":
			err = capturePayment(ctx, tx, a.providerEnvironment, event.OrderID, event.PaymentID)
		case "payment.failed":
			var paymentID, accountID uuid.UUID
			err = tx.QueryRow(ctx, `UPDATE payments SET status='failed',provider_payment_id=COALESCE(NULLIF($2,''),provider_payment_id),
				failure_reason='provider reported failure',updated_at=now() WHERE provider='razorpay' AND provider_order_id=$1
				AND provider_environment=$3 AND status IN ('created','authorized') RETURNING id,account_id`, event.OrderID, event.PaymentID, a.providerEnvironment).Scan(&paymentID, &accountID)
			if errors.Is(err, pgx.ErrNoRows) {
				err = nil
			} else if err == nil {
				err = accountEvent(ctx, tx, accountID, "payment", paymentID, "payment.failed",
					map[string]any{"payment_id": paymentID, "reason": "provider_reported_failure"})
			}
		case "refund.processed":
			err = refundPayment(ctx, tx, event.PaymentID, event.RefundID, event.Amount, event.Currency)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE provider_events SET processed_at=now() WHERE id=$1`, inserted)
		}
		return err
	})
}

func (a *API) recordRefundMismatch(ctx context.Context, event normalizedRazorpayEvent) error {
	var paymentID, accountID uuid.UUID
	var orderID string
	err := a.pool.QueryRow(ctx, `SELECT id,account_id,COALESCE(provider_order_id,'') FROM payments
		WHERE provider='razorpay' AND provider_payment_id=$1`, event.PaymentID).Scan(&paymentID, &accountID, &orderID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	issue := reconciliationIssue{
		Environment: a.providerEnvironment, Type: "refund_mismatch", PaymentRef: event.PaymentID,
		RefundRef: event.RefundID, Summary: "provider refund could not be applied to Billmesh financial records",
		Details: map[string]any{"event_id": event.ID, "amount_minor": event.Amount, "currency": event.Currency},
	}
	if err == nil {
		issue.PaymentID = &paymentID
		issue.AccountID = &accountID
		issue.OrderID = orderID
	}
	return pgx.BeginFunc(ctx, a.pool, func(tx pgx.Tx) error {
		return upsertReconciliationIssue(ctx, tx, issue)
	})
}
