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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func securityToken(t *testing.T, key *rsa.PrivateKey, kid, issuer string, now time.Time, mutate func(*Claims)) string {
	t.Helper()
	claims := Claims{
		ClientID: "daybook-billing", ActorType: "user", OrgID: "org-1", App: "daybook", Environment: "test",
		RegisteredClaims: jwt.RegisteredClaims{Issuer: issuer, Audience: jwt.ClaimStrings{"billmesh-test"}, Subject: "user-1", IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))},
	}
	if mutate != nil {
		mutate(&claims)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	raw, err := token.SignedString(key)
	require.NoError(t, err)
	return raw
}

func securityJWK(key *rsa.PrivateKey, kid string) map[string]any {
	return map[string]any{"kty": "RSA", "kid": kid, "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}
}

func TestSecurityVerifierAcceptsMultipleAudiences(t *testing.T) {
	f := newJWKSFixture(t)
	defer f.close()
	raw := f.token(t, func(c *Claims) { c.Audience = jwt.ClaimStrings{"another-service", "billmesh-test"} })
	_, err := NewJWKSVerifier(f.issuer, "billmesh-test", nil).Verify(context.Background(), raw)
	require.NoError(t, err)
}

func TestSecurityVerifierEnforcesTimeBoundariesWithControlledClock(t *testing.T) {
	f := newJWKSFixture(t)
	defer f.close()
	now := time.Now().UTC().Truncate(time.Second)
	v := NewJWKSVerifier(f.issuer, "billmesh-test", nil)
	v.now = func() time.Time { return now }
	for _, tc := range []struct {
		name   string
		mutate func(*Claims)
		valid  bool
	}{
		{"one second before expiry", func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(now.Add(time.Second)) }, true},
		{"exact expiry", func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(now) }, false},
		{"not before now", func(c *Claims) { c.NotBefore = jwt.NewNumericDate(now) }, true},
		{"future not before", func(c *Claims) { c.NotBefore = jwt.NewNumericDate(now.Add(time.Second)) }, false},
		{"future issued at", func(c *Claims) { c.IssuedAt = jwt.NewNumericDate(now.Add(time.Second)) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := v.Verify(context.Background(), f.token(t, tc.mutate))
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestSecurityVerifierRetiresCachedKeyAfterRefreshWindow(t *testing.T) {
	oldKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	newKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Second)
	var mu sync.Mutex
	active := []any{securityJWK(oldKey, "old")}
	srv := newOIDCTestServer(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": active})
	})
	defer srv.Close()
	v := NewJWKSVerifier(srv.URL, "billmesh-test", srv.Client())
	v.now = func() time.Time { return now }
	oldToken := securityToken(t, oldKey, "old", srv.URL, now, nil)
	_, err = v.Verify(context.Background(), oldToken)
	require.NoError(t, err)
	mu.Lock()
	active = []any{securityJWK(oldKey, "old"), securityJWK(newKey, "new")}
	mu.Unlock()
	now = now.Add(keyCacheLifetime + time.Second)
	_, err = v.Verify(context.Background(), oldToken)
	require.NoError(t, err, "old key should work during the issuer's overlap period")
	newToken := securityToken(t, newKey, "new", srv.URL, now, nil)
	_, err = v.Verify(context.Background(), newToken)
	require.NoError(t, err)
	mu.Lock()
	active = []any{securityJWK(newKey, "new")}
	mu.Unlock()
	now = now.Add(keyCacheLifetime + time.Second)
	_, err = v.Verify(context.Background(), oldToken)
	require.Error(t, err, "retired key must stop working after the cache refresh window")
	_, err = v.Verify(context.Background(), newToken)
	require.NoError(t, err)
}

func TestSecurityVerifierBoundsUnknownKeyFetches(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	var fetches atomic.Int32
	srv := newOIDCTestServer(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{securityJWK(key, "known")}})
	})
	defer srv.Close()
	v := NewJWKSVerifier(srv.URL, "billmesh-test", srv.Client())
	now := time.Now()
	known := securityToken(t, key, "known", srv.URL, now, nil)
	_, err = v.Verify(context.Background(), known)
	require.NoError(t, err)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			raw := securityToken(t, key, "unknown-"+strconv.Itoa(i), srv.URL, now, nil)
			_, verifyErr := v.Verify(context.Background(), raw)
			require.Error(t, verifyErr)
		}(i)
	}
	wg.Wait()
	require.LessOrEqual(t, fetches.Load(), int32(2), "random key IDs must not trigger one JWKS fetch each")
	_, err = v.Verify(context.Background(), known)
	require.NoError(t, err, "legitimate authentication must remain available")
}

