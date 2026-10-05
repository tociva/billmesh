package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tociva/billmesh/internal/auth"
	"github.com/tociva/billmesh/internal/products"
)

var errConflictingProviderEvent = errors.New("conflicting provider event")

var supportedWebhookEventTypes = map[string]struct{}{
	"billing.ownership_transferred":       {},
	"credits.expired":                     {},
	"credits.granted":                     {},
	"payment.failed":                      {},
	"payment.late_capture":                {},
	"payment.refunded":                    {},
	"payment.succeeded":                   {},
	"subscription.cancelled":              {},
	"subscription.cancellation_scheduled": {},
	"subscription.cancellation_withdrawn": {},
	"subscription.expired":                {},
	"subscription.past_due":               {},
	"subscription.renewed":                {},
	"subscription.transition_completed":   {},
}

func validateWebhookEventTypes(values []string) error {
	for _, value := range values {
		if _, ok := supportedWebhookEventTypes[value]; !ok {
			return errors.New("unsupported webhook event type " + value)
		}
	}
	return nil
}

func (a *API) createPaymentOrder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CreditPackID uuid.UUID `json:"credit_pack_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.CreditPackID == uuid.Nil {
		writeCodedError(w, http.StatusBadRequest, "credit_pack_id_required", "credit_pack_id is required")
		return
	}
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeCodedError(w, http.StatusNotFound, "billing_account_not_found", "billing account not found")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		writeCodedError(w, http.StatusBadRequest, "idempotency_key_required", "a valid Idempotency-Key header is required")
		return
	}
	conn, err := a.pool.Acquire(r.Context())
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer conn.Release()
	lockKey := "credit-pack-order:" + accountID.String() + ":" + key
	if _, err := conn.Exec(r.Context(), `SELECT pg_advisory_lock(hashtextextended($1,0))`, lockKey); err != nil {
		writeDBError(w, err)
		return
	}
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockContext, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, lockKey)
	}()
	operationRef := "credit-pack:" + accountID.String() + ":" + key
	var existingID, existingPackID uuid.UUID
	err = conn.QueryRow(r.Context(), `SELECT id,credit_pack_id FROM payments WHERE operation_ref=$1`, operationRef).Scan(&existingID, &existingPackID)
	if err == nil {
		if existingPackID != in.CreditPackID {
			writeCodedError(w, http.StatusConflict, "idempotency_conflict", "idempotency key was reused with a different request")
			return
		}
		a.writeCreditPackOrder(w, r, existingID, http.StatusOK)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeDBError(w, err)
		return
	}
	var packID, productID uuid.UUID
	var credits, price int64
	var currency string
	var policy products.BillingPolicy
	err = conn.QueryRow(r.Context(), `SELECT cp.id,cp.product_id,cp.credits,cp.price_minor,cp.currency,p.billing_policy
		FROM credit_packs cp JOIN products p ON p.id=cp.product_id WHERE cp.id=$1 AND cp.active AND p.active`, in.CreditPackID).
		Scan(&packID, &productID, &credits, &price, &currency, &policy)
	if err != nil {
		writeCodedError(w, http.StatusBadRequest, "credit_pack_unavailable", "credit pack is not available")
		return
	}
	if !a.canAccessProduct(r, productID) {
		writeError(w, 403, "product not accessible")
		return
	}
	if !a.paymentProviderConfigured() {
		writeCodedError(w, http.StatusServiceUnavailable, "checkout_not_configured", "payment checkout is not configured")
		return
	}
	if policy.Checkout.Presentation == "provider_hosted" && a.providerCheckoutURL == "" {
		writeCodedError(w, http.StatusServiceUnavailable, "checkout_not_configured", "provider-hosted checkout is not configured")
		return
	}
	providerOrder, err := a.createProviderOrder(r.Context(), price, currency, operationRef)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "payment provider unavailable")
		return
	}
	orderID, _ := providerOrder["id"].(string)
	if orderID == "" {
		writeCodedError(w, http.StatusBadGateway, "invalid_provider_response", "payment provider returned an invalid order")
		return
	}
	var id uuid.UUID
	expiresAt := time.Now().UTC().Add(30 * time.Minute)
	err = conn.QueryRow(r.Context(), `INSERT INTO payments(account_id,provider,provider_order_id,status,amount_minor,currency,credit_pack_id,credits,operation_ref,checkout_expires_at)
		VALUES($1,'razorpay',$2,'created',$3,$4,$5,$6,$7,$8) RETURNING id`, accountID, orderID, price, currency, packID, credits, operationRef, expiresAt).Scan(&id)
	if err != nil {
		writeDBError(w, err)
		return
	}
	a.writeCreditPackOrder(w, r, id, http.StatusCreated)
}

func (a *API) writeCreditPackOrder(w http.ResponseWriter, r *http.Request, paymentID uuid.UUID, statusCode int) {
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeCodedError(w, http.StatusNotFound, "billing_account_not_found", "billing account not found")
		return
	}
	var packID uuid.UUID
	var orderID, currency string
	var amount, credits int64
	var expiresAt *time.Time
	var policy products.BillingPolicy
	err = a.pool.QueryRow(r.Context(), `SELECT p.credit_pack_id,p.provider_order_id,p.amount_minor,p.currency,p.credits,p.checkout_expires_at,pr.billing_policy
		FROM payments p JOIN credit_packs cp ON cp.id=p.credit_pack_id JOIN products pr ON pr.id=cp.product_id
		WHERE p.id=$1 AND p.account_id=$2`, paymentID, accountID).Scan(&packID, &orderID, &amount, &currency, &credits, &expiresAt, &policy)
	if err != nil {
		writeDBError(w, err)
		return
	}
	checkout := checkoutResponse{Provider: "razorpay", Presentation: policy.Checkout.Presentation, ExpiresAt: expiresAt}
	if policy.Checkout.Presentation == "provider_hosted" {
		value := a.providerCheckoutURL + "?order_id=" + url.QueryEscape(orderID)
		checkout.CheckoutURL = &value
	} else {
		checkout.ClientConfig = &checkoutClientConfig{PublicKey: a.providerPublicKey, OrderID: orderID, AmountMinor: amount, Currency: currency}
	}
	writeJSON(w, statusCode, paymentOrderResponse{PaymentID: paymentID, CreditPackID: packID, Credits: credits, Checkout: checkout})
}

func (a *API) paymentWebhook(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, 400, "invalid webhook body")
		return
	}
	signature := r.Header.Get("X-Razorpay-Signature")
	if a.webhookSecret == "" {
		writeError(w, 503, "webhook secret is not configured")
		return
	}
	mac := hmac.New(sha256.New, []byte(a.webhookSecret))
	mac.Write(raw)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		writeError(w, 401, "invalid webhook signature")
		return
	}
	var event struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		PaymentID string `json:"payment_id"`
		OrderID   string `json:"order_id"`
		Status    string `json:"status"`
		RefundID  string `json:"refund_id"`
		Amount    int64  `json:"amount_minor"`
		Currency  string `json:"currency"`
		Payload   struct {
			Payment struct {
				Entity struct {
					ID       string `json:"id"`
					OrderID  string `json:"order_id"`
					Status   string `json:"status"`
					Amount   int64  `json:"amount"`
					Currency string `json:"currency"`
				} `json:"entity"`
			} `json:"payment"`
			Refund struct {
				Entity struct {
					ID        string `json:"id"`
					PaymentID string `json:"payment_id"`
					Amount    int64  `json:"amount"`
				} `json:"entity"`
			} `json:"refund"`
		} `json:"payload"`
	}
	if json.Unmarshal(raw, &event) != nil || event.ID == "" || event.Type == "" {
		writeError(w, 400, "malformed webhook payload")
		return
	}
	if event.PaymentID == "" {
		event.PaymentID = event.Payload.Payment.Entity.ID
	}
	if event.OrderID == "" {
		event.OrderID = event.Payload.Payment.Entity.OrderID
	}
	if event.Status == "" {
		event.Status = event.Payload.Payment.Entity.Status
	}
	if event.Amount == 0 {
		event.Amount = event.Payload.Payment.Entity.Amount
	}
	if event.Currency == "" {
		event.Currency = event.Payload.Payment.Entity.Currency
	}
	if event.RefundID == "" {
		event.RefundID = event.Payload.Refund.Entity.ID
	}
	if event.Type == "refund.processed" {
		if event.PaymentID == "" {
			event.PaymentID = event.Payload.Refund.Entity.PaymentID
		}
		if event.Amount == 0 {
			event.Amount = event.Payload.Refund.Entity.Amount
		}
	}
	switch event.Type {
	case "payment.captured", "order.paid", "payment.failed", "refund.processed":
	default:
		writeError(w, 400, "unsupported webhook event type")
		return
	}
	if event.Type == "payment.captured" || event.Type == "order.paid" {
		if event.PaymentID == "" || event.Amount <= 0 || event.Currency == "" || (event.Type == "payment.captured" && event.Status != "captured") || (event.Type == "order.paid" && event.Status != "paid" && event.Status != "captured") {
			writeError(w, 400, "incomplete or contradictory capture")
			return
		}
		var expectedAmount int64
		var expectedCurrency string
		if err := a.pool.QueryRow(r.Context(), `SELECT amount_minor,currency FROM payments WHERE provider='razorpay' AND provider_order_id=$1`, event.OrderID).Scan(&expectedAmount, &expectedCurrency); err != nil {
			writeError(w, 400, "unknown payment order")
			return
		}
		if event.Amount != expectedAmount || event.Currency != expectedCurrency {
			writeError(w, 400, "captured amount or currency does not match the order")
			return
		}
	}
	err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		var inserted uuid.UUID
		err := tx.QueryRow(r.Context(), `INSERT INTO provider_events(provider,provider_event_id,event_type,payload) VALUES('razorpay',$1,$2,$3) ON CONFLICT(provider,provider_event_id) DO NOTHING RETURNING id`, event.ID, event.Type, raw).Scan(&inserted)
		if errors.Is(err, pgx.ErrNoRows) {
			var identical bool
			if err := tx.QueryRow(r.Context(), `SELECT payload=$2::jsonb FROM provider_events WHERE provider='razorpay' AND provider_event_id=$1`, event.ID, raw).Scan(&identical); err != nil {
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
			err = capturePayment(r.Context(), tx, event.OrderID, event.PaymentID)
		case "payment.failed":
			var paymentID, accountID uuid.UUID
			err = tx.QueryRow(r.Context(), `UPDATE payments SET status='failed',provider_payment_id=COALESCE(NULLIF($2,''),provider_payment_id),
				failure_reason='provider reported failure',updated_at=now() WHERE provider='razorpay' AND provider_order_id=$1
				AND status IN ('created','authorized') RETURNING id,account_id`, event.OrderID, event.PaymentID).Scan(&paymentID, &accountID)
			if errors.Is(err, pgx.ErrNoRows) {
				err = nil
			} else if err == nil {
				err = accountEvent(r.Context(), tx, accountID, "payment", paymentID, "payment.failed",
					map[string]any{"payment_id": paymentID, "reason": "provider_reported_failure"})
			}
		case "refund.processed":
			err = refundPayment(r.Context(), tx, event.PaymentID, event.RefundID, event.Amount)
		}
		if err == nil {
			_, err = tx.Exec(r.Context(), `UPDATE provider_events SET processed_at=now() WHERE id=$1`, inserted)
		}
		return err
	})
	if err != nil {
		if errors.Is(err, errConflictingProviderEvent) {
			writeError(w, 409, "conflicting provider event")
			return
		}
		writeDBError(w, err)
		return
	}
	writeNoContent(w)
}

func refundPayment(ctx context.Context, tx pgx.Tx, providerPaymentID, providerRefundID string, refundAmount int64) error {
	var purpose string
	if err := tx.QueryRow(ctx, `SELECT purpose FROM payments WHERE provider='razorpay' AND provider_payment_id=$1`, providerPaymentID).Scan(&purpose); err != nil {
		return err
	}
	if purpose == "subscription" {
		return refundSubscriptionPayment(ctx, tx, providerPaymentID, providerRefundID, refundAmount)
	}
	return refundCreditPackPayment(ctx, tx, providerPaymentID, providerRefundID, refundAmount)
}

func refundCreditPackPayment(ctx context.Context, tx pgx.Tx, providerPaymentID, providerRefundID string, refundAmount int64) error {
	if providerPaymentID == "" || providerRefundID == "" || refundAmount <= 0 {
		return errors.New("refund event is missing payment, refund, or amount")
	}
	var paymentID, accountID, productID uuid.UUID
	var amount, credits int64
	var currency, status string
	if err := tx.QueryRow(ctx, `SELECT p.id,p.account_id,cp.product_id,p.amount_minor,p.credits,p.currency,p.status
		FROM payments p JOIN credit_packs cp ON cp.id=p.credit_pack_id
		WHERE p.provider='razorpay' AND p.provider_payment_id=$1 FOR UPDATE`, providerPaymentID).
		Scan(&paymentID, &accountID, &productID, &amount, &credits, &currency, &status); err != nil {
		return err
	}
	if status != "captured" && status != "refunded" {
		return errors.New("payment is not refundable")
	}
	var refunded int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount_minor),0) FROM refunds WHERE payment_id=$1`, paymentID).Scan(&refunded); err != nil {
		return err
	}
	if refunded+refundAmount > amount {
		return errors.New("refund exceeds captured amount")
	}
	creditsToReverse := credits * refundAmount / amount
	if refunded+refundAmount == amount {
		var alreadyReversed int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(credits_reversed),0) FROM refunds WHERE payment_id=$1`, paymentID).Scan(&alreadyReversed); err != nil {
			return err
		}
		creditsToReverse = credits - alreadyReversed
	}
	var walletID, grantID uuid.UUID
	var remaining int64
	if err := tx.QueryRow(ctx, `SELECT w.id,g.id,g.remaining FROM wallets w JOIN credit_grants g ON g.wallet_id=w.id
		WHERE w.account_id=$1 AND w.product_id=$2 AND g.operation_ref=$3 FOR UPDATE OF w,g`, accountID, productID, "payment:"+paymentID.String()).Scan(&walletID, &grantID, &remaining); err != nil {
		return err
	}
	if creditsToReverse > remaining {
		creditsToReverse = remaining
	}
	var refundID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO refunds(payment_id,provider_refund_id,amount_minor,credits_reversed)
		VALUES($1,$2,$3,$4) ON CONFLICT(provider_refund_id) DO NOTHING RETURNING id`, paymentID, providerRefundID, refundAmount, creditsToReverse).Scan(&refundID); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if creditsToReverse > 0 {
		if _, err := tx.Exec(ctx, `UPDATE credit_grants SET remaining=remaining-$2 WHERE id=$1`, grantID, creditsToReverse); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE wallets SET available=available-$2 WHERE id=$1`, walletID, creditsToReverse); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO credit_ledger(wallet_id,grant_id,operation_ref,kind,available_delta,reserved_delta)
			VALUES($1,$2,$3,'refund',$4,0)`, walletID, grantID, "refund:"+providerRefundID, -creditsToReverse); err != nil {
			return err
		}
	}
	newStatus := "captured"
	if refunded+refundAmount == amount {
		newStatus = "refunded"
	}
	if _, err := tx.Exec(ctx, `UPDATE payments SET status=$2,updated_at=now() WHERE id=$1`, paymentID, newStatus); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO credit_notes(invoice_id,refund_id,credit_note_number,currency,amount_minor)
		SELECT id,$2,$3,$4,$5 FROM invoices WHERE payment_id=$1 ON CONFLICT(refund_id) DO NOTHING`, paymentID, refundID, "CN-"+time.Now().UTC().Format("20060102")+"-"+refundID.String()[:8], currency, refundAmount); err != nil {
		return err
	}
	return appEvent(ctx, tx, "wallet", walletID, "payment.refunded", map[string]any{"payment_id": paymentID, "refund_id": refundID, "amount_minor": refundAmount, "credits_reversed": creditsToReverse})
}
func capturePayment(ctx context.Context, tx pgx.Tx, orderID, paymentID string) error {
	var purpose string
	if err := tx.QueryRow(ctx, `SELECT purpose FROM payments WHERE provider='razorpay' AND provider_order_id=$1`, orderID).Scan(&purpose); err != nil {
		return err
	}
	if purpose == "subscription" {
		return captureSubscriptionPayment(ctx, tx, orderID, paymentID)
	}
	return captureCreditPackPayment(ctx, tx, orderID, paymentID)
}

func captureCreditPackPayment(ctx context.Context, tx pgx.Tx, orderID, paymentID string) error {
	var payment, account, pack, product uuid.UUID
	var credits, amount int64
	var currency string
	err := tx.QueryRow(ctx, `SELECT p.id,p.account_id,p.credit_pack_id,cp.product_id,p.credits,p.amount_minor,p.currency FROM payments p JOIN credit_packs cp ON cp.id=p.credit_pack_id WHERE p.provider='razorpay' AND p.provider_order_id=$1 FOR UPDATE`, orderID).Scan(&payment, &account, &pack, &product, &credits, &amount, &currency)
	if err != nil {
		return err
	}
	var current string
	var existingPaymentID *string
	if err = tx.QueryRow(ctx, `SELECT status,provider_payment_id FROM payments WHERE id=$1`, payment).Scan(&current, &existingPaymentID); err != nil {
		return err
	}
	if current == "captured" || current == "refunded" {
		if existingPaymentID == nil || *existingPaymentID != paymentID {
			return errConflictingProviderEvent
		}
		return nil
	}
	if _, err = tx.Exec(ctx, `UPDATE payments SET status='captured',provider_payment_id=$2,updated_at=now() WHERE id=$1`, payment, paymentID); err != nil {
		return err
	}
	var wallet uuid.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO wallets(account_id,product_id) VALUES($1,$2) ON CONFLICT(account_id,product_id) DO UPDATE SET account_id=excluded.account_id RETURNING id`, account, product).Scan(&wallet); err != nil {
		return err
	}
	operation := "payment:" + payment.String()
	var grant uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO credit_grants(wallet_id,source,operation_ref,amount,remaining) VALUES($1,'purchase',$2,$3,$3) ON CONFLICT(operation_ref) DO NOTHING RETURNING id`, wallet, operation, credits).Scan(&grant)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE wallets SET available=available+$2 WHERE id=$1`, wallet, credits); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO credit_ledger(wallet_id,grant_id,operation_ref,kind,available_delta,reserved_delta) VALUES($1,$2,$3,'grant',$4,0)`, wallet, grant, "grant:"+operation, credits); err != nil {
		return err
	}
	invoiceNumber := "INV-" + time.Now().UTC().Format("20060102") + "-" + payment.String()[:8]
	if _, err = tx.Exec(ctx, `INSERT INTO invoices(account_id,payment_id,billing_operation_ref,invoice_number,status,currency,subtotal_minor,total_minor,finalized_at,paid_at) VALUES($1,$2,$3,$4,'paid',$5,$6,$6,now(),now()) ON CONFLICT(billing_operation_ref) DO NOTHING`, account, payment, operation, invoiceNumber, currency, amount); err != nil {
		return err
	}
	return appEvent(ctx, tx, "wallet", wallet, "payment.succeeded", map[string]any{"payment_id": payment, "credits": credits})
}

func captureSubscriptionPayment(ctx context.Context, tx pgx.Tx, orderID, providerPaymentID string) error {
	var paymentID, accountID, transitionID uuid.UUID
	var currentStatus, currency string
	var existingPaymentID *string
	var amount int64
	if err := tx.QueryRow(ctx, `SELECT p.id,p.account_id,p.transition_id,p.status,p.provider_payment_id,p.amount_minor,p.currency
		FROM payments p WHERE p.provider='razorpay' AND p.provider_order_id=$1 AND p.purpose='subscription' FOR UPDATE`, orderID).
		Scan(&paymentID, &accountID, &transitionID, &currentStatus, &existingPaymentID, &amount, &currency); err != nil {
		return err
	}
	if currentStatus == "captured" || currentStatus == "refunded" {
		if existingPaymentID == nil || *existingPaymentID != providerPaymentID {
			return errConflictingProviderEvent
		}
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE payments SET status='captured',provider_payment_id=$2,updated_at=now() WHERE id=$1`, paymentID, providerPaymentID); err != nil {
		return err
	}
	var transitionStatus, effective string
	var checkoutExpires, effectiveAt *time.Time
	var existingSubscriptionID *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT status,checkout_expires_at,effective,effective_at,subscription_id FROM subscription_transitions WHERE id=$1 FOR UPDATE`, transitionID).
		Scan(&transitionStatus, &checkoutExpires, &effective, &effectiveAt, &existingSubscriptionID); err != nil {
		return err
	}
	if transitionStatus != "requires_payment" && transitionStatus != "processing" {
		return accountEvent(ctx, tx, accountID, "payment", paymentID, "payment.late_capture",
			map[string]any{"payment_id": paymentID, "transition_id": transitionID, "reason": "transition_" + transitionStatus})
	}
	if checkoutExpires != nil && checkoutExpires.Before(time.Now().UTC()) {
		if _, err := tx.Exec(ctx, `UPDATE subscription_transitions SET status='expired',failure_code='late_capture',updated_at=now() WHERE id=$1`, transitionID); err != nil {
			return err
		}
		return accountEvent(ctx, tx, accountID, "payment", paymentID, "payment.late_capture",
			map[string]any{"payment_id": paymentID, "transition_id": transitionID, "reason": "checkout_expired"})
	}
	deferred := effective == "period_end" && effectiveAt != nil && effectiveAt.After(time.Now().UTC())
	if deferred {
		if existingSubscriptionID == nil {
			return errors.New("a deferred paid transition requires an existing subscription")
		}
		if _, err := tx.Exec(ctx, `UPDATE subscription_transitions SET status='processing',failure_code=NULL,updated_at=now() WHERE id=$1`, transitionID); err != nil {
			return err
		}
	} else if err := applySubscriptionTransition(ctx, tx, transitionID, providerPaymentID); err != nil {
		return err
	}
	var subscriptionID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT subscription_id FROM subscription_transitions WHERE id=$1`, transitionID).Scan(&subscriptionID); err != nil {
		return err
	}
	invoiceNumber := "INV-" + time.Now().UTC().Format("20060102") + "-" + paymentID.String()[:8]
	operation := "payment:" + paymentID.String()
	if _, err := tx.Exec(ctx, `INSERT INTO invoices(account_id,subscription_id,payment_id,billing_operation_ref,invoice_number,status,currency,
		subtotal_minor,total_minor,finalized_at,paid_at) VALUES($1,$2,$3,$4,$5,'paid',$6,$7,$7,now(),now())
		ON CONFLICT(billing_operation_ref) DO NOTHING`, accountID, subscriptionID, paymentID, operation, invoiceNumber, currency, amount); err != nil {
		return err
	}
	return accountEvent(ctx, tx, accountID, "payment", paymentID, "payment.succeeded",
		map[string]any{"payment_id": paymentID, "transition_id": transitionID, "subscription_id": subscriptionID, "deferred": deferred})
}

