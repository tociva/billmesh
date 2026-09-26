package plans_test

import (
	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/internal/products"
	"testing"
)

func TestPLAN005ValidatePricesAndCurrencies(t *testing.T) {
	require.NoError(t, products.ValidatePlan(products.PlanInput{PriceMinor: 999, IncludedCredits: 500, Currency: "INR"}))
	require.Error(t, products.ValidatePlan(products.PlanInput{PriceMinor: 999, IncludedCredits: 500, Currency: "inr"}))
}
func TestPLAN006RejectNegativeValues(t *testing.T) {
	require.Error(t, products.ValidatePlan(products.PlanInput{PriceMinor: -1, Currency: "INR"}))
	require.Error(t, products.ValidatePlan(products.PlanInput{IncludedCredits: -1, Currency: "INR"}))
}
