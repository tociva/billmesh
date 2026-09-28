//go:build integration

package reliability_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestTEST003RequiredPostgresSuiteCannotSilentlySkip(t *testing.T) {
	pool := testkit.Database(t)
	require.NoError(t, pool.Ping(context.Background()))
}
