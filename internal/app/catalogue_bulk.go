package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tociva/billmesh/internal/products"
)

const catalogueBulkSchemaVersion = 1

type catalogueBulkDocument struct {
	SchemaVersion int                    `json:"schema_version"`
	Products      []catalogueBulkProduct `json:"products"`
}

type catalogueBulkProduct struct {
	Operation         string                                `json:"operation"`
	ExpectedVersion   int64                                 `json:"expected_version"`
	Slug              string                                `json:"slug"`
	Name              string                                `json:"name"`
	Description       string                                `json:"description"`
	EntitlementSchema products.EntitlementSchema            `json:"entitlement_schema"`
	BillingPolicy     products.BillingPolicy                `json:"billing_policy"`
	Active            *bool                                 `json:"active"`
	DelegatedAccess   []productDelegationConfigurationInput `json:"delegated_access,omitempty"`
	Plans             []catalogueBulkPlan                   `json:"plans,omitempty"`
	CreditPacks       []catalogueBulkCreditPack             `json:"credit_packs,omitempty"`
}

type catalogueBulkPlan struct {
	Operation       string `json:"operation"`
	ExpectedVersion int64  `json:"expected_version"`
	catalogueTransferPlan
}

type catalogueBulkCreditPack struct {
	Operation       string `json:"operation"`
	ExpectedVersion int64  `json:"expected_version"`
	catalogueTransferCreditPack
}

type catalogueBulkResourceResult struct {
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
}

type catalogueBulkResult struct {
	SchemaVersion   int                         `json:"schema_version"`
	Products        catalogueBulkResourceResult `json:"products"`
	Plans           catalogueBulkResourceResult `json:"plans"`
	CreditPacks     catalogueBulkResourceResult `json:"credit_packs"`
	DelegatedAccess catalogueBulkResourceResult `json:"delegated_access"`
}

type catalogueBulkValidation struct {
	Valid         bool                     `json:"valid"`
	SchemaVersion int                      `json:"schema_version"`
	Products      int                      `json:"products"`
	Plans         int                      `json:"plans"`
	CreditPacks   int                      `json:"credit_packs"`
	Issues        []catalogueTransferIssue `json:"issues"`
}

type catalogueBulkApplyError struct {
	issue catalogueTransferIssue
}

func (e catalogueBulkApplyError) Error() string { return e.issue.Message }

func bulkConflict(path, code, message string) error {
	return catalogueBulkApplyError{issue: transferIssue(path, code, message)}
}

