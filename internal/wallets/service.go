package wallets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInsufficientCredits = errors.New("insufficient credits")
	ErrInvalidSettlement   = errors.New("settled credits exceed reserved credits")
	ErrFinalized           = errors.New("reservation already finalized with a different operation")
)

type Clock interface{ Now() time.Time }
type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

type Service struct {
	pool  *pgxpool.Pool
	clock Clock
}

func NewService(pool *pgxpool.Pool, clock Clock) *Service {
	if clock == nil {
		clock = realClock{}
	}
	return &Service{pool: pool, clock: clock}
}

type Wallet struct {
	ID, AccountID, ProductID uuid.UUID
	Available, Reserved      int64
	CreatedAt                time.Time
}
type Reservation struct {
	ID, WalletID uuid.UUID
	ExecutionID  string
	OperationSeq int
	Requested    int64
	Settled      *int64
	Status       string
	ExpiresAt    time.Time
}

func (s *Service) CreateWallet(ctx context.Context, accountID, productID uuid.UUID) (Wallet, error) {
	var w Wallet
	err := s.pool.QueryRow(ctx, `INSERT INTO wallets(account_id,product_id) VALUES($1,$2)
		ON CONFLICT(account_id,product_id) DO UPDATE SET account_id=excluded.account_id
		RETURNING id,account_id,product_id,available,reserved,created_at`, accountID, productID).
		Scan(&w.ID, &w.AccountID, &w.ProductID, &w.Available, &w.Reserved, &w.CreatedAt)
	return w, err
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Wallet, error) {
	var w Wallet
	err := s.pool.QueryRow(ctx, `SELECT id,account_id,product_id,available,reserved,created_at FROM wallets WHERE id=$1`, id).
		Scan(&w.ID, &w.AccountID, &w.ProductID, &w.Available, &w.Reserved, &w.CreatedAt)
	return w, err
}

func (s *Service) Grant(ctx context.Context, walletID uuid.UUID, source, operationRef string, amount int64, expiresAt *time.Time) error {
	if amount <= 0 {
		return errors.New("grant amount must be positive")
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var grantID uuid.UUID
		err := tx.QueryRow(ctx, `INSERT INTO credit_grants(wallet_id,source,operation_ref,amount,remaining,expires_at)
			VALUES($1,$2,$3,$4,$4,$5) ON CONFLICT(operation_ref) DO NOTHING RETURNING id`, walletID, source, operationRef, amount, expiresAt).Scan(&grantID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE wallets SET available=available+$2 WHERE id=$1`, walletID, amount); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO credit_ledger(wallet_id,grant_id,operation_ref,kind,available_delta,reserved_delta) VALUES($1,$2,$3,'grant',$4,0)`, walletID, grantID, "grant:"+operationRef, amount); err != nil {
			return err
		}
		return insertEvent(ctx, tx, "wallet", walletID, "credits.granted", map[string]any{"amount": amount, "operation_ref": operationRef})
	})
}

