package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseDelegationClientRegistryBindsAuthorizerAndActor(t *testing.T) {
	registry, err := ParseDelegationClientRegistry(`[
		{"authorizer_client_id":"daybook-authorizer","actor_client_id":"daybook-billing-user","scope":"billmesh.billing","type":"billing","actor_type":"user","app":"daybook","environment":"production"},
		{"authorizer_client_id":"daybook-authorizer","actor_client_id":"daybook-billing-service","scope":"billmesh.billing","type":"billing","actor_type":"service","app":"daybook","environment":"production"}
	]`)
	require.NoError(t, err)
	require.Len(t, registry, 2)

	claims := &Claims{AuthorizerClientID: "daybook-authorizer", ClientID: "daybook-billing-user"}
	require.NoError(t, registry.authenticate(claims, "billmesh.billing"))
	require.Equal(t, ClientBilling, claims.ClientType)
	require.Equal(t, "user", claims.ActorType)
	require.Equal(t, "daybook", claims.App)
}

func TestDelegationClientRegistryRejectsPairAndScopeConfusion(t *testing.T) {
	registry, err := ParseDelegationClientRegistry(`[{"authorizer_client_id":"daybook-authorizer","actor_client_id":"daybook-runtime","scope":"billmesh.runtime","type":"runtime","actor_type":"service","app":"daybook","environment":"production"}]`)
	require.NoError(t, err)

	require.Error(t, registry.authenticate(&Claims{AuthorizerClientID: "taskmesh-authorizer", ClientID: "daybook-runtime"}, "billmesh.runtime"))
	require.Error(t, registry.authenticate(&Claims{AuthorizerClientID: "daybook-authorizer", ClientID: "daybook-runtime"}, "billmesh.billing"))
	require.Error(t, registry.authenticate(&Claims{AuthorizerClientID: "daybook-authorizer", ClientID: "daybook-runtime", App: "taskmesh"}, "billmesh.runtime"))
	require.Error(t, registry.authenticate(&Claims{AuthorizerClientID: "daybook-authorizer", ClientID: "daybook-runtime", Environment: "staging"}, "billmesh.runtime"))
}

func TestParseDelegationClientRegistryRejectsInvalidDefinitions(t *testing.T) {
	tests := []string{
		`[]`,
		`[{"authorizer_client_id":"a","actor_client_id":"b","scope":"billmesh.billing","type":"billing","actor_type":"robot","app":"daybook","environment":"production"}]`,
		`[{"authorizer_client_id":"a","actor_client_id":"b","scope":"billmesh.admin","type":"billing","actor_type":"user","app":"daybook","environment":"production"}]`,
		`[{"authorizer_client_id":"a","actor_client_id":"b","scope":"billmesh.runtime","type":"runtime","actor_type":"user","app":"daybook","environment":"production"}]`,
		`[{"authorizer_client_id":"a","actor_client_id":"b","scope":"billmesh.admin","type":"admin","actor_type":"service","app":"*","environment":"production"}]`,
		`[{"authorizer_client_id":"a","actor_client_id":"b","scope":"billmesh.billing","type":"billing","actor_type":"user","app":"daybook","environment":"production","unexpected":true}]`,
		`[{"authorizer_client_id":"a","actor_client_id":"b","scope":"billmesh.billing","type":"billing","actor_type":"user","app":"daybook","environment":"production"},{"authorizer_client_id":"a","actor_client_id":"b","scope":"billmesh.runtime","type":"runtime","actor_type":"service","app":"daybook","environment":"production"}]`,
	}
	for _, raw := range tests {
		_, err := ParseDelegationClientRegistry(raw)
		require.Error(t, err)
	}
}
