package plans_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/internal/products"
)

func TestPRD005ProductTextValidation(t *testing.T) {
	require.NoError(t, products.ValidateProduct("invoice-api", "Invoice API", "Usage billing"))
	require.Error(t, products.ValidateProduct("invoice-api", "   ", "Usage billing"))
	require.Error(t, products.ValidateProduct("invoice-api", strings.Repeat("n", 121), ""))
	require.Error(t, products.ValidateProduct("invoice-api", "Invoice API", strings.Repeat("d", 2001)))
}

func TestPRD006AndPRD007ProductSlugValidation(t *testing.T) {
	for _, slug := range []string{"daybook", "invoice-api", "a1"} {
		require.NoError(t, products.ValidateProduct(slug, "Product", ""), slug)
	}
	for _, slug := range []string{"", "a", "UPPER", "white space", "-prefix", "suffix-", "two--hyphens", strings.Repeat("a", 64)} {
		require.Error(t, products.ValidateProduct(slug, "Product", ""), slug)
	}
}

func TestPLAN018CommercialValidation(t *testing.T) {
	valid := products.PlanInput{PriceMinor: 100, IncludedCredits: 10, Currency: "INR", BillingInterval: "monthly", PlanFamilyID: "professional"}
	require.NoError(t, products.ValidatePlan(valid))

	cases := []products.PlanInput{
		{PriceMinor: -1, Currency: "INR", BillingInterval: "monthly"},
		{IncludedCredits: -1, Currency: "INR", BillingInterval: "monthly"},
		{Currency: "inr", BillingInterval: "monthly"},
		{Currency: "INR", BillingInterval: "weekly"},
		{Currency: "INR", BillingInterval: "monthly", PlanFamilyID: "Professional Monthly"},
	}
	for _, input := range cases {
		require.Error(t, products.ValidatePlan(input))
	}
}

func TestPlanFamilyIDValidation(t *testing.T) {
	for _, familyID := range []string{"basic", "professional-v2", "tier1"} {
		require.NoError(t, products.ValidatePlanFamilyID(familyID), familyID)
	}
	for _, familyID := range []string{"", "a", "Basic", "basic_monthly", "basic--monthly", strings.Repeat("a", 64)} {
		require.Error(t, products.ValidatePlanFamilyID(familyID), familyID)
	}
}

func TestPLAN019PlanSlugValidation(t *testing.T) {
	require.NoError(t, products.ValidatePlanSlug("professional-monthly"))
	require.Error(t, products.ValidatePlanSlug("Professional Monthly"))
}
