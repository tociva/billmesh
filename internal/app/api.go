package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tociva/billmesh/internal/auth"
	"github.com/tociva/billmesh/internal/wallets"
)

type API struct {
	pool            *pgxpool.Pool
	wallets         *wallets.Service
	auth            auth.TokenVerifier
	log             *slog.Logger
	client          *http.Client
	providerBaseURL string
	webhookSecret   string
	sseMu           sync.Mutex
	sseActive       map[uuid.UUID]int
	authFailures    *failureWindow
	mutations       *keyedBuckets
}

const maxSSEConnectionsPerAccount = 8

func NewAPI(pool *pgxpool.Pool, verifier auth.TokenVerifier, log *slog.Logger) *API {
	if log == nil {
		log = slog.Default()
	}
	a := &API{pool: pool, wallets: wallets.NewService(pool, nil), auth: verifier, log: log, client: &http.Client{Timeout: 5 * time.Second}, providerBaseURL: strings.TrimRight(os.Getenv("RAZORPAY_BASE_URL"), "/"), webhookSecret: os.Getenv("RAZORPAY_WEBHOOK_SECRET"), sseActive: make(map[uuid.UUID]int)}
	a.ConfigureRequestLimits(30, 10, 50)
	return a
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", a.ready)
	mux.HandleFunc("POST /v1/payments/webhook", a.paymentWebhook)
	protected := http.NewServeMux()
	protected.HandleFunc("POST /v1/accounts", auth.Require("billing:write", a.createAccount))
	protected.HandleFunc("GET /v1/accounts/{id}", auth.Require("billing:read", a.getAccount))
	protected.HandleFunc("PATCH /v1/accounts/{id}", auth.Require("billing:write", a.updateAccount))
	protected.HandleFunc("POST /v1/accounts/{id}/links", auth.Require("billing:write", a.linkAccount))
	protected.HandleFunc("POST /v1/account-links", auth.Require("billing:write", a.linkCurrentAccount))
	protected.HandleFunc("POST /v1/products", a.requireAdmin("product.create", "product", a.createProduct))
	protected.HandleFunc("GET /v1/products", a.listProducts)
	protected.HandleFunc("POST /v1/plans", a.requireAdmin("plan.create", "plan", a.createPlan))
	protected.HandleFunc("GET /v1/plans", a.listPlans)
	protected.HandleFunc("PATCH /v1/plans/{id}", a.requireAdmin("plan.update", "plan", a.updatePlan))
	protected.HandleFunc("POST /v1/credit-packs", a.requireAdmin("credit_pack.create", "credit_pack", a.createCreditPack))
	protected.HandleFunc("POST /v1/subscriptions", auth.Require("billing:write", a.createSubscription))
	protected.HandleFunc("GET /v1/subscriptions/current", auth.Require("billing:read", a.currentSubscription))
	protected.HandleFunc("POST /v1/subscriptions/{id}/change-plan", auth.Require("billing:write", a.changeSubscriptionPlan))
	protected.HandleFunc("POST /v1/subscriptions/{id}/cancel", auth.Require("billing:write", a.cancelSubscription))
	protected.HandleFunc("POST /v1/subscriptions/current/cancel", auth.Require("billing:write", a.cancelCurrentSubscription))
	protected.HandleFunc("POST /v1/subscriptions/{id}/reactivate", auth.Require("billing:write", a.reactivateSubscription))
	protected.HandleFunc("POST /v1/subscriptions/{id}/renew", auth.Require("billing:write", a.renewSubscription))
	protected.HandleFunc("GET /v1/entitlements", auth.Require("billing:read", a.getEntitlements))
	protected.HandleFunc("GET /v1/entitlements/check", auth.Require("billing:read", a.checkEntitlement))
	protected.HandleFunc("POST /v1/wallets", auth.Require("billing:write", a.createWallet))
	protected.HandleFunc("GET /v1/wallets/{id}", auth.Require("billing:read", a.getWallet))
	protected.HandleFunc("GET /v1/wallets/{id}/ledger", auth.Require("billing:read", a.walletLedger))
	protected.HandleFunc("POST /v1/wallets/{id}/grants", auth.Require("credits:grant", a.grant))
	protected.HandleFunc("POST /v1/wallets/{id}/reservations", auth.Require("credits:reserve", a.reserve))
	protected.HandleFunc("POST /v1/reservations/{id}/settle", auth.Require("credits:settle", a.settle))
	protected.HandleFunc("POST /v1/reservations/{id}/release", auth.Require("credits:settle", a.releaseReservation))
	protected.HandleFunc("POST /v1/reservations/{id}/extend", auth.Require("credits:reserve", a.extendReservation))
	protected.HandleFunc("POST /v1/executions/authorize", auth.Require("credits:reserve", a.authorizeExecution))
	protected.HandleFunc("POST /v1/accounts/{id}/installations", auth.Require("billing:write", a.createInstallation))
	protected.HandleFunc("POST /v1/installations/{id}/revoke", auth.Require("billing:write", a.revokeInstallation))
	protected.HandleFunc("POST /v1/installations/{id}/settle-active", auth.Require("credits:settle", a.settleInstallationExecution))
	protected.HandleFunc("POST /v1/usage-events", auth.Require("credits:settle", a.recordUsage))
	protected.HandleFunc("GET /v1/usage-events", auth.Require("billing:read", a.listUsage))
	protected.HandleFunc("POST /v1/payments/orders", auth.Require("billing:write", a.createPaymentOrder))
	protected.HandleFunc("GET /v1/payments", auth.Require("billing:read", a.listPayments))
	protected.HandleFunc("GET /v1/invoices", auth.Require("billing:read", a.listInvoices))
	protected.HandleFunc("POST /v1/webhooks", auth.Require("billing:write", a.registerWebhook))
	protected.HandleFunc("GET /v1/webhooks", auth.Require("billing:read", a.listWebhooks))
	protected.HandleFunc("GET /v1/limits", auth.Require("billing:read", a.getLimits))
	protected.HandleFunc("POST /v1/admin/adjustments", a.requireAdmin("credit.adjust", "wallet", a.adminAdjustment))
	protected.HandleFunc("GET /v1/admin/audit", a.requireAdmin("audit.read", "audit_log", a.listAudit))
	protected.HandleFunc("POST /v1/admin/webhooks/{id}/replay", a.requireAdmin("webhook.replay", "webhook_delivery", a.replayWebhook))
	protected.HandleFunc("GET /v1/events", auth.Require("billing:read", a.events))
	if a.auth != nil {
		mux.Handle("/v1/", auth.MiddlewareWithHooks(a.auth, a.requestLimitHooks())(protected))
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
	if (claims.OrgID != in.OrganizationID || claims.App != in.Application) && !claims.Has("billing:link") {
		writeError(w, 403, "identity mismatch")
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
	environment := claims.Environment
	if environment == "" {
		environment = "production"
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO account_links(account_id,application,organization_id,environment) VALUES($1,$2,$3,$4)`, id, in.Application, in.OrganizationID, environment); err != nil {
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
	if strings.TrimSpace(in.Slug) == "" || strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "slug and name are required")
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
	if !a.canAccessProduct(r, in.ProductID) {
		writeError(w, 403, "product not accessible")
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
	if !a.canAccessWallet(r, id) {
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
	environment := claims.Environment
	if environment == "" {
		environment = "production"
	}
	var found bool
	err := a.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM account_links WHERE account_id=$1 AND application=$2 AND organization_id=$3 AND environment=$4)`, accountID, claims.App, claims.OrgID, environment).Scan(&found)
	return err == nil && found
}
func (a *API) canAccessWallet(r *http.Request, walletID uuid.UUID) bool {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		return false
	}
	environment := claims.Environment
	if environment == "" {
		environment = "production"
	}
	var found bool
	err := a.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM wallets w JOIN products p ON p.id=w.product_id JOIN account_links l ON l.account_id=w.account_id WHERE w.id=$1 AND l.application=$2 AND l.organization_id=$3 AND l.environment=$4 AND p.slug=$2)`, walletID, claims.App, claims.OrgID, environment).Scan(&found)
	return err == nil && found
}

func (a *API) canAccessProduct(r *http.Request, productID uuid.UUID) bool {
	_, ok := auth.FromContext(r.Context())
	if !ok {
		return false
	}
	var slug string
	if err := a.pool.QueryRow(r.Context(), `SELECT slug FROM products WHERE id=$1`, productID).Scan(&slug); err != nil {
		return false
	}
	return canReadProductSlug(r, slug)
}

func canReadProductSlug(r *http.Request, slug string) bool {
	claims, ok := auth.FromContext(r.Context())
	return ok && (slug == claims.App || claims.Has("billing:link"))
}

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.FromContext(r.Context())
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, http.StatusForbidden, "billing account not accessible")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "streaming unsupported")
		return
	}
	after := int64(0)
	if h := r.Header.Get("Last-Event-ID"); h != "" {
		var parseErr error
		after, parseErr = strconv.ParseInt(h, 10, 64)
		if parseErr != nil || after <= 0 {
			writeError(w, http.StatusBadRequest, "invalid event cursor")
			return
		}
		var exists, owned bool
		if err := a.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM outbox_events WHERE sequence=$1), EXISTS(SELECT 1 FROM outbox_events e WHERE e.sequence=$1 AND ((e.aggregate_type='wallet' AND EXISTS(SELECT 1 FROM wallets w JOIN products p ON p.id=w.product_id WHERE w.id=e.aggregate_id AND w.account_id=$2 AND (p.slug=$3 OR $4))) OR (e.aggregate_type='account' AND e.aggregate_id=$2) OR (e.aggregate_type='subscription' AND EXISTS(SELECT 1 FROM subscriptions s JOIN products p ON p.id=s.product_id WHERE s.id=e.aggregate_id AND s.account_id=$2 AND (p.slug=$3 OR $4)))))`, after, accountID, claims.App, claims.Has("billing:link")).Scan(&exists, &owned); err != nil {
			writeDBError(w, err)
			return
		}
		if exists && !owned {
			writeError(w, http.StatusForbidden, "event cursor not accessible")
			return
		}
		if !exists {
			writeError(w, http.StatusGone, "event history is no longer available; resynchronization required")
			return
		}
	}
	if !a.acquireSSE(accountID) {
		writeError(w, http.StatusTooManyRequests, "too many event streams for billing account")
		return
	}
	defer a.releaseSSE(accountID)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if claims.ExpiresAt != nil && !time.Now().Before(claims.ExpiresAt.Time) {
			return
		}
		rows, err := a.pool.Query(r.Context(), `SELECT e.sequence,e.event_type,e.payload FROM outbox_events e WHERE e.sequence>$1 AND ((e.aggregate_type='wallet' AND EXISTS(SELECT 1 FROM wallets w JOIN products p ON p.id=w.product_id WHERE w.id=e.aggregate_id AND w.account_id=$2 AND (p.slug=$3 OR $4))) OR (e.aggregate_type='account' AND e.aggregate_id=$2) OR (e.aggregate_type='subscription' AND EXISTS(SELECT 1 FROM subscriptions s JOIN products p ON p.id=s.product_id WHERE s.id=e.aggregate_id AND s.account_id=$2 AND (p.slug=$3 OR $4)))) ORDER BY e.sequence LIMIT 100`, after, accountID, claims.App, claims.Has("billing:link"))
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

