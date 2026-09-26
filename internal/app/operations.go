package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tociva/billmesh/internal/auth"
	"github.com/tociva/billmesh/internal/wallets"
)

func (a *API) walletLedger(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid wallet id")
		return
	}
	if !a.canAccessWallet(r, id) {
		writeError(w, 403, "wallet not accessible")
		return
	}
	rows, err := a.pool.Query(r.Context(), `SELECT id,operation_ref,kind,available_delta,reserved_delta,created_at FROM credit_ledger WHERE wallet_id=$1 ORDER BY created_at,id LIMIT $2`, id, parseLimit(r))
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var rowID uuid.UUID
		var op, kind string
		var available, reserved int64
		var created time.Time
		if rows.Scan(&rowID, &op, &kind, &available, &reserved, &created) != nil {
			continue
		}
		items = append(items, map[string]any{"id": rowID, "operation_ref": op, "kind": kind, "available_delta": available, "reserved_delta": reserved, "created_at": created})
	}
	writeJSON(w, 200, items)
}
func (a *API) releaseReservation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid reservation id")
		return
	}
	var walletID uuid.UUID
	if err = a.pool.QueryRow(r.Context(), `SELECT wallet_id FROM reservations WHERE id=$1`, id).Scan(&walletID); err != nil {
		writeDBError(w, err)
		return
	}
	if !a.canAccessWallet(r, walletID) {
		writeError(w, 403, "reservation not accessible")
		return
	}
	result, err := a.wallets.Release(r.Context(), id, "released")
	if errors.Is(err, wallets.ErrFinalized) {
		writeError(w, 409, err.Error())
		return
	}
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func (a *API) extendReservation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid reservation id")
		return
	}
	var in struct {
		TTLSeconds int `json:"ttl_seconds"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.TTLSeconds <= 0 {
		writeError(w, 400, "ttl_seconds must be positive")
		return
	}
	var walletID uuid.UUID
	if err = a.pool.QueryRow(r.Context(), `SELECT wallet_id FROM reservations WHERE id=$1`, id).Scan(&walletID); err != nil {
		writeDBError(w, err)
		return
	}
	if !a.canAccessWallet(r, walletID) {
		writeError(w, 403, "reservation not accessible")
		return
	}
	result, err := a.wallets.Extend(r.Context(), id, time.Duration(in.TTLSeconds)*time.Second)
	if errors.Is(err, wallets.ErrFinalized) {
		writeError(w, 409, err.Error())
		return
	}
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func (a *API) authorizeExecution(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Context      string `json:"context"`
		ExecutionID  string `json:"execution_id"`
		Credits      int64  `json:"credits"`
		OperationSeq int    `json:"operation_seq"`
		TTLSeconds   int    `json:"ttl_seconds"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Credits <= 0 {
		writeError(w, 400, "credits must be positive")
		return
	}
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "billing account not found")
		return
	}
	product := in.Context
	if product == "" {
		claims, _ := authFromRequest(r)
		product = claims
	}
	if product == "standalone" {
		product = "taskmesh"
	}
	var walletID uuid.UUID
	err = a.pool.QueryRow(r.Context(), `SELECT w.id FROM wallets w JOIN products p ON p.id=w.product_id
		WHERE w.account_id=$1 AND p.slug=$2 AND EXISTS (
			SELECT 1 FROM subscriptions s JOIN plans sp ON sp.id=s.plan_id
			WHERE s.account_id=w.account_id AND sp.product_id=w.product_id
			AND (s.status='active' OR (s.status='past_due' AND s.grace_period_end>now()))
		)`, accountID, product).Scan(&walletID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 409, "no funded wallet for billing context")
		return
	}
	if err != nil {
		writeDBError(w, err)
		return
	}
	if in.ExecutionID == "" {
		in.ExecutionID = uuid.NewString()
	}
	ttl := 15 * time.Minute
	if in.TTLSeconds > 0 {
		ttl = time.Duration(in.TTLSeconds) * time.Second
	}
	reservation, err := a.wallets.Reserve(r.Context(), walletID, in.ExecutionID, in.OperationSeq, in.Credits, ttl)
	if errors.Is(err, wallets.ErrInsufficientCredits) {
		writeError(w, 409, err.Error())
		return
	}
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"authorized": true, "wallet_id": walletID, "reservation": reservation})
}
func authFromRequest(r *http.Request) (string, bool) {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		return "", false
	}
	return claims.App, true
}

type usageInput struct {
	EventID       string     `json:"event_id"`
	ReservationID uuid.UUID  `json:"reservation_id"`
	Meter         string     `json:"meter"`
	Application   string     `json:"application"`
	Quantity      int64      `json:"quantity"`
	Units         int64      `json:"units"`
	OccurredAt    *time.Time `json:"occurred_at"`
	Metadata      map[string]any
}