func TestSecurityMiddlewareRejectsDuplicateAuthorizationBeforeHandler(t *testing.T) {
	f := newJWKSFixture(t)
	defer f.close()
	var calls atomic.Int32
	handler := Middleware(NewJWKSVerifier(f.issuer, "billmesh-test", nil))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/protected", nil)
	req.Header.Add("Authorization", "Bearer "+f.token(t, nil))
	req.Header.Add("Authorization", "Bearer invalid")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	require.Equal(t, http.StatusUnauthorized, resp.Code)
	require.Equal(t, "application/json", resp.Header().Get("Content-Type"))
	var body map[string]string
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	require.Equal(t, "missing or ambiguous bearer token", body["error"])
	require.Zero(t, calls.Load())
}

func TestSecurityVerifierJWKSOutageGraceAndRecovery(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Second)
	var mu sync.Mutex
	status := http.StatusOK
	srv := newOIDCTestServer(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if status != http.StatusOK {
			http.Error(w, "unavailable", status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{securityJWK(key, "known")}})
	})
	defer srv.Close()
	v := NewJWKSVerifier(srv.URL, "billmesh-test", srv.Client())
	v.now = func() time.Time { return now }
	known := securityToken(t, key, "known", srv.URL, now, nil)
	_, err = v.Verify(context.Background(), known)
	require.NoError(t, err)
	mu.Lock()
	status = http.StatusServiceUnavailable
	mu.Unlock()
	now = now.Add(keyCacheLifetime + time.Second)
	_, err = v.Verify(context.Background(), known)
	require.NoError(t, err, "known key should work during bounded outage grace")
	now = now.Add(keyCacheOutageGrace)
	_, err = v.Verify(context.Background(), known)
	require.Error(t, err, "known key should fail after outage grace")
	mu.Lock()
	status = http.StatusOK
	mu.Unlock()
	_, err = v.Verify(context.Background(), known)
	require.NoError(t, err, "JWKS recovery should restore verification")
}

func TestSecurityVerifierJWKSTimeoutFailsClosedAndRecovers(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Second)
	var stalled atomic.Bool
	srv := newOIDCTestServer(func(w http.ResponseWriter, r *http.Request) {
		if stalled.Load() {
			<-r.Context().Done()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{securityJWK(key, "known")}})
	})
	defer srv.Close()
	client := srv.Client()
	client.Timeout = 25 * time.Millisecond
	v := NewJWKSVerifier(srv.URL, "billmesh-test", client)
	v.now = func() time.Time { return now }
	known := securityToken(t, key, "known", srv.URL, now, nil)
	_, err = v.Verify(context.Background(), known)
	require.NoError(t, err)
	stalled.Store(true)
	now = now.Add(keyCacheLifetime + time.Second)
	_, err = v.Verify(context.Background(), known)
	require.NoError(t, err, "a cached key may be used only during the bounded outage grace")
	now = now.Add(keyCacheOutageGrace)
	_, err = v.Verify(context.Background(), known)
	require.Error(t, err, "a timed-out JWKS fetch must fail closed after the grace period")
	stalled.Store(false)
	_, err = v.Verify(context.Background(), known)
	require.NoError(t, err, "verification should recover when JWKS responds again")
}

