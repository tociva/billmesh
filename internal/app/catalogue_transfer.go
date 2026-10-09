package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tociva/billmesh/internal/products"
)

const catalogueTransferSchemaVersion = 2

type catalogueTransfer struct {
	SchemaVersion int                        `json:"schema_version"`
	ExportedAt    time.Time                  `json:"exported_at"`
	Products      []catalogueTransferProduct `json:"products"`
}

type catalogueTransferProduct struct {
	Slug              string                        `json:"slug"`
	Name              string                        `json:"name"`
	Description       string                        `json:"description"`
	EntitlementSchema products.EntitlementSchema    `json:"entitlement_schema"`
	BillingPolicy     products.BillingPolicy        `json:"billing_policy"`
	Active            *bool                         `json:"active"`
	Plans             []catalogueTransferPlan       `json:"plans"`
	CreditPacks       []catalogueTransferCreditPack `json:"credit_packs"`
}

type catalogueTransferPlan struct {
	Slug            string         `json:"slug"`
	PlanFamilyID    string         `json:"plan_family_id"`
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	PriceMinor      int64          `json:"price_minor"`
	Currency        string         `json:"currency"`
	IncludedCredits int64          `json:"included_credits"`
	Entitlements    map[string]any `json:"entitlements"`
	BillingInterval string         `json:"billing_interval"`
	BillingModel    string         `json:"billing_model"`
	Selectable      *bool          `json:"selectable"`
	Default         *bool          `json:"default_for_product"`
	CheckoutEnabled *bool          `json:"checkout_enabled"`
	EffectiveFrom   *time.Time     `json:"effective_from,omitempty"`
	EffectiveTo     *time.Time     `json:"effective_to"`
	Active          *bool          `json:"active"`
}

type catalogueTransferCreditPack struct {
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Credits      int64  `json:"credits"`
	PriceMinor   int64  `json:"price_minor"`
	Currency     string `json:"currency"`
	ValidityDays *int   `json:"validity_days"`
	Active       *bool  `json:"active"`
}

type catalogueTransferIssue struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type catalogueTransferValidation struct {
	Valid         bool                     `json:"valid"`
	SchemaVersion int                      `json:"schema_version"`
	Products      int                      `json:"products"`
	Plans         int                      `json:"plans"`
	CreditPacks   int                      `json:"credit_packs"`
	Issues        []catalogueTransferIssue `json:"issues"`
}

type catalogueTransferImportResult struct {
	SchemaVersion int `json:"schema_version"`
	Products      int `json:"products"`
	Plans         int `json:"plans"`
	CreditPacks   int `json:"credit_packs"`
}

func boolValue(value bool) *bool { return &value }

