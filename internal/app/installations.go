package app

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/tociva/billmesh/internal/auth"
)

func (a *API) createInstallation(w http.ResponseWriter, r *http.Request) {
	accountID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid account id")
		return
	}
	if !a.canAccessAccount(r, accountID) {
		writeError(w, 403, "account not accessible")
		return
	}
	var in struct {
		Application    string `json:"application"`
		OrganizationID string `json:"organization_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Application == "" || in.OrganizationID == "" {
		writeError(w, 400, "application and organization_id are required")
		return
	}
	claims, _ := auth.FromContext(r.Context())
	if (in.Application != claims.App || in.OrganizationID != claims.OrgID) && !claims.Has("billing:link") {
		writeError(w, 403, "installation identity not accessible")
		return
	}
	var id uuid.UUID
	err = a.pool.QueryRow(r.Context(), `INSERT INTO application_installations(account_id,application,organization_id)
		VALUES($1,$2,$3) ON CONFLICT(account_id,application,organization_id) DO UPDATE SET active=true,updated_at=now() RETURNING id`, accountID, in.Application, in.OrganizationID).Scan(&id)
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "account_id": accountID, "application": in.Application, "organization_id": in.OrganizationID})
}

func (a *API) revokeInstallation(w http.ResponseWriter, r *http.Request) {
	id, ok := a.accessibleInstallation(r)
	if !ok {
		writeError(w, 403, "installation not accessible")
		return
	}
	if _, err := a.pool.Exec(r.Context(), `UPDATE application_installations SET active=false,updated_at=now() WHERE id=$1`, id); err != nil {
		writeDBError(w, err)
		return
	}
	writeNoContent(w)
}

func (a *API) settleInstallationExecution(w http.ResponseWriter, r *http.Request) {
	id, ok := a.accessibleInstallation(r)
	if !ok {
		writeError(w, 403, "installation not accessible")
		return
	}
	var in struct {
		Actual int64 `json:"actual"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Actual < 0 {
		writeError(w, 400, "actual must not be negative")
		return
	}
	tag, err := a.pool.Exec(r.Context(), `UPDATE application_installations SET active_execution_id=NULL,updated_at=now() WHERE id=$1 AND active_execution_id IS NOT NULL`, id)
	if err != nil {
		writeDBError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 409, "installation has no active execution")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"installation_id": id, "settled": in.Actual})
}

func (a *API) accessibleInstallation(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, false
	}
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		return uuid.Nil, false
	}
	environment := claims.Environment
	if environment == "" {
		environment = "production"
	}
	var found bool
	err = a.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM application_installations i JOIN account_links l ON l.account_id=i.account_id
		WHERE i.id=$1 AND l.application=$2 AND l.organization_id=$3 AND l.environment=$4
		AND ((i.application=$2 AND i.organization_id=$3) OR $5))`, id, claims.App, claims.OrgID, environment, claims.Has("billing:link")).Scan(&found)
	return id, err == nil && found
}
