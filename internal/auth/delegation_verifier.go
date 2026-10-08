package auth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	delegationAuthorizationDetailsType = "urn:idnest:delegation"
	billmeshContextType                = "urn:billmesh:context:v1"
	maxDelegatedTokenLifetime          = 5 * time.Minute
	maxDelegatedTokenSize              = 16 << 10
	maxDelegationMetadataSize          = 1 << 20
	maxAuthorizationContextValueSize   = 512
)

type BillmeshAuthorizationContext struct {
	Type              string `json:"type"`
	OrgID             string `json:"org_id,omitempty"`
	BillingCustomerID string `json:"billing_customer_id,omitempty"`
	Application       string `json:"application,omitempty"`
	Environment       string `json:"environment,omitempty"`
}

func (c *BillmeshAuthorizationContext) UnmarshalJSON(data []byte) error {
	type contextFields BillmeshAuthorizationContext
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded contextFields
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("invalid Billmesh authorization context: %w", err)
	}
	*c = BillmeshAuthorizationContext(decoded)
	return nil
}

type DelegationAuthorizationDetail struct {
	Type                  string                        `json:"type"`
	GrantID               string                        `json:"grant_id"`
	AuthorizerClientID    string                        `json:"authorizer_client_id"`
	ContextProfileVersion int                           `json:"context_profile_version"`
	Context               *BillmeshAuthorizationContext `json:"context,omitempty"`
}

func (d *DelegationAuthorizationDetail) UnmarshalJSON(data []byte) error {
	type detailFields DelegationAuthorizationDetail
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded detailFields
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("invalid delegation authorization detail: %w", err)
	}
	*d = DelegationAuthorizationDetail(decoded)
	return nil
}

type delegatedAccessClaims struct {
	ClientID string `json:"client_id"`
	Actor    struct {
		Subject string `json:"sub"`
	} `json:"act"`
	Scope                string                          `json:"scope"`
	AuthorizationDetails []DelegationAuthorizationDetail `json:"authorization_details"`
	jwt.RegisteredClaims
}

// DelegationVerifier validates only IdNest ES256 delegated access tokens.
// Browser OIDC access and ID tokens deliberately continue through JWKSVerifier.
type DelegationVerifier struct {
	issuer, audience, discoveryURL string
	clients                        DelegationClientRegistry
	client                         *http.Client
	mu                             sync.RWMutex
	refreshMu                      sync.Mutex
	keys                           map[string]*ecdsa.PublicKey
	jwksURL                        string
	lastRefresh                    time.Time
	lastAttempt                    time.Time
	now                            func() time.Time
}

func NewDelegationVerifier(issuer, audience, discoveryURL string, clients DelegationClientRegistry, client *http.Client) *DelegationVerifier {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &DelegationVerifier{
		issuer: issuer, audience: audience, discoveryURL: discoveryURL, clients: clients,
		client: client, keys: map[string]*ecdsa.PublicKey{}, now: time.Now,
	}
}

func (v *DelegationVerifier) Verify(ctx context.Context, raw string) (*Claims, error) {
	if raw == "" {
		return nil, errors.New("missing token")
	}
	if len(raw) > maxDelegatedTokenSize {
		return nil, errors.New("delegated token exceeds maximum size")
	}
	delegated := new(delegatedAccessClaims)
	token, err := jwt.ParseWithClaims(raw, delegated, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodES256.Alg() {
			return nil, fmt.Errorf("unexpected signing algorithm %q", token.Method.Alg())
		}
		if typ, _ := token.Header["typ"].(string); typ != "at+jwt" {
			return nil, errors.New("delegated token typ must be at+jwt")
		}
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing key id")
		}
		return v.lookupKey(ctx, kid)
	}, jwt.WithIssuer(v.issuer), jwt.WithAudience(v.audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(v.now))
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid delegated token: %w", err)
	}
	if delegated.Subject == "" || delegated.IssuedAt == nil || delegated.NotBefore == nil || delegated.ExpiresAt == nil || delegated.ID == "" {
		return nil, errors.New("delegated token is missing required registered claims")
	}
	if len(delegated.Audience) != 1 || delegated.Audience[0] != v.audience {
		return nil, errors.New("delegated token must contain exactly the Billmesh audience")
	}
	if !delegated.ExpiresAt.Time.After(delegated.IssuedAt.Time) {
		return nil, errors.New("delegated token expiry must follow its issued-at time")
	}
	if delegated.ExpiresAt.Time.Sub(delegated.IssuedAt.Time) > maxDelegatedTokenLifetime {
		return nil, errors.New("delegated token lifetime exceeds five minutes")
	}
	if delegated.ClientID == "" || delegated.ClientID != delegated.Actor.Subject {
		return nil, errors.New("delegated token actor does not match client_id")
	}
	if len(delegated.AuthorizationDetails) != 1 {
		return nil, errors.New("delegated token must contain exactly one authorization detail")
	}
	detail := delegated.AuthorizationDetails[0]
	if detail.Type != delegationAuthorizationDetailsType || detail.GrantID == "" || detail.AuthorizerClientID == "" || detail.ContextProfileVersion != 1 {
		return nil, errors.New("delegated token authorization detail is invalid")
	}
	if strings.TrimSpace(detail.GrantID) != detail.GrantID || strings.TrimSpace(detail.AuthorizerClientID) != detail.AuthorizerClientID {
		return nil, errors.New("delegated token authorization detail identifiers must be canonical")
	}
	if strings.TrimSpace(delegated.Subject) != delegated.Subject || strings.TrimSpace(delegated.ClientID) != delegated.ClientID || strings.TrimSpace(delegated.ID) != delegated.ID {
		return nil, errors.New("delegated token identifiers must be canonical")
	}
	scopes := strings.Fields(delegated.Scope)
	if len(scopes) != 1 || scopes[0] != delegated.Scope {
		return nil, errors.New("delegated token must contain exactly one canonical scope")
	}
	claims := &Claims{
		ClientID: delegated.ClientID, AuthorizerClientID: detail.AuthorizerClientID,
		GrantID: detail.GrantID, Scope: delegated.Scope, RegisteredClaims: delegated.RegisteredClaims,
	}
	if detail.Context != nil {
		if detail.Context.Type != billmeshContextType {
			return nil, errors.New("delegated token context type is invalid")
		}
		contextValues := []string{detail.Context.OrgID, detail.Context.BillingCustomerID, detail.Context.Application, detail.Context.Environment}
		for _, value := range contextValues {
			if strings.TrimSpace(value) != value || len(value) > maxAuthorizationContextValueSize {
				return nil, errors.New("delegated token context contains a non-canonical or oversized value")
			}
		}
		claims.OrgID = detail.Context.OrgID
		claims.BillingCustomerID = detail.Context.BillingCustomerID
		claims.App = detail.Context.Application
		claims.Environment = detail.Context.Environment
	}
	if err := v.clients.authenticate(claims, delegated.Scope); err != nil {
		return nil, err
	}
	return claims, nil
}

