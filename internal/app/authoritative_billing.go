package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tociva/billmesh/internal/auth"
)

const billingSnapshotMaxAge = 5 * time.Minute

var (
	errIdempotencyConflict  = errors.New("idempotency key was reused with a different request")
	errTransitionConflict   = errors.New("another subscription transition is already pending")
	errTransitionNotPayable = errors.New("subscription transition no longer accepts payment")
)

type transitionPlan struct {
	ID              uuid.UUID
	ProductID       uuid.UUID
	Product         string
	Version         int64
	Name            string
	Description     string
	BillingModel    string
	PriceMinor      int64
	Currency        string
	BillingInterval string
	IncludedCredits int64
	Entitlements    map[string]any
	CheckoutEnabled bool
}

type currentSubscriptionState struct {
	ID               uuid.UUID
	PlanID           uuid.UUID
	Status           string
	BillingModel     string
	PriceMinor       int64
	CurrentPeriodEnd *time.Time
}

func billingETag(revision int64) string {
	return fmt.Sprintf(`"billing-%d"`, revision)
}

func catalogueETag(revision int64) string {
	return fmt.Sprintf(`"catalogue-%d"`, revision)
}

func (a *API) catalogue(w http.ResponseWriter, r *http.Request) {
	product := r.URL.Query().Get("product")
	if product == "" {
		claims, _ := auth.FromContext(r.Context())
		product = claims.App
	}
	if !canReadProductSlug(r, product) {
		writeError(w, http.StatusForbidden, "product not accessible")
		return
	}
	var productID uuid.UUID
	var productName, description string
	var revision int64
	err := a.pool.QueryRow(r.Context(), `SELECT p.id,p.name,p.description,COALESCE(cr.revision,1)
		FROM products p LEFT JOIN catalogue_revisions cr ON cr.product_id=p.id
		WHERE p.slug=$1 AND p.active`, product).Scan(&productID, &productName, &description, &revision)
	if err != nil {
		writeDBError(w, err)
		return
	}
	etag := catalogueETag(revision)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=60")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	rows, err := a.pool.Query(r.Context(), `SELECT id,name,description,price_minor,currency,included_credits,entitlements,billing_interval,
		billing_model,default_for_product,checkout_enabled,effective_from,effective_to,version
		FROM plans WHERE product_id=$1 AND active AND selectable AND effective_from<=now()
		AND (effective_to IS NULL OR effective_to>now()) ORDER BY default_for_product DESC,price_minor,name,id`, productID)
	if err != nil {
		writeDBError(w, err)
		return
	}
	plans := []map[string]any{}
	for rows.Next() {
		var id uuid.UUID
		var name, planDescription, currency, interval, model string
		var price, credits, version int64
		var entitlements map[string]any
		var isDefault, checkout bool
		var from time.Time
		var to *time.Time
		if err := rows.Scan(&id, &name, &planDescription, &price, &currency, &credits, &entitlements, &interval, &model,
			&isDefault, &checkout, &from, &to, &version); err != nil {
			rows.Close()
			writeDBError(w, err)
			return
		}
		plans = append(plans, map[string]any{
			"id": id, "name": name, "description": planDescription, "price_minor": price, "currency": currency,
			"included_credits": credits, "entitlements": entitlements, "billing_interval": interval,
			"billing_model": model, "selectable": true, "default_for_product": isDefault,
			"checkout_enabled": checkout, "effective_from": from, "effective_to": to, "version": version,
		})
	}
	rows.Close()

	packs, err := a.creditPacksForProduct(r.Context(), productID)
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"product":  map[string]any{"id": productID, "slug": product, "name": productName, "description": description},
		"revision": revision, "plans": plans, "credit_packs": packs,
	})
}

func (a *API) listCreditPacks(w http.ResponseWriter, r *http.Request) {
	product := r.URL.Query().Get("product")
	if product == "" {
		claims, _ := auth.FromContext(r.Context())
		product = claims.App
	}
	if !canReadProductSlug(r, product) {
		writeError(w, http.StatusForbidden, "product not accessible")
		return
	}
	var productID uuid.UUID
	if err := a.pool.QueryRow(r.Context(), `SELECT id FROM products WHERE slug=$1 AND active`, product).Scan(&productID); err != nil {
		writeDBError(w, err)
		return
	}
	packs, err := a.creditPacksForProduct(r.Context(), productID)
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, packs)
}

func (a *API) creditPacksForProduct(ctx context.Context, productID uuid.UUID) ([]map[string]any, error) {
	rows, err := a.pool.Query(ctx, `SELECT id,name,credits,price_minor,currency,validity_days FROM credit_packs
		WHERE product_id=$1 AND active ORDER BY price_minor,credits,id`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id uuid.UUID
		var name, currency string
		var credits, price int64
		var validity *int
		if err := rows.Scan(&id, &name, &credits, &price, &currency, &validity); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "name": name, "credits": credits, "price_minor": price, "currency": currency, "validity_days": validity})
	}
	return items, rows.Err()
}

func (a *API) createSubscriptionTransition(w http.ResponseWriter, r *http.Request) {
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, http.StatusNotFound, "billing account not found")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" || len(idempotencyKey) > 200 {
		writeError(w, http.StatusBadRequest, "a valid Idempotency-Key header is required")
		return
	}
	var in struct {
		PlanID    uuid.UUID `json:"plan_id"`
		Effective string    `json:"effective"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.PlanID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "plan_id is required")
		return
	}
	if in.Effective != "" && in.Effective != "immediate" && in.Effective != "period_end" {
		writeError(w, http.StatusBadRequest, "effective must be immediate or period_end")
		return
	}

	plan, err := a.transitionPlan(r.Context(), in.PlanID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "plan is not available")
		return
	}
	if !a.canAccessProduct(r, plan.ProductID) {
		writeError(w, http.StatusForbidden, "product not accessible")
		return
	}

	requestDigest := sha256.Sum256([]byte(in.PlanID.String() + "\n" + in.Effective))
	requestHash := hex.EncodeToString(requestDigest[:])
	if existing, found, err := a.findTransitionByKey(r.Context(), accountID, plan.ProductID, idempotencyKey); err != nil {
		writeDBError(w, err)
		return
	} else if found {
		if existing.requestHash != requestHash {
			writeError(w, http.StatusConflict, errIdempotencyConflict.Error())
			return
		}
		a.writeTransition(w, r, existing.id, http.StatusOK)
		return
	}

	revision, err := ensureBillingRevision(r.Context(), a.pool, accountID, plan.ProductID)
	if err != nil {
		writeDBError(w, err)
		return
	}
	if match := strings.TrimSpace(r.Header.Get("If-Match")); match != "" && match != billingETag(revision) {
		writeError(w, http.StatusPreconditionFailed, "billing state changed; refresh the billing snapshot")
		return
	}

	current, err := a.currentSubscriptionState(r.Context(), accountID, plan.ProductID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeDBError(w, err)
		return
	}
	hasCurrent := err == nil
	operation := transitionOperation(current, hasCurrent, plan)
	effective := in.Effective
	if effective == "" {
		if hasCurrent && (operation == "downgrade" || (current.BillingModel == "paid" && plan.BillingModel == "free")) {
			effective = "period_end"
		} else {
			effective = "immediate"
		}
	}
	if effective == "period_end" && !hasCurrent {
		writeError(w, http.StatusBadRequest, "period_end requires an existing subscription")
		return
	}
	requiresPayment := plan.BillingModel == "paid" && (!hasCurrent || current.BillingModel == "free" || operation == "upgrade" || operation == "reactivate")
	if requiresPayment && !plan.CheckoutEnabled {
		writeError(w, http.StatusConflict, "checkout is not available for this plan")
		return
	}

	transitionID := uuid.New()
	status := "processing"
	var effectiveAt *time.Time
	if effective == "period_end" && current.CurrentPeriodEnd != nil {
		effectiveAt = current.CurrentPeriodEnd
	}
	var subscriptionID *uuid.UUID
	if hasCurrent {
		subscriptionID = &current.ID
	}
	err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, accountID.String()+":"+plan.ProductID.String()); err != nil {
			return err
		}
		if plan.BillingModel == "free" {
			var customerID *uuid.UUID
			if err := tx.QueryRow(r.Context(), `SELECT customer_id FROM billing_accounts WHERE id=$1`, accountID).Scan(&customerID); err != nil {
				return err
			}
			if customerID != nil {
				if _, err := tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "free-customer:"+customerID.String()); err != nil {
					return err
				}
			}
			eligible, err := freePlanEligible(r.Context(), tx, accountID)
			if err != nil {
				return err
			}
			if !eligible {
				return errFreePlanIneligible
			}
		}
		var pending bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM subscription_transitions WHERE account_id=$1 AND product_id=$2 AND status IN ('requires_payment','processing'))`, accountID, plan.ProductID).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return errTransitionConflict
		}
		_, err := tx.Exec(r.Context(), `INSERT INTO subscription_transitions(id,account_id,product_id,subscription_id,target_plan_id,target_plan_version,
			target_plan_name,target_plan_description,billing_model,price_minor,currency,billing_interval,included_credits,entitlements,
			operation,effective,status,idempotency_key,request_hash,checkout_expires_at,effective_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`, transitionID, accountID,
			plan.ProductID, subscriptionID, plan.ID, plan.Version, plan.Name, plan.Description, plan.BillingModel, plan.PriceMinor, plan.Currency,
			plan.BillingInterval, plan.IncludedCredits, plan.Entitlements, operation, effective, status, idempotencyKey, requestHash,
			nil, effectiveAt)
		if err != nil {
			return err
		}
		if requiresPayment || effective == "period_end" {
			return nil
		}
		return applySubscriptionTransition(r.Context(), tx, transitionID, "")
	})
	if errors.Is(err, errTransitionConflict) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, errFreePlanIneligible) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeDBError(w, err)
		return
	}

	if requiresPayment {
		expires := time.Now().UTC().Add(30 * time.Minute)
		providerOrder, err := a.createProviderOrder(r.Context(), plan.PriceMinor, plan.Currency, transitionID.String())
		if err != nil {
			_, _ = a.pool.Exec(r.Context(), `UPDATE subscription_transitions SET status='failed',failure_code='provider_unavailable',updated_at=now() WHERE id=$1`, transitionID)
			writeError(w, http.StatusServiceUnavailable, "payment provider unavailable")
			return
		}
		orderID, _ := providerOrder["id"].(string)
		if orderID == "" {
			_, _ = a.pool.Exec(r.Context(), `UPDATE subscription_transitions SET status='failed',failure_code='invalid_provider_response',updated_at=now() WHERE id=$1`, transitionID)
			writeError(w, http.StatusBadGateway, "invalid provider response")
			return
		}
		err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
			var paymentID uuid.UUID
			if err := tx.QueryRow(r.Context(), `INSERT INTO payments(account_id,provider,provider_order_id,status,amount_minor,currency,operation_ref,purpose,transition_id)
				VALUES($1,'razorpay',$2,'created',$3,$4,$5,'subscription',$6) RETURNING id`, accountID, orderID, plan.PriceMinor, plan.Currency,
				"transition:"+transitionID.String(), transitionID).Scan(&paymentID); err != nil {
				return err
			}
			_, err := tx.Exec(r.Context(), `UPDATE subscription_transitions SET status='requires_payment',checkout_expires_at=$2,updated_at=now() WHERE id=$1`, transitionID, expires)
			return err
		})
		if err != nil {
			writeDBError(w, err)
			return
		}
	}
	a.writeTransition(w, r, transitionID, http.StatusCreated)
}

func (a *API) transitionPlan(ctx context.Context, planID uuid.UUID) (transitionPlan, error) {
	var plan transitionPlan
	err := a.pool.QueryRow(ctx, `SELECT p.id,p.product_id,pr.slug,p.version,p.name,p.description,p.billing_model,p.price_minor,p.currency,
		p.billing_interval,p.included_credits,p.entitlements,p.checkout_enabled
		FROM plans p JOIN products pr ON pr.id=p.product_id
		WHERE p.id=$1 AND p.active AND p.selectable AND pr.active AND p.effective_from<=now() AND (p.effective_to IS NULL OR p.effective_to>now())`, planID).
		Scan(&plan.ID, &plan.ProductID, &plan.Product, &plan.Version, &plan.Name, &plan.Description, &plan.BillingModel, &plan.PriceMinor,
			&plan.Currency, &plan.BillingInterval, &plan.IncludedCredits, &plan.Entitlements, &plan.CheckoutEnabled)
	return plan, err
}

func (a *API) currentSubscriptionState(ctx context.Context, accountID, productID uuid.UUID) (currentSubscriptionState, error) {
	var current currentSubscriptionState
	err := a.pool.QueryRow(ctx, `SELECT id,plan_id,status,billing_model,price_minor,current_period_end FROM subscriptions
		WHERE account_id=$1 AND product_id=$2 ORDER BY created_at DESC LIMIT 1`, accountID, productID).
		Scan(&current.ID, &current.PlanID, &current.Status, &current.BillingModel, &current.PriceMinor, &current.CurrentPeriodEnd)
	return current, err
}

func transitionOperation(current currentSubscriptionState, hasCurrent bool, target transitionPlan) string {
	if !hasCurrent {
		return "activate"
	}
	if current.Status == "cancelled" || current.Status == "expired" {
		return "reactivate"
	}
	if target.PriceMinor > current.PriceMinor {
		return "upgrade"
	}
	if target.PriceMinor < current.PriceMinor {
		return "downgrade"
	}
	return "change"
}

type billingQueryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

var errFreePlanIneligible = errors.New("free plan eligibility has already been used")

func freePlanEligible(ctx context.Context, q billingQueryRower, accountID uuid.UUID) (bool, error) {
	var eligible bool
	err := q.QueryRow(ctx, `SELECT NOT EXISTS(
		SELECT 1 FROM billing_accounts current
		JOIN billing_accounts other ON other.customer_id=current.customer_id AND other.id<>current.id
		JOIN subscriptions s ON s.account_id=other.id
		WHERE current.id=$1 AND current.customer_id IS NOT NULL AND s.billing_model='free'
		AND s.status IN ('pending','active','past_due')
	)`, accountID).Scan(&eligible)
	return eligible, err
}

type foundTransition struct {
	id          uuid.UUID
	requestHash string
}

func (a *API) findTransitionByKey(ctx context.Context, accountID, productID uuid.UUID, key string) (foundTransition, bool, error) {
	var found foundTransition
	err := a.pool.QueryRow(ctx, `SELECT id,request_hash FROM subscription_transitions WHERE account_id=$1 AND product_id=$2 AND idempotency_key=$3`, accountID, productID, key).
		Scan(&found.id, &found.requestHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return found, false, nil
	}
	return found, err == nil, err
}

func (a *API) getSubscriptionTransition(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid transition id")
		return
	}
	a.writeTransition(w, r, id, http.StatusOK)
}

func (a *API) writeTransition(w http.ResponseWriter, r *http.Request, id uuid.UUID, statusCode int) {
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, http.StatusNotFound, "billing account not found")
		return
	}
	var transitionID, productID, planID uuid.UUID
	var subscriptionID, paymentID *uuid.UUID
	var operation, effective, status, planName, billingModel, currency, productSlug string
	var failureCode, providerOrderID *string
	var price, revision int64
	var checkoutExpiryPtr, effectiveAtPtr *time.Time
	err = a.pool.QueryRow(r.Context(), `SELECT t.id,t.product_id,t.subscription_id,t.target_plan_id,t.operation,t.effective,t.status,t.target_plan_name,
		t.billing_model,t.price_minor,t.currency,t.checkout_expires_at,t.effective_at,t.failure_code,p.id,p.provider_order_id,
		COALESCE(br.revision,1),pr.slug
		FROM subscription_transitions t
		JOIN products pr ON pr.id=t.product_id
		LEFT JOIN payments p ON p.transition_id=t.id
		LEFT JOIN billing_state_revisions br ON br.account_id=t.account_id AND br.product_id=t.product_id
		WHERE t.id=$1 AND t.account_id=$2`, id, accountID).Scan(&transitionID, &productID, &subscriptionID, &planID, &operation, &effective,
		&status, &planName, &billingModel, &price, &currency, &checkoutExpiryPtr, &effectiveAtPtr, &failureCode, &paymentID, &providerOrderID, &revision, &productSlug)
	if err != nil {
		writeDBError(w, err)
		return
	}
	result := map[string]any{
		"id": transitionID, "subscription_id": subscriptionID, "plan_id": planID, "plan_name": planName,
		"operation": operation, "effective": effective, "status": status, "effective_at": effectiveAtPtr,
		"failure_code": failureCode, "payment_id": paymentID, "revision": revision,
		"snapshot_url": "/v1/billing-snapshot?product=" + productSlug,
	}
	if paymentID != nil {
		result["checkout"] = map[string]any{"provider": "razorpay", "order_id": providerOrderID, "amount_minor": price, "currency": currency, "expires_at": checkoutExpiryPtr}
	}
	w.Header().Set("ETag", billingETag(revision))
	writeJSON(w, statusCode, result)
}

func (a *API) cancelSubscriptionTransition(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid transition id")
		return
	}
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, http.StatusNotFound, "billing account not found")
		return
	}
	tag, err := a.pool.Exec(r.Context(), `UPDATE subscription_transitions SET status='cancelled',updated_at=now()
		WHERE id=$1 AND account_id=$2 AND status IN ('requires_payment','processing')`, id, accountID)
	if err != nil {
		writeDBError(w, err)
		return
	}
	if tag.RowsAffected() != 1 {
		writeError(w, http.StatusConflict, "transition cannot be cancelled")
		return
	}
	a.writeTransition(w, r, id, http.StatusOK)
}

func (a *API) createProviderOrder(ctx context.Context, amount int64, currency, receipt string) (map[string]any, error) {
	providerOrder := map[string]any{"id": "order_" + uuid.NewString(), "status": "created", "amount": amount, "currency": currency}
	if a.providerBaseURL == "" {
		return providerOrder, nil
	}
	raw, err := json.Marshal(map[string]any{"amount": amount, "currency": currency, "receipt": receipt})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.providerBaseURL+"/v1/orders", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("provider order status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&providerOrder); err != nil {
		return nil, err
	}
	return providerOrder, nil
}

type billingRevisionQuerier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func ensureBillingRevision(ctx context.Context, q billingRevisionQuerier, accountID, productID uuid.UUID) (int64, error) {
	if _, err := q.Exec(ctx, `INSERT INTO billing_state_revisions(account_id,product_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, accountID, productID); err != nil {
		return 0, err
	}
	var revision int64
	err := q.QueryRow(ctx, `SELECT revision FROM billing_state_revisions WHERE account_id=$1 AND product_id=$2`, accountID, productID).Scan(&revision)
	return revision, err
}

