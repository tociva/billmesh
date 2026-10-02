package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tociva/billmesh/internal/auth"
	"github.com/tociva/billmesh/internal/products"
)

func (a *API) accountIDForClaims(ctx context.Context) (uuid.UUID, error) {
	claims, ok := auth.FromContext(ctx)
	if !ok {
		return uuid.Nil, errors.New("missing claims")
	}
	var id uuid.UUID
	environment := claims.Environment
	if environment == "" {
		environment = "production"
	}
	err := a.pool.QueryRow(ctx, `SELECT account_id FROM account_links WHERE application=$1 AND organization_id=$2 AND environment=$3 ORDER BY created_at LIMIT 1`, claims.App, claims.OrgID, environment).Scan(&id)
	return id, err
}

func (a *API) getAccount(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid account id")
		return
	}
	if !a.canAccessAccount(r, id) {
		writeError(w, 403, "account not accessible")
		return
	}
	var name string
	var ref *string
	var created, updated time.Time
	if err = a.pool.QueryRow(r.Context(), `SELECT name,external_ref,created_at,updated_at FROM billing_accounts WHERE id=$1`, id).Scan(&name, &ref, &created, &updated); err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "name": name, "external_ref": ref, "created_at": created, "updated_at": updated})
}

func (a *API) getCurrentAccount(w http.ResponseWriter, r *http.Request) {
	id, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "billing account not found")
		return
	}
	var name string
	var ref *string
	var created, updated time.Time
	if err = a.pool.QueryRow(r.Context(), `SELECT name,external_ref,created_at,updated_at FROM billing_accounts WHERE id=$1`, id).Scan(&name, &ref, &created, &updated); err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "name": name, "external_ref": ref, "created_at": created, "updated_at": updated})
}
func (a *API) updateAccount(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid account id")
		return
	}
	if !a.canAccessAccount(r, id) {
		writeError(w, 403, "account not accessible")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Name == "" {
		writeError(w, 400, "name is required")
		return
	}
	tag, err := a.pool.Exec(r.Context(), `UPDATE billing_accounts SET name=$2,updated_at=now() WHERE id=$1`, id, in.Name)
	if err != nil || tag.RowsAffected() != 1 {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "name": in.Name})
}
func (a *API) linkAccount(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid account id")
		return
	}
	if !a.canAccessAccount(r, id) {
		writeError(w, 403, "account not accessible")
		return
	}
	var in struct {
		Application    string `json:"application"`
		OrganizationID string `json:"organization_id"`
		Environment    string `json:"environment"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Application == "" || in.OrganizationID == "" {
		writeError(w, 400, "application and organization_id are required")
		return
	}
	if in.Environment == "" {
		in.Environment = "production"
	}
	claims, _ := auth.FromContext(r.Context())
	callerEnvironment := claims.Environment
	if callerEnvironment == "" {
		callerEnvironment = "production"
	}
	if (in.Application != claims.App || in.OrganizationID != claims.OrgID || in.Environment != callerEnvironment) && !claims.Has("billing:link") {
		writeError(w, 403, "target identity requires billing:link")
		return
	}
	_, err = a.pool.Exec(r.Context(), `INSERT INTO account_links(account_id,application,organization_id,environment) VALUES($1,$2,$3,$4)`, id, in.Application, in.OrganizationID, in.Environment)
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeNoContent(w)
}
func (a *API) linkCurrentAccount(w http.ResponseWriter, r *http.Request) {
	id, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "account not found")
		return
	}
	r.SetPathValue("id", id.String())
	a.linkAccount(w, r)
}

func (a *API) listProducts(w http.ResponseWriter, r *http.Request) {
	rows, err := a.pool.Query(r.Context(), `SELECT id,slug,name,description,entitlement_schema,entitlement_schema_version,active,version,created_at,updated_at FROM products WHERE active ORDER BY slug,id`)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	items := []productRecord{}
	for rows.Next() {
		var item productRecord
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.Description, &item.EntitlementSchema, &item.EntitlementSchemaVersion, &item.Active, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
			writeDBError(w, err)
			return
		}
		items = append(items, item)
	}
	writeJSON(w, 200, items)
}
func (a *API) createPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProductID       uuid.UUID      `json:"product_id"`
		Slug            string         `json:"slug"`
		Name            string         `json:"name"`
		Currency        string         `json:"currency"`
		BillingInterval string         `json:"billing_interval"`
		PriceMinor      int64          `json:"price_minor"`
		IncludedCredits int64          `json:"included_credits"`
		Entitlements    map[string]any `json:"entitlements"`
		SchemaVersion   *int64         `json:"entitlement_schema_version"`
		Active          *bool          `json:"active"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.BillingInterval == "" {
		in.BillingInterval = "monthly"
	}
	if in.Currency == "" {
		in.Currency = "INR"
	}
	if nestedProduct := r.PathValue("id"); nestedProduct != "" {
		productID, err := uuid.Parse(nestedProduct)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid product id")
			return
		}
		if in.ProductID != uuid.Nil && in.ProductID != productID {
			writeError(w, http.StatusBadRequest, "product id does not match request path")
			return
		}
		in.ProductID = productID
	}
	in.Slug = strings.TrimSpace(in.Slug)
	in.Name = strings.TrimSpace(in.Name)
	if err := products.ValidatePlan(products.PlanInput{PriceMinor: in.PriceMinor, IncludedCredits: in.IncludedCredits, Currency: in.Currency, BillingInterval: in.BillingInterval}); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := products.ValidatePlanSlug(in.Slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := products.ValidatePlanName(in.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.ProductID == uuid.Nil || in.Slug == "" || in.Name == "" {
		writeError(w, 400, "product_id, slug and name are required")
		return
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	if in.Entitlements == nil {
		in.Entitlements = map[string]any{}
	}
	var created planRecord
	err := pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		var productSlug string
		var productActive bool
		var entitlementSchema products.EntitlementSchema
		var entitlementSchemaVersion int64
		if err := tx.QueryRow(r.Context(), `SELECT slug,active,entitlement_schema,entitlement_schema_version FROM products WHERE id=$1 FOR SHARE`, in.ProductID).Scan(&productSlug, &productActive, &entitlementSchema, &entitlementSchemaVersion); err != nil {
			return err
		}
		if in.SchemaVersion != nil && *in.SchemaVersion != entitlementSchemaVersion {
			return errEntitlementSchemaVersionConflict
		}
		validatedEntitlements, err := products.ValidateEntitlements(entitlementSchema, in.Entitlements)
		if err != nil {
			return entitlementValidationError{err.Error()}
		}
		in.Entitlements = validatedEntitlements
		if active && !productActive {
			return catalogueValidationError{"active plans require an active product"}
		}
		row := tx.QueryRow(r.Context(), `INSERT INTO plans(product_id,slug,name,price_minor,currency,included_credits,entitlements,active,billing_interval)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
			RETURNING id,product_id,$10::text,slug,name,price_minor,currency,included_credits,entitlements,billing_interval,active,version,created_at,updated_at`,
			in.ProductID, in.Slug, in.Name, in.PriceMinor, in.Currency, in.IncludedCredits, in.Entitlements, active, in.BillingInterval, productSlug)
		created, err = scanPlan(row)
		if err != nil {
			return err
		}
		return a.writeCatalogueAudit(r.Context(), tx, "plan.create", "plan", created.ID, nil, planState(created))
	})
	var validation catalogueValidationError
	var entitlementValidation entitlementValidationError
	if errors.Is(err, errEntitlementSchemaVersionConflict) {
		writeError(w, http.StatusConflict, "entitlement schema was modified; reload the product")
		return
	}
	if errors.As(err, &entitlementValidation) {
		writeError(w, http.StatusBadRequest, entitlementValidation.Error())
		return
	}
	if errors.As(err, &validation) {
		writeError(w, http.StatusConflict, validation.Error())
		return
	}
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}
func (a *API) listPlans(w http.ResponseWriter, r *http.Request) {
	product := r.URL.Query().Get("product")
	rows, err := a.pool.Query(r.Context(), `SELECT p.id,p.product_id,pr.slug,p.slug,p.name,p.price_minor,p.currency,p.included_credits,p.entitlements,
		p.billing_interval,p.active,p.version,p.created_at,p.updated_at
		FROM plans p JOIN products pr ON pr.id=p.product_id
		WHERE p.active AND pr.active AND ($1='' OR pr.slug=$1) ORDER BY pr.slug,p.price_minor,p.slug,p.id`, product)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	items := []planRecord{}
	for rows.Next() {
		item, err := scanPlan(rows)
		if err != nil {
			writeDBError(w, err)
			return
		}
		items = append(items, item)
	}
	writeJSON(w, 200, items)
}
func (a *API) updatePlan(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid plan id")
		return
	}
	var in struct {
		Name            *string        `json:"name"`
		PriceMinor      *int64         `json:"price_minor"`
		Currency        *string        `json:"currency"`
		IncludedCredits *int64         `json:"included_credits"`
		Entitlements    map[string]any `json:"entitlements"`
		SchemaVersion   *int64         `json:"entitlement_schema_version"`
		BillingInterval *string        `json:"billing_interval"`
		Active          *bool          `json:"active"`
		Version         *int64         `json:"version"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/admin/") && (in.Version == nil || *in.Version < 1) {
		writeError(w, http.StatusBadRequest, "version is required")
		return
	}
	var updated planRecord
	err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(r.Context(), `SELECT p.id,p.product_id,pr.slug,p.slug,p.name,p.price_minor,p.currency,p.included_credits,
			p.entitlements,p.billing_interval,p.active,p.version,p.created_at,p.updated_at
			FROM plans p JOIN products pr ON pr.id=p.product_id WHERE p.id=$1 FOR UPDATE OF p`, id)
		before, err := scanPlan(row)
		if err != nil {
			return err
		}
		if in.Version != nil && before.Version != *in.Version {
			return errCatalogueVersionConflict
		}
		updated = before
		if in.Name != nil {
			updated.Name = strings.TrimSpace(*in.Name)
		}
		if in.PriceMinor != nil {
			updated.PriceMinor = *in.PriceMinor
		}
		if in.Currency != nil {
			updated.Currency = strings.TrimSpace(*in.Currency)
		}
		if in.IncludedCredits != nil {
			updated.IncludedCredits = *in.IncludedCredits
		}
		if in.Entitlements != nil {
			var entitlementSchema products.EntitlementSchema
			var entitlementSchemaVersion int64
			if err := tx.QueryRow(r.Context(), `SELECT entitlement_schema,entitlement_schema_version FROM products WHERE id=$1 FOR SHARE`, before.ProductID).Scan(&entitlementSchema, &entitlementSchemaVersion); err != nil {
				return err
			}
			if in.SchemaVersion != nil && *in.SchemaVersion != entitlementSchemaVersion {
				return errEntitlementSchemaVersionConflict
			}
			validatedEntitlements, err := products.ValidateEntitlements(entitlementSchema, in.Entitlements)
			if err != nil {
				return catalogueValidationError{err.Error()}
			}
			updated.Entitlements = validatedEntitlements
		}
		if in.BillingInterval != nil {
			updated.BillingInterval = *in.BillingInterval
		}
		if in.Active != nil {
			updated.Active = *in.Active
		}
		if err := products.ValidatePlanName(updated.Name); err != nil {
			return catalogueValidationError{err.Error()}
		}
		if err := products.ValidatePlan(products.PlanInput{PriceMinor: updated.PriceMinor, IncludedCredits: updated.IncludedCredits, Currency: updated.Currency, BillingInterval: updated.BillingInterval}); err != nil {
			return catalogueValidationError{err.Error()}
		}
		if updated.Active {
			var productActive bool
			if err := tx.QueryRow(r.Context(), `SELECT active FROM products WHERE id=$1`, updated.ProductID).Scan(&productActive); err != nil {
				return err
			}
			if !productActive {
				return catalogueValidationError{"active plans require an active product"}
			}
		}
		row = tx.QueryRow(r.Context(), `UPDATE plans SET name=$2,price_minor=$3,currency=$4,included_credits=$5,entitlements=$6,
			billing_interval=$7,active=$8,version=version+1,updated_at=now() WHERE id=$1
			RETURNING id,product_id,$9::text,slug,name,price_minor,currency,included_credits,entitlements,billing_interval,active,version,created_at,updated_at`,
			id, updated.Name, updated.PriceMinor, updated.Currency, updated.IncludedCredits, updated.Entitlements,
			updated.BillingInterval, updated.Active, before.Product)
		updated, err = scanPlan(row)
		if err != nil {
			return err
		}
		return a.writeCatalogueAudit(r.Context(), tx, "plan.update", "plan", id, planState(before), planState(updated))
	})
	if errors.Is(err, errCatalogueVersionConflict) {
		writeError(w, http.StatusConflict, "plan was modified by another request")
		return
	}
	if errors.Is(err, errEntitlementSchemaVersionConflict) {
		writeError(w, http.StatusConflict, "entitlement schema was modified; reload the product")
		return
	}
	var validation catalogueValidationError
	if errors.As(err, &validation) {
		writeError(w, http.StatusBadRequest, validation.Error())
		return
	}
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
func (a *API) createCreditPack(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProductID    uuid.UUID `json:"product_id"`
		Slug         string    `json:"slug"`
		Name         string    `json:"name"`
		Currency     string    `json:"currency"`
		Credits      int64     `json:"credits"`
		PriceMinor   int64     `json:"price_minor"`
		ValidityDays *int      `json:"validity_days"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.ProductID == uuid.Nil || in.Slug == "" || in.Name == "" || in.Credits <= 0 || in.PriceMinor < 0 {
		writeError(w, 400, "invalid credit pack")
		return
	}
	if in.Currency == "" {
		in.Currency = "INR"
	}
	var id uuid.UUID
	if err := a.pool.QueryRow(r.Context(), `INSERT INTO credit_packs(product_id,slug,name,credits,price_minor,currency,validity_days) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, in.ProductID, in.Slug, in.Name, in.Credits, in.PriceMinor, in.Currency, in.ValidityDays).Scan(&id); err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

func billingPeriod(start time.Time, interval string) time.Time {
	if interval == "annual" {
		return start.AddDate(1, 0, 0)
	}
	return start.AddDate(0, 1, 0)
}
func (a *API) createSubscription(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID     uuid.UUID `json:"account_id"`
		PlanID        uuid.UUID `json:"plan_id"`
		Product       string    `json:"product"`
		Plan          string    `json:"plan"`
		PaymentStatus string    `json:"payment_status"`
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
	var planID, productID uuid.UUID
	var price, credits int64
	var currency, interval string
	var entitlements map[string]any
	var active, productActive bool
	query := `SELECT p.id,p.product_id,p.price_minor,p.included_credits,p.currency,p.billing_interval,p.entitlements,p.active,pr.active FROM plans p JOIN products pr ON pr.id=p.product_id WHERE `
	args := []any{}
	if in.PlanID != uuid.Nil {
		query += `p.id=$1`
		args = []any{in.PlanID}
	} else {
		query += `p.slug=$1 AND ($2='' OR pr.slug=$2)`
		args = []any{in.Plan, in.Product}
	}
	if err := a.pool.QueryRow(r.Context(), query, args...).Scan(&planID, &productID, &price, &credits, &currency, &interval, &entitlements, &active, &productActive); err != nil {
		writeDBError(w, err)
		return
	}
	if !active || !productActive {
		writeError(w, 409, "plan is inactive")
		return
	}
	if !a.canAccessProduct(r, productID) {
		writeError(w, 403, "product not accessible")
		return
	}
	var duplicate bool
	if err := a.pool.QueryRow(r.Context(), `SELECT EXISTS(
		SELECT 1 FROM subscriptions s JOIN plans existing ON existing.id=s.plan_id
		WHERE s.account_id=$1 AND existing.product_id=$2 AND s.status IN ('pending','active','past_due')
	)`, in.AccountID, productID).Scan(&duplicate); err != nil {
		writeDBError(w, err)
		return
	}
	if duplicate {
		writeError(w, 409, "an active subscription already exists for this product")
		return
	}
	status := "pending"
	if price == 0 || in.PaymentStatus == "verified" {
		status = "active"
	}
	now := time.Now().UTC()
	var subscriptionID uuid.UUID
	err := pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `INSERT INTO subscriptions(account_id,plan_id,product_id,status,current_period_start,current_period_end,price_minor,currency,billing_interval,included_credits,entitlements)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`, in.AccountID, planID, productID, status, now, billingPeriod(now, interval), price, currency, interval, credits, entitlements).Scan(&subscriptionID); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO subscription_history(subscription_id,status,plan_id,operation_ref) VALUES($1,$2,$3,$4)`, subscriptionID, status, planID, "subscription:create:"+subscriptionID.String()); err != nil {
			return err
		}
		if status == "active" && credits > 0 {
			if err := allocateCredits(r.Context(), tx, in.AccountID, productID, subscriptionID, credits, billingPeriod(now, interval)); err != nil {
				return err
			}
		}
		if status == "active" && price > 0 {
			invoiceNumber := "INV-" + now.Format("20060102") + "-" + subscriptionID.String()[:8]
			if _, err := tx.Exec(r.Context(), `INSERT INTO invoices(account_id,subscription_id,billing_operation_ref,invoice_number,status,currency,subtotal_minor,total_minor,finalized_at,paid_at)
				VALUES($1,$2,$3,$4,'paid',$5,$6,$6,now(),now()) ON CONFLICT(billing_operation_ref) DO NOTHING`, in.AccountID, subscriptionID, "subscription:"+subscriptionID.String(), invoiceNumber, currency, price); err != nil {
				return err
			}
		}
		return accountEvent(r.Context(), tx, in.AccountID, "subscription", subscriptionID, "subscription."+status, map[string]any{"subscription_id": subscriptionID, "plan_id": planID, "status": status})
	})
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": subscriptionID, "status": status, "current_period_end": billingPeriod(now, interval)})
}
func allocateCredits(ctx context.Context, tx pgx.Tx, accountID, productID, subscriptionID uuid.UUID, credits int64, expires time.Time) error {
	var walletID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO wallets(account_id,product_id) VALUES($1,$2) ON CONFLICT(account_id,product_id) DO UPDATE SET account_id=excluded.account_id RETURNING id`, accountID, productID).Scan(&walletID); err != nil {
		return err
	}
	op := "subscription:" + subscriptionID.String() + ":" + expires.UTC().Format("2006-01-02")
	var grantID uuid.UUID
	err := tx.QueryRow(ctx, `INSERT INTO credit_grants(wallet_id,source,operation_ref,amount,remaining,expires_at) VALUES($1,'subscription',$2,$3,$3,$4) ON CONFLICT(operation_ref) DO NOTHING RETURNING id`, walletID, op, credits, expires).Scan(&grantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE wallets SET available=available+$2 WHERE id=$1`, walletID, credits); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO credit_ledger(wallet_id,grant_id,operation_ref,kind,available_delta,reserved_delta) VALUES($1,$2,$3,'grant',$4,0)`, walletID, grantID, "grant:"+op, credits); err != nil {
		return err
	}
	return appEvent(ctx, tx, "wallet", walletID, "credits.granted", map[string]any{"amount": credits, "source": "subscription"})
}
func (a *API) currentSubscription(w http.ResponseWriter, r *http.Request) {
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "subscription not found")
		return
	}
	var id, planID uuid.UUID
	var status string
	var start, end *time.Time
	var cancel bool
	product := r.URL.Query().Get("product")
	if product == "" {
		claims, _ := auth.FromContext(r.Context())
		product = claims.App
	}
	if !canReadProductSlug(r, product) {
		writeError(w, 403, "product not accessible")
		return
	}
	err = a.pool.QueryRow(r.Context(), `SELECT s.id,s.plan_id,s.status,s.current_period_start,s.current_period_end,s.cancel_at_period_end
		FROM subscriptions s JOIN plans p ON p.id=s.plan_id JOIN products pr ON pr.id=p.product_id
		WHERE s.account_id=$1 AND pr.slug=$2 ORDER BY s.created_at DESC LIMIT 1`, accountID, product).Scan(&id, &planID, &status, &start, &end, &cancel)
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "plan_id": planID, "status": status, "current_period_start": start, "current_period_end": end, "cancel_at_period_end": cancel})
}
func (a *API) changeSubscriptionPlan(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid subscription id")
		return
	}
	if !a.canAccessSubscription(r, id) {
		writeError(w, 403, "subscription not accessible")
		return
	}
	var in struct {
		PlanID        uuid.UUID `json:"plan_id"`
		Plan          string    `json:"plan"`
		Effective     string    `json:"effective"`
		PaymentStatus string    `json:"payment_status"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.PaymentStatus == "failed" {
		writeError(w, http.StatusPaymentRequired, "plan change payment failed")
		return
	}
	if in.PlanID == uuid.Nil && in.Plan != "" {
		if err := a.pool.QueryRow(r.Context(), `SELECT p.id FROM plans p JOIN subscriptions s ON s.product_id=p.product_id WHERE s.id=$1 AND p.slug=$2 AND p.active`, id, in.Plan).Scan(&in.PlanID); err != nil {
			writeError(w, 400, "unknown plan")
			return
		}
	}
	if in.PlanID == uuid.Nil {
		writeError(w, 400, "plan_id is required")
		return
	}
	err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		var accountID uuid.UUID
		var status string
		if err := tx.QueryRow(r.Context(), `UPDATE subscriptions s SET plan_id=$2,version=s.version+1,updated_at=now(),price_minor=p.price_minor,currency=p.currency,
			billing_interval=p.billing_interval,included_credits=p.included_credits,entitlements=p.entitlements
			FROM plans p JOIN products pr ON pr.id=p.product_id
			WHERE s.id=$1 AND p.id=$2 AND p.active AND pr.active AND p.product_id=s.product_id RETURNING s.account_id,s.status`, id, in.PlanID).Scan(&accountID, &status); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO subscription_history(subscription_id,status,plan_id,operation_ref) VALUES($1,$2,$3,$4)`, id, status, in.PlanID, "subscription:change:"+uuid.NewString()); err != nil {
			return err
		}
		return accountEvent(r.Context(), tx, accountID, "subscription", id, "subscription.plan_changed", map[string]any{"subscription_id": id, "plan_id": in.PlanID, "effective": in.Effective})
	})
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "plan_id": in.PlanID})
}
func (a *API) cancelSubscription(w http.ResponseWriter, r *http.Request) {
	a.setSubscriptionStatus(w, r, "cancelled")
}
func (a *API) cancelCurrentSubscription(w http.ResponseWriter, r *http.Request) {
	account, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "account not found")
		return
	}
	claims, _ := auth.FromContext(r.Context())
	err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		var id, planID uuid.UUID
		if err := tx.QueryRow(r.Context(), `UPDATE subscriptions SET status='cancelled',cancelled_at=now(),cancel_at_period_end=false,updated_at=now(),version=version+1
			WHERE id=(SELECT s.id FROM subscriptions s JOIN plans p ON p.id=s.plan_id JOIN products pr ON pr.id=p.product_id
				WHERE s.account_id=$1 AND pr.slug=$2 ORDER BY s.created_at DESC LIMIT 1) RETURNING id,plan_id`, account, claims.App).Scan(&id, &planID); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO subscription_history(subscription_id,status,plan_id,operation_ref) VALUES($1,'cancelled',$2,$3)`, id, planID, "subscription:cancel:"+uuid.NewString()); err != nil {
			return err
		}
		return accountEvent(r.Context(), tx, account, "subscription", id, "subscription.cancelled", map[string]any{"subscription_id": id})
	})
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeNoContent(w)
}
func (a *API) reactivateSubscription(w http.ResponseWriter, r *http.Request) {
	a.setSubscriptionStatus(w, r, "active")
}
func (a *API) setSubscriptionStatus(w http.ResponseWriter, r *http.Request, status string) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid subscription id")
		return
	}
	if !a.canAccessSubscription(r, id) {
		writeError(w, 403, "subscription not accessible")
		return
	}
	immediate := true
	var in struct{ Immediate *bool }
	if r.ContentLength > 0 && decode(w, r, &in) && in.Immediate != nil {
		immediate = *in.Immediate
	}
	err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		var accountID, planID uuid.UUID
		if err := tx.QueryRow(r.Context(), `SELECT account_id,plan_id FROM subscriptions WHERE id=$1 FOR UPDATE`, id).Scan(&accountID, &planID); err != nil {
			return err
		}
		if status == "cancelled" && !immediate {
			if _, err := tx.Exec(r.Context(), `UPDATE subscriptions SET cancel_at_period_end=true,updated_at=now(),version=version+1 WHERE id=$1`, id); err != nil {
				return err
			}
		} else if _, err := tx.Exec(r.Context(), `UPDATE subscriptions SET status=$2::subscription_status,cancelled_at=CASE WHEN $2::text='cancelled' THEN now() ELSE NULL END,cancel_at_period_end=false,updated_at=now(),version=version+1 WHERE id=$1`, id, status); err != nil {
			return err
		}
		historyStatus := status
		if status == "cancelled" && !immediate {
			historyStatus = "active"
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO subscription_history(subscription_id,status,plan_id,operation_ref,metadata) VALUES($1,$2,$3,$4,$5)`, id, historyStatus, planID, "subscription:status:"+uuid.NewString(), map[string]any{"scheduled": !immediate, "requested_status": status}); err != nil {
			return err
		}
		eventType := "subscription." + status
		if !immediate {
			eventType = "subscription.cancellation_scheduled"
		}
		return accountEvent(r.Context(), tx, accountID, "subscription", id, eventType, map[string]any{"subscription_id": id, "scheduled": !immediate})
	})
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": status, "scheduled": !immediate})
}
func (a *API) renewSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid subscription id")
		return
	}
	if !a.canAccessSubscription(r, id) {
		writeError(w, 403, "subscription not accessible")
		return
	}
	var in struct {
		OperationRef  string `json:"operation_ref"`
		PaymentStatus string `json:"payment_status"`
	}
	if r.ContentLength > 0 && !decode(w, r, &in) {
		return
	}
	var accountID, planID, productID uuid.UUID
	var currentStatus string
	var credits, price int64
	var interval string
	var end *time.Time
	if err = a.pool.QueryRow(r.Context(), `SELECT s.account_id,s.plan_id,s.product_id,s.included_credits,s.price_minor,s.billing_interval,s.current_period_end,s.status FROM subscriptions s WHERE s.id=$1`, id).Scan(&accountID, &planID, &productID, &credits, &price, &interval, &end, &currentStatus); err != nil {
		writeDBError(w, err)
		return
	}
	if currentStatus == "cancelled" || currentStatus == "expired" {
		writeError(w, 409, "subscription cannot be renewed from its current state")
		return
	}
	if price > 0 && (in.PaymentStatus != "verified" || in.OperationRef == "") {
		writeError(w, 409, "paid renewal requires a verified payment and operation_ref")
		return
	}
	start := time.Now().UTC()
	if end != nil && end.After(start) {
		start = *end
	}
	next := billingPeriod(start, interval)
	if in.OperationRef == "" {
		in.OperationRef = "subscription:renew:" + id.String() + ":" + start.Format("2006-01-02")
	}
	renewed := false
	err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		var historyID uuid.UUID
		err := tx.QueryRow(r.Context(), `INSERT INTO subscription_history(subscription_id,status,plan_id,operation_ref) VALUES($1,'active',$2,$3) ON CONFLICT(operation_ref) DO NOTHING RETURNING id`, id, planID, in.OperationRef).Scan(&historyID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		tag, err := tx.Exec(r.Context(), `UPDATE subscriptions SET status='active',current_period_start=$2,current_period_end=$3,updated_at=now(),version=version+1 WHERE id=$1 AND current_period_end IS DISTINCT FROM $3`, id, start, next)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		renewed = true
		if credits > 0 {
			if err := allocateCredits(r.Context(), tx, accountID, productID, id, credits, next); err != nil {
				return err
			}
		}
		return accountEvent(r.Context(), tx, accountID, "subscription", id, "subscription.renewed", map[string]any{"subscription_id": id, "current_period_end": next})
	})
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": "active", "current_period_end": next, "duplicate": !renewed})
}
func (a *API) getEntitlements(w http.ResponseWriter, r *http.Request) {
	accountID, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 404, "account not found")
		return
	}
	var raw []byte
	var status string
	product := r.URL.Query().Get("product")
	if product == "" {
		claims, _ := auth.FromContext(r.Context())
		product = claims.App
	}
	if !canReadProductSlug(r, product) {
		writeError(w, 403, "product not accessible")
		return
	}
	err = a.pool.QueryRow(r.Context(), `SELECT s.entitlements,s.status FROM subscriptions s JOIN products pr ON pr.id=s.product_id WHERE s.account_id=$1 AND pr.slug=$2 ORDER BY s.created_at DESC LIMIT 1`, accountID, product).Scan(&raw, &status)
	if err != nil {
		writeDBError(w, err)
		return
	}
	if status != "active" {
		raw = []byte(`{}`)
	}
	writeJSON(w, 200, map[string]any{"status": status, "entitlements": json.RawMessage(raw)})
}
func (a *API) checkEntitlement(w http.ResponseWriter, r *http.Request) {
	feature := r.URL.Query().Get("feature")
	if feature == "" {
		writeError(w, 400, "feature is required")
		return
	}
	account, err := a.accountIDForClaims(r.Context())
	if err != nil {
		writeError(w, 403, "feature unavailable")
		return
	}
	var enabled bool
	claims, _ := auth.FromContext(r.Context())
	err = a.pool.QueryRow(r.Context(), `SELECT COALESCE((s.entitlements->$2)::boolean,false) FROM subscriptions s JOIN products pr ON pr.id=s.product_id WHERE s.account_id=$1 AND s.status='active' AND pr.slug=$3 ORDER BY s.created_at DESC LIMIT 1`, account, feature, claims.App).Scan(&enabled)
	if err != nil || !enabled {
		writeError(w, 403, "feature unavailable")
		return
	}
	writeNoContent(w)
}

func (a *API) canAccessSubscription(r *http.Request, id uuid.UUID) bool {
	account, err := a.accountIDForClaims(r.Context())
	if err != nil {
		return false
	}
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		return false
	}
	var found bool
	err = a.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM subscriptions s JOIN products p ON p.id=s.product_id WHERE s.id=$1 AND s.account_id=$2 AND (p.slug=$3 OR $4))`, id, account, claims.App, claims.Has("billing:link")).Scan(&found)
	return err == nil && found
}

func appEvent(ctx context.Context, tx pgx.Tx, aggregateType string, aggregateID uuid.UUID, eventType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var eventID uuid.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,payload) VALUES($1,$2,$3,$4) RETURNING id`, aggregateType, aggregateID, eventType, raw).Scan(&eventID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO webhook_deliveries(event_id,target_url,endpoint_id)
		SELECT $1,e.target_url,e.id FROM webhook_endpoints e JOIN wallets w ON w.account_id=e.account_id JOIN products p ON p.id=w.product_id
		WHERE w.id=$2 AND e.application=p.slug AND e.active ON CONFLICT DO NOTHING`, eventID, aggregateID)
	return err
}

func accountEvent(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, aggregateType string, aggregateID uuid.UUID, eventType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var eventID uuid.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,payload) VALUES($1,$2,$3,$4) RETURNING id`, aggregateType, aggregateID, eventType, raw).Scan(&eventID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO webhook_deliveries(event_id,target_url,endpoint_id) SELECT $1,target_url,id FROM webhook_endpoints WHERE account_id=$2 AND active ON CONFLICT DO NOTHING`, eventID, accountID)
	return err
}

func parseLimit(r *http.Request) int {
	value, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if value <= 0 || value > 200 {
		return 50
	}
	return value
}
