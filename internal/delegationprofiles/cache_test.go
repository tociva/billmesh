package delegationprofiles

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tociva/billmesh/internal/auth"
)

type loaderFunc func(context.Context) ([]auth.DelegationClientRegistration, error)

func (f loaderFunc) Load(ctx context.Context) ([]auth.DelegationClientRegistration, error) {
	return f(ctx)
}

func billingProfile(actor string) auth.DelegationClientRegistration {
	return auth.DelegationClientRegistration{
		AuthorizerClientID: "daybook-authorizer", ActorClientID: actor,
		Scope: "billmesh.billing", Type: auth.ClientBilling, ActorType: "user",
		App: "daybook", Environment: "production", ContextProfileVersion: 1,
	}
}

func TestCachedResolverRefreshesAtomicallyAndFailsClosedWhenStale(t *testing.T) {
	now := time.Now()
	profiles := []auth.DelegationClientRegistration{billingProfile("billing-v1")}
	resolver, err := NewCachedResolver(loaderFunc(func(context.Context) ([]auth.DelegationClientRegistration, error) {
		return profiles, nil
	}), time.Second, time.Minute)
	require.NoError(t, err)
	resolver.now = func() time.Time { return now }
	require.NoError(t, resolver.Refresh(context.Background()))

	claims := &auth.Claims{AuthorizerClientID: "daybook-authorizer", ClientID: "billing-v1"}
	require.NoError(t, resolver.Authenticate(claims, "billmesh.billing", 1))

	profiles = []auth.DelegationClientRegistration{billingProfile("billing-v2")}
	require.NoError(t, resolver.Refresh(context.Background()))
	require.Error(t, resolver.Authenticate(&auth.Claims{AuthorizerClientID: "daybook-authorizer", ClientID: "billing-v1"}, "billmesh.billing", 1))
	require.NoError(t, resolver.Authenticate(&auth.Claims{AuthorizerClientID: "daybook-authorizer", ClientID: "billing-v2"}, "billmesh.billing", 1))

	now = now.Add(time.Minute + time.Second)
	require.ErrorIs(t, resolver.Authenticate(&auth.Claims{AuthorizerClientID: "daybook-authorizer", ClientID: "billing-v2"}, "billmesh.billing", 1), auth.ErrDelegationPolicyUnavailable)
}

func TestCachedResolverRetainsLastKnownGoodSnapshotAfterRefreshFailure(t *testing.T) {
	fail := false
	profiles := []auth.DelegationClientRegistration{billingProfile("billing")}
	resolver, err := NewCachedResolver(loaderFunc(func(context.Context) ([]auth.DelegationClientRegistration, error) {
		if fail {
			return nil, errors.New("database unavailable")
		}
		return profiles, nil
	}), time.Second, time.Minute)
	require.NoError(t, err)
	require.NoError(t, resolver.Refresh(context.Background()))
	fail = true
	require.Error(t, resolver.Refresh(context.Background()))
	require.NoError(t, resolver.Authenticate(&auth.Claims{AuthorizerClientID: "daybook-authorizer", ClientID: "billing"}, "billmesh.billing", 1))

	fail = false
	profiles = []auth.DelegationClientRegistration{billingProfile("duplicate"), billingProfile("duplicate")}
	require.Error(t, resolver.Refresh(context.Background()))
	require.NoError(t, resolver.Authenticate(&auth.Claims{AuthorizerClientID: "daybook-authorizer", ClientID: "billing"}, "billmesh.billing", 1))
}

func TestCachedResolverRejectsInvalidOrEmptySnapshots(t *testing.T) {
	for name, profiles := range map[string][]auth.DelegationClientRegistration{
		"empty":   nil,
		"invalid": {billingProfile("billing"), billingProfile("billing")},
	} {
		t.Run(name, func(t *testing.T) {
			resolver, err := NewCachedResolver(loaderFunc(func(context.Context) ([]auth.DelegationClientRegistration, error) {
				return profiles, nil
			}), time.Second, time.Minute)
			require.NoError(t, err)
			require.Error(t, resolver.Refresh(context.Background()))
			require.ErrorIs(t, resolver.Authenticate(&auth.Claims{}, "billmesh.billing", 1), auth.ErrDelegationPolicyUnavailable)
		})
	}
}
