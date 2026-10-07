package bff

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestTokenExchangeUsesConfidentialClientAndPKCE(t *testing.T) {
	cfg := testConfig()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, request.Method)
		username, password, ok := request.BasicAuth()
		require.True(t, ok)
		require.Equal(t, cfg.ClientID, username)
		require.Equal(t, cfg.ClientSecret, password)
		require.NoError(t, request.ParseForm())
		require.Equal(t, "authorization_code", request.Form.Get("grant_type"))
		require.Equal(t, "verifier", request.Form.Get("code_verifier"))
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"access_token":"access","refresh_token":"refresh","id_token":"id","token_type":"Bearer","scope":"openid profile"}`)),
		}, nil
	})}
	oidc := newOIDCClient(cfg, client)
	oidc.meta = &providerMetadata{Issuer: cfg.Issuer, AuthorizationEndpoint: "https://identity.example/authorize", TokenEndpoint: "https://identity.example/token"}
	tokens, err := oidc.exchange(context.Background(), "code", "verifier")
	require.NoError(t, err)
	require.Equal(t, tokenSet{AccessToken: "access", RefreshToken: "refresh", IDToken: "id"}, tokens)
}

func TestAuthorizationURLContainsOIDCSecurityParameters(t *testing.T) {
	cfg := testConfig()
	oidc := newOIDCClient(cfg, nil)
	oidc.meta = &providerMetadata{Issuer: cfg.Issuer, AuthorizationEndpoint: "https://identity.example/authorize", TokenEndpoint: "https://identity.example/token"}
	raw, err := oidc.authorizationURL(context.Background(), loginTransaction{State: "state", Nonce: "nonce", CodeVerifier: "verifier"})
	require.NoError(t, err)
	require.Contains(t, raw, "code_challenge_method=S256")
	require.Contains(t, raw, "state=state")
	require.Contains(t, raw, "nonce=nonce")
	require.Contains(t, raw, "audience=billmesh")
}
