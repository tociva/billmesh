package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tociva/billmesh/internal/auth"
	"github.com/tociva/billmesh/internal/wallets"
)

type API struct {
	pool    *pgxpool.Pool
	wallets *wallets.Service
	auth    auth.TokenVerifier
	log     *slog.Logger
}

func NewAPI(pool *pgxpool.Pool, verifier auth.TokenVerifier, log *slog.Logger) *API {
	if log == nil {
		log = slog.Default()
	}
	return &API{pool: pool, wallets: wallets.NewService(pool, nil), auth: verifier, log: log}
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", a.ready)
	protected := http.NewServeMux()
	protected.HandleFunc("POST /v1/accounts", auth.Require("billing:write", a.createAccount))
	protected.HandleFunc("POST /v1/products", auth.Require("billing:admin", a.createProduct))
	protected.HandleFunc("POST /v1/wallets", auth.Require("billing:write", a.createWallet))
	protected.HandleFunc("GET /v1/wallets/{id}", auth.Require("billing:read", a.getWallet))
	protected.HandleFunc("POST /v1/wallets/{id}/grants", auth.Require("credits:grant", a.grant))
	protected.HandleFunc("POST /v1/wallets/{id}/reservations", auth.Require("credits:reserve", a.reserve))
	protected.HandleFunc("POST /v1/reservations/{id}/settle", auth.Require("credits:settle", a.settle))
	protected.HandleFunc("GET /v1/events", auth.Require("billing:read", a.events))
	if a.auth != nil {
		mux.Handle("/v1/", auth.Middleware(a.auth)(protected))
	}
	return requestLog(a.log, recoverer(mux))
}

func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := a.pool.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (a *API) createAccount(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name           string `json:"name"`
		ExternalRef    string `json:"external_ref"`
		Application    string `json:"application"`
		OrganizationID string `json:"organization_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Name == "" || in.Application == "" || in.OrganizationID == "" {
		writeError(w, 400, "name, application and organization_id are required")
		return
	}
	claims, _ := auth.FromContext(r.Context())
	if claims.OrgID != "" && claims.OrgID != in.OrganizationID {
		writeError(w, 403, "organization mismatch")
		return
	}
	tx, err := a.pool.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "database error")
		return
	}
	defer tx.Rollback(r.Context())
	var id uuid.UUID
	var created time.Time
	if err = tx.QueryRow(r.Context(), `INSERT INTO billing_accounts(name,external_ref) VALUES($1,NULLIF($2,'')) RETURNING id,created_at`, in.Name, in.ExternalRef).Scan(&id, &created); err != nil {
		writeDBError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO account_links(account_id,application,organization_id) VALUES($1,$2,$3)`, id, in.Application, in.OrganizationID); err != nil {
		writeDBError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "database error")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "name": in.Name, "created_at": created})
}

func (a *API) createProduct(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	var id uuid.UUID
	if err := a.pool.QueryRow(r.Context(), `INSERT INTO products(slug,name) VALUES($1,$2) RETURNING id`, in.Slug, in.Name).Scan(&id); err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "slug": in.Slug, "name": in.Name})
}

func (a *API) createWallet(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID uuid.UUID `json:"account_id"`
		ProductID uuid.UUID `json:"product_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !a.canAccessAccount(r, in.AccountID) {
		writeError(w, 403, "account not accessible")
		return
	}
	v, err := a.wallets.CreateWallet(r.Context(), in.AccountID, in.ProductID)
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 201, v)
}

func (a *API) getWallet(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid wallet id")
		return
	}
	v, err := a.wallets.Get(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "wallet not found")
		return
	}
	if err != nil {
		writeDBError(w, err)
		return
	}
	if !a.canAccessAccount(r, v.AccountID) {
		writeError(w, 403, "wallet not accessible")
		return
	}
	writeJSON(w, 200, v)
}

func (a *API) grant(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid wallet id")
		return
	}
	if !a.canAccessWallet(r, id) {
		writeError(w, 403, "wallet not accessible")
		return
	}
	var in struct {
		Source       string     `json:"source"`
		OperationRef string     `json:"operation_ref"`
		Amount       int64      `json:"amount"`
		ExpiresAt    *time.Time `json:"expires_at"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err = a.wallets.Grant(r.Context(), id, in.Source, in.OperationRef, in.Amount, in.ExpiresAt); err != nil {
		writeDBError(w, err)
		return
	}
	v, _ := a.wallets.Get(r.Context(), id)
	writeJSON(w, 200, v)
}

func (a *API) reserve(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid wallet id")
		return
	}
	if !a.canAccessWallet(r, id) {
		writeError(w, 403, "wallet not accessible")
		return
	}
	var in struct {
		ExecutionID  string `json:"execution_id"`
		OperationSeq int    `json:"operation_seq"`
		Amount       int64  `json:"amount"`
		TTLSeconds   int    `json:"ttl_seconds"`
	}
	if !decode(w, r, &in) {
		return
	}
	ttl := 15 * time.Minute
	if in.TTLSeconds > 0 {
		ttl = time.Duration(in.TTLSeconds) * time.Second
	}
	v, err := a.wallets.Reserve(r.Context(), id, in.ExecutionID, in.OperationSeq, in.Amount, ttl)
	if errors.Is(err, wallets.ErrInsufficientCredits) {
		writeError(w, 409, err.Error())
		return
	}
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 201, v)
}

func (a *API) settle(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid reservation id")
		return
	}
	var in struct {
		Actual int64 `json:"actual"`
	}
	if !decode(w, r, &in) {
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
	v, err := a.wallets.Settle(r.Context(), id, in.Actual)
	if errors.Is(err, wallets.ErrInvalidSettlement) || errors.Is(err, wallets.ErrFinalized) {
		writeError(w, 409, err.Error())
		return
	}
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (a *API) canAccessAccount(r *http.Request, accountID uuid.UUID) bool {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		return false
	}
	var found bool
	err := a.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM account_links WHERE account_id=$1 AND application=$2 AND organization_id=$3)`, accountID, claims.App, claims.OrgID).Scan(&found)
	return err == nil && found
}
func (a *API) canAccessWallet(r *http.Request, walletID uuid.UUID) bool {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		return false
	}
	var found bool
	err := a.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM wallets w JOIN products p ON p.id=w.product_id JOIN account_links l ON l.account_id=w.account_id WHERE w.id=$1 AND l.application=$2 AND l.organization_id=$3 AND p.slug=$2)`, walletID, claims.App, claims.OrgID).Scan(&found)
	return err == nil && found
}

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "streaming unsupported")
		return
	}
	after := int64(0)
	if h := r.Header.Get("Last-Event-ID"); h != "" {
		after, _ = strconv.ParseInt(h, 10, 64)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		rows, err := a.pool.Query(r.Context(), `SELECT sequence,event_type,payload FROM outbox_events WHERE sequence>$1 ORDER BY sequence LIMIT 100`, after)
		if err != nil {
			return
		}
		for rows.Next() {
			var seq int64
			var event string
			var payload []byte
			if rows.Scan(&seq, &event, &payload) != nil {
				continue
			}
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", seq, event, payload)
			after = seq
		}
		rows.Close()
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		writeError(w, 400, "invalid JSON: "+err.Error())
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func writeDBError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not found")
		return
	}
	writeError(w, 500, "database operation failed")
}
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				writeError(w, 500, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func requestLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if !strings.HasSuffix(r.URL.Path, "healthz") {
			log.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
		}
	})
}
