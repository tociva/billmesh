package bff

import (
	"context"
	"crypto/subtle"
	"encoding/json"
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
	appOrigin, err := parseSecureURL(cfg.AppOrigin, "BFF_APP_ORIGIN", cfg.AllowInsecureHTTP)
	if err != nil {
		return err
	}
	if appOrigin.Path != "" && appOrigin.Path != "/" || appOrigin.RawQuery != "" || appOrigin.Fragment != "" {
		return errors.New("BFF_APP_ORIGIN must be an origin without a path")
	}
	redirect, err := parseSecureURL(cfg.RedirectURI, "BFF_REDIRECT_URI", cfg.AllowInsecureHTTP)
	if err != nil {
		return err
	}
	logout, err := parseSecureURL(cfg.PostLogoutRedirectURI, "BFF_POST_LOGOUT_REDIRECT_URI", cfg.AllowInsecureHTTP)
	if err != nil {
		return err
	}
	if redirect.Scheme != logout.Scheme || redirect.Host != logout.Host {
		return errors.New("BFF callback and post-logout callback must use the same origin")
	}
	if redirect.Scheme != appOrigin.Scheme || redirect.Host != appOrigin.Host {
		return errors.New("BFF callbacks and application must use the same origin")
	}
	if _, err = parseSecureURL(cfg.Issuer, "BFF_ISSUER", cfg.AllowInsecureHTTP); err != nil {
		return err
	}
	if cfg.StandaloneLogoutURI != "" {
		if _, err = parseSecureURL(cfg.StandaloneLogoutURI, "BFF_STANDALONE_LOGOUT_URI", cfg.AllowInsecureHTTP); err != nil {
			return err
		}
	}
	if len(cfg.ReturnPathPrefixes) == 0 {
		return errors.New("BFF_RETURN_PATH_PREFIXES must contain at least one path")
	}
	for _, prefix := range cfg.ReturnPathPrefixes {
		if !strings.HasPrefix(prefix, "/") || strings.HasPrefix(prefix, "//") || strings.Contains(prefix, "\\") {
			return fmt.Errorf("invalid BFF return path prefix %q", prefix)
		}
	}
	return nil
}

func parseSecureURL(value, name string, allowInsecure bool) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("%s must be an absolute URL", name)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("%s must use HTTP or HTTPS", name)
	}
	if parsed.Scheme == "http" && !allowInsecure && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" {
		return nil, fmt.Errorf("%s must use HTTPS outside localhost", name)
	}
	return parsed, nil
}

func (m *Manager) AuthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/login", m.login)
	mux.HandleFunc("GET /auth/callback", m.callback)
	mux.HandleFunc("GET /auth/session", m.session)
	mux.HandleFunc("POST /auth/logout", m.logout)
	mux.HandleFunc("GET /auth/logout/continue", m.continueLogout)
	mux.HandleFunc("GET /auth/logout/callback", m.logoutCallback)
	return mux
}

