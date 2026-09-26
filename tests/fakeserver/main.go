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
	key         *rsa.PrivateKey
	kid         string
	mu          sync.Mutex
	webhooks    []map[string]any
	failureCode int
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
	mux.HandleFunc("POST /v1/orders", s.order)
	mux.HandleFunc("GET /v1/payments/{id}", s.payment)
	mux.HandleFunc("POST /receivers/{app}", s.receiver)
	mux.HandleFunc("GET /test/webhooks", s.listWebhooks)
	mux.HandleFunc("POST /test/failure", s.setFailure)
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	log.Printf("fake external services listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
func (s *server) jwks(w http.ResponseWriter, _ *http.Request) {
	write(w, map[string]any{"keys": []any{map[string]any{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": s.kid, "n": base64.RawURLEncoding.EncodeToString(s.key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(s.key.E)).Bytes())}}})
}
func (s *server) token(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Subject     string   `json:"sub"`
		OrgID       string   `json:"org_id"`
		App         string   `json:"app"`
		Permissions []string `json:"permissions"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.Subject == "" {
		in.Subject = "test-user"
	}
	issuer := "http://mock-external:8090"
	claims := jwt.MapClaims{"iss": issuer, "aud": "billmesh-test", "sub": in.Subject, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "org_id": in.OrgID, "app": in.App, "permissions": in.Permissions}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = s.kid
	raw, err := token.SignedString(s.key)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	write(w, map[string]string{"access_token": raw, "token_type": "Bearer"})
}
func (s *server) order(w http.ResponseWriter, r *http.Request) {
	write(w, map[string]any{"id": "order_test_001", "status": "created", "currency": "INR", "amount": 50000})
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
	s.webhooks = append(s.webhooks, map[string]any{"app": r.PathValue("app"), "event_id": r.Header.Get("X-Billmesh-Event-ID"), "payload": payload})
	s.mu.Unlock()
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
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