func refundSubscriptionPayment(ctx context.Context, tx pgx.Tx, providerPaymentID, providerRefundID string, refundAmount int64) error {
	if providerRefundID == "" || refundAmount <= 0 {
		return errors.New("refund event is missing refund or amount")
	}
	var paymentID, accountID, transitionID, subscriptionID, productID uuid.UUID
	var amount int64
	var currency, status string
	var policy products.BillingPolicy
	if err := tx.QueryRow(ctx, `SELECT p.id,p.account_id,p.transition_id,t.subscription_id,t.product_id,p.amount_minor,p.currency,p.status,s.billing_policy
		FROM payments p JOIN subscription_transitions t ON t.id=p.transition_id
		JOIN subscriptions s ON s.id=t.subscription_id
		WHERE p.provider='razorpay' AND p.provider_payment_id=$1 FOR UPDATE OF p,t`, providerPaymentID).
		Scan(&paymentID, &accountID, &transitionID, &subscriptionID, &productID, &amount, &currency, &status, &policy); err != nil {
		return err
	}
	if status != "captured" && status != "refunded" {
		return errors.New("payment is not refundable")
	}
	var refunded int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount_minor),0) FROM refunds WHERE payment_id=$1`, paymentID).Scan(&refunded); err != nil {
		return err
	}
	if refunded+refundAmount > amount {
		return errors.New("refund exceeds captured amount")
	}
	var refundID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO refunds(payment_id,provider_refund_id,amount_minor,credits_reversed)
		VALUES($1,$2,$3,0) ON CONFLICT(provider_refund_id) DO NOTHING RETURNING id`, paymentID, providerRefundID, refundAmount).Scan(&refundID); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	fullyRefunded := refunded+refundAmount == amount
	if fullyRefunded && policy.Lifecycle.RefundEntitlements == "revoke" {
		var walletID, grantID uuid.UUID
		var remaining int64
		err := tx.QueryRow(ctx, `SELECT w.id,g.id,g.remaining FROM wallets w JOIN credit_grants g ON g.wallet_id=w.id
			WHERE w.account_id=$1 AND w.product_id=$2 AND g.source='subscription' AND g.operation_ref LIKE $3
			ORDER BY g.created_at DESC LIMIT 1 FOR UPDATE OF w,g`, accountID, productID, "subscription:"+subscriptionID.String()+":%").
			Scan(&walletID, &grantID, &remaining)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && remaining > 0 {
			if _, err := tx.Exec(ctx, `UPDATE credit_grants SET remaining=0 WHERE id=$1`, grantID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE wallets SET available=available-$2 WHERE id=$1`, walletID, remaining); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE refunds SET credits_reversed=$2 WHERE id=$1`, refundID, remaining); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO credit_ledger(wallet_id,grant_id,operation_ref,kind,available_delta,reserved_delta)
				VALUES($1,$2,$3,'refund',$4,0)`, walletID, grantID, "refund:"+providerRefundID, -remaining); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE subscriptions SET status='cancelled',cancelled_at=now(),cancel_at_period_end=false,
			updated_at=now(),version=version+1 WHERE id=$1`, subscriptionID); err != nil {
			return err
		}
	}
	if fullyRefunded {
		if _, err := tx.Exec(ctx, `UPDATE payments SET status='refunded',updated_at=now() WHERE id=$1`, paymentID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO credit_notes(invoice_id,refund_id,credit_note_number,currency,amount_minor)
		SELECT id,$2,$3,$4,$5 FROM invoices WHERE payment_id=$1 ON CONFLICT(refund_id) DO NOTHING`, paymentID, refundID,
		"CN-"+time.Now().UTC().Format("20060102")+"-"+refundID.String()[:8], currency, refundAmount); err != nil {
		return err
	}
	return accountEvent(ctx, tx, accountID, "subscription", subscriptionID, "payment.refunded",
		map[string]any{"payment_id": paymentID, "transition_id": transitionID, "refund_id": refundID, "amount_minor": refundAmount,
			"full": fullyRefunded, "entitlement_policy": policy.Lifecycle.RefundEntitlements})
}

func (a *API) listPayments(w http.ResponseWriter, r *http.Request) {
	account, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "account not found")
		return
	}
	claims, _ := auth.FromContext(r.Context())
	beforeTime, beforeID, err := decodeListCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeCodedError(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
		return
	}
	limit := parseLimit(r)
	rows, err := a.pool.Query(r.Context(), `SELECT pay.id,pay.provider,pay.provider_order_id,pay.provider_payment_id,pay.status,pay.amount_minor,pay.currency,pay.created_at
		FROM payments pay LEFT JOIN credit_packs cp ON cp.id=pay.credit_pack_id
		LEFT JOIN subscription_transitions st ON st.id=pay.transition_id
		JOIN products p ON p.id=COALESCE(cp.product_id,st.product_id)
		WHERE pay.account_id=$1 AND (p.slug=$2 OR $3)
		AND ($4::timestamptz IS NULL OR (pay.created_at,pay.id)<($4,$5))
		ORDER BY pay.created_at DESC,pay.id DESC LIMIT $6`, account, claims.App, claims.Has("billing:link"), beforeTime, beforeID, limit+1)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	items := make([]paymentResponse, 0, limit+1)
	for rows.Next() {
		var item paymentResponse
		if err := rows.Scan(&item.ID, &item.Provider, &item.ProviderOrderID, &item.ProviderPaymentID, &item.Status, &item.AmountMinor, &item.Currency, &item.CreatedAt); err != nil {
			writeDBError(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeDBError(w, err)
		return
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		cursor := encodeListCursor(items[len(items)-1].CreatedAt, items[len(items)-1].ID)
		next = &cursor
	}
	writeJSON(w, http.StatusOK, paymentListResponse{Items: items, NextCursor: next})
}
func (a *API) listInvoices(w http.ResponseWriter, r *http.Request) {
	account, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "account not found")
		return
	}
	claims, _ := auth.FromContext(r.Context())
	beforeTime, beforeID, err := decodeListCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeCodedError(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
		return
	}
	limit := parseLimit(r)
	rows, err := a.pool.Query(r.Context(), `SELECT i.id,i.invoice_number,i.status,i.currency,i.total_minor,i.finalized_at,i.paid_at,i.created_at
		FROM invoices i LEFT JOIN payments pay ON pay.id=i.payment_id LEFT JOIN credit_packs cp ON cp.id=pay.credit_pack_id
		LEFT JOIN subscriptions s ON s.id=i.subscription_id LEFT JOIN products p ON p.id=COALESCE(cp.product_id,s.product_id)
		WHERE i.account_id=$1 AND (p.slug=$2 OR $3)
		AND ($4::timestamptz IS NULL OR (i.created_at,i.id)<($4,$5))
		ORDER BY i.created_at DESC,i.id DESC LIMIT $6`, account, claims.App, claims.Has("billing:link"), beforeTime, beforeID, limit+1)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	items := make([]invoiceResponse, 0, limit+1)
	for rows.Next() {
		var item invoiceResponse
		if err := rows.Scan(&item.ID, &item.InvoiceNumber, &item.Status, &item.Currency, &item.TotalMinor, &item.FinalizedAt, &item.PaidAt, &item.CreatedAt); err != nil {
			writeDBError(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeDBError(w, err)
		return
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		cursor := encodeListCursor(items[len(items)-1].CreatedAt, items[len(items)-1].ID)
		next = &cursor
	}
	writeJSON(w, http.StatusOK, invoiceListResponse{Items: items, NextCursor: next})
}

func encodeListCursor(createdAt time.Time, id uuid.UUID) string {
	value := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeListCursor(value string) (*time.Time, uuid.UUID, error) {
	if value == "" {
		return nil, uuid.Nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, uuid.Nil, err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return nil, uuid.Nil, errors.New("invalid cursor")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, uuid.Nil, err
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return nil, uuid.Nil, err
	}
	return &createdAt, id, nil
}

func (a *API) registerWebhook(w http.ResponseWriter, r *http.Request) {
	account, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "account not found")
		return
	}
	var in struct {
		TargetURL  string   `json:"target_url"`
		Secret     string   `json:"secret"`
		APIVersion string   `json:"api_version"`
		EventTypes []string `json:"event_types"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.TargetURL == "" || len(in.Secret) < 32 {
		writeError(w, 400, "target_url and a secret of at least 32 characters are required")
		return
	}
	claims, _ := auth.FromContext(r.Context())
	application := claims.App
	if in.APIVersion == "" {
		in.APIVersion = "2"
	}
	if in.EventTypes == nil {
		in.EventTypes = []string{}
	}
	if in.APIVersion != "2" {
		writeCodedError(w, http.StatusBadRequest, "unsupported_webhook_version", "api_version must be 2")
		return
	}
	if err := validateWebhookEventTypes(in.EventTypes); err != nil {
		writeCodedError(w, http.StatusBadRequest, "unsupported_webhook_event_type", err.Error())
		return
	}
	if err := validateWebhookTarget(r.Context(), in.TargetURL, nil); err != nil {
		writeError(w, 400, "invalid webhook destination")
		return
	}
	var id uuid.UUID
	err = a.pool.QueryRow(r.Context(), `INSERT INTO webhook_endpoints(account_id,application,target_url,secret,api_version,event_types,previous_secret,previous_secret_expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT(account_id,application) DO UPDATE SET target_url=excluded.target_url,secret=excluded.secret,api_version=excluded.api_version,
		event_types=excluded.event_types,previous_secret=excluded.previous_secret,previous_secret_expires_at=excluded.previous_secret_expires_at,
		active=true,updated_at=now() RETURNING id`, account, application, in.TargetURL, in.Secret, in.APIVersion, in.EventTypes,
		"", nil).Scan(&id)
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "application": application, "target_url": in.TargetURL, "api_version": in.APIVersion, "event_types": in.EventTypes})
}
func (a *API) listWebhooks(w http.ResponseWriter, r *http.Request) {
	account, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "account not found")
		return
	}
	rows, err := a.pool.Query(r.Context(), `SELECT id,application,target_url,active,api_version,event_types,created_at FROM webhook_endpoints WHERE account_id=$1 ORDER BY application`, account)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id uuid.UUID
		var application, target, apiVersion string
		var eventTypes []string
		var active bool
		var created time.Time
		if rows.Scan(&id, &application, &target, &active, &apiVersion, &eventTypes, &created) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "application": application, "target_url": target, "active": active, "api_version": apiVersion, "event_types": eventTypes, "created_at": created})
	}
	writeJSON(w, 200, items)
}

