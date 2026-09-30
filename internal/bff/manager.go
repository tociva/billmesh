package bff

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tociva/billmesh/internal/auth"
	"github.com/tociva/billmesh/internal/config"
	"github.com/tociva/billmesh/internal/httpresponse"
)

type Manager struct {
	config   config.BFFConfig
	store    *sessionStore
	oidc     *oidcClient
	verifier tokenVerifier
	log      *slog.Logger
	now      func() time.Time
	attempts *attemptLimiter
}

var errRefreshBusy = errors.New("browser session refresh is busy")

func New(cfg config.BFFConfig, pool *pgxpool.Pool, verifier tokenVerifier, log *slog.Logger) (*Manager, error) {
	if pool == nil || verifier == nil {
		return nil, errors.New("BFF requires a database and token verifier")
	}
	if log == nil {
		log = slog.Default()
	}
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	keys, err := newKeyring(cfg.SessionEncryptionKeys)
	if err != nil {
		return nil, err
	}
	return &Manager{
		config: cfg, store: newSessionStore(pool, keys, cfg.SessionIdleTTL, cfg.LoginTTL, cfg.LogoutTTL),
		oidc: newOIDCClient(cfg, nil), verifier: verifier, log: log, now: time.Now,
		attempts: &attemptLimiter{entries: make(map[string]attemptCount), limit: cfg.LoginAttemptsPerMinute, now: time.Now},
	}, nil
}

func ValidateConfig(cfg config.BFFConfig) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}
	_, err := newKeyring(cfg.SessionEncryptionKeys)
	return err
}

func validateConfig(cfg config.BFFConfig) error {
	if cfg.Realm != "console" && cfg.Realm != "admin" {
		return errors.New("BFF realm must be console or admin")
	}
	prefix := "BFF_" + strings.ToUpper(cfg.Realm)
	appOriginName := strings.ToUpper(cfg.Realm) + "_APP_ORIGIN"
	for name, value := range map[string]string{
		appOriginName: cfg.AppOrigin, prefix + "_ISSUER": cfg.Issuer,
		prefix + "_CLIENT_ID": cfg.ClientID, prefix + "_CLIENT_SECRET": cfg.ClientSecret,
		prefix + "_AUDIENCE": cfg.Audience, prefix + "_REDIRECT_URI": cfg.RedirectURI,
		prefix + "_POST_LOGOUT_REDIRECT_URI": cfg.PostLogoutRedirectURI,
		prefix + "_STANDALONE_LOGOUT_URI":    cfg.StandaloneLogoutURI,
		"BFF_SESSION_ENCRYPTION_KEYS":        cfg.SessionEncryptionKeys,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	appOrigin, err := parseSecureURL(cfg.AppOrigin, appOriginName)
	if err != nil {
		return err
	}
	if appOrigin.Path != "" && appOrigin.Path != "/" || appOrigin.RawQuery != "" || appOrigin.Fragment != "" {
		return fmt.Errorf("%s must be an origin without a path", appOriginName)
	}
	redirect, err := parseSecureURL(cfg.RedirectURI, prefix+"_REDIRECT_URI")
	if err != nil {
		return err
	}
	logout, err := parseSecureURL(cfg.PostLogoutRedirectURI, prefix+"_POST_LOGOUT_REDIRECT_URI")
	if err != nil {
		return err
	}
	if redirect.Scheme != logout.Scheme || redirect.Host != logout.Host {
		return fmt.Errorf("%s_POST_LOGOUT_REDIRECT_URI must use the same origin as %s_REDIRECT_URI", prefix, prefix)
	}
	if redirect.Path != "/api/v1/auth/"+cfg.Realm+"/callback" {
		return fmt.Errorf("%s_REDIRECT_URI must point to /api/v1/auth/%s/callback", prefix, cfg.Realm)
	}
	if logout.Path != "/api/v1/auth/"+cfg.Realm+"/logout/callback" {
		return fmt.Errorf("%s_POST_LOGOUT_REDIRECT_URI must point to /api/v1/auth/%s/logout/callback", prefix, cfg.Realm)
	}
	if _, err = parseSecureURL(cfg.Issuer, prefix+"_ISSUER"); err != nil {
		return err
	}
	if _, err = parseSecureURL(cfg.StandaloneLogoutURI, prefix+"_STANDALONE_LOGOUT_URI"); err != nil {
		return err
	}
	if !validReturnPath(cfg.DefaultReturnPath) {
		return fmt.Errorf("invalid BFF default return path %q", cfg.DefaultReturnPath)
	}
	if len(cfg.ReturnPathPrefixes) == 0 {
		return errors.New("BFF return path policy must contain at least one prefix")
	}
	defaultAllowed := false
	for _, allowedPrefix := range cfg.ReturnPathPrefixes {
		if !validReturnPath(allowedPrefix) {
			return fmt.Errorf("invalid BFF return path prefix %q", allowedPrefix)
		}
		if pathMatchesPrefix(cfg.DefaultReturnPath, allowedPrefix) {
			defaultAllowed = true
		}
	}
	if !defaultAllowed {
		return errors.New("BFF default return path must match the realm return path policy")
	}
	if cfg.Realm == "admin" && !containsScope(cfg.Scope, "billing:admin") {
		return errors.New("BFF_ADMIN_SCOPE must include billing:admin")
	}
	return nil
}

func containsScope(value, expected string) bool {
	for _, scope := range strings.Fields(value) {
		if scope == expected {
			return true
		}
	}
	return false
}

func validReturnPath(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") &&
		!strings.Contains(value, "\\") && !strings.HasPrefix(value, "/auth/")
}

func pathMatchesPrefix(path, prefix string) bool {
	return prefix == "/" || path == prefix || strings.HasPrefix(path, strings.TrimRight(prefix, "/")+"/")
}

func parseSecureURL(value, name string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("%s must be an absolute URL", name)
	}
	if parsed.Scheme != "https" {
		return nil, fmt.Errorf("%s must use HTTPS", name)
	}
	return parsed, nil
}

