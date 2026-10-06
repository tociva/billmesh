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

func (a *API) createPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProductID       uuid.UUID      `json:"product_id"`
		Slug            string         `json:"slug"`
		PlanFamilyID    string         `json:"plan_family_id"`
		Name            string         `json:"name"`
		Description     string         `json:"description"`
		Currency        string         `json:"currency"`
		BillingInterval string         `json:"billing_interval"`
		BillingModel    string         `json:"billing_model"`
		PriceMinor      int64          `json:"price_minor"`
		IncludedCredits int64          `json:"included_credits"`
		Entitlements    map[string]any `json:"entitlements"`
		SchemaVersion   *int64         `json:"entitlement_schema_version"`
		Active          *bool          `json:"active"`
		Selectable      *bool          `json:"selectable"`
		Default         bool           `json:"default_for_product"`
		CheckoutEnabled *bool          `json:"checkout_enabled"`
		EffectiveFrom   *time.Time     `json:"effective_from"`
		EffectiveTo     *time.Time     `json:"effective_to"`
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
	if in.BillingModel == "" {
		if in.PriceMinor == 0 {
			in.BillingModel = "free"
		} else {
			in.BillingModel = "paid"
		}
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
	in.PlanFamilyID = strings.TrimSpace(in.PlanFamilyID)
	if in.PlanFamilyID == "" {
		in.PlanFamilyID = in.Slug
	}
	in.Name = strings.TrimSpace(in.Name)
	if err := products.ValidatePlan(products.PlanInput{PriceMinor: in.PriceMinor, IncludedCredits: in.IncludedCredits, Currency: in.Currency, BillingInterval: in.BillingInterval, PlanFamilyID: in.PlanFamilyID}); err != nil {
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
	selectable := true
	if in.Selectable != nil {
		selectable = *in.Selectable
	}
	checkoutEnabled := active && selectable
	if in.CheckoutEnabled != nil {
		checkoutEnabled = *in.CheckoutEnabled
	}
	effectiveFrom := time.Now().UTC()
	if in.EffectiveFrom != nil {
		effectiveFrom = *in.EffectiveFrom
	}
	if err := validatePlanSemantics(in.BillingModel, in.PriceMinor, active, selectable, in.Default, checkoutEnabled, effectiveFrom, in.EffectiveTo); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
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
		row := tx.QueryRow(r.Context(), `INSERT INTO plans(product_id,slug,plan_family_id,name,description,price_minor,currency,included_credits,entitlements,active,billing_interval,
			billing_model,selectable,default_for_product,checkout_enabled,effective_from,effective_to)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
			RETURNING id,product_id,$18::text,slug,plan_family_id,name,description,price_minor,currency,included_credits,entitlements,billing_interval,billing_model,
			selectable,default_for_product,checkout_enabled,effective_from,effective_to,active,version,created_at,updated_at`,
			in.ProductID, in.Slug, in.PlanFamilyID, in.Name, in.Description, in.PriceMinor, in.Currency, in.IncludedCredits, in.Entitlements, active, in.BillingInterval,
			in.BillingModel, selectable, in.Default, checkoutEnabled, effectiveFrom, in.EffectiveTo, productSlug)
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
func (a *API) updatePlan(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid plan id")
		return
	}
	var in struct {
		Name            *string        `json:"name"`
		PlanFamilyID    *string        `json:"plan_family_id"`
		Description     *string        `json:"description"`
		PriceMinor      *int64         `json:"price_minor"`
		Currency        *string        `json:"currency"`
		IncludedCredits *int64         `json:"included_credits"`
		Entitlements    map[string]any `json:"entitlements"`
		SchemaVersion   *int64         `json:"entitlement_schema_version"`
		BillingInterval *string        `json:"billing_interval"`
		BillingModel    *string        `json:"billing_model"`
		Active          *bool          `json:"active"`
		Selectable      *bool          `json:"selectable"`
		Default         *bool          `json:"default_for_product"`
		CheckoutEnabled *bool          `json:"checkout_enabled"`
		EffectiveFrom   *time.Time     `json:"effective_from"`
		EffectiveTo     **time.Time    `json:"effective_to"`
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
		row := tx.QueryRow(r.Context(), `SELECT p.id,p.product_id,pr.slug,p.slug,p.plan_family_id,p.name,p.description,p.price_minor,p.currency,p.included_credits,
			p.entitlements,p.billing_interval,p.billing_model,p.selectable,p.default_for_product,p.checkout_enabled,p.effective_from,p.effective_to,p.active,p.version,p.created_at,p.updated_at
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
		if in.PlanFamilyID != nil {
			updated.PlanFamilyID = strings.TrimSpace(*in.PlanFamilyID)
		}
		if in.Description != nil {
			updated.Description = strings.TrimSpace(*in.Description)
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
		if in.BillingModel != nil {
			updated.BillingModel = *in.BillingModel
		}
		if in.Active != nil {
			updated.Active = *in.Active
			if in.CheckoutEnabled == nil {
				updated.CheckoutEnabled = updated.Active && updated.Selectable
			}
			if !updated.Active && in.Default == nil {
				updated.Default = false
			}
		}
		if in.Selectable != nil {
			updated.Selectable = *in.Selectable
			if !updated.Selectable && in.CheckoutEnabled == nil {
				updated.CheckoutEnabled = false
			}
			if !updated.Selectable && in.Default == nil {
				updated.Default = false
			}
		}
		if in.Default != nil {
			updated.Default = *in.Default
		}
		if in.CheckoutEnabled != nil {
			updated.CheckoutEnabled = *in.CheckoutEnabled
		}
		if in.EffectiveFrom != nil {
			updated.EffectiveFrom = *in.EffectiveFrom
		}
		if in.EffectiveTo != nil {
			updated.EffectiveTo = *in.EffectiveTo
		}
		if err := products.ValidatePlanName(updated.Name); err != nil {
			return catalogueValidationError{err.Error()}
		}
		if err := products.ValidatePlan(products.PlanInput{PriceMinor: updated.PriceMinor, IncludedCredits: updated.IncludedCredits, Currency: updated.Currency, BillingInterval: updated.BillingInterval, PlanFamilyID: updated.PlanFamilyID}); err != nil {
			return catalogueValidationError{err.Error()}
		}
		if err := validatePlanSemantics(updated.BillingModel, updated.PriceMinor, updated.Active, updated.Selectable, updated.Default, updated.CheckoutEnabled, updated.EffectiveFrom, updated.EffectiveTo); err != nil {
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
		row = tx.QueryRow(r.Context(), `UPDATE plans SET plan_family_id=$2,name=$3,description=$4,price_minor=$5,currency=$6,included_credits=$7,entitlements=$8,
			billing_interval=$9,billing_model=$10,selectable=$11,default_for_product=$12,checkout_enabled=$13,effective_from=$14,effective_to=$15,
			active=$16,version=version+1,updated_at=now() WHERE id=$1
			RETURNING id,product_id,$17::text,slug,plan_family_id,name,description,price_minor,currency,included_credits,entitlements,billing_interval,billing_model,
			selectable,default_for_product,checkout_enabled,effective_from,effective_to,active,version,created_at,updated_at`,
			id, updated.PlanFamilyID, updated.Name, updated.Description, updated.PriceMinor, updated.Currency, updated.IncludedCredits, updated.Entitlements,
			updated.BillingInterval, updated.BillingModel, updated.Selectable, updated.Default, updated.CheckoutEnabled, updated.EffectiveFrom,
			updated.EffectiveTo, updated.Active, before.Product)
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

func validatePlanSemantics(model string, price int64, active, selectable, isDefault, checkoutEnabled bool, effectiveFrom time.Time, effectiveTo *time.Time) error {
	if model != "free" && model != "paid" {
		return errors.New("billing_model must be free or paid")
	}
	if model == "free" && price != 0 {
		return errors.New("free plans must have a zero price")
	}
	if model == "paid" && price <= 0 {
		return errors.New("paid plans must have a positive price")
	}
	if isDefault && (!active || !selectable) {
		return errors.New("the default plan must be active and selectable")
	}
	if checkoutEnabled && (!active || !selectable) {
		return errors.New("checkout requires an active selectable plan")
	}
	if effectiveTo != nil && !effectiveTo.After(effectiveFrom) {
		return errors.New("effective_to must be after effective_from")
	}
	return nil
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

func appEvent(ctx context.Context, tx pgx.Tx, aggregateType string, aggregateID uuid.UUID, eventType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var accountID, productID uuid.UUID
	if aggregateType == "wallet" {
		if err := tx.QueryRow(ctx, `SELECT account_id,product_id FROM wallets WHERE id=$1`, aggregateID).Scan(&accountID, &productID); err != nil {
			return err
		}
	}
	var revision int64
	if accountID != uuid.Nil && productID != uuid.Nil {
		revision, err = ensureBillingRevision(ctx, tx, accountID, productID)
		if err != nil {
			return err
		}
	}
	var eventID uuid.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,payload,account_id,product_id,billing_revision,schema_version)
		VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,0),'2') RETURNING id`, aggregateType, aggregateID, eventType, raw, accountID, productID, revision).Scan(&eventID); err != nil {
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
	var productID uuid.UUID
	switch aggregateType {
	case "subscription":
		err = tx.QueryRow(ctx, `SELECT product_id FROM subscriptions WHERE id=$1`, aggregateID).Scan(&productID)
	case "payment":
		err = tx.QueryRow(ctx, `SELECT COALESCE(cp.product_id,st.product_id) FROM payments p
			LEFT JOIN credit_packs cp ON cp.id=p.credit_pack_id LEFT JOIN subscription_transitions st ON st.id=p.transition_id
			WHERE p.id=$1`, aggregateID).Scan(&productID)
	case "wallet":
		err = tx.QueryRow(ctx, `SELECT product_id FROM wallets WHERE id=$1`, aggregateID).Scan(&productID)
	default:
		err = nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var revision int64
	if productID != uuid.Nil {
		revision, err = ensureBillingRevision(ctx, tx, accountID, productID)
		if err != nil {
			return err
		}
	}
	var eventID uuid.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,payload,account_id,product_id,billing_revision,schema_version)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,'00000000-0000-0000-0000-000000000000'::uuid),NULLIF($7,0),'2') RETURNING id`,
		aggregateType, aggregateID, eventType, raw, accountID, productID, revision).Scan(&eventID); err != nil {
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