func (a *API) updateWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid webhook id")
		return
	}
	account, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	var in struct {
		TargetURL  *string   `json:"target_url"`
		Active     *bool     `json:"active"`
		APIVersion *string   `json:"api_version"`
		EventTypes *[]string `json:"event_types"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.TargetURL != nil {
		if err := validateWebhookTarget(r.Context(), *in.TargetURL, nil); err != nil {
			writeError(w, http.StatusBadRequest, "invalid webhook destination")
			return
		}
	}
	if in.APIVersion != nil && *in.APIVersion != "2" {
		writeCodedError(w, http.StatusBadRequest, "unsupported_webhook_version", "api_version must be 2")
		return
	}
	if in.EventTypes != nil {
		if err := validateWebhookEventTypes(*in.EventTypes); err != nil {
			writeCodedError(w, http.StatusBadRequest, "unsupported_webhook_event_type", err.Error())
			return
		}
	}
	var application, target, apiVersion string
	var active bool
	var eventTypes []string
	err = a.pool.QueryRow(r.Context(), `UPDATE webhook_endpoints SET
		target_url=COALESCE($3,target_url),active=COALESCE($4,active),api_version=COALESCE($5,api_version),
		event_types=COALESCE($6,event_types),updated_at=now() WHERE id=$1 AND account_id=$2
		RETURNING application,target_url,active,api_version,event_types`, id, account, in.TargetURL, in.Active, in.APIVersion, in.EventTypes).
		Scan(&application, &target, &active, &apiVersion, &eventTypes)
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "application": application, "target_url": target, "active": active,
		"api_version": apiVersion, "event_types": eventTypes})
}

func (a *API) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid webhook id")
		return
	}
	account, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	tag, err := a.pool.Exec(r.Context(), `UPDATE webhook_endpoints SET active=false,updated_at=now() WHERE id=$1 AND account_id=$2`, id, account)
	if err != nil {
		writeDBError(w, err)
		return
	}
	if tag.RowsAffected() != 1 {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	writeNoContent(w)
}

func (a *API) rotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid webhook id")
		return
	}
	account, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	var in struct {
		Secret       string `json:"secret"`
		GraceSeconds int64  `json:"grace_seconds"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Secret) < 32 {
		writeError(w, http.StatusBadRequest, "secret must contain at least 32 characters")
		return
	}
	if in.GraceSeconds == 0 {
		in.GraceSeconds = 86400
	}
	if in.GraceSeconds < 0 || in.GraceSeconds > 7*86400 {
		writeError(w, http.StatusBadRequest, "grace_seconds must be between 0 and 604800")
		return
	}
	tag, err := a.pool.Exec(r.Context(), `UPDATE webhook_endpoints SET previous_secret=secret,
		previous_secret_expires_at=now()+($4::bigint * interval '1 second'),secret=$3,updated_at=now()
		WHERE id=$1 AND account_id=$2`, id, account, in.Secret, in.GraceSeconds)
	if err != nil {
		writeDBError(w, err)
		return
	}
	if tag.RowsAffected() != 1 {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	writeNoContent(w)
}