func (a *API) billingSnapshot(w http.ResponseWriter, r *http.Request) {
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, http.StatusNotFound, "billing account not found")
		return
	}
	product := r.URL.Query().Get("product")
	if product == "" {
		claims, _ := auth.FromContext(r.Context())
		product = claims.App
	}
	if !canReadProductSlug(r, product) {
		writeError(w, http.StatusForbidden, "product not accessible")
		return
	}
	var productID uuid.UUID
	if err := a.pool.QueryRow(r.Context(), `SELECT id FROM products WHERE slug=$1`, product).Scan(&productID); err != nil {
		writeDBError(w, err)
		return
	}
	if _, err := ensureBillingRevision(r.Context(), a.pool, accountID, productID); err != nil {
		writeDBError(w, err)
		return
	}
	tx, err := a.pool.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var revision int64
	var revisionUpdated time.Time
	if err := tx.QueryRow(r.Context(), `SELECT revision,updated_at FROM billing_state_revisions WHERE account_id=$1 AND product_id=$2`, accountID, productID).Scan(&revision, &revisionUpdated); err != nil {
		writeDBError(w, err)
		return
	}
	etag := billingETag(revision)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=60, must-revalidate")
	if r.Header.Get("If-None-Match") == etag {
		_ = tx.Commit(r.Context())
		w.WriteHeader(http.StatusNotModified)
		return
	}
	var accountName string
	var customerID *uuid.UUID
	if err := tx.QueryRow(r.Context(), `SELECT name,customer_id FROM billing_accounts WHERE id=$1`, accountID).Scan(&accountName, &customerID); err != nil {
		writeDBError(w, err)
		return
	}

	var subscription any
	var entitlements any = map[string]any{}
	var subID, planID uuid.UUID
	var status, planName, planDescription, model, currency, interval string
	var start, end *time.Time
	var cancelAtPeriodEnd bool
	var price, credits, planVersion int64
	var rawEntitlements map[string]any
	err = tx.QueryRow(r.Context(), `SELECT id,plan_id,status,current_period_start,current_period_end,cancel_at_period_end,plan_name,plan_description,
		billing_model,price_minor,currency,billing_interval,included_credits,entitlements,plan_version
		FROM subscriptions WHERE account_id=$1 AND product_id=$2 ORDER BY created_at DESC LIMIT 1`, accountID, productID).
		Scan(&subID, &planID, &status, &start, &end, &cancelAtPeriodEnd, &planName, &planDescription, &model, &price, &currency,
			&interval, &credits, &rawEntitlements, &planVersion)
	if err == nil {
		subscription = map[string]any{
			"id": subID, "status": status, "current_period_start": start, "current_period_end": end,
			"cancel_at_period_end": cancelAtPeriodEnd,
			"effective_plan": map[string]any{"id": planID, "version": planVersion, "name": planName, "description": planDescription,
				"billing_model": model, "price_minor": price, "currency": currency, "billing_interval": interval,
				"included_credits": credits, "entitlements": rawEntitlements},
		}
		if status == "active" {
			entitlements = rawEntitlements
		}
	} else if errors.Is(err, pgx.ErrNoRows) {
		subscription = nil
	} else {
		writeDBError(w, err)
		return
	}

	var limits any
	var walletID uuid.UUID
	var available, reserved, totalGranted int64
	err = tx.QueryRow(r.Context(), `SELECT w.id,w.available,w.reserved,COALESCE((SELECT sum(amount) FROM credit_grants g WHERE g.wallet_id=w.id),0)
		FROM wallets w WHERE w.account_id=$1 AND w.product_id=$2`, accountID, productID).Scan(&walletID, &available, &reserved, &totalGranted)
	if err == nil {
		limits = map[string]any{"wallet_id": walletID, "available": available, "reserved": reserved, "total_granted": totalGranted}
	} else if errors.Is(err, pgx.ErrNoRows) {
		limits = nil
	} else {
		writeDBError(w, err)
		return
	}

	var pending any
	var transitionID, targetPlanID uuid.UUID
	var transitionStatus, operation, effective string
	var transitionEffectiveAt, checkoutExpires *time.Time
	err = tx.QueryRow(r.Context(), `SELECT id,target_plan_id,status,operation,effective,effective_at,checkout_expires_at
		FROM subscription_transitions WHERE account_id=$1 AND product_id=$2 AND status IN ('requires_payment','processing')
		ORDER BY created_at DESC LIMIT 1`, accountID, productID).Scan(&transitionID, &targetPlanID, &transitionStatus, &operation, &effective,
		&transitionEffectiveAt, &checkoutExpires)
	if err == nil {
		pending = map[string]any{"id": transitionID, "target_plan_id": targetPlanID, "status": transitionStatus, "operation": operation,
			"effective": effective, "effective_at": transitionEffectiveAt, "checkout_expires_at": checkoutExpires}
	} else if errors.Is(err, pgx.ErrNoRows) {
		pending = nil
	} else {
		writeDBError(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeDBError(w, err)
		return
	}
	now := time.Now().UTC()
	writeJSON(w, http.StatusOK, map[string]any{
		"revision": revision, "generated_at": now, "effective_at": revisionUpdated, "verified_at": now,
		"expires_at":   now.Add(billingSnapshotMaxAge),
		"account":      map[string]any{"id": accountID, "customer_id": customerID, "name": accountName},
		"subscription": subscription, "entitlements": entitlements, "limits": limits, "pending_transition": pending,
	})
}

