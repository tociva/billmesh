package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tociva/billmesh/internal/products"
	"github.com/tociva/billmesh/internal/wallets"
)

type Worker struct {
	pool            *pgxpool.Pool
	client          *http.Client
	interval        time.Duration
	log             *slog.Logger
	providerBaseURL string
	providerKey     string
	providerSecret  string
}

func NewWorker(pool *pgxpool.Pool, client *http.Client, interval time.Duration, log *slog.Logger) *Worker {
	if client == nil {
		client = newWebhookClient(nil, nil)
	}
	if log == nil {
		log = slog.Default()
	}
	return &Worker{pool: pool, client: client, interval: interval, log: log,
		providerBaseURL: strings.TrimRight(os.Getenv("RAZORPAY_BASE_URL"), "/"),
		providerKey:     strings.TrimSpace(os.Getenv("RAZORPAY_KEY_ID")), providerSecret: strings.TrimSpace(os.Getenv("RAZORPAY_KEY_SECRET"))}
}
func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.process(ctx); err != nil && ctx.Err() == nil {
			w.log.Error("worker cycle", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (w *Worker) process(ctx context.Context) error {
	if _, err := w.pool.Exec(ctx, `UPDATE account_ownership_transfers SET status='expired',updated_at=now()
		WHERE status IN ('authorized','requires_paid_transition') AND expires_at<=now()`); err != nil {
		return err
	}
	if err := w.reconcileProviderPayments(ctx); err != nil {
		w.log.Error("provider payment reconciliation", "error", err)
	}
	if err := w.processSubscriptionTransitions(ctx); err != nil {
		return err
	}
	if err := w.processSubscriptions(ctx); err != nil {
		return err
	}
	if err := w.expireCredits(ctx); err != nil {
		return err
	}
	rows, err := w.pool.Query(ctx, `SELECT id FROM reservations WHERE status='reserved' AND expires_at<=now() LIMIT 100`)
	if err != nil {
		return err
	}
	var expired []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		expired = append(expired, id)
	}
	rows.Close()
	walletService := wallets.NewService(w.pool, nil)
	for _, id := range expired {
		if _, err := walletService.Release(ctx, id, "expired"); err != nil && !errors.Is(err, wallets.ErrFinalized) {
			return err
		}
	}
	return pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT d.id,d.event_id,d.target_url,e.event_type,e.payload,d.attempts,COALESCE(ep.secret,''),
			COALESCE(ep.previous_secret,''),ep.previous_secret_expires_at,COALESCE(ep.api_version,'1'),e.created_at,e.account_id,e.product_id,
			e.billing_revision,e.aggregate_type,e.aggregate_id
			FROM webhook_deliveries d JOIN outbox_events e ON e.id=d.event_id
			LEFT JOIN webhook_endpoints ep ON ep.id=d.endpoint_id AND ep.active
			WHERE d.status IN ('pending','failed') AND d.next_attempt_at<=now() AND d.attempts<8
			AND (COALESCE(cardinality(ep.event_types),0)=0 OR e.event_type=ANY(ep.event_types))
			ORDER BY d.next_attempt_at FOR UPDATE OF d SKIP LOCKED LIMIT 20`)
		if err != nil {
			return err
		}
		defer rows.Close()
		type job struct {
			id, eventID        uuid.UUID
			url, event, secret string
			previousSecret     string
			previousExpires    *time.Time
			apiVersion         string
			payload            []byte
			attempts           int
			occurredAt         time.Time
			accountID          *uuid.UUID
			productID          *uuid.UUID
			revision           *int64
			aggregateType      string
			aggregateID        uuid.UUID
		}
		var jobs []job
		for rows.Next() {
			var j job
			if err := rows.Scan(&j.id, &j.eventID, &j.url, &j.event, &j.payload, &j.attempts, &j.secret, &j.previousSecret, &j.previousExpires, &j.apiVersion,
				&j.occurredAt, &j.accountID, &j.productID, &j.revision, &j.aggregateType, &j.aggregateID); err != nil {
				return err
			}
			jobs = append(jobs, j)
		}
		rows.Close()
		for _, j := range jobs {
			var err error
			if j.secret == "" {
				err = errors.New("webhook signing endpoint is unavailable")
			}
			body := j.payload
			if j.apiVersion == "2" {
				var marshalErr error
				body, marshalErr = json.Marshal(map[string]any{
					"specversion": "1.0", "schema_version": "2", "id": j.eventID, "type": j.event,
					"occurred_at": j.occurredAt, "account_id": j.accountID, "product_id": j.productID,
					"revision": j.revision, "aggregate": map[string]any{"type": j.aggregateType, "id": j.aggregateID},
					"snapshot_url": "/v1/billing-snapshot",
					"data":         json.RawMessage(j.payload),
				})
				if marshalErr != nil {
					err = marshalErr
				}
			}
			var req *http.Request
			if err == nil {
				req, err = http.NewRequestWithContext(ctx, http.MethodPost, j.url, bytes.NewReader(body))
			}
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Billmesh-Event-ID", j.eventID.String())
				req.Header.Set("X-Billmesh-Event-Type", j.event)
				req.Header.Set("X-Billmesh-Webhook-Version", j.apiVersion)
				if j.secret != "" {
					signedPayload := body
					var timestamp string
					if j.apiVersion == "2" {
						timestamp = fmt.Sprint(time.Now().UTC().Unix())
						req.Header.Set("X-Billmesh-Timestamp", timestamp)
						signedPayload = append([]byte(timestamp+"."), body...)
					}
					mac := hmac.New(sha256.New, []byte(j.secret))
					mac.Write(signedPayload)
					req.Header.Set("X-Billmesh-Signature", hex.EncodeToString(mac.Sum(nil)))
					if j.previousSecret != "" && j.previousExpires != nil && j.previousExpires.After(time.Now().UTC()) {
						previousMAC := hmac.New(sha256.New, []byte(j.previousSecret))
						previousMAC.Write(signedPayload)
						req.Header.Set("X-Billmesh-Previous-Signature", hex.EncodeToString(previousMAC.Sum(nil)))
					}
				}
				var resp *http.Response
				resp, err = w.client.Do(req)
				if resp != nil {
					resp.Body.Close()
					if err == nil && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
						err = fmt.Errorf("status %d", resp.StatusCode)
					}
				}
			}
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE webhook_deliveries SET status='delivered',attempts=attempts+1,delivered_at=now(),last_error=NULL WHERE id=$1`, j.id)
				if err == nil {
					_, err = tx.Exec(ctx, `UPDATE outbox_events SET published_at=COALESCE(published_at,now()) WHERE id=$1
						AND NOT EXISTS(SELECT 1 FROM webhook_deliveries WHERE event_id=$1 AND status<>'delivered')`, j.eventID)
				}
			} else {
				delay := time.Duration(1<<min(j.attempts, 8)) * time.Second
				_, err = tx.Exec(ctx, `UPDATE webhook_deliveries SET status='failed',attempts=attempts+1,next_attempt_at=now()+$2::interval,last_error=$3 WHERE id=$1`, j.id, delay.String(), err.Error())
				if j.attempts+1 >= 8 {
					w.log.Error("webhook delivery exhausted", "delivery_id", j.id, "event_id", j.eventID, "event_type", j.event)
				}
			}
			if err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE outbox_events e SET published_at=COALESCE(published_at,now())
			WHERE published_at IS NULL AND NOT EXISTS(SELECT 1 FROM webhook_deliveries d WHERE d.event_id=e.id)`)
		return err
	})
}

func (w *Worker) reconcileProviderPayments(ctx context.Context) error {
	if w.providerBaseURL == "" || w.providerKey == "" || w.providerSecret == "" {
		return nil
	}
	rows, err := w.pool.Query(ctx, `SELECT pay.id,pay.provider_order_id,pay.amount_minor,pay.currency
		FROM payments pay
		LEFT JOIN credit_packs cp ON cp.id=pay.credit_pack_id
		LEFT JOIN subscription_transitions st ON st.id=pay.transition_id
		JOIN products pr ON pr.id=COALESCE(cp.product_id,st.product_id)
		WHERE pay.provider='razorpay' AND pay.status IN ('created','authorized')
		AND COALESCE(pay.last_reconciled_at,pay.created_at) <= now() -
			(COALESCE((pr.billing_policy->'projection'->>'reconciliation_seconds')::integer,300) * interval '1 second')
		ORDER BY COALESCE(pay.last_reconciled_at,pay.created_at),pay.id LIMIT 100`)
	if err != nil {
		return err
	}
	type candidate struct {
		id       uuid.UUID
		orderID  string
		amount   int64
		currency string
	}
	items := make([]candidate, 0, 100)
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.orderID, &item.amount, &item.currency); err != nil {
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
		if _, err := w.pool.Exec(ctx, `UPDATE payments SET last_reconciled_at=now() WHERE id=$1`, item.id); err != nil {
			return err
		}
		paymentID, found, err := w.fetchCapturedProviderPayment(ctx, item.orderID, item.amount, item.currency)
		if err != nil {
			w.log.Warn("provider order reconciliation failed", "payment_id", item.id, "order_id", item.orderID, "error", err)
			continue
		}
		if !found {
			continue
		}
		if err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
			providerEventID := "reconciliation:" + paymentID
			payload, err := json.Marshal(map[string]any{"payment_id": paymentID, "order_id": item.orderID, "status": "captured", "amount": item.amount, "currency": item.currency})
			if err != nil {
				return err
			}
			var eventID uuid.UUID
			err = tx.QueryRow(ctx, `INSERT INTO provider_events(provider,provider_event_id,event_type,payload)
				VALUES('razorpay',$1,'payment.reconciled',$2) ON CONFLICT(provider,provider_event_id) DO UPDATE
				SET provider_event_id=excluded.provider_event_id RETURNING id`, providerEventID, payload).Scan(&eventID)
			if err != nil {
				return err
			}
			if err := capturePayment(ctx, tx, item.orderID, paymentID); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE provider_events SET processed_at=COALESCE(processed_at,now()) WHERE id=$1`, eventID)
			return err
		}); err != nil && !errors.Is(err, errConflictingProviderEvent) {
			w.log.Error("provider payment reconciliation apply failed", "payment_id", item.id, "order_id", item.orderID, "error", err)
		}
	}
	return nil
}