func (m *Manager) AuthHandler() http.Handler {
	mux := http.NewServeMux()
	base := m.AuthBasePath()
	mux.HandleFunc("GET "+base+"/login", m.login)
	mux.HandleFunc("GET "+base+"/callback", m.callback)
	mux.HandleFunc("GET "+base+"/session", m.session)
	mux.HandleFunc("POST "+base+"/logout", m.logout)
	mux.HandleFunc("GET "+base+"/logout/continue", m.continueLogout)
	mux.HandleFunc("GET "+base+"/logout/provider", m.providerLogout)
	mux.HandleFunc("GET "+base+"/logout/callback", m.logoutCallback)
	return httpresponse.JSONFallbacks(mux)
}

func (m *Manager) AuthBasePath() string {
	return "/api/v1/auth/" + m.config.Realm
}

func (m *Manager) AppOrigin() string {
	return strings.TrimRight(m.config.AppOrigin, "/")
}

func (m *Manager) sessionCookieName() string {
	return "__Host-billmesh-" + m.config.Realm + "-session"
}

func (m *Manager) loginCookieName() string {
	return "__Host-billmesh-" + m.config.Realm + "-login"
}

func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		if r.Header.Get("Authorization") != "" {
			writeError(w, http.StatusBadRequest, "authorization header is not accepted on browser routes")
			return
		}
		session, claims, status, err := m.authenticate(r)
		if err != nil {
			writeError(w, status, http.StatusText(status))
			return
		}
		csrfValues := r.Header.Values(csrfHeaderName)
		if !safeMethod(r.Method) && (len(csrfValues) != 1 || !equalSecret(session.CSRFToken, csrfValues[0])) {
			writeError(w, http.StatusForbidden, "CSRF validation failed")
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), claims)))
	})
}

func (m *Manager) login(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if !m.allowAuthAttempt(w, r) {
		return
	}
	state, err := randomIdentifier()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "login unavailable")
		return
	}
	nonce, err := randomIdentifier()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "login unavailable")
		return
	}
	verifier, err := randomIdentifier()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "login unavailable")
		return
	}
	correlation, err := randomIdentifier()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "login unavailable")
		return
	}
	transaction := loginTransaction{
		SchemaVersion: schemaVersion, Realm: m.config.Realm, State: state, Nonce: nonce, CodeVerifier: verifier,
		CorrelationHash: digest(correlation), ReturnTo: m.safeReturnTo(r.URL.Query().Get("returnTo")), CreatedAt: m.now(),
	}
	redirect, err := m.oidc.authorizationURL(r.Context(), transaction)
	if err == nil {
		err = m.store.saveLogin(r.Context(), transaction)
	}
	if err != nil {
		m.log.Error("BFF login start failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "login unavailable")
		return
	}
	http.SetCookie(w, secureCookie(m.loginCookieName(), correlation, m.config.LoginTTL))
	writeRedirect(w, redirect, http.StatusFound)
}