func validateCatalogueBulkDocument(document catalogueBulkDocument, now time.Time) catalogueBulkValidation {
	validation := catalogueBulkValidation{
		SchemaVersion: document.SchemaVersion,
		Products:      len(document.Products),
		Issues:        []catalogueTransferIssue{},
	}
	if document.SchemaVersion != catalogueBulkSchemaVersion {
		validation.Issues = append(validation.Issues, transferIssue("/schema_version", "unsupported_schema_version", "Bulk catalogue schema_version must be 1."))
	}

	transfer := catalogueTransfer{SchemaVersion: catalogueTransferSchemaVersion, Products: make([]catalogueTransferProduct, 0, len(document.Products))}
	seenProducts := make(map[string]bool, len(document.Products))
	for productIndex, item := range document.Products {
		path := fmt.Sprintf("/products/%d", productIndex)
		item.Slug = strings.TrimSpace(item.Slug)
		if seenProducts[item.Slug] {
			validation.Issues = append(validation.Issues, transferIssue(path+"/slug", "duplicate_product_slug", fmt.Sprintf("Product slug %q is duplicated.", item.Slug)))
		}
		seenProducts[item.Slug] = true
		if item.Operation != "create" && item.Operation != "update" {
			validation.Issues = append(validation.Issues, transferIssue(path+"/operation", "invalid_operation", "operation must be create or update."))
		}
		if (item.Operation == "create" && item.ExpectedVersion != 0) || (item.Operation == "update" && item.ExpectedVersion < 1) {
			validation.Issues = append(validation.Issues, transferIssue(path+"/expected_version", "invalid_expected_version", "create requires expected_version 0; update requires a positive expected_version."))
		}
		delegated, err := normalizeAndValidateDelegationConfigurations(item.DelegatedAccess, item.Operation == "create")
		if err != nil {
			validation.Issues = append(validation.Issues, transferIssue(path+"/delegated_access", "invalid_delegated_access", err.Error()))
		}
		if item.Active != nil && !*item.Active {
			for configIndex, config := range delegated {
				if config.Enabled {
					validation.Issues = append(validation.Issues, transferIssue(fmt.Sprintf("%s/delegated_access/%d/enabled", path, configIndex), "delegation_on_archived_product", "Archived products cannot enable delegated access."))
				}
			}
		}

		plans := make([]catalogueTransferPlan, 0, len(item.Plans))
		for planIndex, plan := range item.Plans {
			validation.Plans++
			planPath := fmt.Sprintf("%s/plans/%d", path, planIndex)
			if plan.Operation != "create" && plan.Operation != "update" {
				validation.Issues = append(validation.Issues, transferIssue(planPath+"/operation", "invalid_operation", "operation must be create or update."))
			}
			if item.Operation == "create" && plan.Operation != "create" {
				validation.Issues = append(validation.Issues, transferIssue(planPath+"/operation", "invalid_nested_operation", "New products can only contain create plan operations."))
			}
			if (plan.Operation == "create" && plan.ExpectedVersion != 0) || (plan.Operation == "update" && plan.ExpectedVersion < 1) {
				validation.Issues = append(validation.Issues, transferIssue(planPath+"/expected_version", "invalid_expected_version", "create requires expected_version 0; update requires a positive expected_version."))
			}
			plans = append(plans, plan.catalogueTransferPlan)
		}

		packs := make([]catalogueTransferCreditPack, 0, len(item.CreditPacks))
		for packIndex, pack := range item.CreditPacks {
			validation.CreditPacks++
			packPath := fmt.Sprintf("%s/credit_packs/%d", path, packIndex)
			if pack.Operation != "create" && pack.Operation != "update" {
				validation.Issues = append(validation.Issues, transferIssue(packPath+"/operation", "invalid_operation", "operation must be create or update."))
			}
			if item.Operation == "create" && pack.Operation != "create" {
				validation.Issues = append(validation.Issues, transferIssue(packPath+"/operation", "invalid_nested_operation", "New products can only contain create credit-pack operations."))
			}
			if (pack.Operation == "create" && pack.ExpectedVersion != 0) || (pack.Operation == "update" && pack.ExpectedVersion < 1) {
				validation.Issues = append(validation.Issues, transferIssue(packPath+"/expected_version", "invalid_expected_version", "create requires expected_version 0; update requires a positive expected_version."))
			}
			packs = append(packs, pack.catalogueTransferCreditPack)
		}
		transfer.Products = append(transfer.Products, catalogueTransferProduct{
			Slug: item.Slug, Name: item.Name, Description: item.Description,
			EntitlementSchema: item.EntitlementSchema, BillingPolicy: item.BillingPolicy,
			Active: item.Active, Plans: plans, CreditPacks: packs,
		})
	}
	base := validateCatalogueTransferDocument(transfer, now)
	validation.Issues = append(validation.Issues, base.Issues...)
	sort.SliceStable(validation.Issues, func(i, j int) bool { return validation.Issues[i].Path < validation.Issues[j].Path })
	validation.Valid = len(validation.Issues) == 0
	return validation
}

func (a *API) validateCatalogueBulk(w http.ResponseWriter, r *http.Request) {
	var document catalogueBulkDocument
	if !decode(w, r, &document) {
		return
	}
	validation := validateCatalogueBulkDocument(document, time.Now().UTC())
	if !validation.Valid {
		writeJSON(w, http.StatusOK, validation)
		return
	}
	tx, err := a.pool.Begin(r.Context())
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = a.applyCatalogueBulk(r.Context(), tx, document); err != nil {
		var applyErr catalogueBulkApplyError
		if errors.As(err, &applyErr) {
			validation.Issues = append(validation.Issues, applyErr.issue)
			validation.Valid = false
			writeJSON(w, http.StatusOK, validation)
			return
		}
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, validation)
}

