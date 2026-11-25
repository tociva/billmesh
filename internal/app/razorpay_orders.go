package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errInvalidProviderOrder     = errors.New("invalid provider order response")
	errProviderOrderRejected    = errors.New("provider rejected order")
	errProviderOrderUnavailable = errors.New("payment provider unavailable")
)

const (
	razorpayCreateAttempts = 3
	razorpayRecoveryPages  = 5
	razorpayRecoveryCount  = 100
)

type razorpayOrderRequest struct {
	Amount   int64             `json:"amount"`
	Currency string            `json:"currency"`
	Receipt  string            `json:"receipt"`
	Notes    map[string]string `json:"notes"`
}

type razorpayOrder struct {
	ID         string            `json:"id"`
	Entity     string            `json:"entity"`
	Amount     int64             `json:"amount"`
	AmountPaid int64             `json:"amount_paid"`
	AmountDue  int64             `json:"amount_due"`
	Currency   string            `json:"currency"`
	Receipt    string            `json:"receipt"`
	Status     string            `json:"status"`
	Attempts   int               `json:"attempts"`
	Notes      map[string]string `json:"notes"`
	CreatedAt  int64             `json:"created_at"`
}

type razorpayOrderExpectation struct {
	PaymentID    uuid.UUID
	AccountID    uuid.UUID
	Amount       int64
	Currency     string
	Receipt      string
	Environment  string
	OperationRef string
	CreatedAt    time.Time
}

func (expected razorpayOrderExpectation) notes() map[string]string {
	return map[string]string{
		"billmesh_payment_id":    expected.PaymentID.String(),
		"billmesh_account_id":    expected.AccountID.String(),
		"billmesh_environment":   expected.Environment,
		"billmesh_operation_ref": expected.OperationRef,
	}
}

type razorpayHTTPError struct {
	Status int
}

func (failure *razorpayHTTPError) Error() string {
	return fmt.Sprintf("provider order status %d", failure.Status)
}

func razorpayOrderAmount(amountMinor int64, currency string) (int64, string, error) {
	if amountMinor <= 0 {
		return 0, "", errors.New("Razorpay order amount must be positive")
	}
	if currency != "INR" {
		return 0, "", errors.New("Razorpay phase-one checkout supports INR only")
	}
	return amountMinor, currency, nil
}

func razorpayReceipt(environment, operationRef string) string {
	digest := sha256.Sum256([]byte(environment + "\x00" + operationRef))
	return "bm_" + hex.EncodeToString(digest[:16])
}

func (a *API) ensureStoredRazorpayOrder(ctx context.Context, paymentID uuid.UUID) (razorpayOrder, error) {
	conn, err := a.pool.Acquire(ctx)
	if err != nil {
		return razorpayOrder{}, err
	}
	defer conn.Release()
	lockKey := "razorpay-order:" + paymentID.String()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, lockKey); err != nil {
		return razorpayOrder{}, err
	}
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockContext, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, lockKey)
	}()

	var expected razorpayOrderExpectation
	var existingOrderID *string
	var failureReason string
	if err := conn.QueryRow(ctx, `SELECT id,account_id,amount_minor,currency,operation_ref,provider_receipt,provider_environment,
		created_at,provider_order_id,COALESCE(failure_reason,'') FROM payments WHERE id=$1 AND provider='razorpay'`, paymentID).
		Scan(&expected.PaymentID, &expected.AccountID, &expected.Amount, &expected.Currency, &expected.OperationRef, &expected.Receipt,
			&expected.Environment, &expected.CreatedAt, &existingOrderID, &failureReason); err != nil {
		return razorpayOrder{}, err
	}
	if existingOrderID != nil && *existingOrderID != "" {
		return razorpayOrder{ID: *existingOrderID}, nil
	}
	if failureReason == "provider_response_invalid" {
		order, recoverErr := a.recoverRazorpayOrder(ctx, expected)
		if recoverErr != nil {
			return razorpayOrder{}, errInvalidProviderOrder
		}
		return a.attachRazorpayOrder(ctx, conn, expected, order)
	}

	order, err := a.createOrRecoverRazorpayOrder(ctx, expected)
	if err != nil {
		reason := "provider_unavailable"
		if errors.Is(err, errInvalidProviderOrder) {
			reason = "provider_response_invalid"
		} else if errors.Is(err, errProviderOrderRejected) {
			reason = "provider_rejected_order"
		}
		_, _ = conn.Exec(ctx, `UPDATE payments SET failure_reason=$2,updated_at=now() WHERE id=$1`, paymentID, reason)
		return razorpayOrder{}, err
	}
	return a.attachRazorpayOrder(ctx, conn, expected, order)
}