func (m *Manager) callback(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if !m.allowAuthAttempt(w, r) {
		return
	}
	clearCookie(w, m.loginCookieName())
	state := r.URL.Query().Get("state")
	if state == "" {
		writeError(w, http.StatusUnauthorized, "invalid login callback")
		return
	}
	transaction, err := m.store.takeLogin(r.Context(), state)
	correlation, cookieErr := r.Cookie(m.loginCookieName())
	if err != nil || cookieErr != nil || transaction == nil || transaction.SchemaVersion != schemaVersion ||
		transaction.Realm != m.config.Realm ||
		!equalBytes(transaction.CorrelationHash, digest(correlation.Value)) {
		writeError(w, http.StatusUnauthorized, "invalid login callback")
		return
	}
	if providerError := r.URL.Query().Get("error"); providerError != "" {
		m.redirectLoginError(w, r)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusUnauthorized, "invalid login callback")
		return
	}
	tokens, err := m.oidc.exchange(r.Context(), code, transaction.CodeVerifier)
	if err != nil {
		m.log.Error("BFF authorization-code exchange failed", "error", err)
		m.redirectLoginError(w, r)
		return
	}
	claims, err := m.verifier.Verify(r.Context(), tokens.AccessToken)
	if err != nil {
		m.log.Warn("BFF received an invalid access token", "realm", m.config.Realm, "error", err)
		m.redirectLoginError(w, r)
		return
	}
	if claims.ExpiresAt == nil {
		m.log.Warn("BFF received an invalid access token", "realm", m.config.Realm, "reason", "missing expiration")
		m.redirectLoginError(w, r)
		return
	}
	claims.AddPermissions(tokens.Scope)
	if err := m.validateRealmClaims(claims); err != nil {
		m.log.Warn("BFF received an unauthorized access token", "realm", m.config.Realm, "error", err)
		m.redirectLoginError(w, r)
		return
	}
	identity, err := m.verifier.VerifyIDToken(r.Context(), tokens.IDToken, m.config.ClientID, transaction.Nonce, tokens.AccessToken)
	if err != nil {
		m.log.Warn("BFF received an invalid ID token", "realm", m.config.Realm, "error", err)
		m.redirectLoginError(w, r)
		return
	}
	if identity.Subject != claims.Subject {
		m.log.Warn("BFF received an invalid ID token", "realm", m.config.Realm, "reason", "subject does not match access token")
		m.redirectLoginError(w, r)
		return
	}
	sessionID, err := randomIdentifier()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "login unavailable")
		return
	}
	csrf, err := randomIdentifier()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "login unavailable")
		return
	}
	session := browserSession{
		SchemaVersion: schemaVersion, Realm: m.config.Realm, SessionID: sessionID, Subject: claims.Subject,
		Email: identity.Email, Name: identity.Name, OrgID: claims.OrgID, App: claims.App,
		Environment: claims.Environment, Permissions: []string(claims.Permissions), CSRFToken: csrf,
		AllowedOrigin: strings.TrimRight(m.config.AppOrigin, "/"), CreatedAt: m.now(),
		AbsoluteExpiry: m.now().Add(m.config.SessionAbsoluteTTL), AccessToken: tokens.AccessToken,
		AccessExpiry: claims.ExpiresAt.Time, RefreshToken: tokens.RefreshToken, IDToken: tokens.IDToken,
	}
	if err := m.store.createSession(r.Context(), session); err != nil {
		m.log.Error("BFF session creation failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "login unavailable")
		return
	}
	http.SetCookie(w, secureCookie(m.sessionCookieName(), sessionID, m.config.SessionAbsoluteTTL))
	writeRedirect(w, newURL(m.config.AppOrigin, transaction.ReturnTo), http.StatusFound)
}

