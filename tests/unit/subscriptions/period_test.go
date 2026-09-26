package subscriptions_test

import (
	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/internal/subscriptions"
	"testing"
	"time"
)

func TestSUB008BillingPeriod(t *testing.T) {
	start := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	from, to, err := subscriptions.Period(start, "monthly")
	require.NoError(t, err)
	require.Equal(t, start, from)
	require.Equal(t, time.Date(2026, 2, 15, 10, 0, 0, 0, time.UTC), to)
}
func TestSUB009MonthlyAndAnnualRenewals(t *testing.T) {
	start := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
	_, monthly, err := subscriptions.Period(start, "monthly")
	require.NoError(t, err)
	require.Equal(t, time.Date(2024, 3, 29, 0, 0, 0, 0, time.UTC), monthly)
	_, annual, err := subscriptions.Period(start, "annual")
	require.NoError(t, err)
	require.Equal(t, time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC), annual)
}