func (a *API) ensureTransitionRazorpayOrder(ctx context.Context, transitionID uuid.UUID) error {
	var paymentID uuid.UUID
	err := a.pool.QueryRow(ctx, `SELECT id FROM payments WHERE transition_id=$1`, transitionID).Scan(&paymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := a.ensureStoredRazorpayOrder(ctx, paymentID); err != nil {
		return err
	}
	_, err = a.pool.Exec(ctx, `UPDATE subscription_transitions SET failure_code=NULL,updated_at=now() WHERE id=$1`, transitionID)
	return err
}

func (a *API) attachRazorpayOrder(ctx context.Context, conn *pgxpool.Conn, expected razorpayOrderExpectation, order razorpayOrder) (razorpayOrder, error) {
	tag, err := conn.Exec(ctx, `UPDATE payments SET provider_order_id=$2,failure_reason=NULL,updated_at=now()
		WHERE id=$1 AND provider_order_id IS NULL`, expected.PaymentID, order.ID)
	if err != nil {
		return razorpayOrder{}, err
	}
	if tag.RowsAffected() != 1 {
		var existing string
		if err := conn.QueryRow(ctx, `SELECT provider_order_id FROM payments WHERE id=$1`, expected.PaymentID).Scan(&existing); err != nil {
			return razorpayOrder{}, err
		}
		if existing != order.ID {
			return razorpayOrder{}, errors.New("payment is already attached to a different provider order")
		}
	}
	return order, nil
}

func (a *API) createOrRecoverRazorpayOrder(ctx context.Context, expected razorpayOrderExpectation) (razorpayOrder, error) {
	var lastErr error
	for attempt := 0; attempt < razorpayCreateAttempts; attempt++ {
		order, err := a.createRazorpayOrderOnce(ctx, expected)
		if err == nil {
			return order, nil
		}
		lastErr = err
		if errors.Is(err, errInvalidProviderOrder) {
			return razorpayOrder{}, err
		}
		var responseErr *razorpayHTTPError
		if errors.As(err, &responseErr) && responseErr.Status != http.StatusConflict && responseErr.Status != http.StatusTooManyRequests && responseErr.Status < 500 {
			if recovered, recoverErr := a.recoverRazorpayOrder(ctx, expected); recoverErr == nil {
				return recovered, nil
			}
			return razorpayOrder{}, fmt.Errorf("%w: status %d", errProviderOrderRejected, responseErr.Status)
		}
		if recovered, recoverErr := a.recoverRazorpayOrder(ctx, expected); recoverErr == nil {
			return recovered, nil
		}
		if attempt+1 < razorpayCreateAttempts {
			delay := time.Duration(50*(1<<attempt)) * time.Millisecond
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return razorpayOrder{}, fmt.Errorf("%w: %v", errProviderOrderUnavailable, ctx.Err())
			case <-timer.C:
			}
		}
	}
	return razorpayOrder{}, fmt.Errorf("%w: %v", errProviderOrderUnavailable, lastErr)
}