func (m *Manager) session(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	session, _, status, err := m.authenticate(r)
	if err != nil {
		writeError(w, status, http.StatusText(status))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"user":          map[string]string{"subject": session.Subject, "email": session.Email, "name": session.Name},
		"context":       map[string]string{"organization_id": session.OrgID, "application": session.App, "environment": session.Environment},
		"csrfToken":     session.CSRFToken, "expiresAt": session.AbsoluteExpiry,
	})
}

func (m *Manager) logout(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	session, _, status, err := m.authenticate(r)
	if err != nil {
		clearCookie(w, m.sessionCookieName())
		writeError(w, status, http.StatusText(status))
		return
	}
	csrfValues := r.Header.Values(csrfHeaderName)
	if len(csrfValues) != 1 || !equalSecret(session.CSRFToken, csrfValues[0]) {
		writeError(w, http.StatusForbidden, "CSRF validation failed")
		return
	}
	if session.RefreshToken != "" {
		if err := m.oidc.revoke(r.Context(), session.RefreshToken); err != nil {
			m.log.Warn("BFF refresh-token revocation failed", "error", err)
		}
	}
	_ = m.store.deleteSession(r.Context(), session.SessionID)
	ticket, err := m.store.saveLogout(r.Context(), logoutTransaction{
		SchemaVersion: schemaVersion, Realm: m.config.Realm, IDToken: session.IDToken, CreatedAt: m.now(),
	})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "logout unavailable")
		return
	}
	clearCookie(w, m.sessionCookieName())
	continuation, _ := url.Parse(m.config.RedirectURI)
	continuation.Path = m.AuthBasePath() + "/logout/continue"
	continuation.RawQuery = "ticket=" + url.QueryEscape(ticket)
	continuation.Fragment = ""
	writeJSON(w, http.StatusOK, map[string]string{"logoutUrl": continuation.String()})
}

func (m *Manager) continueLogout(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	transaction, err := m.store.takeLogout(r.Context(), r.URL.Query().Get("ticket"))
	if err != nil || transaction == nil || transaction.SchemaVersion != schemaVersion || transaction.Realm != m.config.Realm {
		writeError(w, http.StatusUnauthorized, "invalid logout transaction")
		return
	}
	redirect, err := m.oidc.providerLogoutURL(r.Context(), transaction.IDToken)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "logout unavailable")
		return
	}
	writeRedirect(w, redirect, http.StatusFound)
}

func (m *Manager) logoutCallback(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	writeRedirect(w, strings.TrimRight(m.config.AppOrigin, "/")+"/auth/logout", http.StatusFound)
}

func (m *Manager) providerLogout(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	clearCookie(w, m.sessionCookieName())
	clearCookie(w, m.loginCookieName())
	logout, err := url.Parse(m.config.StandaloneLogoutURI)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "logout unavailable")
		return
	}
	query := logout.Query()
	query.Set("client_id", m.config.ClientID)
	query.Set("return_to", m.config.PostLogoutRedirectURI)
	logout.RawQuery = query.Encode()
	writeRedirect(w, logout.String(), http.StatusFound)
}

