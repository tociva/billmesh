package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type server struct {
	key                  *rsa.PrivateKey
	kid                  string
	delegationKey        *ecdsa.PrivateKey
	delegationKid        string
	mu                   sync.Mutex
	webhooks             []map[string]any
	failureCode          int
	receiverDelay        time.Duration
	ssrfHits             int
	orderCalls           int
	razorpayOrders       map[string]fakeRazorpayOrder
	razorpayOrderReceipt map[string]string
	razorpayPayments     map[string][]fakeRazorpayPayment
	razorpayRefunds      map[string][]fakeRazorpayRefund
	razorpayBehavior     fakeRazorpayBehavior
	oauthCodes           map[string]oauthGrant
	refreshTokens        map[string]oauthGrant
}

type fakeRazorpayOrder struct {
	ID         string            `json:"id,omitempty"`
	Entity     string            `json:"entity"`
	Amount     int64             `json:"amount"`
	AmountPaid int64             `json:"amount_paid"`
	AmountDue  int64             `json:"amount_due"`
	Currency   string            `json:"currency"`
	Receipt    string            `json:"receipt"`
	Status     string            `json:"status"`
	Attempts   int               `json:"attempts"`
	Notes      map[string]string `json:"notes"`
	CreatedAt  int64             `json:"created_at"`
}

