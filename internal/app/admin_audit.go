package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/tociva/billmesh/internal/auth"
)

func (a *API) requireAdmin(action, resourceType string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := auth.FromContext(r.Context())
		if !ok || !claims.Has("billing:admin") {
			a.auditDeniedAdmin(r, action, resourceType, r.URL.Path, "missing_permission")
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next(w, r)
	}
}

// A denied privileged request never becomes a business mutation. Audit failures
// are logged, but cannot change the HTTP denial into an authorization success.
func (a *API) auditDeniedAdmin(r *http.Request, action, resourceType, resourceID, reason string) {
	if a.pool == nil {
		return
	}
	claims, ok := auth.FromContext(r.Context())
	if !ok || claims.Subject == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	var accountID any
	if id, err := a.accountIDForClaims(ctx); err == nil {
		accountID = id
	}
	actorType := "user"
	if strings.HasPrefix(claims.Subject, "service:") {
		actorType = "service"
	}
	_, err := a.pool.Exec(ctx, `INSERT INTO audit_log(account_id,actor_subject,actor_type,action,resource_type,resource_id,reason) VALUES($1,$2,$3,$4,$5,$6,$7)`, accountID, claims.Subject, actorType, action+".denied", resourceType, resourceID, reason)
	if err != nil {
		a.log.Error("failed to audit denied admin action", "action", action, "error", err)
	}
}