func (m *Manager) authenticate(r *http.Request) (*browserSession, *auth.Claims, int, error) {
	if !m.originAllowed(r) {
		return nil, nil, http.StatusForbidden, errors.New("origin is not allowed")
	}
	cookie, err := singleCookie(r, m.sessionCookieName())
	if err != nil {
		return nil, nil, http.StatusUnauthorized, err
	}
	session, err := m.store.loadSession(r.Context(), cookie.Value)
	if err != nil {
		m.log.Error("BFF session load failed", "error", err)
		return nil, nil, http.StatusServiceUnavailable, err
	}
	if session == nil || session.SchemaVersion != schemaVersion || session.Realm != m.config.Realm || session.SessionID != cookie.Value ||
		session.AllowedOrigin != strings.TrimRight(m.config.AppOrigin, "/") || !m.now().Before(session.AbsoluteExpiry) {
		return nil, nil, http.StatusUnauthorized, errors.New("invalid browser session")
	}
	session, err = m.refreshIfNeeded(r.Context(), *session)
	if err != nil {
		if errors.Is(err, errRefreshBusy) {
			return nil, nil, http.StatusServiceUnavailable, err
		}
		m.log.Warn("BFF browser session refresh failed", "realm", m.config.Realm, "error", err)
		_ = m.store.deleteSession(r.Context(), cookie.Value)
		return nil, nil, http.StatusUnauthorized, err
	}
	claims, err := m.verifier.Verify(r.Context(), session.AccessToken)
	if err != nil {
		m.log.Warn("BFF stored access token is invalid", "realm", m.config.Realm, "error", err)
		_ = m.store.deleteSession(r.Context(), cookie.Value)
		return nil, nil, http.StatusUnauthorized, errors.New("browser access token is invalid")
	}
	if claims.Subject != session.Subject || claims.OrgID != session.OrgID || claims.App != session.App ||
		claims.Environment != session.Environment {
		m.log.Warn("BFF stored access token no longer matches session", "realm", m.config.Realm)
		_ = m.store.deleteSession(r.Context(), cookie.Value)
		return nil, nil, http.StatusUnauthorized, errors.New("browser access token no longer matches session")
	}
	if m.config.Realm == "admin" {
		claims.AddPermissions(session.Permissions...)
	}
	if err := m.validateRealmClaims(claims); err != nil {
		m.log.Warn("BFF stored access token is unauthorized", "realm", m.config.Realm, "error", err)
		_ = m.store.deleteSession(r.Context(), cookie.Value)
		return nil, nil, http.StatusUnauthorized, errors.New("browser access token is unauthorized")
	}
	ok, err := m.store.touchSession(r.Context(), *session)
	if err != nil {
		return nil, nil, http.StatusServiceUnavailable, err
	}
	if !ok {
		return nil, nil, http.StatusUnauthorized, errors.New("browser session expired")
	}
	return session, claims, 0, nil
}

// Authenticate validates a browser session for non-BFF resources such as the
// API documentation, without exposing the stored browser tokens.
func (m *Manager) Authenticate(r *http.Request) (*auth.Claims, int, error) {
	_, claims, status, err := m.authenticate(r)
	return claims, status, err
}

func (m *Manager) refreshIfNeeded(ctx context.Context, session browserSession) (*browserSession, error) {
	if session.AccessExpiry.Sub(m.now()) > m.config.RefreshSkew {
		return &session, nil
	}
	if session.RefreshToken == "" {
		if m.now().Before(session.AccessExpiry) {
			return &session, nil
		}
		return nil, errors.New("browser access token expired")
	}
	owner, err := randomIdentifier()
	if err != nil {
		return nil, err
	}
	acquired, err := m.store.acquireRefreshLease(ctx, session.SessionID, owner)
	if err != nil {
		return nil, err
	}
	if !acquired {
		latest, err := m.store.loadSession(ctx, session.SessionID)
		if err != nil || latest == nil || !m.now().Before(latest.AccessExpiry) {
			return nil, errRefreshBusy
		}
		return latest, nil
	}
	defer m.store.releaseRefreshLease(ctx, session.SessionID, owner)
	latest, err := m.store.loadSession(ctx, session.SessionID)
	if err != nil || latest == nil {
		return nil, errors.New("browser session expired")
	}
	if latest.AccessExpiry.Sub(m.now()) > m.config.RefreshSkew {
		return latest, nil
	}
	tokens, err := m.oidc.refresh(ctx, latest.RefreshToken)
	if err != nil {
		return nil, err
	}
	claims, err := m.verifier.Verify(ctx, tokens.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("verify refreshed access token: %w", err)
	}
	if claims.ExpiresAt == nil {
		return nil, errors.New("refreshed access token is missing expiration")
	}
	claims.AddPermissions(tokens.Scope)
	if m.config.Realm == "admin" && tokens.Scope == "" {
		claims.AddPermissions(latest.Permissions...)
	}
	if err := m.validateRealmClaims(claims); err != nil {
		return nil, fmt.Errorf("refreshed access token is unauthorized: %w", err)
	}
	if claims.Subject != latest.Subject || claims.OrgID != latest.OrgID || claims.App != latest.App ||
		claims.Environment != latest.Environment {
		return nil, errors.New("refreshed access token changed browser identity")
	}
	if tokens.IDToken != "" {
		identity, verifyErr := m.verifier.VerifyIDToken(ctx, tokens.IDToken, m.config.ClientID, "", tokens.AccessToken)
		if verifyErr != nil {
			return nil, fmt.Errorf("verify refreshed ID token: %w", verifyErr)
		}
		if identity.Subject != latest.Subject {
			return nil, errors.New("refreshed ID token changed browser identity")
		}
		latest.Email, latest.Name, latest.IDToken = identity.Email, identity.Name, tokens.IDToken
	}
	latest.AccessToken = tokens.AccessToken
	latest.AccessExpiry = claims.ExpiresAt.Time
	latest.Permissions = []string(claims.Permissions)
	if tokens.RefreshToken != "" {
		latest.RefreshToken = tokens.RefreshToken
	}
	replaced, err := m.store.replaceAfterRefresh(ctx, *latest, owner)
	if err != nil || !replaced {
		return nil, errors.New("browser session expired during refresh")
	}
	return latest, nil
}