type fakeRazorpayPayment struct {
	ID       string `json:"id"`
	Entity   string `json:"entity"`
	OrderID  string `json:"order_id"`
	Status   string `json:"status"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

type fakeRazorpayRefund struct {
	ID        string `json:"id"`
	Entity    string `json:"entity"`
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	CreatedAt int64  `json:"created_at"`
}

type fakeRazorpayBehavior struct {
	Status             int  `json:"status"`
	DelayMS            int  `json:"delay_ms"`
	MalformedJSON      bool `json:"malformed_json"`
	MissingOrderID     bool `json:"missing_order_id"`
	DropAfterCommit    bool `json:"drop_after_commit"`
	MismatchedEntity   bool `json:"mismatched_entity"`
	MismatchedAmount   bool `json:"mismatched_amount"`
	MismatchedCurrency bool `json:"mismatched_currency"`
	MismatchedReceipt  bool `json:"mismatched_receipt"`
	MismatchedNotes    bool `json:"mismatched_notes"`
}

type oauthGrant struct {
	Nonce, CodeChallenge, ClientID, RedirectURI, Subject string
}

func main() {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatal(err)
	}
	delegationKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	s := newFakeServerState()
	s.key = key
	s.kid = "billmesh-test-key"
	s.delegationKey = delegationKey
	s.delegationKid = "billmesh-delegation-test-key"
	handler := newHandler(s)
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
		log.Fatal(http.ListenAndServeTLS(addr, certFile, keyFile, handler))
	}
	log.Printf("fake external services listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, handler))
}

func newFakeServerState() *server {
	return &server{
		oauthCodes: make(map[string]oauthGrant), refreshTokens: make(map[string]oauthGrant),
		razorpayOrders: make(map[string]fakeRazorpayOrder), razorpayOrderReceipt: make(map[string]string),
		razorpayPayments: make(map[string][]fakeRazorpayPayment),
		razorpayRefunds:  make(map[string][]fakeRazorpayRefund),
	}
}

func newHandler(s *server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", s.discovery)
	mux.HandleFunc("GET /.well-known/jwks.json", s.jwks)
	mux.HandleFunc("GET /.well-known/idnest-delegation-configuration", s.delegationDiscovery)
	mux.HandleFunc("GET /auth/v1/delegation/jwks", s.delegationJWKS)
	mux.HandleFunc("GET /oauth/authorize", s.authorize)
	mux.HandleFunc("POST /oauth/token", s.oauthToken)
	mux.HandleFunc("POST /oauth/revoke", s.revoke)
	mux.HandleFunc("GET /oauth/logout", s.providerLogout)
	mux.HandleFunc("POST /test/token", s.token)
	mux.HandleFunc("POST /test/rotate-key", s.rotateKey)
	mux.HandleFunc("POST /v1/orders", s.order)
	mux.HandleFunc("GET /v1/orders", s.listRazorpayOrders)
	mux.HandleFunc("GET /v1/orders/{id}/payments", s.orderPayments)
	mux.HandleFunc("GET /test/order-count", s.orderCount)
	mux.HandleFunc("GET /v1/payments/{id}", s.payment)
	mux.HandleFunc("GET /v1/payments/{id}/refunds", s.paymentRefunds)
	mux.HandleFunc("GET /test/razorpay/orders", s.listRazorpayOrders)
	mux.HandleFunc("POST /test/razorpay/payments", s.addRazorpayPayment)
	mux.HandleFunc("POST /test/razorpay/refunds", s.addRazorpayRefund)
	mux.HandleFunc("POST /test/razorpay/behavior", s.setRazorpayBehavior)
	mux.HandleFunc("POST /receivers/{app}", s.receiver)
	mux.HandleFunc("GET /test/webhooks", s.listWebhooks)
	mux.HandleFunc("POST /test/failure", s.setFailure)
	mux.HandleFunc("POST /test/receiver-delay", s.setReceiverDelay)
	mux.HandleFunc("POST /test/ssrf-hit", s.ssrfHit)
	mux.HandleFunc("GET /test/ssrf-hits", s.listSSRFHits)
	return mux
}

var mockIssuer = value("MOCK_ISSUER", "http://mock-external:8090")
var mockDelegationIssuer = mockIssuer + "/auth/v1/delegation"

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

func (s *server) delegationDiscovery(w http.ResponseWriter, _ *http.Request) {
	write(w, map[string]any{
		"issuer":   mockDelegationIssuer,
		"jwks_uri": mockIssuer + "/auth/v1/delegation/jwks",
		"delegated_access_signing_alg_values_supported": []string{"ES256"},
		"authorization_details_types_supported":         []string{"urn:idnest:delegation"},
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
		"client_id": grant.ClientID,
		"org_id":    "bff-org", "app": "daybook", "environment": "test", "actor_type": "user",
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
func (s *server) delegationJWKS(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	key, kid := s.delegationKey, s.delegationKid
	s.mu.Unlock()
	write(w, map[string]any{"keys": []any{map[string]any{
		"kty": "EC", "crv": "P-256", "use": "sig", "alg": "ES256", "kid": kid,
		"x": base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))),
		"y": base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32))),
	}}})
}
func (s *server) token(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Subject           string         `json:"sub"`
		ClientID          string         `json:"client_id"`
		OrgID             string         `json:"org_id"`
		App               string         `json:"app"`
		Permissions       []string       `json:"permissions"`
		Issuer            string         `json:"issuer"`
		Audience          string         `json:"audience"`
		ExpiresInSeconds  *int64         `json:"expires_in_seconds"`
		UnknownKey        bool           `json:"unknown_key"`
		Environment       string         `json:"environment"`
		ActorType         string         `json:"actor_type"`
		AuthorizerID      string         `json:"authorizer_client_id"`
		BillingCustomerID string         `json:"billing_customer_id"`
		TokenUse          string         `json:"token_use"`
		OmitTokenUse      bool           `json:"omit_token_use"`
		OmitClaims        []string       `json:"omit_claims"`
		ClaimOverrides    map[string]any `json:"claim_overrides"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.Subject == "" {
		in.Subject = "test-user"
	}
	if in.ActorType == "" {
		in.ActorType = "user"
	}
	issuer := in.Issuer
	if issuer == "" {
		issuer = mockDelegationIssuer
	}
	audience := in.Audience
	if audience == "" {
		audience = "https://api.test"
	}
	expires := int64(300)
	if in.ExpiresInSeconds != nil {
		expires = *in.ExpiresInSeconds
	}
	tokenUse := in.TokenUse
	if tokenUse == "" {
		tokenUse = "access"
	}
	clientID := in.ClientID
	if clientID == "" {
		clientID = clientIDForPermissions(in.App, in.Environment, in.Permissions)
	}
	if clientID == "billmesh-global-admin-test" && in.ActorType == "user" {
		clientID = "billmesh-global-admin-user-test"
	}
	authorizerID := in.AuthorizerID
	if authorizerID == "" {
		authorizerID = authorizerForClient(clientID)
	}
	scope := scopeForClient(clientID)
	now := time.Now()
	context := map[string]any{"type": "urn:billmesh:context:v1"}
	if strings.HasPrefix(clientID, "billmesh-global-admin") {
		context["application"] = in.App
		context["environment"] = in.Environment
	}
	if in.OrgID != "" {
		context["org_id"] = in.OrgID
	}
	if in.BillingCustomerID != "" {
		context["billing_customer_id"] = in.BillingCustomerID
	}
	detail := map[string]any{
		"type": "urn:idnest:delegation", "grant_id": "grant-" + randomString(),
		"authorizer_client_id": authorizerID, "context_profile_version": 1,
	}
	if len(context) > 1 || scope != "billmesh.catalogue" {
		detail["context"] = context
	}
	claims := jwt.MapClaims{
		"iss": issuer, "aud": audience, "sub": in.Subject, "exp": now.Add(time.Duration(expires) * time.Second).Unix(),
		"iat": now.Unix(), "nbf": now.Unix(), "jti": "token-" + randomString(), "client_id": clientID,
		"act": map[string]any{"sub": clientID}, "scope": scope, "authorization_details": []any{detail},
	}
	if !in.OmitTokenUse {
		claims["token_use"] = tokenUse
	}
	for _, name := range in.OmitClaims {
		delete(claims, name)
		if name == "org_id" || name == "app" || name == "environment" || name == "billing_customer_id" {
			contextName := name
			if name == "app" {
				contextName = "application"
			}
			delete(context, contextName)
		}
	}
	for name, value := range in.ClaimOverrides {
		if name == "org_id" || name == "app" || name == "environment" || name == "billing_customer_id" {
			contextName := name
			if name == "app" {
				contextName = "application"
			}
			context[contextName] = value
		} else {
			claims[name] = value
		}
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	s.mu.Lock()
	key := s.delegationKey
	kid := s.delegationKid
	s.mu.Unlock()
	if in.UnknownKey {
		key, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		kid = "unknown-key"
	}
	token.Header["kid"] = kid
	token.Header["typ"] = "at+jwt"
	raw, err := token.SignedString(key)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	write(w, map[string]string{"access_token": raw, "token_type": "Bearer"})
}

func authorizerForClient(clientID string) string {
	if strings.HasPrefix(clientID, "daybook-") {
		return "daybook-billmesh-authorizer-test"
	}
	if strings.HasPrefix(clientID, "taskmesh-") {
		return "taskmesh-billmesh-authorizer-test"
	}
	return "billmesh-authorizer-test"
}

func scopeForClient(clientID string) string {
	switch {
	case strings.Contains(clientID, "catalogue"):
		return "billmesh.catalogue"
	case strings.Contains(clientID, "runtime"):
		return "billmesh.runtime"
	case strings.Contains(clientID, "admin"):
		return "billmesh.admin"
	default:
		return "billmesh.billing"
	}
}

func clientIDForPermissions(app, environment string, permissions []string) string {
	if app != "daybook" && app != "taskmesh" {
		return "billmesh-global-admin-test"
	}
	clientType := "billing"
	hasBilling := false
	for _, permission := range permissions {
		switch permission {
		case "billing:admin", "credits:grant":
			return "billmesh-global-admin-test"
		case "billing:read", "billing:write", "billing:ownership", "billing:link":
			hasBilling = true
		case "catalogue:read":
			clientType = "catalogue"
		}
	}
	if !hasBilling {
		for _, permission := range permissions {
			if permission == "credits:reserve" || permission == "credits:settle" {
				clientType = "runtime"
				break
			}
		}
	}
	if environment == "staging" {
		return app + "-" + clientType + "-staging-test"
	}
	return app + "-" + clientType + "-test"
}
func (s *server) rotateKey(w http.ResponseWriter, _ *http.Request) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.mu.Lock()
	s.delegationKey = key
	s.delegationKid = "billmesh-delegation-test-key-" + time.Now().UTC().Format("150405.000000")
	kid := s.delegationKid
	s.mu.Unlock()
	write(w, map[string]string{"kid": kid})
}
func (s *server) order(w http.ResponseWriter, r *http.Request) {
	if !validRazorpayAuth(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="razorpay-test"`)
		http.Error(w, "invalid Razorpay credentials", http.StatusUnauthorized)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	var request struct {
		Amount   int64             `json:"amount"`
		Currency string            `json:"currency"`
		Receipt  string            `json:"receipt"`
		Notes    map[string]string `json:"notes"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid order JSON", http.StatusBadRequest)
		return
	}
	if request.Amount <= 0 {
		http.Error(w, "amount must be positive", http.StatusBadRequest)
		return
	}
	if len(request.Currency) != 3 || strings.ToUpper(request.Currency) != request.Currency {
		http.Error(w, "currency must be a three-letter uppercase code", http.StatusBadRequest)
		return
	}
	if !validRazorpayReceipt(request.Receipt) {
		http.Error(w, "receipt must be 1-40 printable ASCII characters", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.orderCalls++
	failureCode := s.failureCode
	behavior := s.razorpayBehavior
	s.mu.Unlock()
	if failureCode != 0 {
		http.Error(w, "configured failure", failureCode)
		return
	}
	if behavior.DelayMS > 0 {
		time.Sleep(time.Duration(behavior.DelayMS) * time.Millisecond)
	}
	if behavior.Status != 0 {
		http.Error(w, "configured Razorpay failure", behavior.Status)
		return
	}

	s.mu.Lock()
	if _, duplicate := s.razorpayOrderReceipt[request.Receipt]; duplicate {
		s.mu.Unlock()
		http.Error(w, "duplicate receipt", http.StatusBadRequest)
		return
	}
	order := fakeRazorpayOrder{
		ID: fmt.Sprintf("order_billmesh_test_%06d", len(s.razorpayOrders)+1), Entity: "order",
		Amount: request.Amount, AmountDue: request.Amount, Currency: request.Currency,
		Receipt: request.Receipt, Status: "created", Notes: request.Notes, CreatedAt: time.Now().UTC().Unix(),
	}
	s.razorpayOrders[order.ID] = order
	s.razorpayOrderReceipt[order.Receipt] = order.ID
	s.mu.Unlock()

	if behavior.DropAfterCommit {
		if hijacker, ok := w.(http.Hijacker); ok {
			connection, _, err := hijacker.Hijack()
			if err == nil {
				_ = connection.Close()
				return
			}
		}
		http.Error(w, "configured connection drop unsupported", http.StatusInternalServerError)
		return
	}
	if behavior.MalformedJSON {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":`)
		return
	}
	if behavior.MissingOrderID {
		order.ID = ""
	}
	if behavior.MismatchedEntity {
		order.Entity = "payment"
	}
	if behavior.MismatchedAmount {
		order.Amount++
	}
	if behavior.MismatchedCurrency {
		order.Currency = "USD"
	}
	if behavior.MismatchedReceipt {
		order.Receipt += "-wrong"
	}
	if behavior.MismatchedNotes {
		order.Notes = map[string]string{"billmesh_environment": "wrong"}
	}
	write(w, order)
}
func (s *server) orderCount(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	count := s.orderCalls
	s.mu.Unlock()
	write(w, map[string]any{"count": count})
}
func (s *server) orderPayments(w http.ResponseWriter, r *http.Request) {
	if !validRazorpayAuth(r) {
		http.Error(w, "invalid Razorpay credentials", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	behavior := s.razorpayBehavior
	orderID := r.PathValue("id")
	if _, exists := s.razorpayOrders[orderID]; !exists {
		s.mu.Unlock()
		http.Error(w, "order not found", http.StatusNotFound)
		return
	}
	items := append([]fakeRazorpayPayment(nil), s.razorpayPayments[orderID]...)
	s.mu.Unlock()
	if writeRazorpayReadBehavior(w, behavior) {
		return
	}
	write(w, map[string]any{"entity": "collection", "count": len(items), "items": items})
}
func (s *server) payment(w http.ResponseWriter, r *http.Request) {
	if !validRazorpayAuth(r) {
		http.Error(w, "invalid Razorpay credentials", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	behavior := s.razorpayBehavior
	var found fakeRazorpayPayment
	exists := false
	for _, payments := range s.razorpayPayments {
		for _, payment := range payments {
			if payment.ID == r.PathValue("id") {
				found = payment
				exists = true
				break
			}
		}
		if exists {
			break
		}
	}
	s.mu.Unlock()
	if writeRazorpayReadBehavior(w, behavior) {
		return
	}
	if exists {
		write(w, found)
		return
	}
	http.Error(w, "payment not found", http.StatusNotFound)
}

func (s *server) paymentRefunds(w http.ResponseWriter, r *http.Request) {
	if !validRazorpayAuth(r) {
		http.Error(w, "invalid Razorpay credentials", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	behavior := s.razorpayBehavior
	items := append([]fakeRazorpayRefund(nil), s.razorpayRefunds[r.PathValue("id")]...)
	s.mu.Unlock()
	if writeRazorpayReadBehavior(w, behavior) {
		return
	}
	skip := int(parseInt64(r.URL.Query().Get("skip")))
	if skip < 0 {
		skip = 0
	}
	count := int(parseInt64(r.URL.Query().Get("count")))
	if count <= 0 || count > 100 {
		count = 10
	}
	if skip >= len(items) {
		items = nil
	} else {
		items = items[skip:]
		if len(items) > count {
			items = items[:count]
		}
	}
	write(w, map[string]any{"entity": "collection", "count": len(items), "items": items})
}

func (s *server) listRazorpayOrders(w http.ResponseWriter, r *http.Request) {
	if !validRazorpayAuth(r) {
		http.Error(w, "invalid Razorpay credentials", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	behavior := s.razorpayBehavior
	items := make([]fakeRazorpayOrder, 0, len(s.razorpayOrders))
	if receipt := r.URL.Query().Get("receipt"); receipt != "" {
		if orderID := s.razorpayOrderReceipt[receipt]; orderID != "" {
			items = append(items, s.razorpayOrders[orderID])
		}
	} else {
		for _, order := range s.razorpayOrders {
			if from := parseInt64(r.URL.Query().Get("from")); from != 0 && order.CreatedAt < from {
				continue
			}
			if to := parseInt64(r.URL.Query().Get("to")); to != 0 && order.CreatedAt > to {
				continue
			}
			items = append(items, order)
		}
	}
	s.mu.Unlock()
	// The /test control route is an observation surface for E2E assertions and
	// must remain available while the provider-facing route is fault-injected.
	if r.URL.Path == "/v1/orders" && writeRazorpayReadBehavior(w, behavior) {
		return
	}
	sort.Slice(items, func(left, right int) bool {
		if items[left].CreatedAt == items[right].CreatedAt {
			return items[left].ID > items[right].ID
		}
		return items[left].CreatedAt > items[right].CreatedAt
	})
	skip := int(parseInt64(r.URL.Query().Get("skip")))
	if skip < 0 {
		skip = 0
	}
	count := int(parseInt64(r.URL.Query().Get("count")))
	if count <= 0 || count > 100 {
		count = 10
	}
	if skip >= len(items) {
		items = nil
	} else {
		items = items[skip:]
		if len(items) > count {
			items = items[:count]
		}
	}
	write(w, map[string]any{"entity": "collection", "count": len(items), "items": items})
}

func writeRazorpayReadBehavior(w http.ResponseWriter, behavior fakeRazorpayBehavior) bool {
	if behavior.DelayMS > 0 {
		time.Sleep(time.Duration(behavior.DelayMS) * time.Millisecond)
	}
	if behavior.Status != 0 {
		http.Error(w, "configured Razorpay failure", behavior.Status)
		return true
	}
	if behavior.MalformedJSON {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"entity":`)
		return true
	}
	return false
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func (s *server) addRazorpayPayment(w http.ResponseWriter, r *http.Request) {
	var payment fakeRazorpayPayment
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payment); err != nil {
		http.Error(w, "invalid payment JSON", http.StatusBadRequest)
		return
	}
	if payment.ID == "" || payment.OrderID == "" || payment.Amount <= 0 || len(payment.Currency) != 3 {
		http.Error(w, "payment fields are required", http.StatusBadRequest)
		return
	}
	if payment.Status != "created" && payment.Status != "authorized" && payment.Status != "captured" && payment.Status != "failed" {
		http.Error(w, "invalid payment status", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	order, exists := s.razorpayOrders[payment.OrderID]
	if !exists {
		http.Error(w, "order not found", http.StatusNotFound)
		return
	}
	if payment.Entity == "" {
		payment.Entity = "payment"
	}
	s.razorpayPayments[payment.OrderID] = append(s.razorpayPayments[payment.OrderID], payment)
	order.Attempts++
	if payment.Status == "captured" {
		order.AmountPaid = payment.Amount
		order.AmountDue = order.Amount - payment.Amount
		order.Status = "paid"
	} else if order.Status == "created" {
		order.Status = "attempted"
	}
	s.razorpayOrders[order.ID] = order
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) addRazorpayRefund(w http.ResponseWriter, r *http.Request) {
	var refund fakeRazorpayRefund
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&refund); err != nil {
		http.Error(w, "invalid refund JSON", http.StatusBadRequest)
		return
	}
	if refund.ID == "" || refund.PaymentID == "" || refund.Amount <= 0 || len(refund.Currency) != 3 {
		http.Error(w, "refund fields are required", http.StatusBadRequest)
		return
	}
	if refund.Status != "pending" && refund.Status != "processed" && refund.Status != "failed" {
		http.Error(w, "invalid refund status", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for _, payments := range s.razorpayPayments {
		for _, payment := range payments {
			if payment.ID == refund.PaymentID {
				found = true
				break
			}
		}
	}
	if !found {
		http.Error(w, "payment not found", http.StatusNotFound)
		return
	}
	if refund.Entity == "" {
		refund.Entity = "refund"
	}
	if refund.CreatedAt == 0 {
		refund.CreatedAt = time.Now().UTC().Unix()
	}
	s.razorpayRefunds[refund.PaymentID] = append(s.razorpayRefunds[refund.PaymentID], refund)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) setRazorpayBehavior(w http.ResponseWriter, r *http.Request) {
	var behavior fakeRazorpayBehavior
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&behavior); err != nil {
		http.Error(w, "invalid behavior JSON", http.StatusBadRequest)
		return
	}
	if behavior.Status != 0 && (behavior.Status < 400 || behavior.Status > 599) {
		http.Error(w, "status must be zero or an error status", http.StatusBadRequest)
		return
	}
	if behavior.DelayMS < 0 || behavior.DelayMS > 10_000 {
		http.Error(w, "delay_ms must be between 0 and 10000", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.razorpayBehavior = behavior
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func validRazorpayAuth(r *http.Request) bool {
	keyID, keySecret, ok := r.BasicAuth()
	return ok && keyID == "rzp_test_billmesh" && keySecret == "test-provider-secret"
}

func validRazorpayReceipt(receipt string) bool {
	if len(receipt) == 0 || len(receipt) > 40 {
		return false
	}
	for _, value := range []byte(receipt) {
		if value < 0x20 || value > 0x7e {
			return false
		}
	}
	return true
}
func (s *server) receiver(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	raw, _ := io.ReadAll(r.Body)
	var payload any
	_ = json.NewDecoder(bytes.NewReader(raw)).Decode(&payload)
	s.mu.Lock()
	code := s.failureCode
	delay := s.receiverDelay
	s.webhooks = append(s.webhooks, map[string]any{
		"app": r.PathValue("app"), "event_id": r.Header.Get("X-Billmesh-Event-ID"), "event_type": r.Header.Get("X-Billmesh-Event-Type"),
		"signature": r.Header.Get("X-Billmesh-Signature"), "previous_signature": r.Header.Get("X-Billmesh-Previous-Signature"),
		"timestamp": r.Header.Get("X-Billmesh-Timestamp"), "raw_body": string(raw), "payload": payload,
	})
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
