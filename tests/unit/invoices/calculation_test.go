package invoices_test

import (
	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/internal/invoices"
	"testing"
)

func TestINV003InvoiceTotals(t *testing.T) {
	total, err := invoices.Total([]invoices.Line{{Quantity: 2, UnitPriceMinor: 49900}, {Quantity: 1, UnitPriceMinor: 1000}}, 2)
	require.NoError(t, err)
	require.Equal(t, int64(100800), total)
	_, err = invoices.Total([]invoices.Line{{Quantity: -1, UnitPriceMinor: 1}}, 2)
	require.Error(t, err)
}
