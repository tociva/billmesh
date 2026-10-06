package products

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

const BillingPolicySchemaVersion = 1

type BillingPolicy struct {
	SchemaVersion int                `json:"schema_version"`
	Customer      CustomerPolicy     `json:"customer"`
	Onboarding    OnboardingPolicy   `json:"onboarding"`
	Catalogue     CataloguePolicy    `json:"catalogue"`
	Lifecycle     SubscriptionPolicy `json:"lifecycle"`
	Projection    ProjectionPolicy   `json:"projection"`
	Checkout      CheckoutPolicy     `json:"checkout"`
}

type CustomerPolicy struct {
	Scope                 string `json:"scope"`
	FreeAllowance         int    `json:"free_allowance"`
	OwnershipChange       string `json:"ownership_change"`
	OwnershipTransfer     string `json:"ownership_transfer"`
	IneligibleOwnerAction string `json:"ineligible_owner_action"`
}

type OnboardingPolicy struct {
	AllowWithoutSubscription bool   `json:"allow_without_subscription"`
	InitialPlan              string `json:"initial_plan"`
	IneligibleAction         string `json:"ineligible_action"`
	DeletionRetention        string `json:"deletion_retention"`
}

type CataloguePolicy struct {
	Access                string   `json:"access"`
	RequiredBeforeAccount bool     `json:"required_before_account"`
	PresentationFields    []string `json:"presentation_fields"`
	TrialEnabled          bool     `json:"trial_enabled"`
	TrialDays             int      `json:"trial_days"`
	TrialConversion       string   `json:"trial_conversion"`
}

type SubscriptionPolicy struct {
	FreeToPaid                string `json:"free_to_paid"`
	PaidToPaid                string `json:"paid_to_paid"`
	Downgrade                 string `json:"downgrade"`
	CancellationDefault       string `json:"cancellation_default"`
	AllowImmediateCancel      bool   `json:"allow_immediate_cancel"`
	ImmediateCancelRefund     string `json:"immediate_cancel_refund"`
	AllowCancellationWithdraw bool   `json:"allow_cancellation_withdraw"`
	Reactivation              string `json:"reactivation"`
	OverLimit                 string `json:"over_limit"`
	Renewal                   string `json:"renewal"`
	GracePeriodDays           int    `json:"grace_period_days"`
	Dunning                   string `json:"dunning"`
	Expiration                string `json:"expiration"`
	RefundEntitlements        string `json:"refund_entitlements"`
	ChargebackEntitlements    string `json:"chargeback_entitlements"`
}

type ProjectionPolicy struct {
	FreshSeconds          int      `json:"fresh_seconds"`
	DegradedSeconds       int      `json:"degraded_seconds"`
	FailClosedOperations  []string `json:"fail_closed_operations"`
	RefreshSeconds        int      `json:"refresh_seconds"`
	ReconciliationSeconds int      `json:"reconciliation_seconds"`
}

type CheckoutPolicy struct {
	AllowedRedirectOrigins []string `json:"allowed_redirect_origins"`
	Presentation           string   `json:"presentation"`
	RecurringMandate       bool     `json:"recurring_mandate"`
	Confirmation           string   `json:"confirmation"`
}

type BillingPolicyMetadata struct {
	SchemaVersion int                 `json:"schema_version"`
	Defaults      BillingPolicy       `json:"defaults"`
	Options       map[string][]string `json:"options"`
	Constraints   map[string]any      `json:"constraints"`
}

func DefaultBillingPolicy() BillingPolicy {
	return BillingPolicy{
		SchemaVersion: BillingPolicySchemaVersion,
		Customer: CustomerPolicy{
			Scope: "identity", FreeAllowance: 1, OwnershipChange: "retain",
			OwnershipTransfer: "unsupported", IneligibleOwnerAction: "require_paid_checkout",
		},
		Onboarding: OnboardingPolicy{
			AllowWithoutSubscription: true, InitialPlan: "explicit_transition",
			IneligibleAction: "require_paid_checkout", DeletionRetention: "retain",
		},
		Catalogue: CataloguePolicy{
			Access: "application_token", PresentationFields: []string{"description", "price", "entitlements", "availability"},
			TrialConversion: "expire",
		},
		Lifecycle: SubscriptionPolicy{
			FreeToPaid: "immediate_after_capture", PaidToPaid: "checkout", Downgrade: "period_end",
			CancellationDefault: "period_end", AllowImmediateCancel: true, ImmediateCancelRefund: "none",
			Reactivation: "new_transition", OverLimit: "block_new", Renewal: "provider_event", GracePeriodDays: 3,
			Dunning: "grace_period", Expiration: "cancel", RefundEntitlements: "revoke",
			ChargebackEntitlements: "revoke",
		},
		Projection: ProjectionPolicy{
			FreshSeconds: 300, DegradedSeconds: 0,
			FailClosedOperations: []string{"subscription_change", "credit_purchase", "limit_increase"},
			RefreshSeconds:       60, ReconciliationSeconds: 300,
		},
		Checkout: CheckoutPolicy{
			AllowedRedirectOrigins: []string{},
			Presentation:           "modal",
			Confirmation:           "webhook",
		},
	}
}

