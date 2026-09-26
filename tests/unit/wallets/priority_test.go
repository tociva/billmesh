package wallets_test

import (
	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/internal/wallets"
	"testing"
	"time"
)

func TestWAL008IncludedBeforePurchased(t *testing.T) {
	now := time.Now()
	got := wallets.Prioritize([]wallets.GrantCandidate{{ID: "p", Source: "purchase", CreatedAt: now}, {ID: "s", Source: "subscription", CreatedAt: now}})
	require.Equal(t, "s", got[0].ID)
}
func TestWAL009ExpirationPriority(t *testing.T) {
	now := time.Now()
	soon := now.Add(time.Hour)
	later := now.Add(2 * time.Hour)
	got := wallets.Prioritize([]wallets.GrantCandidate{{ID: "later", ExpiresAt: &later}, {ID: "never"}, {ID: "soon", ExpiresAt: &soon}})
	require.Equal(t, []string{"soon", "later", "never"}, []string{got[0].ID, got[1].ID, got[2].ID})
}
