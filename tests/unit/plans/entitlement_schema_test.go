package plans_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/internal/products"
)

func TestEntitlementSchemaValidatesAndAppliesDefaults(t *testing.T) {
	minimum := float64(0)
	schema := products.EntitlementSchema{Fields: []products.EntitlementField{
		{Key: "reports", Label: "Reports", Type: "boolean", Required: true, Default: true},
		{Key: "members", Label: "Maximum members", Type: "integer", Required: true, Default: float64(5), Minimum: &minimum},
		{Key: "tier", Label: "Support tier", Type: "select", Options: []products.EntitlementOption{{Label: "Standard", Value: "standard"}, {Label: "Priority", Value: "priority"}}},
	}}
	require.NoError(t, products.ValidateEntitlementSchema(schema))

	values, err := products.ValidateEntitlements(schema, map[string]any{"tier": "priority"})
	require.NoError(t, err)
	require.Equal(t, true, values["reports"])
	require.Equal(t, float64(5), values["members"])
	require.Equal(t, "priority", values["tier"])

	_, err = products.ValidateEntitlements(schema, map[string]any{"reports": "yes"})
	require.ErrorContains(t, err, "must be boolean")
	_, err = products.ValidateEntitlements(schema, map[string]any{"unknown": true})
	require.ErrorContains(t, err, "not defined")
}

func TestEntitlementSchemaSupportsNestedObjectsAndArrays(t *testing.T) {
	minimumItems := 1
	schema := products.EntitlementSchema{Fields: []products.EntitlementField{
		{Key: "policy", Label: "Policy", Type: "object", Fields: []products.EntitlementField{{Key: "mode", Label: "Mode", Type: "string", Required: true}}},
		{Key: "regions", Label: "Regions", Type: "array", MinItems: &minimumItems, Items: &products.EntitlementField{Type: "string"}},
	}}
	require.NoError(t, products.ValidateEntitlementSchema(schema))
	_, err := products.ValidateEntitlements(schema, map[string]any{
		"policy":  map[string]any{"mode": "strict"},
		"regions": []any{"in", "us"},
	})
	require.NoError(t, err)

	_, err = products.ValidateEntitlements(schema, map[string]any{"policy": map[string]any{"extra": true}, "regions": []any{}})
	require.Error(t, err)
}

func TestEntitlementSchemaRejectsDuplicatesAndIncompatibleStoredValues(t *testing.T) {
	schema := products.EntitlementSchema{Fields: []products.EntitlementField{
		{Key: "reports", Label: "Reports", Type: "boolean"},
		{Key: "reports", Label: "Duplicate", Type: "boolean"},
	}}
	require.ErrorContains(t, products.ValidateEntitlementSchema(schema), "duplicated")

	schema.Fields = schema.Fields[:1]
	require.ErrorContains(t, products.ValidateStoredEntitlements(schema, map[string]any{"reports": 1}), "must be boolean")
}