func TestSecurityVerifierRejectsUnusableAndMissingKeyIDs(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	now := time.Now()
	srv := newOIDCTestServer(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "bad", "n": "***", "e": "AQAB"}}})
	})
	defer srv.Close()
	v := NewJWKSVerifier(srv.URL, "billmesh-test", srv.Client())
	_, err = v.Verify(context.Background(), securityToken(t, key, "bad", srv.URL, now, nil))
	require.Error(t, err)
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, Claims{OrgID: "org-1", App: "daybook", RegisteredClaims: jwt.RegisteredClaims{Issuer: srv.URL, Audience: jwt.ClaimStrings{"billmesh-test"}, Subject: "user-1", ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))}})
	raw, err := token.SignedString(key)
	require.NoError(t, err)
	_, err = v.Verify(context.Background(), raw)
	require.ErrorContains(t, err, "missing key id")
}

func TestSecurityVerifierRejectsWrongClaimTypes(t *testing.T) {
	f := newJWKSFixture(t)
	defer f.close()
	base := jwt.MapClaims{"iss": f.issuer, "aud": "billmesh-test", "sub": "user-1", "exp": time.Now().Add(time.Hour).Unix(), "org_id": "org-1", "app": "daybook", "client_id": "daybook-billing"}
	for _, tc := range []struct {
		name, claim string
		value       any
	}{
		{"organization", "org_id", []string{"org-1"}},
		{"application", "app", true},
		{"expiry", "exp", "tomorrow"},
		{"audience", "aud", 5},
		{"issuer", "iss", []string{f.issuer}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := jwt.MapClaims{}
			for k, v := range base {
				claims[k] = v
			}
			claims[tc.claim] = tc.value
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			token.Header["kid"] = f.kid
			raw, err := token.SignedString(f.key)
			require.NoError(t, err)
			_, err = NewJWKSVerifier(f.issuer, "billmesh-test", nil).Verify(context.Background(), raw)
			require.Error(t, err)
		})
	}
}

func TestSecurityVerifierRejectsMalformedJWTShapes(t *testing.T) {
	f := newJWKSFixture(t)
	defer f.close()
	v := NewJWKSVerifier(f.issuer, "billmesh-test", nil)
	for _, raw := range []string{"", "a.b", "a.b.c.d", "%%%%.%%%%.%%%%", strings.Repeat("a", 256*1024)} {
		_, err := v.Verify(context.Background(), raw)
		require.Error(t, err)
	}
}

func TestSecurityIssuedClientIdentityRemainsValidUntilExpiry(t *testing.T) {
	f := newJWKSFixture(t)
	defer f.close()
	now := time.Now().UTC().Truncate(time.Second)
	v := NewJWKSVerifier(f.issuer, "billmesh-test", nil)
	v.now = func() time.Time { return now }
	old := f.token(t, func(c *Claims) {
		c.ClientID = "daybook-billing"
		c.ExpiresAt = jwt.NewNumericDate(now.Add(time.Minute))
	})
	newToken := f.token(t, func(c *Claims) {
		c.ClientID = "daybook-runtime"
		c.ExpiresAt = jwt.NewNumericDate(now.Add(time.Minute))
	})
	oldClaims, err := v.Verify(context.Background(), old)
	require.NoError(t, err)
	require.Equal(t, "daybook-billing", oldClaims.ClientID)
	newClaims, err := v.Verify(context.Background(), newToken)
	require.NoError(t, err)
	require.Equal(t, "daybook-runtime", newClaims.ClientID)
	_, err = v.Verify(context.Background(), old)
	require.NoError(t, err, "there is no live issuer revocation check for an already signed token")
	now = now.Add(time.Minute)
	_, err = v.Verify(context.Background(), old)
	require.Error(t, err, "client identity must stop at token expiry")
}