func (a *API) recordUsage(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, 400, "invalid usage payload")
		return
	}
	var inputs []usageInput
	if len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '[' {
		if json.Unmarshal(raw, &inputs) != nil {
			writeError(w, 400, "invalid usage batch")
			return
		}
	} else {
		var wrapper struct {
			Events []usageInput `json:"events"`
		}
		if json.Unmarshal(raw, &wrapper) == nil && wrapper.Events != nil {
			inputs = wrapper.Events
		} else {
			var single usageInput
			if json.Unmarshal(raw, &single) != nil {
				writeError(w, 400, "invalid usage event")
				return
			}
			inputs = []usageInput{single}
		}
	}
	if len(inputs) == 0 || len(inputs) > 1000 {
		writeError(w, 400, "usage batch must contain between 1 and 1000 events")
		return
	}
	duplicates := 0
	for _, in := range inputs {
		duplicate, err := a.persistUsage(r, in)
		if err != nil {
			var apiErr usageError
			if errors.As(err, &apiErr) {
				writeError(w, apiErr.status, apiErr.Error())
			} else {
				writeDBError(w, err)
			}
			return
		}
		if duplicate {
			duplicates++
		}
	}
	writeJSON(w, 202, map[string]any{"accepted": len(inputs), "duplicates": duplicates})
}

type usageError struct {
	status  int
	message string
}

func (e usageError) Error() string { return e.message }

func (a *API) persistUsage(r *http.Request, in usageInput) (bool, error) {
	if in.EventID == "" || in.ReservationID == uuid.Nil {
		return false, usageError{400, "event_id and reservation_id are required"}
	}
	if in.Quantity == 0 {
		in.Quantity = in.Units
	}
	if in.Quantity < 0 {
		return false, usageError{400, "quantity cannot be negative"}
	}
	valid := map[string]bool{"workflow.execution": true, "llm.input_tokens": true, "llm.output_tokens": true, "agent.runtime_ms": true, "compute.milliseconds": true}
	if !valid[in.Meter] {
		return false, usageError{400, "unsupported meter"}
	}
	var walletID uuid.UUID
	if err := a.pool.QueryRow(r.Context(), `SELECT wallet_id FROM reservations WHERE id=$1`, in.ReservationID).Scan(&walletID); err != nil {
		return false, err
	}
	if !a.canAccessWallet(r, walletID) {
		return false, usageError{403, "usage customer not accessible"}
	}
	when := time.Now().UTC()
	if in.OccurredAt != nil {
		when = *in.OccurredAt
	}
	meta, _ := json.Marshal(in.Metadata)
	tag, err := a.pool.Exec(r.Context(), `INSERT INTO usage_events(wallet_id,reservation_id,external_id,units,quantity,meter,application,occurred_at,metadata) VALUES($1,$2,$3,$4,$4,$5,$6,$7,$8) ON CONFLICT(external_id) DO NOTHING`, walletID, in.ReservationID, in.EventID, in.Quantity, in.Meter, in.Application, when, meta)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 0, nil
}
func (a *API) listUsage(w http.ResponseWriter, r *http.Request) {
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "account not found")
		return
	}
	meter := r.URL.Query().Get("meter")
	product := r.URL.Query().Get("product")
	application := r.URL.Query().Get("application")
	var from, to *time.Time
	if value := r.URL.Query().Get("from"); value != "" {
		parsed, parseErr := time.Parse(time.RFC3339, value)
		if parseErr != nil {
			writeError(w, 400, "from must be RFC3339")
			return
		}
		from = &parsed
	}
	if value := r.URL.Query().Get("to"); value != "" {
		parsed, parseErr := time.Parse(time.RFC3339, value)
		if parseErr != nil {
			writeError(w, 400, "to must be RFC3339")
			return
		}
		to = &parsed
	}
	rows, err := a.pool.Query(r.Context(), `SELECT u.external_id,u.meter,u.quantity,u.application,u.occurred_at,p.slug FROM usage_events u JOIN wallets w ON w.id=u.wallet_id JOIN products p ON p.id=w.product_id
		WHERE w.account_id=$1 AND ($2='' OR u.meter=$2) AND ($3='' OR p.slug=$3) AND ($4='' OR u.application=$4)
		AND ($5::timestamptz IS NULL OR u.occurred_at >= $5) AND ($6::timestamptz IS NULL OR u.occurred_at < $6)
		ORDER BY u.occurred_at DESC LIMIT $7`, accountID, meter, product, application, from, to, parseLimit(r))
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var event, meterName, application, product string
		var quantity int64
		var occurred time.Time
		if rows.Scan(&event, &meterName, &quantity, &application, &occurred, &product) != nil {
			continue
		}
		items = append(items, map[string]any{"event_id": event, "meter": meterName, "quantity": quantity, "application": application, "product": product, "occurred_at": occurred})
	}
	writeJSON(w, 200, items)
}
func (a *API) getLimits(w http.ResponseWriter, r *http.Request) {
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "account not found")
		return
	}
	rows, err := a.pool.Query(r.Context(), `SELECT w.id,p.slug,w.available,w.reserved,COALESCE(sum(g.amount),0) FROM wallets w JOIN products p ON p.id=w.product_id LEFT JOIN credit_grants g ON g.wallet_id=w.id WHERE w.account_id=$1 GROUP BY w.id,p.slug ORDER BY p.slug`, accountID)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id uuid.UUID
		var product string
		var available, reserved, total int64
		if rows.Scan(&id, &product, &available, &reserved, &total) != nil {
			continue
		}
		used := total - available - reserved
		percent := 0
		if total > 0 {
			percent = int(used * 100 / total)
			if percent > 100 {
				percent = 100
			}
		}
		items = append(items, map[string]any{"wallet_id": id, "product": product, "available": available, "reserved": reserved, "total_granted": total, "usage_percent": percent})
	}
	writeJSON(w, 200, items)
}
