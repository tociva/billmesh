package app

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tociva/billmesh/internal/products"
)

func TestDaybookCatalogueV2IsValid(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/catalogues/daybook-catalogue.v2.json")
	require.NoError(t, err)
	var transfer catalogueTransfer
	require.NoError(t, json.Unmarshal(raw, &transfer))

	result := validateCatalogueTransferDocument(transfer, time.Now().UTC())
	require.True(t, result.Valid, "issues: %#v", result.Issues)
	require.Equal(t, 1, result.Products)
	require.Equal(t, 5, result.Plans)
	require.Equal(t, 1, result.CreditPacks)
}

func validCatalogueTransferForTest(version int) catalogueTransfer {
	active, selectable, isDefault, checkout := true, true, true, true
	return catalogueTransfer{
		SchemaVersion: version,
		Products: []catalogueTransferProduct{{
			Slug: "daybook", Name: "Daybook", Description: "Daybook billing catalogue",
			EntitlementSchema: products.EntitlementSchema{Fields: []products.EntitlementField{}},
			BillingPolicy:     products.DefaultBillingPolicy(), Active: &active,
			Plans: []catalogueTransferPlan{{
				Slug: "daybook-free", PlanFamilyID: "free", Name: "Free", Description: "Free plan",
				Currency: "INR", BillingInterval: "monthly", BillingModel: "free", Entitlements: map[string]any{},
				Active: &active, Selectable: &selectable, Default: &isDefault, CheckoutEnabled: &checkout,
			}},
		}},
	}
}

func TestCatalogueTransferAcceptsV1AndV2(t *testing.T) {
	for _, version := range []int{1, 2} {
		result := validateCatalogueTransferDocument(validCatalogueTransferForTest(version), time.Now().UTC())
		require.True(t, result.Valid, "version %d issues: %#v", version, result.Issues)
		require.Equal(t, 1, result.Products)
		require.Equal(t, 1, result.Plans)
	}
}

func TestCatalogueTransferRejectsMissingStateAndOverlappingFamilies(t *testing.T) {
	transfer := validCatalogueTransferForTest(2)
	transfer.Products[0].Plans[0].Active = nil
	second := transfer.Products[0].Plans[0]
	second.Slug = "daybook-free-v2"
	active := true
	second.Active = &active
	third := second
	third.Slug = "daybook-free-v3"
	transfer.Products[0].Plans = append(transfer.Products[0].Plans, second, third)

	result := validateCatalogueTransferDocument(transfer, time.Now().UTC())
	require.False(t, result.Valid)
	codes := make([]string, 0, len(result.Issues))
	for _, issue := range result.Issues {
		codes = append(codes, issue.Code)
	}
	require.Contains(t, codes, "required_state")
	require.Contains(t, codes, "overlapping_plan_family")
	require.Contains(t, codes, "multiple_default_plans")
}

func TestCatalogueTransferValidatesCreditPacks(t *testing.T) {
	transfer := validCatalogueTransferForTest(2)
	active := true
	transfer.Products[0].CreditPacks = []catalogueTransferCreditPack{{
		Slug: "credits-500", Name: "500 Credits", Credits: 500, PriceMinor: 49900, Currency: "INR", Active: &active,
	}}

	result := validateCatalogueTransferDocument(transfer, time.Now().UTC())
	require.True(t, result.Valid, "issues: %#v", result.Issues)
	require.Equal(t, 1, result.CreditPacks)

	transfer.Products[0].CreditPacks[0].Credits = 0
	result = validateCatalogueTransferDocument(transfer, time.Now().UTC())
	require.False(t, result.Valid)
	require.Equal(t, "invalid_credits", result.Issues[0].Code)
}