func (a *API) requestCurrentCancellation(w http.ResponseWriter, r *http.Request) {
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, http.StatusNotFound, "billing account not found")
		return
	}
	claims, _ := auth.FromContext(r.Context())
	var in struct {
		Effective string `json:"effective"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Effective == "" {
		in.Effective = "period_end"
	}
	if in.Effective != "immediate" && in.Effective != "period_end" {
		writeError(w, http.StatusBadRequest, "effective must be immediate or period_end")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" || len(idempotencyKey) > 200 {
		writeError(w, http.StatusBadRequest, "a valid Idempotency-Key header is required")
		return
	}
	var subscriptionID, planID uuid.UUID
	var currentStatus string
	var periodEnd *time.Time
	err = a.pool.QueryRow(r.Context(), `SELECT s.id,s.plan_id,s.status,s.current_period_end FROM subscriptions s JOIN products p ON p.id=s.product_id
		WHERE s.account_id=$1 AND p.slug=$2 ORDER BY s.created_at DESC LIMIT 1`, accountID, claims.App).
		Scan(&subscriptionID, &planID, &currentStatus, &periodEnd)
	if err != nil {
		writeDBError(w, err)
		return
	}
	operationRef := "subscription:cancel:" + accountID.String() + ":" + idempotencyKey
	duplicate := false
	err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		var existingEffective string
		err := tx.QueryRow(r.Context(), `SELECT COALESCE(metadata->>'effective','') FROM subscription_history WHERE operation_ref=$1`, operationRef).Scan(&existingEffective)
		if err == nil {
			if existingEffective != in.Effective {
				return errIdempotencyConflict
			}
			duplicate = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if in.Effective == "period_end" {
			if _, err := tx.Exec(r.Context(), `UPDATE subscriptions SET cancel_at_period_end=true,updated_at=now(),version=version+1 WHERE id=$1`, subscriptionID); err != nil {
				return err
			}
		} else if _, err := tx.Exec(r.Context(), `UPDATE subscriptions SET status='cancelled',cancelled_at=now(),cancel_at_period_end=false,updated_at=now(),version=version+1 WHERE id=$1`, subscriptionID); err != nil {
			return err
		}
		historyStatus := currentStatus
		if in.Effective == "immediate" {
			historyStatus = "cancelled"
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO subscription_history(subscription_id,status,plan_id,operation_ref,metadata)
			VALUES($1,$2,$3,$4,$5)`, subscriptionID, historyStatus, planID, operationRef, map[string]any{"effective": in.Effective}); err != nil {
			return err
		}
		eventType := "subscription.cancellation_scheduled"
		if in.Effective == "immediate" {
			eventType = "subscription.cancelled"
		}
		return accountEvent(r.Context(), tx, accountID, "subscription", subscriptionID, eventType, map[string]any{"subscription_id": subscriptionID, "effective": in.Effective})
	})
	if errors.Is(err, errIdempotencyConflict) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeDBError(w, err)
		return
	}
	var revision int64
	var productID uuid.UUID
	if err := a.pool.QueryRow(r.Context(), `SELECT product_id FROM subscriptions WHERE id=$1`, subscriptionID).Scan(&productID); err == nil {
		revision, _ = ensureBillingRevision(r.Context(), a.pool, accountID, productID)
	}
	status := "active"
	var effectiveAt any = periodEnd
	if in.Effective == "immediate" {
		status = "cancelled"
		effectiveAt = time.Now().UTC()
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": subscriptionID, "status": status, "cancel_at_period_end": in.Effective == "period_end",
		"effective_at": effectiveAt, "revision": revision, "duplicate": duplicate})
}

