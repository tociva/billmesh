package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseClientRegistryAndAuthenticate(t *testing.T) {
	registry, err := ParseClientRegistry(`[
		{"client_id":"daybook-catalogue","type":"catalogue","app":"daybook","environment":"production"},
		{"client_id":"daybook-billing","type":"billing","app":"daybook","environment":"production"},
		{"client_id":"daybook-runtime","type":"runtime","app":"daybook","environment":"production"}
	]`)
	require.NoError(t, err)

	claims := &Claims{ClientID: "daybook-billing", App: "daybook", Environment: "production"}
	require.NoError(t, registry.authenticate(claims))
	require.Equal(t, ClientBilling, claims.ClientType)

	for name, candidate := range map[string]*Claims{
		"missing client":    {App: "daybook", Environment: "production"},
		"unknown client":    {ClientID: "unknown", App: "daybook", Environment: "production"},
		"wrong application": {ClientID: "daybook-billing", App: "taskmesh", Environment: "production"},
		"wrong environment": {ClientID: "daybook-billing", App: "daybook", Environment: "staging"},
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, registry.authenticate(candidate))
		})
	}
}

func TestParseClientRegistryRejectsUnsafeConfiguration(t *testing.T) {
	for name, raw := range map[string]string{
		"missing":         ``,
		"empty":           `[]`,
		"duplicate":       `[{"client_id":"same","type":"billing","app":"daybook","environment":"production"},{"client_id":"same","type":"runtime","app":"daybook","environment":"production"}]`,
		"unknown type":    `[{"client_id":"client","type":"superuser","app":"daybook","environment":"production"}]`,
		"missing context": `[{"client_id":"client","type":"billing","app":"","environment":"production"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseClientRegistry(raw)
			require.Error(t, err)
		})
	}
}

func TestClientProfileCannotExpandToAnotherSurface(t *testing.T) {
	claims := &Claims{ClientType: ClientCatalogue}
	require.True(t, claims.IsClient(ClientCatalogue))
	require.False(t, claims.IsClient(ClientBilling, ClientRuntime, ClientAdmin))
}