func (a *API) acquireSSE(accountID uuid.UUID) bool {
	a.sseMu.Lock()
	defer a.sseMu.Unlock()
	if a.sseActive[accountID] >= maxSSEConnectionsPerAccount {
		return false
	}
	a.sseActive[accountID]++
	return true
}

func (a *API) releaseSSE(accountID uuid.UUID) {
	a.sseMu.Lock()
	defer a.sseMu.Unlock()
	a.sseActive[accountID]--
	if a.sseActive[accountID] == 0 {
		delete(a.sseActive, accountID)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, 415, "Content-Type must be application/json")
		return false
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, 400, "invalid JSON body")
		return false
	}
	check := json.NewDecoder(bytes.NewReader(raw))
	if err := rejectDuplicateJSONKeys(check, 0); err != nil {
		writeError(w, 400, "invalid JSON body")
		return false
	}
	if _, err := check.Token(); err != io.EOF {
		writeError(w, 400, "invalid JSON body")
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		writeError(w, 400, "invalid JSON body")
		return false
	}
	return true
}

func rejectDuplicateJSONKeys(d *json.Decoder, depth int) error {
	if depth > 100 {
		return errors.New("JSON nesting limit exceeded")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	start, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch start {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("duplicate or invalid JSON key")
			}
			seen[name] = true
			if err := rejectDuplicateJSONKeys(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := rejectDuplicateJSONKeys(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = d.Token()
	return err
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
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			writeError(w, 409, "resource already exists")
			return
		case "23502", "23503", "23514", "22P02":
			writeError(w, 400, "request violates a data constraint")
			return
		}
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