func (a *API) adminAdjustment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		WalletID uuid.UUID `json:"wallet_id"`
		Amount   int64
		Reason   string
	}
	if !decode(w, r, &in) {
		return
	}
	if in.WalletID == uuid.Nil || in.Amount <= 0 || in.Reason == "" {
		writeError(w, 400, "wallet_id, positive amount and reason are required")
		return
	}
	if !a.canAccessWallet(r, in.WalletID) {
		a.auditDeniedAdmin(r, "credit.adjust", "wallet", in.WalletID.String(), "wallet_not_accessible")
		writeError(w, 403, "wallet not accessible")
		return
	}
	operation := "admin:" + uuid.NewString()
	claims, _ := auth.FromContext(r.Context())
	err := pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		var accountID uuid.UUID
		if err := tx.QueryRow(r.Context(), `SELECT account_id FROM wallets WHERE id=$1`, in.WalletID).Scan(&accountID); err != nil {
			return err
		}
		if err := a.wallets.GrantInTx(r.Context(), tx, in.WalletID, "admin", operation, in.Amount, nil); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `INSERT INTO audit_log(account_id,actor_subject,actor_type,action,resource_type,resource_id,reason,after_state) VALUES($1,$2,'user','credit.adjust','wallet',$3,$4,$5)`, accountID, claims.Subject, in.WalletID.String(), in.Reason, map[string]any{"amount": in.Amount, "operation_ref": operation})
		return err
	})
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"operation_ref": operation})
}
func (a *API) listAudit(w http.ResponseWriter, r *http.Request) {
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		a.auditDeniedAdmin(r, "audit.read", "audit_log", "current", "account_not_accessible")
		writeError(w, 404, "account not found")
		return
	}
	rows, err := a.pool.Query(r.Context(), `SELECT id,actor_subject,actor_type,action,resource_type,resource_id,reason,created_at FROM audit_log WHERE account_id=$1 ORDER BY created_at DESC LIMIT $2`, accountID, parseLimit(r))
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id uuid.UUID
		var actor, actorType, action, resourceType, resourceID string
		var reason *string
		var created time.Time
		if rows.Scan(&id, &actor, &actorType, &action, &resourceType, &resourceID, &reason, &created) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "actor": actor, "actor_type": actorType, "action": action, "resource_type": resourceType, "resource_id": resourceID, "reason": reason, "created_at": created})
	}
	writeJSON(w, 200, items)
}
func (a *API) replayWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid delivery id")
		return
	}
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		a.auditDeniedAdmin(r, "webhook.replay", "webhook_delivery", id.String(), "account_not_accessible")
		writeError(w, 403, "account not accessible")
		return
	}
	tag, err := a.pool.Exec(r.Context(), `UPDATE webhook_deliveries SET status='pending',next_attempt_at=now(),last_error=NULL WHERE id=$1 AND endpoint_id IN (SELECT id FROM webhook_endpoints WHERE account_id=$2)`, id, accountID)
	if err != nil || tag.RowsAffected() != 1 {
		if err != nil {
			writeDBError(w, err)
		} else {
			a.auditDeniedAdmin(r, "webhook.replay", "webhook_delivery", id.String(), "delivery_not_accessible")
			writeError(w, 403, "delivery not accessible")
		}
		return
	}
	writeNoContent(w)
}
