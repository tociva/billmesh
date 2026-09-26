package entitlements_test

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/internal/entitlements"
	"testing"
)

func TestENT002BooleanFeatures(t *testing.T) {
	values := map[string]json.RawMessage{"premium": json.RawMessage(`true`)}
	enabled, err := entitlements.Boolean(values, "premium")
	require.NoError(t, err)
	require.True(t, enabled)
	_, err = entitlements.Boolean(map[string]json.RawMessage{"premium": json.RawMessage(`2`)}, "premium")
	require.Error(t, err)
}
func TestENT003NumericLimits(t *testing.T) {
	values := map[string]json.RawMessage{"workflows": json.RawMessage(`25`)}
	limit, present, err := entitlements.Limit(values, "workflows")
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, int64(25), limit)
}
