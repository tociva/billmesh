//go:build integration

package wallets_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestWalletDatabaseCases(t *testing.T) {
	testkit.RunCases(t, "WAL", "I", func(t *testing.T, tc testkit.PlanCase) { testkit.ExerciseDatabaseContract(t, tc, "credit_grants") })
}