func BillingPolicyContract() BillingPolicyMetadata {
	return BillingPolicyMetadata{
		SchemaVersion: BillingPolicySchemaVersion,
		Defaults:      DefaultBillingPolicy(),
		Options: map[string][]string{
			"customer.scope":                    {"identity", "organization", "external_customer"},
			"customer.ownership_change":         {"retain", "reevaluate"},
			"customer.ownership_transfer":       {"unsupported", "retain", "preauthorized_recheck"},
			"customer.ineligible_owner_action":  {"reject", "require_paid_checkout"},
			"onboarding.initial_plan":           {"automatic_default", "explicit_transition"},
			"onboarding.ineligible_action":      {"reject", "restricted", "require_paid_checkout"},
			"onboarding.deletion_retention":     {"retain", "anonymize"},
			"catalogue.access":                  {"public", "application_token"},
			"catalogue.trial_conversion":        {"expire", "require_checkout", "automatic_mandate"},
			"lifecycle.free_to_paid":            {"immediate_after_capture"},
			"lifecycle.paid_to_paid":            {"checkout", "mandate_proration", "period_end"},
			"lifecycle.downgrade":               {"period_end"},
			"lifecycle.cancellation_default":    {"period_end", "immediate"},
			"lifecycle.immediate_cancel_refund": {"none", "prorated", "full"},
			"lifecycle.reactivation":            {"resume", "new_transition"},
			"lifecycle.over_limit":              {"block_new", "grace_period", "reject_transition"},
			"lifecycle.renewal":                 {"provider_event", "manual"},
			"lifecycle.dunning":                 {"none", "grace_period"},
			"lifecycle.expiration":              {"cancel", "downgrade_to_default"},
			"lifecycle.refund_entitlements":     {"retain", "revoke"},
			"lifecycle.chargeback_entitlements": {"retain", "revoke"},
			"checkout.presentation":             {"inline", "modal", "provider_hosted"},
			"checkout.confirmation":             {"webhook", "poll_and_webhook"},
		},
		Constraints: map[string]any{
			"customer.free_allowance":           map[string]int{"minimum": 0, "maximum": 1000000},
			"catalogue.trial_days":              map[string]int{"minimum": 0, "maximum": 3650},
			"lifecycle.grace_period_days":       map[string]int{"minimum": 0, "maximum": 365},
			"projection.seconds":                map[string]int{"minimum": 0, "maximum": 2592000},
			"checkout.allowed_redirect_origins": map[string]int{"maximum_items": 50},
		},
	}
}

