package metering_test

import (
	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/internal/metering"
	"testing"
)

func TestMTR004FixedPointCredits(t *testing.T) {
	got, err := metering.Credits(1500, metering.Rate{Numerator: 1, Denominator: 1000})
	require.NoError(t, err)
	require.Equal(t, int64(2), got)
}
func TestMTR005ConfiguredMeterPrice(t *testing.T) {
	got, err := metering.Price("llm.input_tokens", 2000, map[string]metering.Rate{"llm.input_tokens": {Numerator: 3, Denominator: 1000}})
	require.NoError(t, err)
	require.Equal(t, int64(6), got)
	_, err = metering.Price("unknown", 1, map[string]metering.Rate{})
	require.Error(t, err)
}