func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "authorization header is not accepted on browser routes", http.StatusBadRequest)
			return
		}
		session, claims, status, err := m.authenticate(r)
		if err != nil {
			http.Error(w, http.StatusText(status), status)
			return
		}
		csrfValues := r.Header.Values(csrfHeaderName)
		if !safeMethod(r.Method) && (len(csrfValues) != 1 || !equalSecret(session.CSRFToken, csrfValues[0])) {
			http.Error(w, "CSRF validation failed", http.StatusForbidden)
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
		http.Error(w, "login unavailable", http.StatusServiceUnavailable)
		return
	}
	nonce, err := randomIdentifier()
	if err != nil {
		http.Error(w, "login unavailable", http.StatusServiceUnavailable)
		return
	}
	verifier, err := randomIdentifier()
	if err != nil {
		http.Error(w, "login unavailable", http.StatusServiceUnavailable)
		return
	}
	correlation, err := randomIdentifier()
	if err != nil {
		http.Error(w, "login unavailable", http.StatusServiceUnavailable)
		return
	}
	transaction := loginTransaction{
		SchemaVersion: schemaVersion, State: state, Nonce: nonce, CodeVerifier: verifier,
		CorrelationHash: digest(correlation), ReturnTo: m.safeReturnTo(r.URL.Query().Get("returnTo")), CreatedAt: m.now(),
	}
	redirect, err := m.oidc.authorizationURL(r.Context(), transaction)
	if err == nil {
		err = m.store.saveLogin(r.Context(), transaction)
	}
	if err != nil {
		m.log.Error("BFF login start failed", "error", err)
		http.Error(w, "login unavailable", http.StatusServiceUnavailable)
		return
	}
	http.SetCookie(w, secureCookie(loginCookieName, correlation, m.config.LoginTTL))
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (m *Manager) callback(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if !m.allowAuthAttempt(w, r) {
		return
	}
	clearCookie(w, loginCookieName)
	state := r.URL.Query().Get("state")
	if state == "" {
		http.Error(w, "invalid login callback", http.StatusUnauthorized)
		return
	}
	transaction, err := m.store.takeLogin(r.Context(), state)
	correlation, cookieErr := r.Cookie(loginCookieName)
	if err != nil || cookieErr != nil || transaction == nil || transaction.SchemaVersion != schemaVersion ||
		!equalBytes(transaction.CorrelationHash, digest(correlation.Value)) {
		http.Error(w, "invalid login callback", http.StatusUnauthorized)
		return
	}
	if providerError := r.URL.Query().Get("error"); providerError != "" {
		m.redirectLoginError(w, r)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "invalid login callback", http.StatusUnauthorized)
		return
	}
	tokens, err := m.oidc.exchange(r.Context(), code, transaction.CodeVerifier)
	if err != nil {
		m.log.Error("BFF authorization-code exchange failed", "error", err)
		m.redirectLoginError(w, r)
		return
	}
	claims, err := m.verifier.Verify(r.Context(), tokens.AccessToken)
	if err != nil || claims.ExpiresAt == nil {
		m.log.Warn("BFF received an invalid access token")
		m.redirectLoginError(w, r)
		return
	}
	identity, err := m.verifier.VerifyIDToken(r.Context(), tokens.IDToken, m.config.ClientID, transaction.Nonce, tokens.AccessToken)
	if err != nil || identity.Subject != claims.Subject {
		m.log.Warn("BFF received an invalid ID token")
		m.redirectLoginError(w, r)
		return
	}
	sessionID, err := randomIdentifier()
	if err != nil {
		http.Error(w, "login unavailable", http.StatusServiceUnavailable)
		return
	}
	csrf, err := randomIdentifier()
	if err != nil {
		http.Error(w, "login unavailable", http.StatusServiceUnavailable)
		return
	}
	session := browserSession{
		SchemaVersion: schemaVersion, SessionID: sessionID, Subject: claims.Subject,
		Email: identity.Email, Name: identity.Name, OrgID: claims.OrgID, App: claims.App,
		Environment: claims.Environment, Permissions: claims.Permissions, CSRFToken: csrf,
		AllowedOrigin: strings.TrimRight(m.config.AppOrigin, "/"), CreatedAt: m.now(),
		AbsoluteExpiry: m.now().Add(m.config.SessionAbsoluteTTL), AccessToken: tokens.AccessToken,
		AccessExpiry: claims.ExpiresAt.Time, RefreshToken: tokens.RefreshToken, IDToken: tokens.IDToken,
	}
	if err := m.store.createSession(r.Context(), session); err != nil {
		m.log.Error("BFF session creation failed", "error", err)
		http.Error(w, "login unavailable", http.StatusServiceUnavailable)
		return
	}
	http.SetCookie(w, secureCookie(sessionCookieName, sessionID, m.config.SessionAbsoluteTTL))
	http.Redirect(w, r, newURL(m.config.AppOrigin, transaction.ReturnTo), http.StatusFound)
}

func (m *Manager) session(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	session, _, status, err := m.authenticate(r)
	if err != nil {
		http.Error(w, http.StatusText(status), status)
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
		clearCookie(w, sessionCookieName)
		http.Error(w, http.StatusText(status), status)
		return
	}
	csrfValues := r.Header.Values(csrfHeaderName)
	if len(csrfValues) != 1 || !equalSecret(session.CSRFToken, csrfValues[0]) {
		http.Error(w, "CSRF validation failed", http.StatusForbidden)
		return
	}
	if session.RefreshToken != "" {
		if err := m.oidc.revoke(r.Context(), session.RefreshToken); err != nil {
			m.log.Warn("BFF refresh-token revocation failed", "error", err)
		}
	}
	_ = m.store.deleteSession(r.Context(), session.SessionID)
	ticket, err := m.store.saveLogout(r.Context(), logoutTransaction{
		SchemaVersion: schemaVersion, IDToken: session.IDToken, CreatedAt: m.now(),
	})
	if err != nil {
		http.Error(w, "logout unavailable", http.StatusServiceUnavailable)
		return
	}
	clearCookie(w, sessionCookieName)
	writeJSON(w, http.StatusOK, map[string]string{"logoutUrl": "/auth/logout/continue?ticket=" + url.QueryEscape(ticket)})
}