func (w *Worker) fetchCapturedProviderPayment(ctx context.Context, orderID string, expectedAmount int64, expectedCurrency string) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.providerBaseURL+"/v1/orders/"+url.PathEscape(orderID)+"/payments", nil)
	if err != nil {
		return "", false, err
	}
	req.SetBasicAuth(w.providerKey, w.providerSecret)
	resp, err := w.client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, fmt.Errorf("provider returned status %d", resp.StatusCode)
	}
	var result struct {
		Items []struct {
			ID       string `json:"id"`
			OrderID  string `json:"order_id"`
			Status   string `json:"status"`
			Amount   int64  `json:"amount"`
			Currency string `json:"currency"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", false, err
	}
	for _, payment := range result.Items {
		if payment.Status != "captured" {
			continue
		}
		if payment.ID == "" || payment.OrderID != orderID || payment.Amount != expectedAmount || payment.Currency != expectedCurrency {
			return "", false, errors.New("captured provider payment does not match the Billmesh order")
		}
		return payment.ID, true, nil
	}
	return "", false, nil
}

func (w *Worker) processSubscriptionTransitions(ctx context.Context) error {
	if _, err := w.pool.Exec(ctx, `UPDATE subscription_transitions SET status='expired',failure_code='checkout_expired',updated_at=now()
		WHERE status='requires_payment' AND checkout_expires_at<=now()`); err != nil {
		return err
	}
	rows, err := w.pool.Query(ctx, `SELECT id FROM subscription_transitions
		WHERE status='processing' AND effective='period_end' AND effective_at<=now()
		ORDER BY effective_at,id LIMIT 100`)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
			return applySubscriptionTransition(ctx, tx, id, "")
		}); err != nil && !errors.Is(err, errTransitionNotPayable) {
			return err
		}
	}
	return nil
}

func (w *Worker) processSubscriptions(ctx context.Context) error {
	rows, err := w.pool.Query(ctx, `SELECT id FROM subscriptions
		WHERE (status='active' AND current_period_end<=now()) OR (status='past_due' AND grace_period_end<=now())
		ORDER BY current_period_end LIMIT 100`)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
			var accountID, productID, planID uuid.UUID
			var status, interval string
			var price, credits int64
			var periodEnd time.Time
			var graceEnd *time.Time
			var cancel bool
			var policy products.BillingPolicy
			var policyVersion int64
			if err := tx.QueryRow(ctx, `SELECT s.account_id,s.product_id,s.plan_id,s.status,s.price_minor,s.included_credits,s.billing_interval,
				s.current_period_end,s.grace_period_end,s.cancel_at_period_end,p.billing_policy,p.billing_policy_version
				FROM subscriptions s JOIN products p ON p.id=s.product_id WHERE s.id=$1 FOR UPDATE OF s`, id).
				Scan(&accountID, &productID, &planID, &status, &price, &credits, &interval, &periodEnd, &graceEnd, &cancel, &policy, &policyVersion); err != nil {
				return err
			}
			now := time.Now().UTC()
			if status == "past_due" {
				if graceEnd == nil || graceEnd.After(now) {
					return nil
				}
				return expireSubscriptionByPolicy(ctx, tx, id, accountID, productID, periodEnd, policy, policyVersion)
			}
			if periodEnd.After(now) {
				return nil
			}
			if cancel {
				if _, err := tx.Exec(ctx, `UPDATE subscriptions SET status='cancelled',cancelled_at=now(),updated_at=now(),version=version+1 WHERE id=$1`, id); err != nil {
					return err
				}
				return accountEvent(ctx, tx, accountID, "subscription", id, "subscription.cancelled", map[string]any{"subscription_id": id})
			}
			if price > 0 {
				if policy.Lifecycle.Dunning == "grace_period" && policy.Lifecycle.GracePeriodDays > 0 {
					grace := now.Add(time.Duration(policy.Lifecycle.GracePeriodDays) * 24 * time.Hour)
					if _, err := tx.Exec(ctx, `UPDATE subscriptions SET status='past_due',grace_period_end=$2,updated_at=now(),version=version+1 WHERE id=$1`, id, grace); err != nil {
						return err
					}
					return accountEvent(ctx, tx, accountID, "subscription", id, "subscription.past_due", map[string]any{
						"subscription_id": id, "grace_period_end": grace, "policy_version": policyVersion,
					})
				}
				return expireSubscriptionByPolicy(ctx, tx, id, accountID, productID, periodEnd, policy, policyVersion)
			}
			next := billingPeriod(periodEnd, interval)
			if _, err := tx.Exec(ctx, `UPDATE subscriptions SET current_period_start=$2,current_period_end=$3,updated_at=now(),version=version+1 WHERE id=$1`, id, periodEnd, next); err != nil {
				return err
			}
			if credits > 0 {
				if err := allocateCredits(ctx, tx, accountID, productID, id, credits, next); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO subscription_history(subscription_id,status,plan_id,operation_ref) VALUES($1,'active',$2,$3) ON CONFLICT(operation_ref) DO NOTHING`, id, planID, "subscription:renew:"+id.String()+":"+next.Format("2006-01-02")); err != nil {
				return err
			}
			return accountEvent(ctx, tx, accountID, "subscription", id, "subscription.renewed", map[string]any{"subscription_id": id, "current_period_end": next})
		}); err != nil {
			return err
		}
	}
	return nil
}

