package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tociva/billmesh/internal/wallets"
)

type Worker struct {
	pool     *pgxpool.Pool
	client   *http.Client
	interval time.Duration
	log      *slog.Logger
}

func NewWorker(pool *pgxpool.Pool, client *http.Client, interval time.Duration, log *slog.Logger) *Worker {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Worker{pool: pool, client: client, interval: interval, log: log}
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
		rows, err := tx.Query(ctx, `SELECT d.id,d.event_id,d.target_url,e.event_type,e.payload,d.attempts,COALESCE((SELECT secret FROM webhook_endpoints WHERE target_url=d.target_url AND active LIMIT 1),'') FROM webhook_deliveries d JOIN outbox_events e ON e.id=d.event_id WHERE d.status IN ('pending','failed') AND d.next_attempt_at<=now() AND d.attempts<8 ORDER BY d.next_attempt_at FOR UPDATE OF d SKIP LOCKED LIMIT 20`)
		if err != nil {
			return err
		}
		defer rows.Close()
		type job struct {
			id, eventID        uuid.UUID
			url, event, secret string
			payload            []byte
			attempts           int
		}
		var jobs []job
		for rows.Next() {
			var j job
			if err := rows.Scan(&j.id, &j.eventID, &j.url, &j.event, &j.payload, &j.attempts, &j.secret); err != nil {
				return err
			}
			jobs = append(jobs, j)
		}
		rows.Close()
		for _, j := range jobs {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.url, bytes.NewReader(j.payload))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Billmesh-Event-ID", j.eventID.String())
				req.Header.Set("X-Billmesh-Event-Type", j.event)
				if j.secret != "" {
					mac := hmac.New(sha256.New, []byte(j.secret))
					mac.Write(j.payload)
					req.Header.Set("X-Billmesh-Signature", hex.EncodeToString(mac.Sum(nil)))
				}
				var resp *http.Response
				resp, err = w.client.Do(req)
				if resp != nil {
					resp.Body.Close()
					if resp.StatusCode < 200 || resp.StatusCode >= 300 {
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
			if err := tx.QueryRow(ctx, `SELECT account_id,product_id,plan_id,status,price_minor,p.included_credits,billing_interval,current_period_end,grace_period_end,cancel_at_period_end
				FROM subscriptions s JOIN plans p ON p.id=s.plan_id WHERE s.id=$1 FOR UPDATE OF s`, id).
				Scan(&accountID, &productID, &planID, &status, &price, &credits, &interval, &periodEnd, &graceEnd, &cancel); err != nil {
				return err
			}
			now := time.Now().UTC()
			if status == "past_due" {
				if graceEnd == nil || graceEnd.After(now) {
					return nil
				}
				if _, err := tx.Exec(ctx, `UPDATE subscriptions SET status='expired',updated_at=now(),version=version+1 WHERE id=$1`, id); err != nil {
					return err
				}
				return accountEvent(ctx, tx, accountID, "subscription", id, "subscription.expired", map[string]any{"subscription_id": id})
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
				grace := now.Add(72 * time.Hour)
				if _, err := tx.Exec(ctx, `UPDATE subscriptions SET status='past_due',grace_period_end=$2,updated_at=now(),version=version+1 WHERE id=$1`, id, grace); err != nil {
					return err
				}
				return accountEvent(ctx, tx, accountID, "subscription", id, "subscription.past_due", map[string]any{"subscription_id": id, "grace_period_end": grace})
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