func (a *API) createRazorpayOrderOnce(ctx context.Context, expected razorpayOrderExpectation) (razorpayOrder, error) {
	if a.providerBaseURL == "" || a.providerPublicKey == "" || a.providerSecret == "" {
		return razorpayOrder{}, errProviderOrderUnavailable
	}
	amount, currency, err := razorpayOrderAmount(expected.Amount, expected.Currency)
	if err != nil {
		return razorpayOrder{}, err
	}
	payload, err := json.Marshal(razorpayOrderRequest{Amount: amount, Currency: currency, Receipt: expected.Receipt, Notes: expected.notes()})
	if err != nil {
		return razorpayOrder{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.providerBaseURL+"/v1/orders", bytes.NewReader(payload))
	if err != nil {
		return razorpayOrder{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth(a.providerPublicKey, a.providerSecret)
	started := time.Now()
	if a.metrics != nil {
		a.metrics.orderCreate("attempt", 0)
	}
	response, err := a.doRazorpayRequest("create_order", request)
	if err != nil {
		if a.metrics != nil {
			a.metrics.orderCreate("ambiguous", time.Since(started))
		}
		return razorpayOrder{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
		if a.metrics != nil {
			a.metrics.orderCreate("failure", time.Since(started))
		}
		return razorpayOrder{}, &razorpayHTTPError{Status: response.StatusCode}
	}
	order, err := decodeRazorpayOrder(response.Body)
	if err != nil {
		if a.metrics != nil {
			a.metrics.orderCreate("ambiguous", time.Since(started))
		}
		return razorpayOrder{}, fmt.Errorf("%w: %v", errInvalidProviderOrder, err)
	}
	if err := validateRazorpayOrder(order, expected); err != nil {
		if a.metrics != nil {
			a.metrics.orderCreate("ambiguous", time.Since(started))
		}
		return razorpayOrder{}, fmt.Errorf("%w: %v", errInvalidProviderOrder, err)
	}
	if a.metrics != nil {
		a.metrics.orderCreate("success", time.Since(started))
	}
	return order, nil
}

func (a *API) recoverRazorpayOrder(ctx context.Context, expected razorpayOrderExpectation) (razorpayOrder, error) {
	from := expected.CreatedAt.Add(-time.Minute).Unix()
	to := time.Now().UTC().Add(time.Minute).Unix()
	var match *razorpayOrder
	for page := 0; page < razorpayRecoveryPages; page++ {
		query := url.Values{
			"from":  []string{strconv.FormatInt(from, 10)},
			"to":    []string{strconv.FormatInt(to, 10)},
			"count": []string{strconv.Itoa(razorpayRecoveryCount)},
			"skip":  []string{strconv.Itoa(page * razorpayRecoveryCount)},
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.providerBaseURL+"/v1/orders?"+query.Encode(), nil)
		if err != nil {
			return razorpayOrder{}, err
		}
		request.SetBasicAuth(a.providerPublicKey, a.providerSecret)
		response, err := a.doRazorpayRequest("recover_order", request)
		if err != nil {
			return razorpayOrder{}, err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			response.Body.Close()
			return razorpayOrder{}, &razorpayHTTPError{Status: response.StatusCode}
		}
		var collection struct {
			Entity string          `json:"entity"`
			Count  int             `json:"count"`
			Items  []razorpayOrder `json:"items"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&collection)
		response.Body.Close()
		if decodeErr != nil || collection.Entity != "collection" || collection.Count != len(collection.Items) {
			return razorpayOrder{}, errInvalidProviderOrder
		}
		for index := range collection.Items {
			candidate := collection.Items[index]
			if candidate.Receipt != expected.Receipt {
				continue
			}
			if err := validateRazorpayOrder(candidate, expected); err != nil {
				return razorpayOrder{}, fmt.Errorf("%w: recovered order: %v", errInvalidProviderOrder, err)
			}
			if match != nil && match.ID != candidate.ID {
				return razorpayOrder{}, errors.New("multiple provider orders matched one Billmesh receipt")
			}
			copy := candidate
			match = &copy
		}
		if len(collection.Items) < razorpayRecoveryCount {
			break
		}
	}
	if match == nil {
		return razorpayOrder{}, errors.New("provider order was not found during recovery")
	}
	return *match, nil
}

func decodeRazorpayOrder(reader io.Reader) (razorpayOrder, error) {
	var order razorpayOrder
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	if err := decoder.Decode(&order); err != nil {
		return razorpayOrder{}, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return razorpayOrder{}, errors.New("provider response contains trailing JSON")
	}
	return order, nil
}

func validateRazorpayOrder(order razorpayOrder, expected razorpayOrderExpectation) error {
	if !strings.HasPrefix(order.ID, "order_") || order.Entity != "order" || order.Amount != expected.Amount || order.AmountPaid != 0 ||
		order.AmountDue != expected.Amount || order.Currency != expected.Currency || order.Receipt != expected.Receipt || order.Status != "created" ||
		order.Attempts != 0 || order.CreatedAt <= 0 {
		return errors.New("provider order fields do not match the Billmesh operation")
	}
	for key, value := range expected.notes() {
		if order.Notes[key] != value {
			return fmt.Errorf("provider order note %s does not match", key)
		}
	}
	return nil
}

func writeProviderOrderError(w http.ResponseWriter, err error) {
	if errors.Is(err, errInvalidProviderOrder) || errors.Is(err, errProviderOrderRejected) {
		writeCodedError(w, http.StatusBadGateway, "invalid_provider_response", "payment provider returned an invalid order")
		return
	}
	writeCodedError(w, http.StatusServiceUnavailable, "payment_provider_unavailable", "payment provider unavailable")
}

func isProviderOrderError(err error) bool {
	return errors.Is(err, errInvalidProviderOrder) || errors.Is(err, errProviderOrderRejected) || errors.Is(err, errProviderOrderUnavailable)
}