func (a *API) exportCatalogueTransfer(w http.ResponseWriter, r *http.Request) {
	tx, err := a.pool.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	transfer := catalogueTransfer{SchemaVersion: catalogueTransferSchemaVersion, ExportedAt: time.Now().UTC(), Products: []catalogueTransferProduct{}}
	rows, err := tx.Query(r.Context(), `SELECT id,slug,name,description,entitlement_schema,billing_policy,active FROM products ORDER BY slug,id`)
	if err != nil {
		writeDBError(w, err)
		return
	}
	type exportedProduct struct {
		id    uuid.UUID
		value catalogueTransferProduct
	}
	productsToExport := []exportedProduct{}
	for rows.Next() {
		var item exportedProduct
		var active bool
		if err := rows.Scan(&item.id, &item.value.Slug, &item.value.Name, &item.value.Description, &item.value.EntitlementSchema, &item.value.BillingPolicy, &active); err != nil {
			rows.Close()
			writeDBError(w, err)
			return
		}
		item.value.Active = boolValue(active)
		item.value.Plans = []catalogueTransferPlan{}
		item.value.CreditPacks = []catalogueTransferCreditPack{}
		productsToExport = append(productsToExport, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeDBError(w, err)
		return
	}
	rows.Close()

	for index := range productsToExport {
		item := &productsToExport[index]
		planRows, err := tx.Query(r.Context(), `SELECT slug,plan_family_id,name,description,price_minor,currency,included_credits,entitlements,billing_interval,billing_model,
			selectable,default_for_product,checkout_enabled,effective_from,effective_to,active FROM plans WHERE product_id=$1 ORDER BY slug,id`, item.id)
		if err != nil {
			writeDBError(w, err)
			return
		}
		for planRows.Next() {
			var plan catalogueTransferPlan
			var selectable, isDefault, checkout, active bool
			if err := planRows.Scan(&plan.Slug, &plan.PlanFamilyID, &plan.Name, &plan.Description, &plan.PriceMinor, &plan.Currency, &plan.IncludedCredits,
				&plan.Entitlements, &plan.BillingInterval, &plan.BillingModel, &selectable, &isDefault, &checkout, &plan.EffectiveFrom, &plan.EffectiveTo, &active); err != nil {
				planRows.Close()
				writeDBError(w, err)
				return
			}
			plan.Selectable, plan.Default, plan.CheckoutEnabled, plan.Active = boolValue(selectable), boolValue(isDefault), boolValue(checkout), boolValue(active)
			item.value.Plans = append(item.value.Plans, plan)
		}
		if err := planRows.Err(); err != nil {
			planRows.Close()
			writeDBError(w, err)
			return
		}
		planRows.Close()

		packRows, err := tx.Query(r.Context(), `SELECT slug,name,credits,price_minor,currency,validity_days,active FROM credit_packs WHERE product_id=$1 ORDER BY slug,id`, item.id)
		if err != nil {
			writeDBError(w, err)
			return
		}
		for packRows.Next() {
			var pack catalogueTransferCreditPack
			var active bool
			if err := packRows.Scan(&pack.Slug, &pack.Name, &pack.Credits, &pack.PriceMinor, &pack.Currency, &pack.ValidityDays, &active); err != nil {
				packRows.Close()
				writeDBError(w, err)
				return
			}
			pack.Active = boolValue(active)
			item.value.CreditPacks = append(item.value.CreditPacks, pack)
		}
		if err := packRows.Err(); err != nil {
			packRows.Close()
			writeDBError(w, err)
			return
		}
		packRows.Close()
		transfer.Products = append(transfer.Products, item.value)
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeDBError(w, err)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="billmesh-catalogue-%s.json"`, transfer.ExportedAt.Format("2006-01-02")))
	writeJSON(w, http.StatusOK, transfer)
}

func (a *API) validateCatalogueTransfer(w http.ResponseWriter, r *http.Request) {
	var transfer catalogueTransfer
	if !decode(w, r, &transfer) {
		return
	}
	validation := validateCatalogueTransferDocument(transfer, time.Now().UTC())
	if len(validation.Issues) == 0 {
		conflicts, err := a.catalogueTransferConflicts(r.Context(), transfer)
		if err != nil {
			writeDBError(w, err)
			return
		}
		validation.Issues = append(validation.Issues, conflicts...)
	}
	validation.Valid = len(validation.Issues) == 0
	writeJSON(w, http.StatusOK, validation)
}

func (a *API) importCatalogueTransfer(w http.ResponseWriter, r *http.Request) {
	var transfer catalogueTransfer
	if !decode(w, r, &transfer) {
		return
	}
	startedAt := time.Now().UTC()
	validation := validateCatalogueTransferDocument(transfer, startedAt)
	if len(validation.Issues) != 0 {
		writeJSON(w, http.StatusUnprocessableEntity, validation)
		return
	}
	result := catalogueTransferImportResult{SchemaVersion: catalogueTransferSchemaVersion}
	err := pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		issues, err := catalogueTransferConflictsWithQuery(r.Context(), tx, transfer)
		if err != nil {
			return err
		}
		if len(issues) != 0 {
			validation.Issues = issues
			validation.Valid = false
			return errCatalogueTransferConflict
		}
		for _, product := range transfer.Products {
			var productID uuid.UUID
			if err := tx.QueryRow(r.Context(), `INSERT INTO products(slug,name,description,entitlement_schema,billing_policy,active)
				VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, strings.TrimSpace(product.Slug), strings.TrimSpace(product.Name), strings.TrimSpace(product.Description),
				product.EntitlementSchema, product.BillingPolicy, *product.Active).Scan(&productID); err != nil {
				return err
			}
			result.Products++
			if err := a.writeCatalogueAudit(r.Context(), tx, "product.import", "product", productID, nil, map[string]any{
				"id": productID, "slug": product.Slug, "name": product.Name, "description": product.Description, "active": *product.Active,
				"entitlement_schema": product.EntitlementSchema, "billing_policy": product.BillingPolicy,
			}); err != nil {
				return err
			}
			for _, plan := range product.Plans {
				effectiveFrom := startedAt
				if plan.EffectiveFrom != nil {
					effectiveFrom = *plan.EffectiveFrom
				}
				validatedEntitlements, err := products.ValidateEntitlements(product.EntitlementSchema, plan.Entitlements)
				if err != nil {
					return err
				}
				var planID uuid.UUID
				if err := tx.QueryRow(r.Context(), `INSERT INTO plans(product_id,slug,plan_family_id,name,description,price_minor,currency,included_credits,entitlements,active,
					billing_interval,billing_model,selectable,default_for_product,checkout_enabled,effective_from,effective_to)
					VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) RETURNING id`, productID, strings.TrimSpace(plan.Slug),
					strings.TrimSpace(plan.PlanFamilyID), strings.TrimSpace(plan.Name), strings.TrimSpace(plan.Description), plan.PriceMinor, plan.Currency,
					plan.IncludedCredits, validatedEntitlements, *plan.Active, plan.BillingInterval, plan.BillingModel, *plan.Selectable, *plan.Default,
					*plan.CheckoutEnabled, effectiveFrom, plan.EffectiveTo).Scan(&planID); err != nil {
					return err
				}
				result.Plans++
				if err := a.writeCatalogueAudit(r.Context(), tx, "plan.import", "plan", planID, nil, map[string]any{
					"id": planID, "product_id": productID, "slug": plan.Slug, "plan_family_id": plan.PlanFamilyID, "name": plan.Name,
					"description": plan.Description, "price_minor": plan.PriceMinor, "currency": plan.Currency, "included_credits": plan.IncludedCredits,
					"entitlements": validatedEntitlements, "billing_interval": plan.BillingInterval, "billing_model": plan.BillingModel,
					"selectable": *plan.Selectable, "default_for_product": *plan.Default, "checkout_enabled": *plan.CheckoutEnabled,
					"effective_from": effectiveFrom, "effective_to": plan.EffectiveTo, "active": *plan.Active,
				}); err != nil {
					return err
				}
			}
			for _, pack := range product.CreditPacks {
				var packID uuid.UUID
				if err := tx.QueryRow(r.Context(), `INSERT INTO credit_packs(product_id,slug,name,credits,price_minor,currency,validity_days,active)
					VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, productID, strings.TrimSpace(pack.Slug), strings.TrimSpace(pack.Name), pack.Credits,
					pack.PriceMinor, pack.Currency, pack.ValidityDays, *pack.Active).Scan(&packID); err != nil {
					return err
				}
				result.CreditPacks++
				if err := a.writeCatalogueAudit(r.Context(), tx, "credit_pack.import", "credit_pack", packID, nil, map[string]any{
					"id": packID, "product_id": productID, "slug": pack.Slug, "name": pack.Name, "credits": pack.Credits,
					"price_minor": pack.PriceMinor, "currency": pack.Currency, "validity_days": pack.ValidityDays, "active": *pack.Active,
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if errors.Is(err, errCatalogueTransferConflict) {
		writeJSON(w, http.StatusConflict, validation)
		return
	}
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

var errCatalogueTransferConflict = errors.New("catalogue transfer conflict")

type catalogueTransferQueryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (a *API) catalogueTransferConflicts(ctx context.Context, transfer catalogueTransfer) ([]catalogueTransferIssue, error) {
	return catalogueTransferConflictsWithQuery(ctx, a.pool, transfer)
}

func catalogueTransferConflictsWithQuery(ctx context.Context, q catalogueTransferQueryRower, transfer catalogueTransfer) ([]catalogueTransferIssue, error) {
	issues := []catalogueTransferIssue{}
	for index, product := range transfer.Products {
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM products WHERE slug=$1)`, strings.TrimSpace(product.Slug)).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			issues = append(issues, transferIssue(fmt.Sprintf("/products/%d/slug", index), "product_slug_conflict", fmt.Sprintf("Product slug %q already exists.", product.Slug)))
		}
	}
	return issues, nil
}