func (a *API) applyCatalogueBulkHandler(w http.ResponseWriter, r *http.Request) {
	var document catalogueBulkDocument
	if !decode(w, r, &document) {
		return
	}
	validation := validateCatalogueBulkDocument(document, time.Now().UTC())
	if !validation.Valid {
		writeJSON(w, http.StatusUnprocessableEntity, validation)
		return
	}
	var result catalogueBulkResult
	err := pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		var err error
		result, err = a.applyCatalogueBulk(r.Context(), tx, document)
		return err
	})
	if err != nil {
		var applyErr catalogueBulkApplyError
		if errors.As(err, &applyErr) {
			validation.Issues = append(validation.Issues, applyErr.issue)
			validation.Valid = false
			writeJSON(w, http.StatusConflict, validation)
			return
		}
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) catalogueBulkTemplate(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, catalogueBulkDocument{SchemaVersion: catalogueBulkSchemaVersion, Products: []catalogueBulkProduct{}})
}

func (a *API) applyCatalogueBulk(ctx context.Context, tx pgx.Tx, document catalogueBulkDocument) (catalogueBulkResult, error) {
	result := catalogueBulkResult{SchemaVersion: catalogueBulkSchemaVersion}
	for productIndex, input := range document.Products {
		path := fmt.Sprintf("/products/%d", productIndex)
		input.Slug = strings.TrimSpace(input.Slug)
		input.Name = strings.TrimSpace(input.Name)
		input.Description = strings.TrimSpace(input.Description)
		input.DelegatedAccess, _ = normalizeAndValidateDelegationConfigurations(input.DelegatedAccess, input.Operation == "create")

		var product productRecord
		archiveProduct := false
		if input.Operation == "create" {
			err := tx.QueryRow(ctx, `INSERT INTO products(slug,name,description,entitlement_schema,billing_policy,active)
				VALUES($1,$2,$3,$4,$5,$6)
				RETURNING id,slug,name,description,entitlement_schema,entitlement_schema_version,billing_policy,billing_policy_version,active,version,created_at,updated_at`,
				input.Slug, input.Name, input.Description, input.EntitlementSchema, input.BillingPolicy, *input.Active).
				Scan(&product.ID, &product.Slug, &product.Name, &product.Description, &product.EntitlementSchema, &product.EntitlementSchemaVersion,
					&product.BillingPolicy, &product.BillingPolicyVersion, &product.Active, &product.Version, &product.CreatedAt, &product.UpdatedAt)
			if err != nil {
				if isUniqueViolation(err) {
					return result, bulkConflict(path+"/slug", "product_slug_conflict", fmt.Sprintf("Product slug %q already exists.", input.Slug))
				}
				return result, err
			}
			result.Products.Created++
			if err := a.writeCatalogueAudit(ctx, tx, "product.bulk_create", "product", product.ID, nil, productState(product)); err != nil {
				return result, err
			}
		} else {
			before, err := lockBulkProduct(ctx, tx, input.Slug)
			if errors.Is(err, pgx.ErrNoRows) {
				return result, bulkConflict(path+"/slug", "product_not_found", fmt.Sprintf("Product slug %q does not exist.", input.Slug))
			}
			if err != nil {
				return result, err
			}
			if before.Version != input.ExpectedVersion {
				return result, bulkConflict(path+"/expected_version", "product_version_conflict", fmt.Sprintf("Product %q is version %d, not %d.", input.Slug, before.Version, input.ExpectedVersion))
			}
			if err := validateBulkStoredPlans(ctx, tx, before.ID, input); err != nil {
				return result, bulkConflict(path+"/entitlement_schema", "incompatible_entitlement_schema", err.Error())
			}
			schemaChanged := !reflect.DeepEqual(before.EntitlementSchema, input.EntitlementSchema)
			policyChanged := !reflect.DeepEqual(before.BillingPolicy, input.BillingPolicy)
			changed := before.Name != input.Name || before.Description != input.Description || before.Active != *input.Active || schemaChanged || policyChanged
			if changed {
				err = tx.QueryRow(ctx, `UPDATE products SET name=$2,description=$3,entitlement_schema=$4,
					entitlement_schema_version=entitlement_schema_version+CASE WHEN $5 THEN 1 ELSE 0 END,
					billing_policy=$6,billing_policy_version=billing_policy_version+CASE WHEN $7 THEN 1 ELSE 0 END,
					active=$8,version=version+1,updated_at=now() WHERE id=$1
					RETURNING id,slug,name,description,entitlement_schema,entitlement_schema_version,billing_policy,billing_policy_version,active,version,created_at,updated_at`,
					before.ID, input.Name, input.Description, input.EntitlementSchema, schemaChanged, input.BillingPolicy, policyChanged, *input.Active).
					Scan(&product.ID, &product.Slug, &product.Name, &product.Description, &product.EntitlementSchema, &product.EntitlementSchemaVersion,
						&product.BillingPolicy, &product.BillingPolicyVersion, &product.Active, &product.Version, &product.CreatedAt, &product.UpdatedAt)
				if err != nil {
					return result, err
				}
				result.Products.Updated++
				archiveProduct = before.Active && !product.Active
				if err := a.writeCatalogueAudit(ctx, tx, "product.bulk_update", "product", product.ID, productState(before), productState(product)); err != nil {
					return result, err
				}
			} else {
				product = before
				result.Products.Unchanged++
			}
		}

		for configIndex, configuration := range input.DelegatedAccess {
			_, err := configureProductDelegation(ctx, tx, product.ID, configuration)
			if err != nil {
				var pgErr *pgconn.PgError
				if errors.As(err, &pgErr) {
					switch pgErr.Code {
					case "40001":
						return result, bulkConflict(fmt.Sprintf("%s/delegated_access/%d/expected_version", path, configIndex), "delegation_version_conflict", "Delegation configuration changed; reload and retry.")
					case "23505":
						return result, bulkConflict(fmt.Sprintf("%s/delegated_access/%d", path, configIndex), "delegation_client_conflict", "An authorizer or actor client is already assigned in this environment.")
					}
				}
				return result, err
			}
			if configuration.ExpectedVersion == 0 {
				result.DelegatedAccess.Created++
			} else {
				result.DelegatedAccess.Updated++
			}
		}
		if archiveProduct {
			if _, err := tx.Exec(ctx, `SELECT disable_product_delegation($1)`, product.ID); err != nil {
				return result, err
			}
		}

		planOrder := append([]catalogueBulkPlan(nil), input.Plans...)
		sort.SliceStable(planOrder, func(i, j int) bool {
			leftDefault := planOrder[i].Default != nil && *planOrder[i].Default
			rightDefault := planOrder[j].Default != nil && *planOrder[j].Default
			return !leftDefault && rightDefault
		})
		for _, plan := range planOrder {
			if err := a.applyBulkPlan(ctx, tx, product, plan, &result); err != nil {
				return result, err
			}
		}
		for _, pack := range input.CreditPacks {
			if err := a.applyBulkCreditPack(ctx, tx, product, pack, &result); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func lockBulkProduct(ctx context.Context, tx pgx.Tx, slug string) (productRecord, error) {
	var item productRecord
	err := tx.QueryRow(ctx, `SELECT id,slug,name,description,entitlement_schema,entitlement_schema_version,billing_policy,billing_policy_version,active,version,created_at,updated_at
		FROM products WHERE slug=$1 FOR UPDATE`, slug).
		Scan(&item.ID, &item.Slug, &item.Name, &item.Description, &item.EntitlementSchema, &item.EntitlementSchemaVersion,
			&item.BillingPolicy, &item.BillingPolicyVersion, &item.Active, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func validateBulkStoredPlans(ctx context.Context, tx pgx.Tx, productID uuid.UUID, input catalogueBulkProduct) error {
	overrides := make(map[string]catalogueBulkPlan, len(input.Plans))
	for _, plan := range input.Plans {
		overrides[strings.TrimSpace(plan.Slug)] = plan
	}
	rows, err := tx.Query(ctx, `SELECT slug,entitlements,active FROM plans WHERE product_id=$1 FOR UPDATE`, productID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var slug string
		var entitlements map[string]any
		var active bool
		if err := rows.Scan(&slug, &entitlements, &active); err != nil {
			return err
		}
		if override, ok := overrides[slug]; ok {
			entitlements = override.Entitlements
			active = override.Active != nil && *override.Active
		}
		if err := products.ValidateStoredEntitlements(input.EntitlementSchema, entitlements); err != nil {
			return fmt.Errorf("schema is incompatible with plan %q: %w", slug, err)
		}
		if input.Active != nil && !*input.Active && active {
			return fmt.Errorf("archived product contains active plan %q", slug)
		}
	}
	return rows.Err()
}

func (a *API) applyBulkPlan(ctx context.Context, tx pgx.Tx, product productRecord, input catalogueBulkPlan, result *catalogueBulkResult) error {
	input.Slug = strings.TrimSpace(input.Slug)
	input.PlanFamilyID = strings.TrimSpace(input.PlanFamilyID)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	validatedEntitlements, err := products.ValidateEntitlements(product.EntitlementSchema, input.Entitlements)
	if err != nil {
		return err
	}
	effectiveFrom := time.Now().UTC()
	if input.EffectiveFrom != nil {
		effectiveFrom = *input.EffectiveFrom
	}
	if input.Operation == "create" {
		row := tx.QueryRow(ctx, `INSERT INTO plans(product_id,slug,plan_family_id,name,description,price_minor,currency,included_credits,entitlements,active,
			billing_interval,billing_model,selectable,default_for_product,checkout_enabled,effective_from,effective_to)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
			RETURNING id,product_id,$18::text,slug,plan_family_id,name,description,price_minor,currency,included_credits,entitlements,billing_interval,billing_model,
			selectable,default_for_product,checkout_enabled,effective_from,effective_to,active,version,created_at,updated_at`,
			product.ID, input.Slug, input.PlanFamilyID, input.Name, input.Description, input.PriceMinor, input.Currency, input.IncludedCredits,
			validatedEntitlements, *input.Active, input.BillingInterval, input.BillingModel, *input.Selectable, *input.Default,
			*input.CheckoutEnabled, effectiveFrom, input.EffectiveTo, product.Slug)
		created, err := scanPlan(row)
		if err != nil {
			if isUniqueViolation(err) {
				return bulkConflict("/products/*/plans/*/slug", "plan_slug_conflict", fmt.Sprintf("Plan slug %q already exists for product %q.", input.Slug, product.Slug))
			}
			return err
		}
		result.Plans.Created++
		return a.writeCatalogueAudit(ctx, tx, "plan.bulk_create", "plan", created.ID, nil, planState(created))
	}
	row := tx.QueryRow(ctx, `SELECT p.id,p.product_id,pr.slug,p.slug,p.plan_family_id,p.name,p.description,p.price_minor,p.currency,p.included_credits,
		p.entitlements,p.billing_interval,p.billing_model,p.selectable,p.default_for_product,p.checkout_enabled,p.effective_from,p.effective_to,p.active,p.version,p.created_at,p.updated_at
		FROM plans p JOIN products pr ON pr.id=p.product_id WHERE p.product_id=$1 AND p.slug=$2 FOR UPDATE OF p`, product.ID, input.Slug)
	before, err := scanPlan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return bulkConflict("/products/*/plans/*/slug", "plan_not_found", fmt.Sprintf("Plan slug %q does not exist for product %q.", input.Slug, product.Slug))
	}
	if err != nil {
		return err
	}
	if before.Version != input.ExpectedVersion {
		return bulkConflict("/products/*/plans/*/expected_version", "plan_version_conflict", fmt.Sprintf("Plan %q is version %d, not %d.", input.Slug, before.Version, input.ExpectedVersion))
	}
	if input.EffectiveFrom == nil {
		effectiveFrom = before.EffectiveFrom
	}
	row = tx.QueryRow(ctx, `UPDATE plans SET plan_family_id=$3,name=$4,description=$5,price_minor=$6,currency=$7,included_credits=$8,entitlements=$9,
		billing_interval=$10,billing_model=$11,selectable=$12,default_for_product=$13,checkout_enabled=$14,effective_from=$15,effective_to=$16,
		active=$17,version=version+1,updated_at=now() WHERE product_id=$1 AND slug=$2
		RETURNING id,product_id,$18::text,slug,plan_family_id,name,description,price_minor,currency,included_credits,entitlements,billing_interval,billing_model,
		selectable,default_for_product,checkout_enabled,effective_from,effective_to,active,version,created_at,updated_at`,
		product.ID, input.Slug, input.PlanFamilyID, input.Name, input.Description, input.PriceMinor, input.Currency, input.IncludedCredits,
		validatedEntitlements, input.BillingInterval, input.BillingModel, *input.Selectable, *input.Default, *input.CheckoutEnabled,
		effectiveFrom, input.EffectiveTo, *input.Active, product.Slug)
	updated, err := scanPlan(row)
	if err != nil {
		return err
	}
	result.Plans.Updated++
	return a.writeCatalogueAudit(ctx, tx, "plan.bulk_update", "plan", updated.ID, planState(before), planState(updated))
}

func (a *API) applyBulkCreditPack(ctx context.Context, tx pgx.Tx, product productRecord, input catalogueBulkCreditPack, result *catalogueBulkResult) error {
	input.Slug = strings.TrimSpace(input.Slug)
	input.Name = strings.TrimSpace(input.Name)
	if input.Operation == "create" {
		var id uuid.UUID
		err := tx.QueryRow(ctx, `INSERT INTO credit_packs(product_id,slug,name,credits,price_minor,currency,validity_days,active)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, product.ID, input.Slug, input.Name, input.Credits,
			input.PriceMinor, input.Currency, input.ValidityDays, *input.Active).Scan(&id)
		if err != nil {
			if isUniqueViolation(err) {
				return bulkConflict("/products/*/credit_packs/*/slug", "credit_pack_slug_conflict", fmt.Sprintf("Credit-pack slug %q already exists for product %q.", input.Slug, product.Slug))
			}
			return err
		}
		result.CreditPacks.Created++
		return a.writeCatalogueAudit(ctx, tx, "credit_pack.bulk_create", "credit_pack", id, nil, map[string]any{
			"id": id, "product_id": product.ID, "slug": input.Slug, "name": input.Name, "credits": input.Credits,
			"price_minor": input.PriceMinor, "currency": input.Currency, "validity_days": input.ValidityDays, "active": *input.Active, "version": 1,
		})
	}
	var id uuid.UUID
	var version int64
	var before map[string]any
	var name, currency string
	var credits, priceMinor int64
	var validityDays *int
	var active bool
	err := tx.QueryRow(ctx, `SELECT id,name,credits,price_minor,currency,validity_days,active,version FROM credit_packs
		WHERE product_id=$1 AND slug=$2 FOR UPDATE`, product.ID, input.Slug).
		Scan(&id, &name, &credits, &priceMinor, &currency, &validityDays, &active, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return bulkConflict("/products/*/credit_packs/*/slug", "credit_pack_not_found", fmt.Sprintf("Credit-pack slug %q does not exist for product %q.", input.Slug, product.Slug))
	}
	if err != nil {
		return err
	}
	if version != input.ExpectedVersion {
		return bulkConflict("/products/*/credit_packs/*/expected_version", "credit_pack_version_conflict", fmt.Sprintf("Credit pack %q is version %d, not %d.", input.Slug, version, input.ExpectedVersion))
	}
	before = map[string]any{"id": id, "product_id": product.ID, "slug": input.Slug, "name": name, "credits": credits, "price_minor": priceMinor, "currency": currency, "validity_days": validityDays, "active": active, "version": version}
	var savedVersion int64
	if err := tx.QueryRow(ctx, `UPDATE credit_packs SET name=$3,credits=$4,price_minor=$5,currency=$6,validity_days=$7,active=$8,
		version=version+1,updated_at=now() WHERE product_id=$1 AND slug=$2 RETURNING version`, product.ID, input.Slug, input.Name,
		input.Credits, input.PriceMinor, input.Currency, input.ValidityDays, *input.Active).Scan(&savedVersion); err != nil {
		return err
	}
	result.CreditPacks.Updated++
	return a.writeCatalogueAudit(ctx, tx, "credit_pack.bulk_update", "credit_pack", id, before, map[string]any{
		"id": id, "product_id": product.ID, "slug": input.Slug, "name": input.Name, "credits": input.Credits,
		"price_minor": input.PriceMinor, "currency": input.Currency, "validity_days": input.ValidityDays, "active": *input.Active, "version": savedVersion,
	})
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
