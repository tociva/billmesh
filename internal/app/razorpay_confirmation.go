package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	errCheckoutConfirmationReplay = errors.New("checkout confirmation was already accepted")
	errProviderPaymentMismatch    = errors.New("provider payment does not match the Billmesh order")
)

type razorpayCheckoutConfirmation struct {
	RazorpayPaymentID string `json:"razorpay_payment_id"`
	RazorpayOrderID   string `json:"razorpay_order_id"`
	RazorpaySignature string `json:"razorpay_signature"`
}

type razorpayProviderPayment struct {
	ID       string `json:"id"`
	Entity   string `json:"entity"`
	OrderID  string `json:"order_id"`
	Status   string `json:"status"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

type checkoutConfirmationResponse struct {
	Status            string `json:"status"`
	PaymentID         string `json:"payment_id"`
	ProviderPaymentID string `json:"provider_payment_id"`
	SnapshotURL       string `json:"snapshot_url"`
}

func razorpayCheckoutSignatureMessage(serverOrderID, providerPaymentID string) string {
	return serverOrderID + "|" + providerPaymentID
}

func verifyRazorpayCheckoutSignature(serverOrderID, providerPaymentID, signature, secret string) bool {
	if serverOrderID == "" || providerPaymentID == "" || secret == "" || !razorpaySignaturePattern.MatchString(signature) {
		return false
	}
	provided, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(razorpayCheckoutSignatureMessage(serverOrderID, providerPaymentID)))
	return secureHMACEqual(mac.Sum(nil), provided)
}

func (a *API) confirmRazorpayCheckout(w http.ResponseWriter, r *http.Request) {
	paymentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeCodedError(w, http.StatusBadRequest, "payment_id_invalid", "payment id is invalid")
		return
	}
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeCodedError(w, http.StatusNotFound, "billing_account_not_found", "billing account not found")
		return
	}
	var input razorpayCheckoutConfirmation
	if !decode(w, r, &input) {
		return
	}
	input.RazorpayPaymentID = strings.TrimSpace(input.RazorpayPaymentID)
	input.RazorpayOrderID = strings.TrimSpace(input.RazorpayOrderID)
	input.RazorpaySignature = strings.TrimSpace(input.RazorpaySignature)
	if input.RazorpayPaymentID == "" || input.RazorpayOrderID == "" || input.RazorpaySignature == "" {
		writeCodedError(w, http.StatusBadRequest, "checkout_result_incomplete", "Razorpay checkout result is incomplete")
		return
	}

	var orderID, environment, currency string
	var amount int64
	var confirmedAt *string
	err = a.pool.QueryRow(r.Context(), `SELECT provider_order_id,provider_environment,currency,amount_minor,
		checkout_confirmed_at::text FROM payments WHERE id=$1 AND account_id=$2 AND provider='razorpay'`, paymentID, accountID).
		Scan(&orderID, &environment, &currency, &amount, &confirmedAt)
	if err != nil {
		writeDBError(w, err)
		return
	}
	if confirmedAt != nil {
		writeCodedError(w, http.StatusConflict, "checkout_confirmation_replayed", errCheckoutConfirmationReplay.Error())
		return
	}
	if input.RazorpayOrderID != orderID {
		writeCodedError(w, http.StatusBadRequest, "checkout_order_mismatch", "checkout order does not match the stored payment")
		return
	}
	if !verifyRazorpayCheckoutSignature(orderID, input.RazorpayPaymentID, input.RazorpaySignature, a.providerSecret) {
		writeCodedError(w, http.StatusUnauthorized, "checkout_signature_invalid", "Razorpay checkout signature is invalid")
		return
	}

	providerPayment, err := a.fetchRazorpayPayment(r.Context(), input.RazorpayPaymentID)
	if err != nil {
		writeCodedError(w, http.StatusServiceUnavailable, "payment_verification_unavailable", "payment verification is temporarily unavailable")
		return
	}
	if providerPayment.ID != input.RazorpayPaymentID || providerPayment.Entity != "payment" || providerPayment.OrderID != orderID ||
		providerPayment.Amount != amount || providerPayment.Currency != currency {
		writeCodedError(w, http.StatusConflict, "provider_payment_mismatch", errProviderPaymentMismatch.Error())
		return
	}
	if providerPayment.Status != "captured" {
		writeJSON(w, http.StatusAccepted, checkoutConfirmationResponse{
			Status: "processing", PaymentID: paymentID.String(), ProviderPaymentID: input.RazorpayPaymentID,
			SnapshotURL: "/v1/billing-snapshot",
		})
		return
	}

	confirmationHash := sha256.Sum256([]byte(paymentID.String() + "\x00" + input.RazorpayPaymentID + "\x00" + input.RazorpaySignature))
	err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		var storedOrderID, storedEnvironment string
		var alreadyConfirmed bool
		if err := tx.QueryRow(r.Context(), `SELECT provider_order_id,provider_environment,checkout_confirmed_at IS NOT NULL
			FROM payments WHERE id=$1 AND account_id=$2 AND provider='razorpay' FOR UPDATE`, paymentID, accountID).
			Scan(&storedOrderID, &storedEnvironment, &alreadyConfirmed); err != nil {
			return err
		}
		if alreadyConfirmed {
			return errCheckoutConfirmationReplay
		}
		if storedOrderID != orderID || storedEnvironment != environment {
			return errProviderPaymentMismatch
		}
		if err := capturePayment(r.Context(), tx, environment, orderID, input.RazorpayPaymentID); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `UPDATE payments SET checkout_confirmation_hash=$2,checkout_confirmed_at=now(),updated_at=now() WHERE id=$1`,
			paymentID, hex.EncodeToString(confirmationHash[:]))
		return err
	})
	if errors.Is(err, errCheckoutConfirmationReplay) {
		writeCodedError(w, http.StatusConflict, "checkout_confirmation_replayed", err.Error())
		return
	}
	if errors.Is(err, errConflictingProviderEvent) || errors.Is(err, errProviderPaymentMismatch) {
		writeCodedError(w, http.StatusConflict, "provider_payment_conflict", "provider payment conflicts with the stored payment")
		return
	}
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, checkoutConfirmationResponse{
		Status: "captured", PaymentID: paymentID.String(), ProviderPaymentID: input.RazorpayPaymentID,
		SnapshotURL: "/v1/billing-snapshot",
	})
}

func (a *API) fetchRazorpayPayment(ctx context.Context, paymentID string) (razorpayProviderPayment, error) {
	if a.providerBaseURL == "" || a.providerPublicKey == "" || a.providerSecret == "" {
		return razorpayProviderPayment{}, errProviderOrderUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.providerBaseURL+"/v1/payments/"+url.PathEscape(paymentID), nil)
	if err != nil {
		return razorpayProviderPayment{}, err
	}
	request.SetBasicAuth(a.providerPublicKey, a.providerSecret)
	response, err := a.client.Do(request)
	if err != nil {
		return razorpayProviderPayment{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
		return razorpayProviderPayment{}, &razorpayHTTPError{Status: response.StatusCode}
	}
	var payment razorpayProviderPayment
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&payment); err != nil {
		return razorpayProviderPayment{}, errInvalidProviderOrder
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return razorpayProviderPayment{}, errInvalidProviderOrder
	}
	return payment, nil
}