func validateCatalogueTransferDocument(transfer catalogueTransfer, defaultEffectiveFrom time.Time) catalogueTransferValidation {
	result := catalogueTransferValidation{SchemaVersion: transfer.SchemaVersion, Products: len(transfer.Products), Issues: []catalogueTransferIssue{}}
	if transfer.SchemaVersion != 1 && transfer.SchemaVersion != catalogueTransferSchemaVersion {
		result.Issues = append(result.Issues, transferIssue("/schema_version", "unsupported_schema_version", "Catalogue schema_version must be 1 or 2."))
	}
	seenProducts := map[string]bool{}
	for productIndex, product := range transfer.Products {
		path := fmt.Sprintf("/products/%d", productIndex)
		slug := strings.TrimSpace(product.Slug)
		if err := products.ValidateProduct(slug, product.Name, product.Description); err != nil {
			result.Issues = append(result.Issues, transferIssue(path, "invalid_product", err.Error()))
		}
		if seenProducts[slug] {
			result.Issues = append(result.Issues, transferIssue(path+"/slug", "duplicate_product_slug", fmt.Sprintf("Product slug %q is duplicated.", slug)))
		}
		seenProducts[slug] = true
		if product.Active == nil {
			result.Issues = append(result.Issues, transferIssue(path+"/active", "required", "active is required."))
		}
		if err := products.ValidateEntitlementSchema(product.EntitlementSchema); err != nil {
			result.Issues = append(result.Issues, transferIssue(path+"/entitlement_schema", "invalid_entitlement_schema", err.Error()))
		}
		if err := products.ValidateBillingPolicy(product.BillingPolicy); err != nil {
			result.Issues = append(result.Issues, transferIssue(path+"/billing_policy", "invalid_billing_policy", err.Error()))
		}

		seenPlans := map[string]bool{}
		defaultPlans := 0
		for planIndex, plan := range product.Plans {
			result.Plans++
			planPath := fmt.Sprintf("%s/plans/%d", path, planIndex)
			planSlug := strings.TrimSpace(plan.Slug)
			if err := products.ValidatePlanSlug(planSlug); err != nil {
				result.Issues = append(result.Issues, transferIssue(planPath+"/slug", "invalid_plan_slug", err.Error()))
			}
			if seenPlans[planSlug] {
				result.Issues = append(result.Issues, transferIssue(planPath+"/slug", "duplicate_plan_slug", fmt.Sprintf("Plan slug %q is duplicated for product %q.", planSlug, slug)))
			}
			seenPlans[planSlug] = true
			if err := products.ValidatePlanName(plan.Name); err != nil {
				result.Issues = append(result.Issues, transferIssue(planPath+"/name", "invalid_plan_name", err.Error()))
			}
			if len([]rune(strings.TrimSpace(plan.Description))) > 2000 {
				result.Issues = append(result.Issues, transferIssue(planPath+"/description", "invalid_plan_description", "description cannot exceed 2000 characters"))
			}
			if err := products.ValidatePlan(products.PlanInput{PriceMinor: plan.PriceMinor, IncludedCredits: plan.IncludedCredits, Currency: plan.Currency, BillingInterval: plan.BillingInterval, PlanFamilyID: plan.PlanFamilyID}); err != nil {
				result.Issues = append(result.Issues, transferIssue(planPath, "invalid_plan", err.Error()))
			}
			if plan.Active == nil || plan.Selectable == nil || plan.Default == nil || plan.CheckoutEnabled == nil {
				result.Issues = append(result.Issues, transferIssue(planPath, "required_state", "active, selectable, default_for_product, and checkout_enabled are required."))
				continue
			}
			if *plan.Default {
				defaultPlans++
			}
			effectiveFrom := defaultEffectiveFrom
			if plan.EffectiveFrom != nil {
				effectiveFrom = *plan.EffectiveFrom
			}
			if err := validatePlanSemantics(plan.BillingModel, plan.PriceMinor, *plan.Active, *plan.Selectable, *plan.Default, *plan.CheckoutEnabled, effectiveFrom, plan.EffectiveTo); err != nil {
				result.Issues = append(result.Issues, transferIssue(planPath, "invalid_plan_semantics", err.Error()))
			}
			if _, err := products.ValidateEntitlements(product.EntitlementSchema, plan.Entitlements); err != nil {
				result.Issues = append(result.Issues, transferIssue(planPath+"/entitlements", "invalid_entitlements", err.Error()))
			}
		}
		if defaultPlans > 1 {
			result.Issues = append(result.Issues, transferIssue(path+"/plans", "multiple_default_plans", "A product cannot have more than one default plan."))
		}
		if product.Active != nil && !*product.Active {
			for planIndex, plan := range product.Plans {
				if plan.Active != nil && *plan.Active {
					result.Issues = append(result.Issues, transferIssue(fmt.Sprintf("%s/plans/%d/active", path, planIndex), "active_plan_on_archived_product", "An archived product cannot contain an active plan."))
				}
			}
		}
		result.Issues = append(result.Issues, validatePlanRangeOverlaps(path, product.Plans, defaultEffectiveFrom)...)

		seenPacks := map[string]bool{}
		for packIndex, pack := range product.CreditPacks {
			result.CreditPacks++
			packPath := fmt.Sprintf("%s/credit_packs/%d", path, packIndex)
			packSlug := strings.TrimSpace(pack.Slug)
			if err := products.ValidatePlanSlug(packSlug); err != nil {
				result.Issues = append(result.Issues, transferIssue(packPath+"/slug", "invalid_credit_pack_slug", err.Error()))
			}
			if seenPacks[packSlug] {
				result.Issues = append(result.Issues, transferIssue(packPath+"/slug", "duplicate_credit_pack_slug", fmt.Sprintf("Credit-pack slug %q is duplicated for product %q.", packSlug, slug)))
			}
			seenPacks[packSlug] = true
			if err := products.ValidatePlanName(pack.Name); err != nil {
				result.Issues = append(result.Issues, transferIssue(packPath+"/name", "invalid_credit_pack_name", err.Error()))
			}
			if pack.Credits <= 0 {
				result.Issues = append(result.Issues, transferIssue(packPath+"/credits", "invalid_credits", "credits must be positive."))
			}
			if pack.PriceMinor < 0 {
				result.Issues = append(result.Issues, transferIssue(packPath+"/price_minor", "invalid_price", "price_minor cannot be negative."))
			}
			if err := products.ValidatePlan(products.PlanInput{Currency: pack.Currency, BillingInterval: "monthly"}); err != nil {
				result.Issues = append(result.Issues, transferIssue(packPath+"/currency", "invalid_currency", err.Error()))
			}
			if pack.ValidityDays != nil && *pack.ValidityDays <= 0 {
				result.Issues = append(result.Issues, transferIssue(packPath+"/validity_days", "invalid_validity", "validity_days must be positive when provided."))
			}
			if pack.Active == nil {
				result.Issues = append(result.Issues, transferIssue(packPath+"/active", "required", "active is required."))
			} else if product.Active != nil && !*product.Active && *pack.Active {
				result.Issues = append(result.Issues, transferIssue(packPath+"/active", "active_credit_pack_on_archived_product", "An archived product cannot contain an active credit pack."))
			}
		}
	}
	sort.SliceStable(result.Issues, func(i, j int) bool { return result.Issues[i].Path < result.Issues[j].Path })
	result.Valid = len(result.Issues) == 0
	return result
}

