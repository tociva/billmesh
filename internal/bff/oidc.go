package bff

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tociva/billmesh/internal/config"
)

type providerMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RevocationEndpoint    string `json:"revocation_endpoint"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
}

type oidcClient struct {
	config config.BFFConfig
	http   *http.Client
	mu     sync.Mutex
	meta   *providerMetadata
}

func newOIDCClient(cfg config.BFFConfig, client *http.Client) *oidcClient {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &oidcClient{config: cfg, http: client}
}

func (o *oidcClient) metadata(ctx context.Context) (*providerMetadata, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.meta != nil {
		return o.meta, nil
	}
	discoveryURL := strings.TrimRight(o.config.Issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discover OIDC provider: status %d", resp.StatusCode)
	}
	var meta providerMetadata
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&meta); err != nil {
		return nil, fmt.Errorf("decode OIDC discovery: %w", err)
	}
	if strings.TrimRight(meta.Issuer, "/") != strings.TrimRight(o.config.Issuer, "/") {
		return nil, errors.New("OIDC discovery issuer mismatch")
	}
	if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
		return nil, errors.New("OIDC discovery is missing required endpoints")
	}
	for name, endpoint := range map[string]string{
		"authorization": meta.AuthorizationEndpoint, "token": meta.TokenEndpoint,
		"revocation": meta.RevocationEndpoint, "end-session": meta.EndSessionEndpoint,
	} {
		if endpoint == "" {
			continue
		}
		if _, err := parseSecureURL(endpoint, "OIDC "+name+" endpoint", o.config.AllowInsecureHTTP); err != nil {
			return nil, err
		}
	}
	o.meta = &meta
	return o.meta, nil
}

func (o *oidcClient) authorizationURL(ctx context.Context, transaction loginTransaction) (string, error) {
	meta, err := o.metadata(ctx)
	if err != nil {
		return "", err
	}
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {o.config.ClientID},
		"redirect_uri":          {o.config.RedirectURI},
		"scope":                 {o.config.Scope},
		"state":                 {transaction.State},
		"nonce":                 {transaction.Nonce},
		"code_challenge":        {base64SHA256(transaction.CodeVerifier)},
		"code_challenge_method": {"S256"},
	}
	if o.config.Audience != "" {
		params.Set("audience", o.config.Audience)
	}
	return meta.AuthorizationEndpoint + "?" + params.Encode(), nil
}

func (o *oidcClient) exchange(ctx context.Context, code, verifier string) (tokenSet, error) {
	return o.tokenRequest(ctx, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {o.config.RedirectURI}, "code_verifier": {verifier},
	})
}

func (o *oidcClient) refresh(ctx context.Context, refreshToken string) (tokenSet, error) {
	return o.tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}})
}

func (o *oidcClient) tokenRequest(ctx context.Context, values url.Values) (tokenSet, error) {
	meta, err := o.metadata(ctx)
	if err != nil {
		return tokenSet{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.TokenEndpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return tokenSet{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(o.config.ClientID, o.config.ClientSecret)
	resp, err := o.http.Do(req)
	if err != nil {
		return tokenSet{}, fmt.Errorf("OIDC token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return tokenSet{}, fmt.Errorf("OIDC token request: status %d", resp.StatusCode)
	}
	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return tokenSet{}, fmt.Errorf("decode OIDC token response: %w", err)
	}
	if body.AccessToken == "" || !strings.EqualFold(body.TokenType, "Bearer") {
		return tokenSet{}, errors.New("OIDC token response is missing a bearer access token")
	}
	return tokenSet{AccessToken: body.AccessToken, RefreshToken: body.RefreshToken, IDToken: body.IDToken}, nil
}

func (o *oidcClient) revoke(ctx context.Context, refreshToken string) error {
	meta, err := o.metadata(ctx)
	if err != nil || meta.RevocationEndpoint == "" {
		return err
	}
	values := url.Values{"token": {refreshToken}, "token_type_hint": {"refresh_token"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.RevocationEndpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(o.config.ClientID, o.config.ClientSecret)
	resp, err := o.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OIDC revocation: status %d", resp.StatusCode)
	}
	return nil
}

func (o *oidcClient) providerLogoutURL(ctx context.Context, idToken string) (string, error) {
	meta, err := o.metadata(ctx)
	if err != nil {
		return "", err
	}
	endpoint := meta.EndSessionEndpoint
	if endpoint == "" {
		endpoint = o.config.StandaloneLogoutURI
	}
	if endpoint == "" {
		return o.config.PostLogoutRedirectURI, nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("client_id", o.config.ClientID)
	query.Set("post_logout_redirect_uri", o.config.PostLogoutRedirectURI)
	if idToken != "" {
		query.Set("id_token_hint", idToken)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func base64SHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
