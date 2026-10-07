package bff

import (
	"context"
	"time"

	"github.com/tociva/billmesh/internal/auth"
)

const (
	schemaVersion      = 2
	csrfHeaderName     = "X-CSRF-Token"
	refreshLease       = time.Minute
	sessionTouchPeriod = 5 * time.Minute
)

type tokenVerifier interface {
	Verify(context.Context, string) (*auth.Claims, error)
	VerifyIDToken(context.Context, string, string, string, string) (*auth.IDTokenClaims, error)
}

type loginTransaction struct {
	SchemaVersion   int       `json:"schema_version"`
	Realm           string    `json:"realm"`
	State           string    `json:"state"`
	Nonce           string    `json:"nonce"`
	CodeVerifier    string    `json:"code_verifier"`
	CorrelationHash []byte    `json:"correlation_hash"`
	ReturnTo        string    `json:"return_to"`
	CreatedAt       time.Time `json:"created_at"`
}

type browserSession struct {
	SchemaVersion  int       `json:"schema_version"`
	Realm          string    `json:"realm"`
	SessionID      string    `json:"session_id"`
	Subject        string    `json:"subject"`
	Email          string    `json:"email,omitempty"`
	Name           string    `json:"name,omitempty"`
	OrgID          string    `json:"org_id"`
	App            string    `json:"app"`
	Environment    string    `json:"environment"`
	CSRFToken      string    `json:"csrf_token"`
	AllowedOrigin  string    `json:"allowed_origin"`
	CreatedAt      time.Time `json:"created_at"`
	AbsoluteExpiry time.Time `json:"absolute_expiry"`
	AccessToken    string    `json:"access_token"`
	AccessExpiry   time.Time `json:"access_expiry"`
	RefreshToken   string    `json:"refresh_token,omitempty"`
	IDToken        string    `json:"id_token,omitempty"`
}

type browserSessionResponse struct {
	Authenticated bool                          `json:"authenticated"`
	User          browserSessionUserResponse    `json:"user"`
	Context       browserSessionContextResponse `json:"context"`
	CSRFToken     string                        `json:"csrfToken"`
	ExpiresAt     time.Time                     `json:"expiresAt"`
}

type browserSessionUserResponse struct {
	Subject string `json:"subject"`
	Email   string `json:"email"`
	Name    string `json:"name"`
}

type browserSessionContextResponse struct {
	OrganizationID string `json:"organization_id"`
	Application    string `json:"application"`
	Environment    string `json:"environment"`
}

func newBrowserSessionResponse(session browserSession) browserSessionResponse {
	return browserSessionResponse{
		Authenticated: true,
		User: browserSessionUserResponse{
			Subject: session.Subject,
			Email:   session.Email,
			Name:    session.Name,
		},
		Context: browserSessionContextResponse{
			OrganizationID: session.OrgID,
			Application:    session.App,
			Environment:    session.Environment,
		},
		CSRFToken: session.CSRFToken,
		ExpiresAt: session.AbsoluteExpiry,
	}
}

type logoutTransaction struct {
	SchemaVersion int       `json:"schema_version"`
	Realm         string    `json:"realm"`
	IDToken       string    `json:"id_token,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type tokenSet struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
}
