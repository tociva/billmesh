package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestJWKSVerifier(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	kid := "test-key"
	var issuer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": kid, "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	defer srv.Close()
	issuer = srv.URL
	claims := Claims{Permissions: []string{"billing:read"}, OrgID: "org-1", App: "daybook", RegisteredClaims: jwt.RegisteredClaims{Issuer: issuer, Audience: jwt.ClaimStrings{"billmesh-test"}, Subject: "user-1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	raw, err := token.SignedString(key)
	require.NoError(t, err)
	got, err := NewJWKSVerifier(issuer, "billmesh-test", srv.URL, nil).Verify(context.Background(), raw)
	require.NoError(t, err)
	require.Equal(t, "org-1", got.OrgID)
	require.True(t, got.Has("billing:read"))
}

func TestJWKSVerifierRejectsWrongAudience(t *testing.T) {
	v := NewJWKSVerifier("issuer", "expected", "http://invalid", nil)
	_, err := v.Verify(context.Background(), "not-a-token")
	require.Error(t, err)
}
