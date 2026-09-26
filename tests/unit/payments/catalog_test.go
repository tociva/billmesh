package payments_test

import (
	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/internal/payments"
	"testing"
)

func TestPAY003ResolveServerSideCreditPack(t *testing.T) {
	packs := map[string]payments.CreditPack{"credits-500": {ID: "credits-500", Credits: 500, PriceMinor: 49900, Currency: "INR"}}
	pack, err := payments.ResolvePack("credits-500", packs)
	require.NoError(t, err)
	require.Equal(t, int64(49900), pack.PriceMinor)
	_, err = payments.ResolvePack("client-price", packs)
	require.Error(t, err)
}
