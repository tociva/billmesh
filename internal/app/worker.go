package app

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	return pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT d.id,d.event_id,d.target_url,e.event_type,e.payload,d.attempts FROM webhook_deliveries d JOIN outbox_events e ON e.id=d.event_id WHERE d.status IN ('pending','failed') AND d.next_attempt_at<=now() ORDER BY d.next_attempt_at FOR UPDATE OF d SKIP LOCKED LIMIT 20`)
		if err != nil {
			return err
		}
		defer rows.Close()
		type job struct {
			id, eventID uuid.UUID
			url, event  string
			payload     []byte
			attempts    int
		}
		var jobs []job
		for rows.Next() {
			var j job
			if err := rows.Scan(&j.id, &j.eventID, &j.url, &j.event, &j.payload, &j.attempts); err != nil {
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
			} else {
				delay := time.Duration(1<<min(j.attempts, 8)) * time.Second
				_, err = tx.Exec(ctx, `UPDATE webhook_deliveries SET status='failed',attempts=attempts+1,next_attempt_at=now()+$2::interval,last_error=$3 WHERE id=$1`, j.id, delay.String(), err.Error())
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}