func expireSubscriptionByPolicy(ctx context.Context, tx pgx.Tx, subscriptionID, accountID, productID uuid.UUID, periodEnd time.Time, policy products.BillingPolicy, policyVersion int64) error {
	if policy.Lifecycle.Expiration == "downgrade_to_default" {
		var plan transitionPlan
		err := tx.QueryRow(ctx, `SELECT p.id,p.plan_family_id,p.version,p.name,p.description,p.billing_model,p.price_minor,p.currency,p.billing_interval,p.included_credits,
			p.entitlements,pr.entitlement_schema_version,pr.entitlement_schema,p.checkout_enabled
			FROM plans p JOIN products pr ON pr.id=p.product_id WHERE p.product_id=$1 AND p.active AND p.selectable AND p.default_for_product AND p.billing_model='free'
			AND effective_from<=now() AND (effective_to IS NULL OR effective_to>now())`, productID).
			Scan(&plan.ID, &plan.PlanFamilyID, &plan.Version, &plan.Name, &plan.Description, &plan.BillingModel, &plan.PriceMinor, &plan.Currency,
				&plan.BillingInterval, &plan.IncludedCredits, &plan.Entitlements, &plan.EntitlementSchemaVersion, &plan.EntitlementSchema, &plan.CheckoutEnabled)
		if err == nil {
			var customerID *uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT customer_id FROM billing_accounts WHERE id=$1`, accountID).Scan(&customerID); err != nil {
				return err
			}
			if customerID != nil {
				if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "free-customer:"+customerID.String()); err != nil {
					return err
				}
			}
			eligible, eligibilityErr := freePlanEligible(ctx, tx, accountID, productID, policy.Customer.FreeAllowance)
			if eligibilityErr != nil {
				return eligibilityErr
			}
			if eligible {
				transitionID := uuid.New()
				digest := sha256.Sum256([]byte("expiration:" + subscriptionID.String() + ":" + periodEnd.UTC().Format(time.RFC3339Nano)))
				if _, err := tx.Exec(ctx, `INSERT INTO subscription_transitions(id,account_id,product_id,subscription_id,target_plan_id,target_plan_family_id,target_plan_version,
					target_plan_name,target_plan_description,billing_model,price_minor,currency,billing_interval,included_credits,entitlements,
					entitlement_schema_version,entitlement_schema,operation,effective,status,idempotency_key,request_hash,effective_at,billing_policy,billing_policy_version)
					VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,'downgrade','immediate','processing',$18,$19,now(),$20,$21)
					ON CONFLICT(account_id,product_id,idempotency_key) DO NOTHING`, transitionID, accountID, productID, subscriptionID, plan.ID,
					plan.PlanFamilyID, plan.Version, plan.Name, plan.Description, plan.BillingModel, plan.PriceMinor, plan.Currency, plan.BillingInterval,
					plan.IncludedCredits, plan.Entitlements, plan.EntitlementSchemaVersion, plan.EntitlementSchema,
					"expiration:"+periodEnd.UTC().Format(time.RFC3339Nano), hex.EncodeToString(digest[:]), policy, policyVersion); err != nil {
					return err
				}
				return applySubscriptionTransition(ctx, tx, transitionID, "")
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE subscriptions SET status='expired',grace_period_end=NULL,updated_at=now(),version=version+1 WHERE id=$1`, subscriptionID); err != nil {
		return err
	}
	return accountEvent(ctx, tx, accountID, "subscription", subscriptionID, "subscription.expired", map[string]any{
		"subscription_id": subscriptionID, "policy_version": policyVersion, "expiration_policy": policy.Lifecycle.Expiration,
	})
}

