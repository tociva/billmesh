package limits_test

import (
	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/internal/notifications"
	"testing"
)

func TestLIM001UsagePercentage(t *testing.T) {
	require.Equal(t, 50, notifications.UsagePercent(50, 100))
	require.Equal(t, 100, notifications.UsagePercent(120, 100))
	require.Equal(t, 0, notifications.UsagePercent(0, 100))
}
