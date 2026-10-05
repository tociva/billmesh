package app

import (
	"time"

	"github.com/google/uuid"

	"github.com/tociva/billmesh/internal/products"
)

type checkoutClientConfig struct {
	PublicKey   string `json:"public_key"`
	OrderID     string `json:"order_id"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

type checkoutResponse struct {
	Provider     string                `json:"provider"`
	Presentation string                `json:"presentation"`
	CheckoutURL  *string               `json:"checkout_url"`
	ClientConfig *checkoutClientConfig `json:"client_config"`
	ExpiresAt    *time.Time            `json:"expires_at"`
	SuccessURL   *string               `json:"success_url"`
	CancelURL    *string               `json:"cancel_url"`
}

type paymentOrderResponse struct {
	PaymentID    uuid.UUID        `json:"payment_id"`
	CreditPackID uuid.UUID        `json:"credit_pack_id"`
	Credits      int64            `json:"credits"`
	Checkout     checkoutResponse `json:"checkout"`
}

type paymentResponse struct {
	ID                uuid.UUID `json:"id"`
	Provider          string    `json:"provider"`
	ProviderOrderID   *string   `json:"provider_order_id"`
	ProviderPaymentID *string   `json:"provider_payment_id"`
	Status            string    `json:"status"`
	AmountMinor       int64     `json:"amount_minor"`
	Currency          string    `json:"currency"`
	CreatedAt         time.Time `json:"created_at"`
}

type paymentListResponse struct {
	Items      []paymentResponse `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

type invoiceResponse struct {
	ID            uuid.UUID  `json:"id"`
	InvoiceNumber string     `json:"invoice_number"`
	Status        string     `json:"status"`
	Currency      string     `json:"currency"`
	TotalMinor    int64      `json:"total_minor"`
	FinalizedAt   *time.Time `json:"finalized_at"`
	PaidAt        *time.Time `json:"paid_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

type invoiceListResponse struct {
	Items      []invoiceResponse `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

type subscriptionTransitionResponse struct {
	ID                   uuid.UUID         `json:"id"`
	SubscriptionID       *uuid.UUID        `json:"subscription_id"`
	PlanID               uuid.UUID         `json:"plan_id"`
	PlanName             string            `json:"plan_name"`
	Operation            string            `json:"operation"`
	Effective            string            `json:"effective"`
	EffectiveAt          *time.Time        `json:"effective_at"`
	Status               string            `json:"status"`
	BillingPolicyVersion int64             `json:"billing_policy_version"`
	FailureCode          *string           `json:"failure_code"`
	PaymentID            *uuid.UUID        `json:"payment_id"`
	Revision             int64             `json:"revision"`
	SnapshotURL          string            `json:"snapshot_url"`
	Checkout             *checkoutResponse `json:"checkout"`
}

type billingSnapshotAccount struct {
	ID         uuid.UUID  `json:"id"`
	CustomerID *uuid.UUID `json:"customer_id"`
	Name       string     `json:"name"`
}

type billingSnapshotEffectivePlan struct {
	ID                       uuid.UUID                  `json:"id"`
	Version                  int64                      `json:"version"`
	Name                     string                     `json:"name"`
	Description              string                     `json:"description"`
	BillingModel             string                     `json:"billing_model"`
	PriceMinor               int64                      `json:"price_minor"`
	Currency                 string                     `json:"currency"`
	BillingInterval          string                     `json:"billing_interval"`
	IncludedCredits          int64                      `json:"included_credits"`
	EntitlementSchemaVersion int64                      `json:"entitlement_schema_version"`
	EntitlementSchema        products.EntitlementSchema `json:"entitlement_schema"`
	Entitlements             map[string]any             `json:"entitlements"`
}

type billingSnapshotSubscription struct {
	ID                   uuid.UUID                    `json:"id"`
	Status               string                       `json:"status"`
	CurrentPeriodStart   *time.Time                   `json:"current_period_start"`
	CurrentPeriodEnd     *time.Time                   `json:"current_period_end"`
	CancelAtPeriodEnd    bool                         `json:"cancel_at_period_end"`
	BillingPolicy        products.BillingPolicy       `json:"billing_policy"`
	BillingPolicyVersion int64                        `json:"billing_policy_version"`
	EffectivePlan        billingSnapshotEffectivePlan `json:"effective_plan"`
}

type billingSnapshotCreditBalance struct {
	WalletID     uuid.UUID `json:"wallet_id"`
	Available    int64     `json:"available"`
	Reserved     int64     `json:"reserved"`
	TotalGranted int64     `json:"total_granted"`
}

type billingSnapshotResourceLimit struct {
	Key         string `json:"key"`
	Resource    string `json:"resource"`
	Unit        string `json:"unit"`
	Enforcement string `json:"enforcement"`
	Value       any    `json:"value"`
}

type billingSnapshotPendingTransition struct {
	ID                   uuid.UUID  `json:"id"`
	TargetPlanID         uuid.UUID  `json:"target_plan_id"`
	Status               string     `json:"status"`
	Operation            string     `json:"operation"`
	Effective            string     `json:"effective"`
	EffectiveAt          *time.Time `json:"effective_at"`
	CheckoutExpiresAt    *time.Time `json:"checkout_expires_at"`
	BillingPolicyVersion int64      `json:"billing_policy_version"`
}

type billingSnapshotProductPolicy struct {
	Version int64                  `json:"version"`
	Policy  products.BillingPolicy `json:"policy"`
}

type billingSnapshotResponse struct {
	Revision          int64                             `json:"revision"`
	GeneratedAt       time.Time                         `json:"generated_at"`
	EffectiveAt       time.Time                         `json:"effective_at"`
	VerifiedAt        time.Time                         `json:"verified_at"`
	ExpiresAt         time.Time                         `json:"expires_at"`
	DegradedUntil     *time.Time                        `json:"degraded_until"`
	ProductPolicy     billingSnapshotProductPolicy      `json:"product_policy"`
	Account           billingSnapshotAccount            `json:"account"`
	Subscription      *billingSnapshotSubscription      `json:"subscription"`
	Entitlements      map[string]any                    `json:"entitlements"`
	CreditBalance     *billingSnapshotCreditBalance     `json:"credit_balance"`
	ResourceLimits    []billingSnapshotResourceLimit    `json:"resource_limits"`
	PendingTransition *billingSnapshotPendingTransition `json:"pending_transition"`
}

type ownershipTransferResponse struct {
	ID             uuid.UUID  `json:"id"`
	Status         string     `json:"status"`
	DecisionCode   string     `json:"decision_code"`
	RequiredAction string     `json:"required_action"`
	BaseRevision   int64      `json:"base_revision"`
	ExpiresAt      time.Time  `json:"expires_at"`
	ConfirmedAt    *time.Time `json:"confirmed_at"`
	CancelledAt    *time.Time `json:"cancelled_at"`
}
