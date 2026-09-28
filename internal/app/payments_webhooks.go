package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tociva/billmesh/internal/auth"
)

func (a *API) createPaymentOrder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID      uuid.UUID `json:"account_id"`
		CreditPack     string    `json:"credit_pack"`
		PriceMinor     *int64    `json:"price_minor"`
		IdempotencyKey string    `json:"idempotency_key"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.AccountID == uuid.Nil {
		var err error
		in.AccountID, err = a.accountIDForClaims(r.Context())
		if err != nil {
			writeError(w, 400, "billing account not found")
			return
		}
	}
	if !a.canAccessAccount(r, in.AccountID) {
		writeError(w, 403, "account not accessible")
		return
	}
	if in.IdempotencyKey != "" {
		paymentID, parseErr := uuid.Parse(in.IdempotencyKey)
		if parseErr != nil {
			writeError(w, 400, "invalid idempotency_key")
			return
		}
		var orderID, status, currency string
		var amount, credits int64
		err := a.pool.QueryRow(r.Context(), `SELECT provider_order_id,status,amount_minor,currency,credits FROM payments WHERE id=$1 AND account_id=$2`, paymentID, in.AccountID).Scan(&orderID, &status, &amount, &currency, &credits)
		if err != nil {
			writeDBError(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"payment_id": paymentID, "provider": "razorpay", "order": map[string]any{"id": orderID, "status": status, "amount": amount, "currency": currency}, "credit_pack": in.CreditPack, "credits": credits})
		return
	}
	var packID, productID uuid.UUID
	var credits, price int64
	var currency string
	if err := a.pool.QueryRow(r.Context(), `SELECT id,product_id,credits,price_minor,currency FROM credit_packs WHERE slug=$1 AND active`, in.CreditPack).Scan(&packID, &productID, &credits, &price, &currency); err != nil {
		writeError(w, 400, "unknown credit pack")
		return
	}
	if in.PriceMinor != nil && *in.PriceMinor != price {
		writeError(w, 400, "price is controlled by the server")
		return
	}
	providerOrder := map[string]any{"id": "order_" + uuid.NewString(), "status": "created", "amount": price, "currency": currency}
	if a.providerBaseURL != "" {
		raw, _ := json.Marshal(map[string]any{"amount": price, "currency": currency, "receipt": uuid.NewString()})
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, a.providerBaseURL+"/v1/orders", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		resp, err := a.client.Do(req)
		if err != nil {
			writeError(w, 503, "payment provider unavailable")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			writeError(w, 503, "payment provider temporarily unavailable")
			return
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			writeError(w, 502, "payment provider rejected order")
			return
		}
		if err = json.NewDecoder(resp.Body).Decode(&providerOrder); err != nil {
			writeError(w, 502, "invalid provider response")
			return
		}
	}
	orderID, _ := providerOrder["id"].(string)
	var id uuid.UUID
	err := a.pool.QueryRow(r.Context(), `INSERT INTO payments(account_id,provider,provider_order_id,status,amount_minor,currency,credit_pack_id,credits,operation_ref) VALUES($1,'razorpay',$2,'created',$3,$4,$5,$6,$7) RETURNING id`, in.AccountID, orderID, price, currency, packID, credits, "purchase:"+orderID).Scan(&id)
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"payment_id": id, "provider": "razorpay", "order": providerOrder, "credit_pack": in.CreditPack, "credits": credits})
}

func (a *API) paymentWebhook(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, 400, "invalid webhook body")
		return
	}
	signature := r.Header.Get("X-Razorpay-Signature")
	if a.webhookSecret == "" {
		writeError(w, 503, "webhook secret is not configured")
		return
	}
	mac := hmac.New(sha256.New, []byte(a.webhookSecret))
	mac.Write(raw)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		writeError(w, 401, "invalid webhook signature")
		return
	}
	var event struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		PaymentID string `json:"payment_id"`
		OrderID   string `json:"order_id"`
		Status    string `json:"status"`
		RefundID  string `json:"refund_id"`
		Amount    int64  `json:"amount_minor"`
		Currency  string `json:"currency"`
		Payload   struct {
			Payment struct {
				Entity struct {
					ID      string `json:"id"`
					OrderID string `json:"order_id"`
					Status  string `json:"status"`
				} `json:"entity"`
			} `json:"payment"`
			Refund struct {
				Entity struct {
					ID        string `json:"id"`
					PaymentID string `json:"payment_id"`
					Amount    int64  `json:"amount"`
				} `json:"entity"`
			} `json:"refund"`
		} `json:"payload"`
	}
	if json.Unmarshal(raw, &event) != nil || event.ID == "" || event.Type == "" {
		writeError(w, 400, "malformed webhook payload")
		return
	}
	if event.PaymentID == "" {
		event.PaymentID = event.Payload.Payment.Entity.ID
	}
	if event.OrderID == "" {
		event.OrderID = event.Payload.Payment.Entity.OrderID
	}
	if event.Status == "" {
		event.Status = event.Payload.Payment.Entity.Status
	}
	if event.RefundID == "" {
		event.RefundID = event.Payload.Refund.Entity.ID
	}
	if event.Type == "refund.processed" {
		if event.PaymentID == "" {
			event.PaymentID = event.Payload.Refund.Entity.PaymentID
		}
		if event.Amount == 0 {
			event.Amount = event.Payload.Refund.Entity.Amount
		}
	}
	switch event.Type {
	case "payment.captured", "order.paid", "payment.failed", "refund.processed":
	default:
		writeError(w, 400, "unsupported webhook event type")
		return
	}
	if event.Type == "payment.captured" || event.Type == "order.paid" {
		var expectedAmount int64
		var expectedCurrency string
		if err := a.pool.QueryRow(r.Context(), `SELECT amount_minor,currency FROM payments WHERE provider='razorpay' AND provider_order_id=$1`, event.OrderID).Scan(&expectedAmount, &expectedCurrency); err != nil {
			writeError(w, 400, "unknown payment order")
			return
		}
		if (event.Amount != 0 && event.Amount != expectedAmount) || (event.Currency != "" && event.Currency != expectedCurrency) {
			writeError(w, 400, "captured amount or currency does not match the order")
			return
		}
	}
	err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		var inserted uuid.UUID
		err := tx.QueryRow(r.Context(), `INSERT INTO provider_events(provider,provider_event_id,event_type,payload) VALUES('razorpay',$1,$2,$3) ON CONFLICT(provider,provider_event_id) DO NOTHING RETURNING id`, event.ID, event.Type, raw).Scan(&inserted)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		switch event.Type {
		case "payment.captured", "order.paid":
			err = capturePayment(r.Context(), tx, event.OrderID, event.PaymentID)
		case "payment.failed":
			_, err = tx.Exec(r.Context(), `UPDATE payments SET status='failed',provider_payment_id=COALESCE(NULLIF($2,''),provider_payment_id),failure_reason='provider reported failure',updated_at=now() WHERE provider='razorpay' AND provider_order_id=$1`, event.OrderID, event.PaymentID)
		case "refund.processed":
			err = refundPayment(r.Context(), tx, event.PaymentID, event.RefundID, event.Amount)
		}
		if err == nil {
			_, err = tx.Exec(r.Context(), `UPDATE provider_events SET processed_at=now() WHERE id=$1`, inserted)
		}
		return err
	})
	if err != nil {
		writeDBError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func refundPayment(ctx context.Context, tx pgx.Tx, providerPaymentID, providerRefundID string, refundAmount int64) error {
	if providerPaymentID == "" || providerRefundID == "" || refundAmount <= 0 {
		return errors.New("refund event is missing payment, refund, or amount")
	}
	var paymentID, accountID, productID uuid.UUID
	var amount, credits int64
	var currency, status string
	if err := tx.QueryRow(ctx, `SELECT p.id,p.account_id,cp.product_id,p.amount_minor,p.credits,p.currency,p.status
		FROM payments p JOIN credit_packs cp ON cp.id=p.credit_pack_id
		WHERE p.provider='razorpay' AND p.provider_payment_id=$1 FOR UPDATE`, providerPaymentID).
		Scan(&paymentID, &accountID, &productID, &amount, &credits, &currency, &status); err != nil {
		return err
	}
	if status != "captured" && status != "refunded" {
		return errors.New("payment is not refundable")
	}
	var refunded int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount_minor),0) FROM refunds WHERE payment_id=$1`, paymentID).Scan(&refunded); err != nil {
		return err
	}
	if refunded+refundAmount > amount {
		return errors.New("refund exceeds captured amount")
	}
	creditsToReverse := credits * refundAmount / amount
	if refunded+refundAmount == amount {
		var alreadyReversed int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(credits_reversed),0) FROM refunds WHERE payment_id=$1`, paymentID).Scan(&alreadyReversed); err != nil {
			return err
		}
		creditsToReverse = credits - alreadyReversed
	}
	var walletID, grantID uuid.UUID
	var remaining int64
	if err := tx.QueryRow(ctx, `SELECT w.id,g.id,g.remaining FROM wallets w JOIN credit_grants g ON g.wallet_id=w.id
		WHERE w.account_id=$1 AND w.product_id=$2 AND g.operation_ref=$3 FOR UPDATE OF w,g`, accountID, productID, "payment:"+paymentID.String()).Scan(&walletID, &grantID, &remaining); err != nil {
		return err
	}
	if creditsToReverse > remaining {
		creditsToReverse = remaining
	}
	var refundID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO refunds(payment_id,provider_refund_id,amount_minor,credits_reversed)
		VALUES($1,$2,$3,$4) ON CONFLICT(provider_refund_id) DO NOTHING RETURNING id`, paymentID, providerRefundID, refundAmount, creditsToReverse).Scan(&refundID); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if creditsToReverse > 0 {
		if _, err := tx.Exec(ctx, `UPDATE credit_grants SET remaining=remaining-$2 WHERE id=$1`, grantID, creditsToReverse); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE wallets SET available=available-$2 WHERE id=$1`, walletID, creditsToReverse); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO credit_ledger(wallet_id,grant_id,operation_ref,kind,available_delta,reserved_delta)
			VALUES($1,$2,$3,'refund',$4,0)`, walletID, grantID, "refund:"+providerRefundID, -creditsToReverse); err != nil {
			return err
		}
	}
	newStatus := "captured"
	if refunded+refundAmount == amount {
		newStatus = "refunded"
	}
	if _, err := tx.Exec(ctx, `UPDATE payments SET status=$2,updated_at=now() WHERE id=$1`, paymentID, newStatus); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO credit_notes(invoice_id,refund_id,credit_note_number,currency,amount_minor)
		SELECT id,$2,$3,$4,$5 FROM invoices WHERE payment_id=$1 ON CONFLICT(refund_id) DO NOTHING`, paymentID, refundID, "CN-"+time.Now().UTC().Format("20060102")+"-"+refundID.String()[:8], currency, refundAmount); err != nil {
		return err
	}
	return appEvent(ctx, tx, "wallet", walletID, "payment.refunded", map[string]any{"payment_id": paymentID, "refund_id": refundID, "amount_minor": refundAmount, "credits_reversed": creditsToReverse})
}
func capturePayment(ctx context.Context, tx pgx.Tx, orderID, paymentID string) error {
	var payment, account, pack, product uuid.UUID
	var credits, amount int64
	var currency string
	err := tx.QueryRow(ctx, `SELECT p.id,p.account_id,p.credit_pack_id,cp.product_id,p.credits,p.amount_minor,p.currency FROM payments p JOIN credit_packs cp ON cp.id=p.credit_pack_id WHERE p.provider='razorpay' AND p.provider_order_id=$1 FOR UPDATE`, orderID).Scan(&payment, &account, &pack, &product, &credits, &amount, &currency)
	if err != nil {
		return err
	}
	var current string
	if err = tx.QueryRow(ctx, `SELECT status FROM payments WHERE id=$1`, payment).Scan(&current); err != nil {
		return err
	}
	if current == "captured" || current == "refunded" {
		return nil
	}
	if _, err = tx.Exec(ctx, `UPDATE payments SET status='captured',provider_payment_id=$2,updated_at=now() WHERE id=$1`, payment, paymentID); err != nil {
		return err
	}
	var wallet uuid.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO wallets(account_id,product_id) VALUES($1,$2) ON CONFLICT(account_id,product_id) DO UPDATE SET account_id=excluded.account_id RETURNING id`, account, product).Scan(&wallet); err != nil {
		return err
	}
	operation := "payment:" + payment.String()
	var grant uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO credit_grants(wallet_id,source,operation_ref,amount,remaining) VALUES($1,'purchase',$2,$3,$3) ON CONFLICT(operation_ref) DO NOTHING RETURNING id`, wallet, operation, credits).Scan(&grant)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE wallets SET available=available+$2 WHERE id=$1`, wallet, credits); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO credit_ledger(wallet_id,grant_id,operation_ref,kind,available_delta,reserved_delta) VALUES($1,$2,$3,'grant',$4,0)`, wallet, grant, "grant:"+operation, credits); err != nil {
		return err
	}
	invoiceNumber := "INV-" + time.Now().UTC().Format("20060102") + "-" + payment.String()[:8]
	if _, err = tx.Exec(ctx, `INSERT INTO invoices(account_id,payment_id,billing_operation_ref,invoice_number,status,currency,subtotal_minor,total_minor,finalized_at,paid_at) VALUES($1,$2,$3,$4,'paid',$5,$6,$6,now(),now()) ON CONFLICT(billing_operation_ref) DO NOTHING`, account, payment, operation, invoiceNumber, currency, amount); err != nil {
		return err
	}
	return appEvent(ctx, tx, "wallet", wallet, "payment.succeeded", map[string]any{"payment_id": payment, "credits": credits})
}

func (a *API) listPayments(w http.ResponseWriter, r *http.Request) {
	account, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "account not found")
		return
	}
	rows, err := a.pool.Query(r.Context(), `SELECT id,provider,provider_order_id,provider_payment_id,status,amount_minor,currency,created_at FROM payments WHERE account_id=$1 ORDER BY created_at DESC LIMIT $2`, account, parseLimit(r))
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id uuid.UUID
		var provider, status, currency string
		var order, payment *string
		var amount int64
		var created time.Time
		if rows.Scan(&id, &provider, &order, &payment, &status, &amount, &currency, &created) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "provider": provider, "provider_order_id": order, "provider_payment_id": payment, "status": status, "amount_minor": amount, "currency": currency, "created_at": created})
	}
	writeJSON(w, 200, items)
}
func (a *API) listInvoices(w http.ResponseWriter, r *http.Request) {
	account, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "account not found")
		return
	}
	rows, err := a.pool.Query(r.Context(), `SELECT id,invoice_number,status,currency,total_minor,finalized_at,paid_at,created_at FROM invoices WHERE account_id=$1 ORDER BY created_at DESC LIMIT $2`, account, parseLimit(r))
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id uuid.UUID
		var number, status, currency string
		var total int64
		var finalized, paid *time.Time
		var created time.Time
		if rows.Scan(&id, &number, &status, &currency, &total, &finalized, &paid, &created) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "invoice_number": number, "status": status, "currency": currency, "total_minor": total, "finalized_at": finalized, "paid_at": paid, "created_at": created})
	}
	writeJSON(w, 200, items)
}

func (a *API) registerWebhook(w http.ResponseWriter, r *http.Request) {
	account, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "account not found")
		return
	}
	var in struct {
		Application, TargetURL, Secret string `json:"-"`
	}
	var raw map[string]string
	if !decode(w, r, &raw) {
		return
	}
	in.Application = raw["application"]
	in.TargetURL = raw["target_url"]
	in.Secret = raw["secret"]
	if in.Application == "" || in.TargetURL == "" || in.Secret == "" {
		writeError(w, 400, "application, target_url and secret are required")
		return
	}
	target, parseErr := url.ParseRequestURI(in.TargetURL)
	if parseErr != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		writeError(w, 400, "target_url must be an absolute HTTP(S) URL")
		return
	}
	if host := net.ParseIP(target.Hostname()); host != nil && (host.IsLoopback() || host.IsPrivate() || host.IsLinkLocalUnicast() || host.IsLinkLocalMulticast() || host.IsUnspecified()) {
		writeError(w, 400, "target_url must not address a private or local network")
		return
	}
	var id uuid.UUID
	err = a.pool.QueryRow(r.Context(), `INSERT INTO webhook_endpoints(account_id,application,target_url,secret) VALUES($1,$2,$3,$4) ON CONFLICT(account_id,application) DO UPDATE SET target_url=excluded.target_url,secret=excluded.secret,active=true,updated_at=now() RETURNING id`, account, in.Application, in.TargetURL, in.Secret).Scan(&id)
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "application": in.Application, "target_url": in.TargetURL})
}
func (a *API) listWebhooks(w http.ResponseWriter, r *http.Request) {
	account, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "account not found")
		return
	}
	rows, err := a.pool.Query(r.Context(), `SELECT id,application,target_url,active,created_at FROM webhook_endpoints WHERE account_id=$1 ORDER BY application`, account)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id uuid.UUID
		var application, target string
		var active bool
		var created time.Time
		if rows.Scan(&id, &application, &target, &active, &created) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "application": application, "target_url": target, "active": active, "created_at": created})
	}
	writeJSON(w, 200, items)
}

func (a *API) adminAdjustment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		WalletID uuid.UUID `json:"wallet_id"`
		Amount   int64
		Reason   string
	}
	if !decode(w, r, &in) {
		return
	}
	if in.WalletID == uuid.Nil || in.Amount <= 0 || in.Reason == "" {
		writeError(w, 400, "wallet_id, positive amount and reason are required")
		return
	}
	if !a.canAccessWallet(r, in.WalletID) {
		writeError(w, 403, "wallet not accessible")
		return
	}
	operation := "admin:" + uuid.NewString()
	if err := a.wallets.Grant(r.Context(), in.WalletID, "admin", operation, in.Amount, nil); err != nil {
		writeDBError(w, err)
		return
	}
	claims, _ := auth.FromContext(r.Context())
	var accountID uuid.UUID
	if err := a.pool.QueryRow(r.Context(), `SELECT account_id FROM wallets WHERE id=$1`, in.WalletID).Scan(&accountID); err != nil {
		writeDBError(w, err)
		return
	}
	_, err := a.pool.Exec(r.Context(), `INSERT INTO audit_log(account_id,actor_subject,actor_type,action,resource_type,resource_id,reason,after_state) VALUES($1,$2,'user','credit.adjust','wallet',$3,$4,$5)`, accountID, claims.Subject, in.WalletID.String(), in.Reason, map[string]any{"amount": in.Amount, "operation_ref": operation})
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"operation_ref": operation})
}
func (a *API) listAudit(w http.ResponseWriter, r *http.Request) {
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "account not found")
		return
	}
	rows, err := a.pool.Query(r.Context(), `SELECT id,actor_subject,actor_type,action,resource_type,resource_id,reason,created_at FROM audit_log WHERE account_id=$1 ORDER BY created_at DESC LIMIT $2`, accountID, parseLimit(r))
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id uuid.UUID
		var actor, actorType, action, resourceType, resourceID string
		var reason *string
		var created time.Time
		if rows.Scan(&id, &actor, &actorType, &action, &resourceType, &resourceID, &reason, &created) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "actor": actor, "actor_type": actorType, "action": action, "resource_type": resourceType, "resource_id": resourceID, "reason": reason, "created_at": created})
	}
	writeJSON(w, 200, items)
}
func (a *API) replayWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid delivery id")
		return
	}
	tag, err := a.pool.Exec(r.Context(), `UPDATE webhook_deliveries SET status='pending',next_attempt_at=now(),last_error=NULL WHERE id=$1`, id)
	if err != nil || tag.RowsAffected() != 1 {
		writeDBError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
