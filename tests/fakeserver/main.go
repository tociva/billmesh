package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type server struct {
	key           *rsa.PrivateKey
	kid           string
	mu            sync.Mutex
	webhooks      []map[string]any
	failureCode   int
	receiverDelay time.Duration
	ssrfHits      int
	orderCalls    int
	oauthCodes    map[string]oauthGrant
	refreshTokens map[string]oauthGrant
}

type oauthGrant struct {
	Nonce, CodeChallenge, ClientID, RedirectURI, Subject string
}

func main() {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatal(err)
	}
	s := &server{key: key, kid: "billmesh-test-key", oauthCodes: make(map[string]oauthGrant), refreshTokens: make(map[string]oauthGrant)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", s.discovery)
	mux.HandleFunc("GET /.well-known/jwks.json", s.jwks)
	mux.HandleFunc("GET /oauth/authorize", s.authorize)
	mux.HandleFunc("POST /oauth/token", s.oauthToken)
	mux.HandleFunc("POST /oauth/revoke", s.revoke)
	mux.HandleFunc("GET /oauth/logout", s.providerLogout)
	mux.HandleFunc("POST /test/token", s.token)
	mux.HandleFunc("POST /test/rotate-key", s.rotateKey)
	mux.HandleFunc("POST /v1/orders", s.order)
	mux.HandleFunc("GET /test/order-count", s.orderCount)
	mux.HandleFunc("GET /v1/payments/{id}", s.payment)
	mux.HandleFunc("POST /receivers/{app}", s.receiver)
	mux.HandleFunc("GET /test/webhooks", s.listWebhooks)
	mux.HandleFunc("POST /test/failure", s.setFailure)
	mux.HandleFunc("POST /test/receiver-delay", s.setReceiverDelay)
	mux.HandleFunc("POST /test/ssrf-hit", s.ssrfHit)
	mux.HandleFunc("GET /test/ssrf-hits", s.listSSRFHits)
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	certFile := os.Getenv("TLS_CERT_FILE")
	keyFile := os.Getenv("TLS_KEY_FILE")
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			log.Fatal("TLS_CERT_FILE and TLS_KEY_FILE must be configured together")
		}
		log.Printf("fake external services listening with TLS on %s", addr)
		log.Fatal(http.ListenAndServeTLS(addr, certFile, keyFile, mux))
	}
	log.Printf("fake external services listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

var mockIssuer = value("MOCK_ISSUER", "http://mock-external:8090")

func value(key, fallback string) string {
	if configured := os.Getenv(key); configured != "" {
		return configured
	}
	return fallback
}

func (s *server) discovery(w http.ResponseWriter, _ *http.Request) {
	write(w, map[string]string{
		"issuer": mockIssuer, "authorization_endpoint": mockIssuer + "/oauth/authorize",
		"token_endpoint": mockIssuer + "/oauth/token", "revocation_endpoint": mockIssuer + "/oauth/revoke",
		"end_session_endpoint": mockIssuer + "/oauth/logout", "jwks_uri": mockIssuer + "/.well-known/jwks.json",
	})
}

func (s *server) authorize(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Get("response_type") != "code" || query.Get("state") == "" || query.Get("nonce") == "" ||
		query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
		http.Error(w, "invalid authorization request", http.StatusBadRequest)
		return
	}
	redirect, err := url.Parse(query.Get("redirect_uri"))
	if err != nil || !redirect.IsAbs() {
		http.Error(w, "invalid redirect URI", http.StatusBadRequest)
		return
	}
	code := randomString()
	s.mu.Lock()
	s.oauthCodes[code] = oauthGrant{
		Nonce: query.Get("nonce"), CodeChallenge: query.Get("code_challenge"),
		ClientID: query.Get("client_id"), RedirectURI: query.Get("redirect_uri"),
		Subject: "bff-operator",
	}
	s.mu.Unlock()
	values := redirect.Query()
	values.Set("code", code)
	values.Set("state", query.Get("state"))
	redirect.RawQuery = values.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (s *server) oauthToken(w http.ResponseWriter, r *http.Request) {
	clientID, clientSecret, ok := r.BasicAuth()
	validClient := clientID == "billmesh-console-test" || clientID == "billmesh-admin-test"
	if !ok || !validClient || clientSecret != "test-secret" {
		http.Error(w, "invalid client", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	var grant oauthGrant
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		code := r.Form.Get("code")
		s.mu.Lock()
		grant, ok = s.oauthCodes[code]
		delete(s.oauthCodes, code)
		s.mu.Unlock()
		if !ok || grant.ClientID != clientID || grant.RedirectURI != r.Form.Get("redirect_uri") ||
			grant.CodeChallenge != sha256Base64(r.Form.Get("code_verifier")) {
			http.Error(w, "invalid authorization code", http.StatusBadRequest)
			return
		}
	case "refresh_token":
		s.mu.Lock()
		grant, ok = s.refreshTokens[r.Form.Get("refresh_token")]
		s.mu.Unlock()
		if !ok || grant.ClientID != clientID {
			http.Error(w, "invalid refresh token", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "unsupported grant", http.StatusBadRequest)
		return
	}
	accessToken, idToken, err := s.browserTokens(grant)
	if err != nil {
		http.Error(w, "token signing failed", http.StatusInternalServerError)
		return
	}
	refreshToken := randomString()
	s.mu.Lock()
	s.refreshTokens[refreshToken] = grant
	s.mu.Unlock()
	write(w, map[string]string{
		"access_token": accessToken, "refresh_token": refreshToken,
		"id_token": idToken, "token_type": "Bearer",
	})
}

func (s *server) browserTokens(grant oauthGrant) (string, string, error) {
	now := time.Now()
	accessClaims := jwt.MapClaims{
		"iss": mockIssuer, "aud": "billmesh-test", "sub": grant.Subject,
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "token_use": "access",
		"org_id": "bff-org", "app": "daybook", "environment": "test",
		"permissions": []string{"billing:read", "billing:write", "billing:admin", "billing:link", "credits:grant", "credits:reserve", "credits:settle"},
	}
	access, err := s.sign(accessClaims)
	if err != nil {
		return "", "", err
	}
	hash := sha256.Sum256([]byte(access))
	idClaims := jwt.MapClaims{
		"iss": mockIssuer, "aud": grant.ClientID, "sub": grant.Subject,
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nonce": grant.Nonce,
		"email": "operator@example.test", "name": "Billmesh Operator",
		"at_hash": base64.RawURLEncoding.EncodeToString(hash[:len(hash)/2]),
	}
	id, err := s.sign(idClaims)
	return access, id, err
}

func (s *server) sign(claims jwt.MapClaims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	s.mu.Lock()
	key, kid := s.key, s.kid
	s.mu.Unlock()
	token.Header["kid"] = kid
	return token.SignedString(key)
}

func (s *server) revoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err == nil {
		s.mu.Lock()
		delete(s.refreshTokens, r.Form.Get("token"))
		s.mu.Unlock()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) providerLogout(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("post_logout_redirect_uri")
	if target == "" {
		http.Error(w, "missing redirect", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func randomString() string {
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func sha256Base64(value string) string {
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func (s *server) jwks(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	key := s.key
	kid := s.kid
	s.mu.Unlock()
	write(w, map[string]any{"keys": []any{map[string]any{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": kid, "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
}
func (s *server) token(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Subject          string         `json:"sub"`
		OrgID            string         `json:"org_id"`
		App              string         `json:"app"`
		Permissions      []string       `json:"permissions"`
		Issuer           string         `json:"issuer"`
		Audience         string         `json:"audience"`
		ExpiresInSeconds *int64         `json:"expires_in_seconds"`
		UnknownKey       bool           `json:"unknown_key"`
		Environment      string         `json:"environment"`
		TokenUse         string         `json:"token_use"`
		OmitTokenUse     bool           `json:"omit_token_use"`
		OmitClaims       []string       `json:"omit_claims"`
		ClaimOverrides   map[string]any `json:"claim_overrides"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.Subject == "" {
		in.Subject = "test-user"
	}
	issuer := in.Issuer
	if issuer == "" {
		issuer = mockIssuer
	}
	audience := in.Audience
	if audience == "" {
		audience = "billmesh-test"
	}
	expires := int64(3600)
	if in.ExpiresInSeconds != nil {
		expires = *in.ExpiresInSeconds
	}
	tokenUse := in.TokenUse
	if tokenUse == "" {
		tokenUse = "access"
	}
	claims := jwt.MapClaims{"iss": issuer, "aud": audience, "sub": in.Subject, "exp": time.Now().Add(time.Duration(expires) * time.Second).Unix(), "iat": time.Now().Unix(), "org_id": in.OrgID, "app": in.App, "environment": in.Environment, "permissions": in.Permissions}
	if !in.OmitTokenUse {
		claims["token_use"] = tokenUse
	}
	for _, name := range in.OmitClaims {
		delete(claims, name)
	}
	for name, value := range in.ClaimOverrides {
		claims[name] = value
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	s.mu.Lock()
	key := s.key
	kid := s.kid
	s.mu.Unlock()
	if in.UnknownKey {
		key, _ = rsa.GenerateKey(rand.Reader, 2048)
		kid = "unknown-key"
	}
	token.Header["kid"] = kid
	raw, err := token.SignedString(key)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	write(w, map[string]string{"access_token": raw, "token_type": "Bearer"})
}
func (s *server) rotateKey(w http.ResponseWriter, _ *http.Request) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.mu.Lock()
	s.key = key
	s.kid = "billmesh-test-key-" + time.Now().UTC().Format("150405.000000")
	kid := s.kid
	s.mu.Unlock()
	write(w, map[string]string{"kid": kid})
}
func (s *server) order(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.orderCalls++
	failureCode := s.failureCode
	s.mu.Unlock()
	if failureCode != 0 {
		http.Error(w, "configured failure", failureCode)
		return
	}
	var request struct {
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
	}
	_ = json.NewDecoder(r.Body).Decode(&request)
	if request.Currency == "" {
		request.Currency = "INR"
	}
	write(w, map[string]any{"id": "order_" + time.Now().UTC().Format("20060102150405.000000000"), "status": "created", "currency": request.Currency, "amount": request.Amount})
}
func (s *server) orderCount(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	count := s.orderCalls
	s.mu.Unlock()
	write(w, map[string]any{"count": count})
}
func (s *server) payment(w http.ResponseWriter, r *http.Request) {
	write(w, map[string]any{"id": r.PathValue("id"), "order_id": "order_test_001", "status": "captured", "currency": "INR", "amount": 50000})
}
func (s *server) receiver(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	raw, _ := io.ReadAll(r.Body)
	var payload any
	_ = json.NewDecoder(bytes.NewReader(raw)).Decode(&payload)
	s.mu.Lock()
	code := s.failureCode
	delay := s.receiverDelay
	s.webhooks = append(s.webhooks, map[string]any{"app": r.PathValue("app"), "event_id": r.Header.Get("X-Billmesh-Event-ID"), "event_type": r.Header.Get("X-Billmesh-Event-Type"), "signature": r.Header.Get("X-Billmesh-Signature"), "raw_body": string(raw), "payload": payload})
	s.mu.Unlock()
	if r.PathValue("app") == "redirect-private" {
		addresses, err := net.LookupIP("mock-external")
		if err != nil || len(addresses) == 0 {
			http.Error(w, "resolve redirect", 500)
			return
		}
		http.Redirect(w, r, "http://"+net.JoinHostPort(addresses[0].String(), "8090")+"/test/ssrf-hit", http.StatusTemporaryRedirect)
		return
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	if code != 0 {
		http.Error(w, "configured failure", code)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) ssrfHit(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.ssrfHits++
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listSSRFHits(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	write(w, map[string]int{"hits": s.ssrfHits})
}
func (s *server) listWebhooks(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	write(w, s.webhooks)
}
func (s *server) setFailure(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status int `json:"status"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	s.mu.Lock()
	s.failureCode = in.Status
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
func (s *server) setReceiverDelay(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Milliseconds int `json:"milliseconds"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.Milliseconds < 0 || in.Milliseconds > 10_000 {
		http.Error(w, "milliseconds must be between 0 and 10000", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.receiverDelay = time.Duration(in.Milliseconds) * time.Millisecond
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
