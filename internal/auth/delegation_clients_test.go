package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDelegationClientRegistryBindsAuthorizerAndActor(t *testing.T) {
	registry, err := NewDelegationClientRegistry([]DelegationClientRegistration{
		{AuthorizerClientID: "daybook-authorizer", ActorClientID: "daybook-billing-user", Scope: "billmesh.billing", Type: ClientBilling, ActorType: "user", App: "daybook", Environment: "production", ContextProfileVersion: 1},
		{AuthorizerClientID: "daybook-authorizer", ActorClientID: "daybook-billing-service", Scope: "billmesh.billing", Type: ClientBilling, ActorType: "service", App: "daybook", Environment: "production", ContextProfileVersion: 1},
	})
	require.NoError(t, err)
	require.Len(t, registry, 2)

	claims := &Claims{AuthorizerClientID: "daybook-authorizer", ClientID: "daybook-billing-user"}
	require.NoError(t, registry.Authenticate(claims, "billmesh.billing", 1))
	require.Equal(t, ClientBilling, claims.ClientType)
	require.Equal(t, "user", claims.ActorType)
	require.Equal(t, "daybook", claims.App)
}

func TestDelegationClientRegistryRejectsPairAndScopeConfusion(t *testing.T) {
	registry, err := NewDelegationClientRegistry([]DelegationClientRegistration{{
		AuthorizerClientID: "daybook-authorizer", ActorClientID: "daybook-runtime", Scope: "billmesh.runtime",
		Type: ClientRuntime, ActorType: "service", App: "daybook", Environment: "production", ContextProfileVersion: 1,
	}})
	require.NoError(t, err)

	require.Error(t, registry.Authenticate(&Claims{AuthorizerClientID: "taskmesh-authorizer", ClientID: "daybook-runtime"}, "billmesh.runtime", 1))
	require.Error(t, registry.Authenticate(&Claims{AuthorizerClientID: "daybook-authorizer", ClientID: "daybook-runtime"}, "billmesh.billing", 1))
	require.Error(t, registry.Authenticate(&Claims{AuthorizerClientID: "daybook-authorizer", ClientID: "daybook-runtime", App: "taskmesh"}, "billmesh.runtime", 1))
	require.Error(t, registry.Authenticate(&Claims{AuthorizerClientID: "daybook-authorizer", ClientID: "daybook-runtime", Environment: "staging"}, "billmesh.runtime", 1))
	require.Error(t, registry.Authenticate(&Claims{AuthorizerClientID: "daybook-authorizer", ClientID: "daybook-runtime"}, "billmesh.runtime", 2))
}

func TestDelegationClientRegistryRejectsInvalidDefinitions(t *testing.T) {
	valid := DelegationClientRegistration{AuthorizerClientID: "a", ActorClientID: "b", Scope: "billmesh.billing", Type: ClientBilling, ActorType: "user", App: "daybook", Environment: "production", ContextProfileVersion: 1}
	tests := [][]DelegationClientRegistration{
		nil,
		{{AuthorizerClientID: "a", ActorClientID: "b", Scope: "billmesh.billing", Type: ClientBilling, ActorType: "robot", App: "daybook", Environment: "production", ContextProfileVersion: 1}},
		{{AuthorizerClientID: "a", ActorClientID: "b", Scope: "billmesh.admin", Type: ClientBilling, ActorType: "user", App: "daybook", Environment: "production", ContextProfileVersion: 1}},
		{{AuthorizerClientID: "a", ActorClientID: "b", Scope: "billmesh.runtime", Type: ClientRuntime, ActorType: "user", App: "daybook", Environment: "production", ContextProfileVersion: 1}},
		{{AuthorizerClientID: "a", ActorClientID: "b", Scope: "billmesh.admin", Type: ClientAdmin, ActorType: "service", App: "*", Environment: "production", ContextProfileVersion: 1}},
		{{AuthorizerClientID: "a", ActorClientID: "b", Scope: "billmesh.billing", Type: ClientBilling, ActorType: "user", App: "daybook", Environment: "production", ContextProfileVersion: 2}},
		{valid, valid},
	}
	for _, registrations := range tests {
		_, err := NewDelegationClientRegistry(registrations)
		require.Error(t, err)
	}
}