func (m *Manager) validateRealmClaims(claims *auth.Claims) error {
	if m.config.Realm == "admin" && !claims.Has("billing:admin") {
		return errors.New("missing billing:admin permission")
	}
	return nil
}

func (m *Manager) originAllowed(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	return len(origins) == 1 && origins[0] == strings.TrimRight(m.config.AppOrigin, "/")
}

func (m *Manager) safeReturnTo(value string) string {
	if !validReturnPath(value) {
		return m.config.DefaultReturnPath
	}
	path := value
	if separator := strings.IndexAny(path, "?#"); separator >= 0 {
		path = path[:separator]
	}
	for _, prefix := range m.config.ReturnPathPrefixes {
		if pathMatchesPrefix(path, prefix) {
			return value
		}
	}
	return m.config.DefaultReturnPath
}

func (m *Manager) redirectLoginError(w http.ResponseWriter, r *http.Request) {
	writeRedirect(w, strings.TrimRight(m.config.AppOrigin, "/")+"/auth/error?reason=login_failed", http.StatusFound)
}

func secureCookie(name, value string, maxAge time.Duration) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: int(maxAge.Seconds()), Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}
}

func clearCookie(w http.ResponseWriter, name string) {
	cookie := secureCookie(name, "", 0)
	cookie.MaxAge = -1
	http.SetCookie(w, cookie)
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func equalSecret(left, right string) bool {
	return left != "" && right != "" && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func equalBytes(left, right []byte) bool {
	return len(left) == len(right) && len(left) > 0 && subtle.ConstantTimeCompare(left, right) == 1
}

func singleCookie(r *http.Request, name string) (*http.Cookie, error) {
	var found *http.Cookie
	for _, cookie := range r.Cookies() {
		if cookie.Name != name {
			continue
		}
		if found != nil {
			return nil, errors.New("ambiguous browser session cookie")
		}
		copy := *cookie
		found = &copy
	}
	if found == nil {
		return nil, http.ErrNoCookie
	}
	return found, nil
}

func newURL(origin, path string) string {
	return strings.TrimRight(origin, "/") + path
}

type attemptLimiter struct {
	mu      sync.Mutex
	entries map[string]attemptCount
	limit   int
	now     func() time.Time
}

type attemptCount struct {
	count int
	reset time.Time
}

func (l *attemptLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	entry := l.entries[key]
	if !now.Before(entry.reset) {
		entry = attemptCount{reset: now.Add(time.Minute)}
	}
	entry.count++
	l.entries[key] = entry
	if len(l.entries) > 4096 {
		for candidate, value := range l.entries {
			if !now.Before(value.reset) {
				delete(l.entries, candidate)
			}
		}
	}
	return entry.count <= l.limit
}

func (m *Manager) allowAuthAttempt(w http.ResponseWriter, r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	if m.attempts.allow(host) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	writeError(w, http.StatusTooManyRequests, "too many authentication attempts")
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	httpresponse.JSON(w, status, value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	httpresponse.Error(w, status, message)
}

func writeRedirect(w http.ResponseWriter, location string, status int) {
	httpresponse.Redirect(w, location, status)
}