func validatePlanRangeOverlaps(productPath string, plans []catalogueTransferPlan, fallback time.Time) []catalogueTransferIssue {
	issues := []catalogueTransferIssue{}
	for leftIndex := range plans {
		left := plans[leftIndex]
		if left.Active == nil || left.Selectable == nil || !*left.Active || !*left.Selectable {
			continue
		}
		leftStart := fallback
		if left.EffectiveFrom != nil {
			leftStart = *left.EffectiveFrom
		}
		for rightIndex := leftIndex + 1; rightIndex < len(plans); rightIndex++ {
			right := plans[rightIndex]
			if right.Active == nil || right.Selectable == nil || !*right.Active || !*right.Selectable || left.PlanFamilyID != right.PlanFamilyID || left.BillingInterval != right.BillingInterval {
				continue
			}
			rightStart := fallback
			if right.EffectiveFrom != nil {
				rightStart = *right.EffectiveFrom
			}
			if rangesOverlap(leftStart, left.EffectiveTo, rightStart, right.EffectiveTo) {
				issues = append(issues, transferIssue(fmt.Sprintf("%s/plans/%d", productPath, rightIndex), "overlapping_plan_family", fmt.Sprintf("Active selectable plans %q and %q overlap for the same family and billing interval.", left.Slug, right.Slug)))
			}
		}
	}
	return issues
}

func rangesOverlap(leftStart time.Time, leftEnd *time.Time, rightStart time.Time, rightEnd *time.Time) bool {
	return (leftEnd == nil || rightStart.Before(*leftEnd)) && (rightEnd == nil || leftStart.Before(*rightEnd))
}

func transferIssue(path, code, message string) catalogueTransferIssue {
	return catalogueTransferIssue{Path: path, Code: code, Message: message}
}
