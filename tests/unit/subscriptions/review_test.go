package subscriptions_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/internal/subscriptions"
)

func TestSUB025RenewalBoundariesAreTimezoneIndependent(t *testing.T) {
	zones := []string{"UTC", "America/New_York", "Asia/Kolkata"}
	for _, zone := range zones {
		t.Run(zone, func(t *testing.T) {
			location, err := time.LoadLocation(zone)
			require.NoError(t, err)
			start := time.Date(2026, 3, 8, 1, 30, 0, 0, location)
			_, end, err := subscriptions.Period(start, "monthly")
			require.NoError(t, err)
			require.Equal(t, start.UTC().AddDate(0, 1, 0), end)
			require.Equal(t, time.UTC, end.Location())
		})
	}
}