func ValidateBillingPolicy(p BillingPolicy) error {
	if p.SchemaVersion != BillingPolicySchemaVersion {
		return fmt.Errorf("billing policy schema_version must be %d", BillingPolicySchemaVersion)
	}
	contract := BillingPolicyContract()
	checks := []struct {
		path, value string
	}{
		{"customer.scope", p.Customer.Scope},
		{"customer.ownership_change", p.Customer.OwnershipChange},
		{"customer.ownership_transfer", p.Customer.OwnershipTransfer},
		{"customer.ineligible_owner_action", p.Customer.IneligibleOwnerAction},
		{"onboarding.initial_plan", p.Onboarding.InitialPlan},
		{"onboarding.ineligible_action", p.Onboarding.IneligibleAction},
		{"onboarding.deletion_retention", p.Onboarding.DeletionRetention},
		{"catalogue.access", p.Catalogue.Access},
		{"catalogue.trial_conversion", p.Catalogue.TrialConversion},
		{"lifecycle.free_to_paid", p.Lifecycle.FreeToPaid},
		{"lifecycle.paid_to_paid", p.Lifecycle.PaidToPaid},
		{"lifecycle.downgrade", p.Lifecycle.Downgrade},
		{"lifecycle.cancellation_default", p.Lifecycle.CancellationDefault},
		{"lifecycle.immediate_cancel_refund", p.Lifecycle.ImmediateCancelRefund},
		{"lifecycle.reactivation", p.Lifecycle.Reactivation},
		{"lifecycle.over_limit", p.Lifecycle.OverLimit},
		{"lifecycle.renewal", p.Lifecycle.Renewal},
		{"lifecycle.dunning", p.Lifecycle.Dunning},
		{"lifecycle.expiration", p.Lifecycle.Expiration},
		{"lifecycle.refund_entitlements", p.Lifecycle.RefundEntitlements},
		{"lifecycle.chargeback_entitlements", p.Lifecycle.ChargebackEntitlements},
		{"checkout.presentation", p.Checkout.Presentation},
		{"checkout.confirmation", p.Checkout.Confirmation},
	}
	for _, check := range checks {
		if !slices.Contains(contract.Options[check.path], check.value) {
			return fmt.Errorf("billing policy %s has unsupported value %q", check.path, check.value)
		}
	}
	if p.Customer.FreeAllowance < 0 || p.Customer.FreeAllowance > 1_000_000 {
		return errors.New("billing policy customer.free_allowance must be between 0 and 1000000")
	}
	if p.Catalogue.TrialDays < 0 || p.Catalogue.TrialDays > 3650 {
		return errors.New("billing policy catalogue.trial_days must be between 0 and 3650")
	}
	if p.Catalogue.TrialEnabled && p.Catalogue.TrialDays == 0 || !p.Catalogue.TrialEnabled && p.Catalogue.TrialDays != 0 {
		return errors.New("billing policy catalogue.trial_days must be positive only when trials are enabled")
	}
	if p.Lifecycle.GracePeriodDays < 0 || p.Lifecycle.GracePeriodDays > 365 {
		return errors.New("billing policy lifecycle.grace_period_days must be between 0 and 365")
	}
	if p.Lifecycle.Dunning == "grace_period" && p.Lifecycle.GracePeriodDays == 0 {
		return errors.New("billing policy lifecycle.grace_period_days is required for grace-period dunning")
	}
	if p.Lifecycle.CancellationDefault == "immediate" && !p.Lifecycle.AllowImmediateCancel {
		return errors.New("billing policy cannot default to immediate cancellation when immediate cancellation is disabled")
	}
	if p.Checkout.RecurringMandate && p.Lifecycle.PaidToPaid != "mandate_proration" && p.Catalogue.TrialConversion != "automatic_mandate" {
		return errors.New("billing policy recurring mandates require mandate proration or automatic trial conversion")
	}
	if (p.Lifecycle.PaidToPaid == "mandate_proration" || p.Catalogue.TrialConversion == "automatic_mandate") && !p.Checkout.RecurringMandate {
		return errors.New("billing policy mandate-based behavior requires recurring mandates")
	}
	for _, seconds := range []int{p.Projection.FreshSeconds, p.Projection.DegradedSeconds, p.Projection.RefreshSeconds, p.Projection.ReconciliationSeconds} {
		if seconds < 0 || seconds > 2_592_000 {
			return errors.New("billing policy projection durations must be between 0 and 2592000 seconds")
		}
	}
	if p.Projection.RefreshSeconds == 0 || p.Projection.ReconciliationSeconds == 0 {
		return errors.New("billing policy refresh and reconciliation intervals must be positive")
	}
	if p.Projection.DegradedSeconds > 0 && p.Projection.DegradedSeconds < p.Projection.FreshSeconds {
		return errors.New("billing policy degraded duration cannot be shorter than fresh duration")
	}
	if err := uniqueAllowed(p.Catalogue.PresentationFields, []string{"description", "price", "entitlements", "availability", "effective_dates"}, "catalogue.presentation_fields"); err != nil {
		return err
	}
	if err := uniqueAllowed(p.Projection.FailClosedOperations, []string{"account_create", "subscription_change", "credit_purchase", "resource_create", "limit_increase"}, "projection.fail_closed_operations"); err != nil {
		return err
	}
	if len(p.Checkout.AllowedRedirectOrigins) > 50 {
		return errors.New("billing policy checkout.allowed_redirect_origins cannot contain more than 50 origins")
	}
	seenOrigins := map[string]bool{}
	for _, origin := range p.Checkout.AllowedRedirectOrigins {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return fmt.Errorf("billing policy checkout origin %q is invalid", origin)
		}
		if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1")) {
			return fmt.Errorf("billing policy checkout origin %q must use https", origin)
		}
		normalized := strings.TrimSuffix(origin, "/")
		if seenOrigins[normalized] {
			return fmt.Errorf("billing policy checkout origin %q is duplicated", origin)
		}
		seenOrigins[normalized] = true
	}
	return nil
}

func uniqueAllowed(values, allowed []string, path string) error {
	seen := map[string]bool{}
	for _, value := range values {
		if !slices.Contains(allowed, value) {
			return fmt.Errorf("billing policy %s contains unsupported value %q", path, value)
		}
		if seen[value] {
			return fmt.Errorf("billing policy %s contains duplicate value %q", path, value)
		}
		seen[value] = true
	}
	return nil
}

func (p BillingPolicy) TransitionTiming(operation string) string {
	switch operation {
	case "downgrade":
		return p.Lifecycle.Downgrade
	case "upgrade", "change":
		if p.Lifecycle.PaidToPaid == "period_end" {
			return "period_end"
		}
	}
	return "immediate"
}

func (p BillingPolicy) SnapshotMaxAgeSeconds() int {
	if p.Projection.FreshSeconds <= 0 {
		return 1
	}
	return p.Projection.FreshSeconds
}

func (p BillingPolicy) AllowsCheckoutRedirect(raw string) bool {
	if raw == "" {
		return true
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	origin := parsed.Scheme + "://" + parsed.Host
	for _, allowed := range p.Checkout.AllowedRedirectOrigins {
		if strings.TrimSuffix(allowed, "/") == origin {
			return true
		}
	}
	return false
}