func (w *Worker) expireCredits(ctx context.Context) error {
	rows, err := w.pool.Query(ctx, `SELECT id FROM credit_grants WHERE remaining>0 AND expires_at<=now() ORDER BY expires_at LIMIT 100`)
	if err != nil {
		return err
	}
	var grantIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		grantIDs = append(grantIDs, id)
	}
	rows.Close()
	for _, grantID := range grantIDs {
		if err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
			var walletID uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT wallet_id FROM credit_grants WHERE id=$1`, grantID).Scan(&walletID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `SELECT 1 FROM wallets WHERE id=$1 FOR UPDATE`, walletID); err != nil {
				return err
			}
			var remaining int64
			if err := tx.QueryRow(ctx, `SELECT remaining FROM credit_grants WHERE id=$1 AND remaining>0 AND expires_at<=now() FOR UPDATE`, grantID).Scan(&remaining); errors.Is(err, pgx.ErrNoRows) {
				return nil
			} else if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE credit_grants SET remaining=0 WHERE id=$1`, grantID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE wallets SET available=available-$2 WHERE id=$1`, walletID, remaining); err != nil {
				return err
			}
			operation := "expire:" + grantID.String()
			if _, err := tx.Exec(ctx, `INSERT INTO credit_ledger(wallet_id,grant_id,operation_ref,kind,available_delta,reserved_delta)
				VALUES($1,$2,$3,'expire',$4,0) ON CONFLICT(operation_ref) DO NOTHING`, walletID, grantID, operation, -remaining); err != nil {
				return err
			}
			return appEvent(ctx, tx, "wallet", walletID, "credits.expired", map[string]any{"grant_id": grantID, "amount": remaining})
		}); err != nil {
			return err
		}
	}
	return nil
}
