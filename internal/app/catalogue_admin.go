package app

import (
	"context"
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

type productRecord struct {
	ID          uuid.UUID `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Active      bool      `json:"active"`
	Version     int64     `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type planRecord struct {
	ID              uuid.UUID      `json:"id"`
	ProductID       uuid.UUID      `json:"product_id"`
	Product         string         `json:"product"`
	Slug            string         `json:"slug"`
	Name            string         `json:"name"`
	PriceMinor      int64          `json:"price_minor"`
	Currency        string         `json:"currency"`
	IncludedCredits int64          `json:"included_credits"`
	Entitlements    map[string]any `json:"entitlements"`
	BillingInterval string         `json:"billing_interval"`
	Active          bool           `json:"active"`
	Version         int64          `json:"version"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

func (a *API) requireCatalogueAdmin(action, resourceType string, next http.HandlerFunc) http.HandlerFunc {
	return a.requireAdmin(action, resourceType, func(w http.ResponseWriter, r *http.Request) {
		claims, _ := auth.FromContext(r.Context())
		if strings.HasPrefix(claims.Subject, "service:") {
			a.auditDeniedAdmin(r, action, resourceType, r.URL.Path, "interactive_admin_required")
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next(w, r)
	})
}

func productState(value productRecord) map[string]any {
	return map[string]any{
		"id": value.ID, "slug": value.Slug, "name": value.Name,
		"description": value.Description, "active": value.Active, "version": value.Version,
	}
}

func planState(value planRecord) map[string]any {
	return map[string]any{
		"id": value.ID, "product_id": value.ProductID, "slug": value.Slug, "name": value.Name,
		"price_minor": value.PriceMinor, "currency": value.Currency,
		"included_credits": value.IncludedCredits, "entitlements": value.Entitlements,
		"billing_interval": value.BillingInterval, "active": value.Active, "version": value.Version,
	}
}

func cataloguePage(r *http.Request) (int, int, bool) {
	limit := 50
	offset := 0
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return 0, 0, false
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, false
		}
	}
	return limit, offset, true
}

func catalogueStatus(r *http.Request) (string, bool) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "all"
	}
	return status, status == "all" || status == "active" || status == "archived" || status == "inactive"
}

func (a *API) writeCatalogueAudit(ctx context.Context, tx pgx.Tx, action, resourceType string, resourceID uuid.UUID, before, after map[string]any) error {
	claims, ok := auth.FromContext(ctx)
	if !ok {
		return errors.New("missing audit actor")
	}
	actorType := "user"
	if strings.HasPrefix(claims.Subject, "service:") {
		actorType = "service"
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_log(actor_subject,actor_type,action,resource_type,resource_id,before_state,after_state)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, claims.Subject, actorType, action, resourceType, resourceID.String(), before, after)
	return err
}

func (a *API) createProduct(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Slug        string `json:"slug"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Slug = strings.TrimSpace(in.Slug)
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	if err := products.ValidateProduct(in.Slug, in.Name, in.Description); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var created productRecord
	err := pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `INSERT INTO products(slug,name,description)
			VALUES($1,$2,$3) RETURNING id,slug,name,description,active,version,created_at,updated_at`,
			in.Slug, in.Name, in.Description).Scan(&created.ID, &created.Slug, &created.Name, &created.Description,
			&created.Active, &created.Version, &created.CreatedAt, &created.UpdatedAt); err != nil {
			return err
		}
		return a.writeCatalogueAudit(r.Context(), tx, "product.create", "product", created.ID, nil, productState(created))
	})
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a *API) listAdminProducts(w http.ResponseWriter, r *http.Request) {
	limit, offset, valid := cataloguePage(r)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid pagination")
		return
	}
	status, valid := catalogueStatus(r)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid product status")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if len(query) > 120 {
		writeError(w, http.StatusBadRequest, "query is too long")
		return
	}
	rows, err := a.pool.Query(r.Context(), `SELECT id,slug,name,description,active,version,created_at,updated_at
		FROM products
		WHERE ($1='' OR slug ILIKE '%' || $1 || '%' OR name ILIKE '%' || $1 || '%')
		AND ($2='all' OR ($2='active' AND active) OR ($2 IN ('archived','inactive') AND NOT active))
		ORDER BY slug,id LIMIT $3 OFFSET $4`, query, status, limit, offset)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	items := []productRecord{}
	for rows.Next() {
		var item productRecord
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.Description, &item.Active, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
			writeDBError(w, err)
			return
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, items)
}

