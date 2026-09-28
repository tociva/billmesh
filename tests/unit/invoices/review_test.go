package invoices_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/internal/invoices"
)

func TestINV010CurrencyMinorUnitCalculationsRemainExact(t *testing.T) {
	tests := []struct {
		name      string
		lines     []invoices.Line
		precision int
		expected  int64
	}{
		{name: "zero decimal currency", lines: []invoices.Line{{Quantity: 3, UnitPriceMinor: 101}}, precision: 0, expected: 303},
		{name: "two decimal currency", lines: []invoices.Line{{Quantity: 1, UnitPriceMinor: 999}}, precision: 2, expected: 999},
		{name: "sum lines before external discounts", lines: []invoices.Line{{Quantity: 2, UnitPriceMinor: 333}, {Quantity: 1, UnitPriceMinor: 334}}, precision: 2, expected: 1000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := invoices.Total(tc.lines, tc.precision)
			require.NoError(t, err)
			require.Equal(t, tc.expected, actual)
		})
	}
}
