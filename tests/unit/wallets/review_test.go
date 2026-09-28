package wallets_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/internal/wallets"
)

func TestWAL024EqualExpiryUsesDeterministicCreationOrder(t *testing.T) {
	expires := time.Now().UTC().Add(time.Hour)
	created := time.Now().UTC()
	candidates := []wallets.GrantCandidate{
		{ID: "second", ExpiresAt: &expires, CreatedAt: created.Add(time.Second)},
		{ID: "first", ExpiresAt: &expires, CreatedAt: created},
	}
	ordered := wallets.Prioritize(candidates)
	require.Equal(t, []string{"first", "second"}, []string{ordered[0].ID, ordered[1].ID})
}
