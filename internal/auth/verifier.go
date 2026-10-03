package auth

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/tociva/billmesh/internal/httpresponse"
)

type Claims struct {
	Permissions       jwt.ClaimStrings `json:"permissions"`
	Scope             jwt.ClaimStrings `json:"scope"`
	Scopes            jwt.ClaimStrings `json:"scp"`
	OrgID             string           `json:"org_id"`
	App               string           `json:"app"`
	Environment       string           `json:"environment"`
	BillingCustomerID string           `json:"billing_customer_id"`
	jwt.RegisteredClaims
}

type IDTokenClaims struct {
	Nonce  string `json:"nonce"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	AtHash string `json:"at_hash"`
	jwt.RegisteredClaims
}

func (c Claims) Has(permission string) bool {
	for _, p := range c.Permissions {
		if p == permission {
			return true
		}
	}
	return false
}

type TokenVerifier interface {
	Verify(context.Context, string) (*Claims, error)
}

type JWKSVerifier struct {
	issuer, audience string
	client           *http.Client
	requireContext   bool
	mu               sync.RWMutex
	refreshMu        sync.Mutex
	keys             map[string]*rsa.PublicKey
	jwksURL          string
	lastRefresh      time.Time
	lastAttempt      time.Time
	now              func() time.Time
}

const keyCacheLifetime = time.Minute
const keyCacheOutageGrace = 5 * time.Minute
const unknownKeyRefreshInterval = time.Second

func NewJWKSVerifier(issuer, audience string, client *http.Client) *JWKSVerifier {
	return newJWKSVerifier(issuer, audience, client, true)
}

// NewOIDCJWKSVerifier validates the standard JWT access-token contract used by
// interactive OIDC clients. Unlike service tokens, interactive Admin tokens do
// not have to carry Billmesh's tenant-specific org_id and app claims.
func NewOIDCJWKSVerifier(issuer, audience string, client *http.Client) *JWKSVerifier {
	return newJWKSVerifier(issuer, audience, client, false)
}

func newJWKSVerifier(issuer, audience string, client *http.Client, requireContext bool) *JWKSVerifier {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &JWKSVerifier{issuer: issuer, audience: audience, client: client, requireContext: requireContext, keys: map[string]*rsa.PublicKey{}, now: time.Now}
}

func (v *JWKSVerifier) Verify(ctx context.Context, raw string) (*Claims, error) {
	if raw == "" {
		return nil, errors.New("missing token")
	}
	claims := new(Claims)
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, fmt.Errorf("unexpected signing algorithm %q", t.Method.Alg())
		}
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing key id")
		}
		key, err := v.lookupKey(ctx, kid)
		if err != nil {
			return nil, err
		}
		if key == nil {
			return nil, errors.New("unknown signing key")
		}
		return key, nil
	}, jwt.WithIssuer(v.issuer), jwt.WithAudience(v.audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(v.now))
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid token: %w", err)
	}
	claims.AddPermissions()
	if claims.Subject == "" {
		return nil, errors.New("missing token subject")
	}
	if v.requireContext && (claims.OrgID == "" || claims.App == "") {
		return nil, errors.New("missing identity context")
	}
	return claims, nil
}

// AddPermissions normalizes the permissions, scp, and scope claim shapes used
// by IdNest/Hydra. Claims and OAuth token responses may encode scopes either as
// arrays or as space-delimited strings.
func (c *Claims) AddPermissions(scopes ...string) {
	seen := make(map[string]struct{})
	permissions := make(jwt.ClaimStrings, 0, len(c.Permissions)+len(c.Scope)+len(c.Scopes))
	add := func(values ...string) {
		for _, value := range values {
			for _, permission := range strings.Fields(value) {
				if _, exists := seen[permission]; exists {
					continue
				}
				seen[permission] = struct{}{}
				permissions = append(permissions, permission)
			}
		}
	}
	add(c.Permissions...)
	add(c.Scopes...)
	add(c.Scope...)
	add(scopes...)
	c.Permissions = permissions
}

// VerifyIDToken validates an OIDC ID token issued for a confidential browser
// client. Access tokens and ID tokens deliberately use separate validation
// contracts: access tokens are validated for their configured resource
// audience, while ID tokens prove the interactive user's identity and nonce.
func (v *JWKSVerifier) VerifyIDToken(ctx context.Context, raw, clientID, nonce, accessToken string) (*IDTokenClaims, error) {
	if raw == "" {
		return nil, errors.New("missing ID token")
	}
	claims := new(IDTokenClaims)
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, fmt.Errorf("unexpected signing algorithm %q", t.Method.Alg())
		}
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing key id")
		}
		key, err := v.lookupKey(ctx, kid)
		if err != nil {
			return nil, err
		}
		if key == nil {
			return nil, errors.New("unknown signing key")
		}
		return key, nil
	}, jwt.WithIssuer(v.issuer), jwt.WithAudience(clientID), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(v.now))
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid ID token: %w", err)
	}
	if claims.Subject == "" {
		return nil, errors.New("ID token subject is missing")
	}
	if nonce != "" && claims.Nonce != nonce {
		return nil, errors.New("ID token nonce mismatch")
	}
	if claims.AtHash != "" {
		digest := sha256.Sum256([]byte(accessToken))
		want := base64.RawURLEncoding.EncodeToString(digest[:len(digest)/2])
		if claims.AtHash != want {
			return nil, errors.New("ID token access-token hash mismatch")
		}
	}
	return claims, nil
}

func (v *JWKSVerifier) key(kid string) *rsa.PublicKey {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.keys[kid]
}

func (v *JWKSVerifier) lookupKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()

	now := v.now()
	key := v.key(kid)
	v.mu.RLock()
	lastRefresh, lastAttempt := v.lastRefresh, v.lastAttempt
	v.mu.RUnlock()
	if key != nil && now.Sub(lastRefresh) < keyCacheLifetime {
		return key, nil
	}
	if key == nil && !lastAttempt.IsZero() && now.Sub(lastAttempt) < unknownKeyRefreshInterval {
		return nil, errors.New("unknown signing key")
	}
	v.mu.Lock()
	v.lastAttempt = now
	v.mu.Unlock()
	if err := v.refresh(ctx); err != nil {
		if key != nil && now.Sub(lastRefresh) < keyCacheOutageGrace {
			return key, nil
		}
		return nil, err
	}
	return v.key(kid), nil
}

func (v *JWKSVerifier) refresh(ctx context.Context) error {
	jwksURL, err := v.resolveJWKSURL(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch JWKS: status %d", resp.StatusCode)
	}
	var set struct {
		Keys []struct{ Kty, Kid, N, E string } `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		e, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		ei := 0
		for _, b := range e {
			ei = ei<<8 + int(b)
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: ei}
	}
	v.mu.Lock()
	v.keys = keys
	v.lastRefresh = v.now()
	v.mu.Unlock()
	return nil
}

