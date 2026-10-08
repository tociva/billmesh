package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientProfileCannotExpandToAnotherSurface(t *testing.T) {
	claims := &Claims{ClientType: ClientCatalogue}
	require.True(t, claims.IsClient(ClientCatalogue))
	require.False(t, claims.IsClient(ClientBilling, ClientRuntime, ClientAdmin))
}
