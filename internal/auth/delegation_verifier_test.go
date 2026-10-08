package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

type delegationFixture struct {
	key      *ecdsa.PrivateKey
	kid      string
	issuer   string
	audience string
	server   *httptest.Server
	registry DelegationClientRegistry
}

func newDelegationFixture(t *testing.T) *delegationFixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	fixture := &delegationFixture{key: key, kid: "delegation-test-key", audience: "https://api.billmesh.test"}
	mux := http.NewServeMux()
	fixture.server = httptest.NewServer(mux)
	fixture.issuer = fixture.server.URL + "/delegation"
	mux.HandleFunc("GET /discovery", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": fixture.issuer, "jwks_uri": fixture.server.URL + "/jwks",
			"delegated_access_signing_alg_values_supported": []string{"ES256"},
			"authorization_details_types_supported":         []string{delegationAuthorizationDetailsType},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{
			"kty": "EC", "crv": "P-256", "use": "sig", "alg": "ES256", "kid": fixture.kid,
			"x": base64.RawURLEncoding.EncodeToString(fixture.key.X.FillBytes(make([]byte, 32))),
			"y": base64.RawURLEncoding.EncodeToString(fixture.key.Y.FillBytes(make([]byte, 32))),
		}}})
	})
	fixture.registry, err = ParseDelegationClientRegistry(`[{"authorizer_client_id":"daybook-authorizer","actor_client_id":"daybook-billing-user","scope":"billmesh.billing","type":"billing","actor_type":"user","app":"daybook","environment":"test"}]`)
	require.NoError(t, err)
	return fixture
}

func (f *delegationFixture) close() { f.server.Close() }

func (f *delegationFixture) token(t *testing.T, mutate func(jwt.MapClaims)) string {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	claims := jwt.MapClaims{
		"iss": f.issuer, "aud": f.audience, "sub": "user-123", "iat": now.Unix(), "nbf": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(), "jti": "token-123", "client_id": "daybook-billing-user",
		"act": map[string]any{"sub": "daybook-billing-user"}, "scope": "billmesh.billing",
		"authorization_details": []any{map[string]any{
			"type": delegationAuthorizationDetailsType, "grant_id": "grant-123",
			"authorizer_client_id": "daybook-authorizer", "context_profile_version": 1,
			"context": map[string]any{"type": billmeshContextType, "org_id": "org-123"},
		}},
	}
	if mutate != nil {
		mutate(claims)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = f.kid
	token.Header["typ"] = "at+jwt"
	raw, err := token.SignedString(f.key)
	require.NoError(t, err)
	return raw
}

func (f *delegationFixture) verifier() *DelegationVerifier {
	return NewDelegationVerifier(f.issuer, f.audience, f.server.URL+"/discovery", f.registry, f.server.Client())
}

func TestDelegationVerifierMapsTrustedPairAndContext(t *testing.T) {
	fixture := newDelegationFixture(t)
	defer fixture.close()
	claims, err := fixture.verifier().Verify(context.Background(), fixture.token(t, nil))
	require.NoError(t, err)
	require.Equal(t, "daybook-authorizer", claims.AuthorizerClientID)
	require.Equal(t, "org-123", claims.OrgID)
	require.Equal(t, "user", claims.ActorType)
	require.Equal(t, ClientBilling, claims.ClientType)
}

func TestDelegationVerifierRejectsContractConfusion(t *testing.T) {
	fixture := newDelegationFixture(t)
	defer fixture.close()
	tests := map[string]func(jwt.MapClaims){
		"wrong authorizer": func(claims jwt.MapClaims) {
			details := claims["authorization_details"].([]any)
			details[0].(map[string]any)["authorizer_client_id"] = "taskmesh-authorizer"
		},
		"actor mismatch":  func(claims jwt.MapClaims) { claims["act"] = map[string]any{"sub": "another-client"} },
		"scope expansion": func(claims jwt.MapClaims) { claims["scope"] = "billmesh.billing billmesh.runtime" },
		"wrong context": func(claims jwt.MapClaims) {
			details := claims["authorization_details"].([]any)
			details[0].(map[string]any)["context"] = map[string]any{"type": "urn:other:context:v1", "org_id": "org-123"}
		},
		"unknown context field": func(claims jwt.MapClaims) {
			details := claims["authorization_details"].([]any)
			details[0].(map[string]any)["context"].(map[string]any)["tenant"] = "unregistered"
		},
		"unknown detail field": func(claims jwt.MapClaims) {
			details := claims["authorization_details"].([]any)
			details[0].(map[string]any)["application"] = "daybook"
		},
		"missing nbf":   func(claims jwt.MapClaims) { delete(claims, "nbf") },
		"long lifetime": func(claims jwt.MapClaims) { claims["exp"] = time.Now().Add(6 * time.Minute).Unix() },
		"multiple details": func(claims jwt.MapClaims) {
			detail := claims["authorization_details"].([]any)[0]
			claims["authorization_details"] = []any{detail, detail}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := fixture.verifier().Verify(context.Background(), fixture.token(t, mutate))
			require.Error(t, err)
		})
	}
}