func (v *JWKSVerifier) resolveJWKSURL(ctx context.Context) (string, error) {
	if v.jwksURL != "" {
		return v.jwksURL, nil
	}
	discoveryURL := strings.TrimRight(v.issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return "", fmt.Errorf("create OIDC discovery request: %w", err)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("discover OIDC provider: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discover OIDC provider: status %d", resp.StatusCode)
	}
	var metadata struct {
		Issuer  string `json:"issuer"`
		JWKSURL string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return "", fmt.Errorf("decode OIDC discovery: %w", err)
	}
	if strings.TrimRight(metadata.Issuer, "/") != strings.TrimRight(v.issuer, "/") {
		return "", errors.New("OIDC discovery issuer mismatch")
	}
	parsed, err := url.Parse(metadata.JWKSURL)
	if err != nil || !parsed.IsAbs() || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return "", errors.New("OIDC discovery contains an invalid jwks_uri")
	}
	v.jwksURL = metadata.JWKSURL
	return v.jwksURL, nil
}

type contextKey struct{}

func Middleware(verifier TokenVerifier) func(http.Handler) http.Handler {
	return MiddlewareWithHooks(verifier, MiddlewareHooks{})
}

type MiddlewareHooks struct {
	Rejected      func(*http.Request) bool
	Authenticated func(http.ResponseWriter, *http.Request, *Claims) bool
}

func MiddlewareWithHooks(verifier TokenVerifier, hooks MiddlewareHooks) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reject := func(message string) {
				if hooks.Rejected != nil && hooks.Rejected(r) {
					w.Header().Set("Retry-After", "60")
					httpresponse.Error(w, http.StatusTooManyRequests, "too many authentication failures")
					return
				}
				httpresponse.Error(w, http.StatusUnauthorized, message)
			}
			if len(r.Header.Values("Authorization")) != 1 {
				reject("missing or ambiguous bearer token")
				return
			}
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				reject("missing bearer token")
				return
			}
			claims, err := verifier.Verify(r.Context(), strings.TrimPrefix(header, "Bearer "))
			if err != nil {
				reject("invalid bearer token")
				return
			}
			r = r.WithContext(WithClaims(r.Context(), claims))
			if hooks.Authenticated != nil && !hooks.Authenticated(w, r, claims) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, contextKey{}, claims)
}

func FromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(contextKey{}).(*Claims)
	return c, ok
}

func Require(permission string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := FromContext(r.Context())
		if !ok || !claims.Has(permission) {
			httpresponse.Error(w, http.StatusForbidden, "forbidden")
			return
		}
		next(w, r)
	}
}