func (a *API) getAdminProduct(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid product id")
		return
	}
	var item productRecord
	err = a.pool.QueryRow(r.Context(), `SELECT id,slug,name,description,active,version,created_at,updated_at FROM products WHERE id=$1`, id).
		Scan(&item.ID, &item.Slug, &item.Name, &item.Description, &item.Active, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *API) updateAdminProduct(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid product id")
		return
	}
	var in struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Active      *bool   `json:"active"`
		Version     int64   `json:"version"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Version < 1 {
		writeError(w, http.StatusBadRequest, "version is required")
		return
	}
	var updated productRecord
	err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		var before productRecord
		if err := tx.QueryRow(r.Context(), `SELECT id,slug,name,description,active,version,created_at,updated_at FROM products WHERE id=$1 FOR UPDATE`, id).
			Scan(&before.ID, &before.Slug, &before.Name, &before.Description, &before.Active, &before.Version, &before.CreatedAt, &before.UpdatedAt); err != nil {
			return err
		}
		if before.Version != in.Version {
			return errCatalogueVersionConflict
		}
		name := before.Name
		description := before.Description
		active := before.Active
		if in.Name != nil {
			name = strings.TrimSpace(*in.Name)
		}
		if in.Description != nil {
			description = strings.TrimSpace(*in.Description)
		}
		if in.Active != nil {
			active = *in.Active
		}
		if err := products.ValidateProduct(before.Slug, name, description); err != nil {
			return catalogueValidationError{err.Error()}
		}
		if name == before.Name && description == before.Description && active == before.Active {
			updated = before
			return nil
		}
		if err := tx.QueryRow(r.Context(), `UPDATE products SET name=$2,description=$3,active=$4,version=version+1,updated_at=now()
			WHERE id=$1 RETURNING id,slug,name,description,active,version,created_at,updated_at`, id, name, description, active).
			Scan(&updated.ID, &updated.Slug, &updated.Name, &updated.Description, &updated.Active, &updated.Version, &updated.CreatedAt, &updated.UpdatedAt); err != nil {
			return err
		}
		return a.writeCatalogueAudit(r.Context(), tx, "product.update", "product", id, productState(before), productState(updated))
	})
	if errors.Is(err, errCatalogueVersionConflict) {
		writeError(w, http.StatusConflict, "product was modified by another request")
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

func (a *API) listAdminPlans(w http.ResponseWriter, r *http.Request) {
	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid product id")
		return
	}
	limit, offset, valid := cataloguePage(r)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid pagination")
		return
	}
	status, valid := catalogueStatus(r)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid plan status")
		return
	}
	rows, err := a.pool.Query(r.Context(), `SELECT p.id,p.product_id,pr.slug,p.slug,p.name,p.price_minor,p.currency,p.included_credits,
		p.entitlements,p.billing_interval,p.active,p.version,p.created_at,p.updated_at
		FROM plans p JOIN products pr ON pr.id=p.product_id
		WHERE p.product_id=$1 AND ($2='all' OR ($2='active' AND p.active) OR ($2 IN ('archived','inactive') AND NOT p.active))
		ORDER BY p.price_minor,p.slug,p.id LIMIT $3 OFFSET $4`, productID, status, limit, offset)
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
	writeJSON(w, http.StatusOK, items)
}

func (a *API) getAdminPlan(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid plan id")
		return
	}
	row := a.pool.QueryRow(r.Context(), `SELECT p.id,p.product_id,pr.slug,p.slug,p.name,p.price_minor,p.currency,p.included_credits,
		p.entitlements,p.billing_interval,p.active,p.version,p.created_at,p.updated_at
		FROM plans p JOIN products pr ON pr.id=p.product_id WHERE p.id=$1`, id)
	item, err := scanPlan(row)
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

type rowScanner interface {
	Scan(...any) error
}

func scanPlan(row rowScanner) (planRecord, error) {
	var item planRecord
	err := row.Scan(&item.ID, &item.ProductID, &item.Product, &item.Slug, &item.Name, &item.PriceMinor, &item.Currency,
		&item.IncludedCredits, &item.Entitlements, &item.BillingInterval, &item.Active, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

var errCatalogueVersionConflict = errors.New("catalogue version conflict")

type catalogueValidationError struct{ message string }

func (e catalogueValidationError) Error() string { return e.message }