func (v *DelegationVerifier) lookupKey(ctx context.Context, kid string) (*ecdsa.PublicKey, error) {
	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()
	now := v.now()
	v.mu.RLock()
	key, lastRefresh, lastAttempt := v.keys[kid], v.lastRefresh, v.lastAttempt
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
	v.mu.RLock()
	defer v.mu.RUnlock()
	key = v.keys[kid]
	if key == nil {
		return nil, errors.New("unknown signing key")
	}
	return key, nil
}

func (v *DelegationVerifier) refresh(ctx context.Context) error {
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
		return fmt.Errorf("fetch delegation JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch delegation JWKS: status %d", resp.StatusCode)
	}
	var set struct {
		Keys []struct{ Kty, Kid, Crv, X, Y, Use, Alg string } `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDelegationMetadataSize)).Decode(&set); err != nil {
		return err
	}
	keys := map[string]*ecdsa.PublicKey{}
	for _, item := range set.Keys {
		if item.Kty != "EC" || item.Crv != "P-256" || item.Kid == "" || item.Alg != "ES256" || (item.Use != "" && item.Use != "sig") {
			continue
		}
		if strings.TrimSpace(item.Kid) != item.Kid {
			continue
		}
		x, xErr := base64.RawURLEncoding.DecodeString(item.X)
		y, yErr := base64.RawURLEncoding.DecodeString(item.Y)
		if xErr != nil || yErr != nil || len(x) != 32 || len(y) != 32 {
			continue
		}
		point := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if !point.Curve.IsOnCurve(point.X, point.Y) {
			continue
		}
		if _, duplicate := keys[item.Kid]; duplicate {
			return errors.New("delegation JWKS contains a duplicate key id")
		}
		keys[item.Kid] = point
	}
	if len(keys) == 0 {
		return errors.New("delegation JWKS contains no trusted ES256 keys")
	}
	v.mu.Lock()
	v.keys, v.lastRefresh = keys, v.now()
	v.mu.Unlock()
	return nil
}

func (v *DelegationVerifier) resolveJWKSURL(ctx context.Context) (string, error) {
	v.mu.RLock()
	resolved := v.jwksURL
	v.mu.RUnlock()
	if resolved != "" {
		return resolved, nil
	}
	if v.discoveryURL == "" {
		return "", errors.New("delegation discovery URL is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.discoveryURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("discover delegation issuer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discover delegation issuer: status %d", resp.StatusCode)
	}
	var metadata struct {
		Issuer                    string   `json:"issuer"`
		JWKSURL                   string   `json:"jwks_uri"`
		SigningAlgorithms         []string `json:"delegated_access_signing_alg_values_supported"`
		AuthorizationDetailsTypes []string `json:"authorization_details_types_supported"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDelegationMetadataSize)).Decode(&metadata); err != nil {
		return "", fmt.Errorf("decode delegation discovery: %w", err)
	}
	if metadata.Issuer != v.issuer {
		return "", errors.New("delegation discovery issuer mismatch")
	}
	if !contains(metadata.SigningAlgorithms, "ES256") || !contains(metadata.AuthorizationDetailsTypes, delegationAuthorizationDetailsType) {
		return "", errors.New("delegation discovery does not advertise the required token contract")
	}
	parsed, err := url.Parse(metadata.JWKSURL)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", errors.New("delegation discovery contains an invalid jwks_uri")
	}
	discovery, err := url.Parse(v.discoveryURL)
	if err != nil || !discovery.IsAbs() || !strings.EqualFold(parsed.Scheme, discovery.Scheme) || !strings.EqualFold(parsed.Host, discovery.Host) {
		return "", errors.New("delegation discovery jwks_uri must use the discovery origin")
	}
	v.mu.Lock()
	v.jwksURL = metadata.JWKSURL
	v.mu.Unlock()
	return metadata.JWKSURL, nil
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