func (m *Manager) continueLogout(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	transaction, err := m.store.takeLogout(r.Context(), r.URL.Query().Get("ticket"))
	if err != nil || transaction == nil || transaction.SchemaVersion != schemaVersion {
		http.Error(w, "invalid logout transaction", http.StatusUnauthorized)
		return
	}
	redirect, err := m.oidc.providerLogoutURL(r.Context(), transaction.IDToken)
	if err != nil {
		http.Error(w, "logout unavailable", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (m *Manager) logoutCallback(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	http.Redirect(w, r, strings.TrimRight(m.config.AppOrigin, "/")+"/", http.StatusFound)
}

func (m *Manager) authenticate(r *http.Request) (*browserSession, *auth.Claims, int, error) {
	if !m.originAllowed(r) {
		return nil, nil, http.StatusForbidden, errors.New("origin is not allowed")
	}
	cookie, err := singleCookie(r, sessionCookieName)
	if err != nil {
		return nil, nil, http.StatusUnauthorized, err
	}
	session, err := m.store.loadSession(r.Context(), cookie.Value)
	if err != nil {
		m.log.Error("BFF session load failed", "error", err)
		return nil, nil, http.StatusServiceUnavailable, err
	}
	if session == nil || session.SchemaVersion != schemaVersion || session.SessionID != cookie.Value ||
		session.AllowedOrigin != strings.TrimRight(m.config.AppOrigin, "/") || !m.now().Before(session.AbsoluteExpiry) {
		return nil, nil, http.StatusUnauthorized, errors.New("invalid browser session")
	}
	session, err = m.refreshIfNeeded(r.Context(), *session)
	if err != nil {
		if errors.Is(err, errRefreshBusy) {
			return nil, nil, http.StatusServiceUnavailable, err
		}
		_ = m.store.deleteSession(r.Context(), cookie.Value)
		return nil, nil, http.StatusUnauthorized, err
	}
	claims, err := m.verifier.Verify(r.Context(), session.AccessToken)
	if err != nil || claims.Subject != session.Subject || claims.OrgID != session.OrgID ||
		claims.App != session.App || claims.Environment != session.Environment {
		_ = m.store.deleteSession(r.Context(), cookie.Value)
		return nil, nil, http.StatusUnauthorized, errors.New("browser access token no longer matches session")
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
	if err != nil || claims.ExpiresAt == nil || claims.Subject != latest.Subject || claims.OrgID != latest.OrgID ||
		claims.App != latest.App || claims.Environment != latest.Environment {
		return nil, errors.New("refreshed access token changed browser identity")
	}
	if tokens.IDToken != "" {
		identity, verifyErr := m.verifier.VerifyIDToken(ctx, tokens.IDToken, m.config.ClientID, "", tokens.AccessToken)
		if verifyErr != nil || identity.Subject != latest.Subject {
			return nil, errors.New("invalid refreshed ID token")
		}
		latest.Email, latest.Name, latest.IDToken = identity.Email, identity.Name, tokens.IDToken
	}
	latest.AccessToken = tokens.AccessToken
	latest.AccessExpiry = claims.ExpiresAt.Time
	latest.Permissions = claims.Permissions
	if tokens.RefreshToken != "" {
		latest.RefreshToken = tokens.RefreshToken
	}
	replaced, err := m.store.replaceAfterRefresh(ctx, *latest, owner)
	if err != nil || !replaced {
		return nil, errors.New("browser session expired during refresh")
	}
	return latest, nil
}

func (m *Manager) originAllowed(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	if len(origins) == 1 {
		return origins[0] == strings.TrimRight(m.config.AppOrigin, "/")
	}
	return len(origins) == 0 && safeMethod(r.Method) && r.Header.Get("Sec-Fetch-Site") != "cross-site"
}

func (m *Manager) safeReturnTo(value string) string {
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") ||
		strings.Contains(value, "\\") || strings.HasPrefix(value, "/auth/") {
		return "/"
	}
	path := strings.SplitN(value, "?", 2)[0]
	for _, prefix := range m.config.ReturnPathPrefixes {
		if prefix == "/" || path == prefix || strings.HasPrefix(path, strings.TrimRight(prefix, "/")+"/") {
			return value
		}
	}
	return "/"
}

func (m *Manager) redirectLoginError(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, strings.TrimRight(m.config.AppOrigin, "/")+"/auth/error?reason=login_failed", http.StatusFound)
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
	http.Error(w, "too many authentication attempts", http.StatusTooManyRequests)
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