func (s *Service) Reserve(ctx context.Context, walletID uuid.UUID, executionID string, seq int, amount int64, ttl time.Duration) (Reservation, error) {
	if amount <= 0 || executionID == "" {
		return Reservation{}, errors.New("execution_id and positive amount are required")
	}
	var result Reservation
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var available int64
		if err := tx.QueryRow(ctx, `SELECT available FROM wallets WHERE id=$1 FOR UPDATE`, walletID).Scan(&available); err != nil {
			return err
		}
		if err := scanReservation(tx.QueryRow(ctx, `SELECT id,wallet_id,execution_id,operation_seq,requested,settled,status,expires_at FROM reservations WHERE wallet_id=$1 AND execution_id=$2 AND operation_seq=$3`, walletID, executionID, seq), &result); err == nil {
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if available < amount {
			return ErrInsufficientCredits
		}
		expires := s.clock.Now().Add(ttl)
		if err := tx.QueryRow(ctx, `INSERT INTO reservations(wallet_id,execution_id,operation_seq,requested,expires_at) VALUES($1,$2,$3,$4,$5) RETURNING id,wallet_id,execution_id,operation_seq,requested,settled,status,expires_at`, walletID, executionID, seq, amount, expires).Scan(&result.ID, &result.WalletID, &result.ExecutionID, &result.OperationSeq, &result.Requested, &result.Settled, &result.Status, &result.ExpiresAt); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,remaining FROM credit_grants WHERE wallet_id=$1 AND remaining>0 AND (expires_at IS NULL OR expires_at>$2) ORDER BY expires_at ASC NULLS LAST,created_at FOR UPDATE`, walletID, s.clock.Now())
		if err != nil {
			return err
		}
		type grantAllocation struct {
			id        uuid.UUID
			remaining int64
		}
		var grants []grantAllocation
		for rows.Next() {
			var grant grantAllocation
			if err := rows.Scan(&grant.id, &grant.remaining); err != nil {
				rows.Close()
				return err
			}
			grants = append(grants, grant)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		remaining := amount
		for _, grant := range grants {
			take := min(remaining, grant.remaining)
			if take == 0 {
				continue
			}
			if _, err = tx.Exec(ctx, `UPDATE credit_grants SET remaining=remaining-$2 WHERE id=$1`, grant.id, take); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO reservation_allocations(reservation_id,grant_id,amount) VALUES($1,$2,$3)`, result.ID, grant.id, take); err != nil {
				return err
			}
			remaining -= take
			if remaining == 0 {
				break
			}
		}
		if remaining != 0 {
			return ErrInsufficientCredits
		}
		if _, err = tx.Exec(ctx, `UPDATE wallets SET available=available-$2,reserved=reserved+$2 WHERE id=$1`, walletID, amount); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO credit_ledger(wallet_id,reservation_id,operation_ref,kind,available_delta,reserved_delta) VALUES($1,$2,$3,'reserve',$4,$5)`, walletID, result.ID, fmt.Sprintf("reserve:%s:%d", executionID, seq), -amount, amount); err != nil {
			return err
		}
		return insertEvent(ctx, tx, "wallet", walletID, "credits.reserved", map[string]any{"reservation_id": result.ID, "amount": amount})
	})
	return result, err
}

func (s *Service) Settle(ctx context.Context, reservationID uuid.UUID, actual int64) (Reservation, error) {
	if actual < 0 {
		return Reservation{}, ErrInvalidSettlement
	}
	var r Reservation
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := scanReservation(tx.QueryRow(ctx, `SELECT id,wallet_id,execution_id,operation_seq,requested,settled,status,expires_at FROM reservations WHERE id=$1 FOR UPDATE`, reservationID), &r); err != nil {
			return err
		}
		if r.Status == "settled" {
			if r.Settled != nil && *r.Settled == actual {
				return nil
			}
			return ErrFinalized
		}
		if r.Status != "reserved" {
			return ErrFinalized
		}
		if actual > r.Requested {
			return ErrInvalidSettlement
		}
		release := r.Requested - actual
		if release > 0 {
			rows, err := tx.Query(ctx, `SELECT a.grant_id,a.amount FROM reservation_allocations a JOIN credit_grants g ON g.id=a.grant_id WHERE a.reservation_id=$1 ORDER BY g.expires_at DESC NULLS FIRST,g.created_at DESC`, r.ID)
			if err != nil {
				return err
			}
			type allocation struct {
				grantID uuid.UUID
				amount  int64
			}
			var allocations []allocation
			for rows.Next() {
				var item allocation
				if err := rows.Scan(&item.grantID, &item.amount); err != nil {
					rows.Close()
					return err
				}
				allocations = append(allocations, item)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			toReturn := release
			for _, item := range allocations {
				returned := min(toReturn, item.amount)
				if returned > 0 {
					if _, err = tx.Exec(ctx, `UPDATE credit_grants SET remaining=remaining+$2 WHERE id=$1`, item.grantID, returned); err != nil {
						return err
					}
					toReturn -= returned
				}
				if toReturn == 0 {
					break
				}
			}
			if toReturn != 0 {
				return errors.New("reservation allocation invariant violated")
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE wallets SET available=available+$2,reserved=reserved-$3 WHERE id=$1`, r.WalletID, release, r.Requested); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE reservations SET status='settled',settled=$2,updated_at=now() WHERE id=$1`, r.ID, actual); err != nil {
			return err
		}
		r.Status = "settled"
		r.Settled = &actual
		if _, err := tx.Exec(ctx, `INSERT INTO credit_ledger(wallet_id,reservation_id,operation_ref,kind,available_delta,reserved_delta) VALUES($1,$2,$3,'settle',$4,$5)`, r.WalletID, r.ID, "settle:"+r.ID.String(), release, -r.Requested); err != nil {
			return err
		}
		return insertEvent(ctx, tx, "wallet", r.WalletID, "credits.settled", map[string]any{"reservation_id": r.ID, "actual": actual, "released": release})
	})
	return r, err
}

func scanReservation(row pgx.Row, r *Reservation) error {
	return row.Scan(&r.ID, &r.WalletID, &r.ExecutionID, &r.OperationSeq, &r.Requested, &r.Settled, &r.Status, &r.ExpiresAt)
}
func insertEvent(ctx context.Context, tx pgx.Tx, aggregateType string, aggregateID uuid.UUID, eventType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,payload) VALUES($1,$2,$3,$4)`, aggregateType, aggregateID, eventType, raw)
	return err
}