func applySubscriptionTransition(ctx context.Context, tx pgx.Tx, transitionID uuid.UUID, providerPaymentID string) error {
	var accountID, productID, targetPlanID uuid.UUID
	var subscriptionID *uuid.UUID
	var targetVersion, price, credits int64
	var name, description, model, currency, interval, operation, effective, status string
	var entitlements map[string]any
	var effectiveAt *time.Time
	err := tx.QueryRow(ctx, `SELECT account_id,product_id,subscription_id,target_plan_id,target_plan_version,target_plan_name,target_plan_description,
		billing_model,price_minor,currency,billing_interval,included_credits,entitlements,operation,effective,status,effective_at
		FROM subscription_transitions WHERE id=$1 FOR UPDATE`, transitionID).Scan(&accountID, &productID, &subscriptionID, &targetPlanID,
		&targetVersion, &name, &description, &model, &price, &currency, &interval, &credits, &entitlements, &operation, &effective, &status, &effectiveAt)
	if err != nil {
		return err
	}
	if status == "completed" {
		return nil
	}
	if status == "cancelled" || status == "failed" || status == "expired" {
		return errTransitionNotPayable
	}
	if effective == "period_end" && (effectiveAt == nil || effectiveAt.After(time.Now().UTC())) {
		return errors.New("subscription transition is not yet effective")
	}
	now := time.Now().UTC()
	periodEnd := billingPeriod(now, interval)
	var resultingSubscription uuid.UUID
	activateNewPeriod := subscriptionID == nil || operation == "activate" || operation == "reactivate"
	if subscriptionID == nil {
		err = tx.QueryRow(ctx, `INSERT INTO subscriptions(account_id,plan_id,product_id,status,current_period_start,current_period_end,price_minor,currency,
			billing_interval,included_credits,entitlements,plan_version,plan_name,plan_description,billing_model)
			VALUES($1,$2,$3,'active',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id`, accountID, targetPlanID, productID,
			now, periodEnd, price, currency, interval, credits, entitlements, targetVersion, name, description, model).Scan(&resultingSubscription)
	} else {
		resultingSubscription = *subscriptionID
		if activateNewPeriod {
			_, err = tx.Exec(ctx, `UPDATE subscriptions SET plan_id=$2,status='active',current_period_start=$3,current_period_end=$4,cancel_at_period_end=false,
				cancelled_at=NULL,grace_period_end=NULL,price_minor=$5,currency=$6,billing_interval=$7,included_credits=$8,entitlements=$9,
				plan_version=$10,plan_name=$11,plan_description=$12,billing_model=$13,version=version+1,updated_at=now() WHERE id=$1`,
				resultingSubscription, targetPlanID, now, periodEnd, price, currency, interval, credits, entitlements, targetVersion, name, description, model)
		} else {
			_, err = tx.Exec(ctx, `UPDATE subscriptions SET plan_id=$2,price_minor=$3,currency=$4,billing_interval=$5,included_credits=$6,entitlements=$7,
				plan_version=$8,plan_name=$9,plan_description=$10,billing_model=$11,version=version+1,updated_at=now() WHERE id=$1`,
				resultingSubscription, targetPlanID, price, currency, interval, credits, entitlements, targetVersion, name, description, model)
		}
	}
	if err != nil {
		return err
	}
	if activateNewPeriod && credits > 0 {
		if err := allocateCredits(ctx, tx, accountID, productID, resultingSubscription, credits, periodEnd); err != nil {
			return err
		}
	}
	operationRef := "transition:" + transitionID.String()
	if providerPaymentID != "" {
		operationRef += ":payment:" + providerPaymentID
	}
	if _, err := tx.Exec(ctx, `INSERT INTO subscription_history(subscription_id,status,plan_id,operation_ref,metadata)
		VALUES($1,'active',$2,$3,$4) ON CONFLICT(operation_ref) DO NOTHING`, resultingSubscription, targetPlanID, operationRef,
		map[string]any{"transition_id": transitionID, "operation": operation}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE subscription_transitions SET subscription_id=$2,status='completed',effective_at=COALESCE(effective_at,now()),updated_at=now()
		WHERE id=$1`, transitionID, resultingSubscription); err != nil {
		return err
	}
	return accountEvent(ctx, tx, accountID, "subscription", resultingSubscription, "subscription.transition_completed",
		map[string]any{"subscription_id": resultingSubscription, "transition_id": transitionID, "plan_id": targetPlanID, "operation": operation})
}
