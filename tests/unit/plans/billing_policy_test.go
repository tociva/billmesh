package plans_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tociva/billmesh/internal/products"
)

func TestDefaultBillingPolicyIsValidAndStable(t *testing.T) {
	policy := products.DefaultBillingPolicy()
	require.NoError(t, products.ValidateBillingPolicy(policy))
	require.Equal(t, products.BillingPolicySchemaVersion, policy.SchemaVersion)
	require.Equal(t, "identity", policy.Customer.Scope)
	require.Equal(t, 1, policy.Customer.FreeAllowance)
	require.Equal(t, "explicit_transition", policy.Onboarding.InitialPlan)
	require.Equal(t, "period_end", policy.Lifecycle.Downgrade)
	require.Equal(t, 300, policy.SnapshotMaxAgeSeconds())
}

func TestBillingPolicyRejectsUnsafeOrIncompatibleConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		change func(*products.BillingPolicy)
		match  string
	}{
		{"unknown schema", func(p *products.BillingPolicy) { p.SchemaVersion = 2 }, "schema_version"},
		{"unknown scope", func(p *products.BillingPolicy) { p.Customer.Scope = "tenant" }, "customer.scope"},
		{"negative allowance", func(p *products.BillingPolicy) { p.Customer.FreeAllowance = -1 }, "free_allowance"},
		{"trial without duration", func(p *products.BillingPolicy) { p.Catalogue.TrialEnabled = true }, "trial_days"},
		{"duration without trial", func(p *products.BillingPolicy) { p.Catalogue.TrialDays = 7 }, "trial_days"},
		{"immediate default disabled", func(p *products.BillingPolicy) {
			p.Lifecycle.CancellationDefault = "immediate"
			p.Lifecycle.AllowImmediateCancel = false
		}, "immediate cancellation"},
		{"dunning without grace", func(p *products.BillingPolicy) {
			p.Lifecycle.Dunning = "grace_period"
			p.Lifecycle.GracePeriodDays = 0
		}, "grace_period_days"},
		{"mandate mode without mandate", func(p *products.BillingPolicy) { p.Lifecycle.PaidToPaid = "mandate_proration" }, "requires recurring mandates"},
		{"short degraded age", func(p *products.BillingPolicy) { p.Projection.DegradedSeconds = 60 }, "degraded duration"},
		{"duplicate presentation field", func(p *products.BillingPolicy) {
			p.Catalogue.PresentationFields = []string{"price", "price"}
		}, "duplicate"},
		{"unsafe redirect", func(p *products.BillingPolicy) {
			p.Checkout.AllowedRedirectOrigins = []string{"http://example.com"}
		}, "https"},
		{"redirect with path", func(p *products.BillingPolicy) {
			p.Checkout.AllowedRedirectOrigins = []string{"https://example.com/checkout"}
		}, "invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := products.DefaultBillingPolicy()
			test.change(&policy)
			require.ErrorContains(t, products.ValidateBillingPolicy(policy), test.match)
		})
	}
}

func TestBillingPolicyTransitionTiming(t *testing.T) {
	policy := products.DefaultBillingPolicy()
	require.Equal(t, "immediate", policy.TransitionTiming("activate"))
	require.Equal(t, "period_end", policy.TransitionTiming("downgrade"))

	policy.Lifecycle.PaidToPaid = "period_end"
	require.Equal(t, "period_end", policy.TransitionTiming("upgrade"))
	require.Equal(t, "period_end", policy.TransitionTiming("change"))
}

func TestBillingPolicyAllowsOnlyConfiguredCheckoutOrigins(t *testing.T) {
	policy := products.DefaultBillingPolicy()
	policy.Checkout.AllowedRedirectOrigins = []string{"https://app.example.test", "http://localhost:4200"}
	require.True(t, policy.AllowsCheckoutRedirect(""))
	require.True(t, policy.AllowsCheckoutRedirect("https://app.example.test/billing/success?transition=1"))
	require.True(t, policy.AllowsCheckoutRedirect("http://localhost:4200/cancel"))
	require.False(t, policy.AllowsCheckoutRedirect("https://evil.example.test/billing/success"))
}

func TestBillingPolicyMetadataContainsEveryConfigurableEnum(t *testing.T) {
	metadata := products.BillingPolicyContract()
	require.NoError(t, products.ValidateBillingPolicy(metadata.Defaults))
	for _, path := range []string{
		"customer.scope", "onboarding.initial_plan", "catalogue.access",
		"lifecycle.paid_to_paid", "lifecycle.cancellation_default",
		"checkout.presentation", "checkout.confirmation",
	} {
		require.NotEmpty(t, metadata.Options[path], path)
	}
}
