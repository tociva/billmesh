//go:build integration

package accounts_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestAccountDatabaseCases(t *testing.T) {
	testkit.RunCases(t, "ACC", "I", func(t *testing.T, tc testkit.PlanCase) { testkit.ExerciseDatabaseContract(t, tc, "billing_accounts") })
}
