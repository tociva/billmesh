package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
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
}

func main() {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatal(err)
	}
	s := &server{key: key, kid: "billmesh-test-key"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/jwks.json", s.jwks)
	mux.HandleFunc("POST /test/token", s.token)
	mux.HandleFunc("POST /test/rotate-key", s.rotateKey)
	mux.HandleFunc("POST /v1/orders", s.order)
	mux.HandleFunc("GET /v1/payments/{id}", s.payment)
	mux.HandleFunc("POST /receivers/{app}", s.receiver)
	mux.HandleFunc("GET /test/webhooks", s.listWebhooks)
	mux.HandleFunc("POST /test/failure", s.setFailure)
	mux.HandleFunc("POST /test/receiver-delay", s.setReceiverDelay)
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	log.Printf("fake external services listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
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
		Subject          string   `json:"sub"`
		OrgID            string   `json:"org_id"`
		App              string   `json:"app"`
		Permissions      []string `json:"permissions"`
		Issuer           string   `json:"issuer"`
		Audience         string   `json:"audience"`
		ExpiresInSeconds *int64   `json:"expires_in_seconds"`
		UnknownKey       bool     `json:"unknown_key"`
		Environment      string   `json:"environment"`
		TokenUse         string   `json:"token_use"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.Subject == "" {
		in.Subject = "test-user"
	}
	issuer := in.Issuer
	if issuer == "" {
		issuer = "http://mock-external:8090"
	}
	audience := in.Audience
	if audience == "" {
		audience = "billmesh-test"
	}
	expires := int64(3600)
	if in.ExpiresInSeconds != nil {
		expires = *in.ExpiresInSeconds
	}
	claims := jwt.MapClaims{"iss": issuer, "aud": audience, "sub": in.Subject, "exp": time.Now().Add(time.Duration(expires) * time.Second).Unix(), "iat": time.Now().Unix(), "org_id": in.OrgID, "app": in.App, "environment": in.Environment, "permissions": in.Permissions}
	if in.TokenUse != "" {
		claims["token_use"] = in.TokenUse
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
func (s *server) payment(w http.ResponseWriter, r *http.Request) {
	write(w, map[string]any{"id": r.PathValue("id"), "order_id": "order_test_001", "status": "captured", "currency": "INR", "amount": 50000})
}
func (s *server) receiver(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var payload any
	_ = json.NewDecoder(r.Body).Decode(&payload)
	s.mu.Lock()
	code := s.failureCode
	delay := s.receiverDelay
	s.webhooks = append(s.webhooks, map[string]any{"app": r.PathValue("app"), "event_id": r.Header.Get("X-Billmesh-Event-ID"), "payload": payload})
	s.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	if code != 0 {
		http.Error(w, "configured failure", code)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
