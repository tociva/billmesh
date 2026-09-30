package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

type jwksFixture struct {
	key    *rsa.PrivateKey
	kid    string
	issuer string
	close  func()
}

func newOIDCTestServer(handler http.HandlerFunc) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": srv.URL, "jwks_uri": srv.URL + "/jwks"})
			return
		}
		if r.URL.Path == "/jwks" {
			handler(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	return srv
}

func newJWKSFixture(t *testing.T) *jwksFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	kid := "test-key"
	srv := newOIDCTestServer(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": kid, "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	})
	return &jwksFixture{key: key, kid: kid, issuer: srv.URL, close: srv.Close}
}

func (f *jwksFixture) token(t *testing.T, mutate func(*Claims)) string {
	t.Helper()
	claims := Claims{Permissions: []string{"billing:read"}, OrgID: "org-1", App: "daybook", TokenUse: "access", RegisteredClaims: jwt.RegisteredClaims{Issuer: f.issuer, Audience: jwt.ClaimStrings{"billmesh-test"}, Subject: "user-1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}}
	if mutate != nil {
		mutate(&claims)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = f.kid
	raw, err := token.SignedString(f.key)
	require.NoError(t, err)
	return raw
}

func TestJWKSVerifier(t *testing.T) {
	fixture := newJWKSFixture(t)
	defer fixture.close()

	got, err := NewJWKSVerifier(fixture.issuer, "billmesh-test", nil).Verify(context.Background(), fixture.token(t, nil))
	require.NoError(t, err)
	require.Equal(t, "org-1", got.OrgID)
	require.True(t, got.Has("billing:read"))
}

func TestJWKSVerifierValidatesOIDCIDTokenNonceAndAccessTokenHash(t *testing.T) {
	fixture := newJWKSFixture(t)
	defer fixture.close()
	accessToken := "access-token"
	digest := sha256.Sum256([]byte(accessToken))
	claims := IDTokenClaims{
		Nonce: "nonce", Email: "operator@example.com", Name: "Operator",
		AtHash: base64.RawURLEncoding.EncodeToString(digest[:len(digest)/2]),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: fixture.issuer, Audience: jwt.ClaimStrings{"billmesh-web"}, Subject: "user-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)), IssuedAt: jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = fixture.kid
	raw, err := token.SignedString(fixture.key)
	require.NoError(t, err)
	verifier := NewJWKSVerifier(fixture.issuer, "billmesh-test", nil)
	got, err := verifier.VerifyIDToken(context.Background(), raw, "billmesh-web", "nonce", accessToken)
	require.NoError(t, err)
	require.Equal(t, "operator@example.com", got.Email)

	_, err = verifier.VerifyIDToken(context.Background(), raw, "billmesh-web", "wrong", accessToken)
	require.ErrorContains(t, err, "nonce")
	_, err = verifier.VerifyIDToken(context.Background(), raw, "billmesh-web", "nonce", "tampered")
	require.ErrorContains(t, err, "hash")
	_, err = verifier.VerifyIDToken(context.Background(), raw, "another-client", "nonce", accessToken)
	require.Error(t, err)
}

func TestJWKSVerifierRejectsWrongAudience(t *testing.T) {
	fixture := newJWKSFixture(t)
	defer fixture.close()

	raw := fixture.token(t, func(claims *Claims) {
		claims.Audience = jwt.ClaimStrings{"wrong-audience"}
	})
	_, err := NewJWKSVerifier(fixture.issuer, "billmesh-test", nil).Verify(context.Background(), raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid token")
}

func TestJWKSVerifierRejectsMalformedToken(t *testing.T) {
	fixture := newJWKSFixture(t)
	defer fixture.close()

	_, err := NewJWKSVerifier(fixture.issuer, "billmesh-test", nil).Verify(context.Background(), "not-a-token")
	require.Error(t, err)
}

func TestJWKSVerifierRejectsUnsupportedAlgorithms(t *testing.T) {
	fixture := newJWKSFixture(t)
	defer fixture.close()
	verifier := NewJWKSVerifier(fixture.issuer, "billmesh-test", nil)

	t.Run("none", func(t *testing.T) {
		claims := Claims{Permissions: []string{"billing:read"}, OrgID: "org-1", TokenUse: "access", RegisteredClaims: jwt.RegisteredClaims{Issuer: fixture.issuer, Audience: jwt.ClaimStrings{"billmesh-test"}, Subject: "user-1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}}
		token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
		token.Header["kid"] = fixture.kid
		raw, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
		require.NoError(t, err)

		_, err = verifier.Verify(context.Background(), raw)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unexpected signing algorithm")
	})

	t.Run("hs256 substitution", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": fixture.issuer, "aud": "billmesh-test", "sub": "user-1", "exp": time.Now().Add(time.Minute).Unix(), "token_use": "access"})
		token.Header["kid"] = fixture.kid
		raw, err := token.SignedString([]byte("shared-secret"))
		require.NoError(t, err)

		_, err = verifier.Verify(context.Background(), raw)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unexpected signing algorithm")
	})
}

func TestJWKSVerifierRejectsTamperedPayloadWithKnownKeyID(t *testing.T) {
	fixture := newJWKSFixture(t)
	defer fixture.close()
	verifier := NewJWKSVerifier(fixture.issuer, "billmesh-test", nil)

	raw := fixture.token(t, nil)
	_, err := verifier.Verify(context.Background(), raw)
	require.NoError(t, err)

	parts := strings.Split(raw, ".")
	require.Len(t, parts, 3)
	tamperedClaims := jwt.MapClaims{"iss": fixture.issuer, "aud": "billmesh-test", "sub": "user-1", "exp": time.Now().Add(time.Minute).Unix(), "org_id": "org-1", "permissions": []string{"billing:admin"}, "token_use": "access"}
	tamperedPayload, err := json.Marshal(tamperedClaims)
	require.NoError(t, err)
	parts[1] = base64.RawURLEncoding.EncodeToString(tamperedPayload)

	_, err = verifier.Verify(context.Background(), strings.Join(parts, "."))
	require.Error(t, err)
}

func TestJWKSVerifierRequiresClaimsAndAccessTokenUse(t *testing.T) {
	fixture := newJWKSFixture(t)
	defer fixture.close()
	verifier := NewJWKSVerifier(fixture.issuer, "billmesh-test", nil)

	tests := []struct {
		name   string
		mutate func(*Claims)
	}{
		{name: "missing expiry", mutate: func(claims *Claims) { claims.ExpiresAt = nil }},
		{name: "missing issuer", mutate: func(claims *Claims) { claims.Issuer = "" }},
		{name: "missing audience", mutate: func(claims *Claims) { claims.Audience = nil }},
		{name: "missing token use", mutate: func(claims *Claims) { claims.TokenUse = "" }},
		{name: "id token", mutate: func(claims *Claims) { claims.TokenUse = "id" }},
		{name: "missing organization", mutate: func(claims *Claims) { claims.OrgID = "" }},
		{name: "missing application", mutate: func(claims *Claims) { claims.App = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := verifier.Verify(context.Background(), fixture.token(t, tt.mutate))
			require.Error(t, err)
		})
	}
}

func TestJWKSVerifierRejectsJWKSFailures(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, Claims{TokenUse: "access", RegisteredClaims: jwt.RegisteredClaims{Issuer: "issuer", Audience: jwt.ClaimStrings{"billmesh-test"}, Subject: "user-1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}})
	token.Header["kid"] = "missing-key"
	raw, err := token.SignedString(key)
	require.NoError(t, err)

	t.Run("unavailable endpoint", func(t *testing.T) {
		srv := newOIDCTestServer(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		})
		defer srv.Close()
		_, err := NewJWKSVerifier(srv.URL, "billmesh-test", srv.Client()).Verify(context.Background(), raw)
		require.Error(t, err)
	})

	t.Run("malformed keyset", func(t *testing.T) {
		srv := newOIDCTestServer(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{`))
		})
		defer srv.Close()
		_, err := NewJWKSVerifier(srv.URL, "billmesh-test", srv.Client()).Verify(context.Background(), raw)
		require.Error(t, err)
	})

	t.Run("empty keyset", func(t *testing.T) {
		srv := newOIDCTestServer(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{}})
		})
		defer srv.Close()
		_, err := NewJWKSVerifier(srv.URL, "billmesh-test", srv.Client()).Verify(context.Background(), raw)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown signing key")
	})
}

func TestJWKSVerifierRejectsInvalidOIDCDiscovery(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, Claims{TokenUse: "access", RegisteredClaims: jwt.RegisteredClaims{Audience: jwt.ClaimStrings{"billmesh-test"}, Subject: "user-1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}})
	token.Header["kid"] = "test-key"
	raw, err := token.SignedString(key)
	require.NoError(t, err)

	for _, tc := range []struct {
		name     string
		metadata any
		want     string
	}{
		{name: "issuer mismatch", metadata: map[string]string{"issuer": "https://wrong.example", "jwks_uri": "https://issuer.example/jwks"}, want: "issuer mismatch"},
		{name: "missing jwks uri", metadata: map[string]string{"issuer": "ISSUER"}, want: "invalid jwks_uri"},
		{name: "relative jwks uri", metadata: map[string]string{"issuer": "ISSUER", "jwks_uri": "/jwks"}, want: "invalid jwks_uri"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				encoded, _ := json.Marshal(tc.metadata)
				encoded = []byte(strings.ReplaceAll(string(encoded), "ISSUER", srv.URL))
				_, _ = w.Write(encoded)
			}))
			defer srv.Close()
			_, err := NewJWKSVerifier(srv.URL, "billmesh-test", srv.Client()).Verify(context.Background(), raw)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestJWKSVerifierRejectsWrongClaimTypes(t *testing.T) {
	fixture := newJWKSFixture(t)
	defer fixture.close()
	claims := jwt.MapClaims{"iss": fixture.issuer, "aud": "billmesh-test", "sub": "user-1", "exp": time.Now().Add(time.Minute).Unix(), "org_id": "org-1", "app": "daybook", "permissions": "billing:read", "token_use": "access"}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = fixture.kid
	raw, err := token.SignedString(fixture.key)
	require.NoError(t, err)

	_, err = NewJWKSVerifier(fixture.issuer, "billmesh-test", nil).Verify(context.Background(), raw)
	require.Error(t, err)
}

func TestJWKSVerifierEnforcesTimeBoundaries(t *testing.T) {
	fixture := newJWKSFixture(t)
	defer fixture.close()
	verifier := NewJWKSVerifier(fixture.issuer, "billmesh-test", nil)

	t.Run("future nbf", func(t *testing.T) {
		_, err := verifier.Verify(context.Background(), fixture.token(t, func(claims *Claims) {
			claims.NotBefore = jwt.NewNumericDate(time.Now().Add(time.Minute))
		}))
		require.Error(t, err)
	})

	t.Run("exact expiry", func(t *testing.T) {
		_, err := verifier.Verify(context.Background(), fixture.token(t, func(claims *Claims) {
			claims.ExpiresAt = jwt.NewNumericDate(time.Now())
		}))
		require.Error(t, err)
	})
}

func TestJWKSVerifierRejectsRetiredCachedSigningKey(t *testing.T) {
	oldKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	newKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	currentKey := oldKey
	currentKid := "old-key"
	srv := newOIDCTestServer(func(w http.ResponseWriter, r *http.Request) {
		key := currentKey
		kid := currentKid
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": kid, "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	})
	defer srv.Close()
	verifier := NewJWKSVerifier(srv.URL, "billmesh-test", srv.Client())
	now := time.Now()
	verifier.now = func() time.Time { return now }
	sign := func(t *testing.T, key *rsa.PrivateKey, kid string) string {
		t.Helper()
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, Claims{Permissions: []string{"billing:read"}, OrgID: "org-1", App: "daybook", TokenUse: "access", RegisteredClaims: jwt.RegisteredClaims{Issuer: srv.URL, Audience: jwt.ClaimStrings{"billmesh-test"}, Subject: "user-1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}})
		token.Header["kid"] = kid
		raw, err := token.SignedString(key)
		require.NoError(t, err)
		return raw
	}

	oldToken := sign(t, oldKey, "old-key")
	_, err = verifier.Verify(context.Background(), oldToken)
	require.NoError(t, err)

	currentKey = newKey
	currentKid = "new-key"
	now = now.Add(unknownKeyRefreshInterval + time.Second)
	_, err = verifier.Verify(context.Background(), sign(t, newKey, "new-key"))
	require.NoError(t, err)

	_, err = verifier.Verify(context.Background(), oldToken)
	require.Error(t, err)
}
