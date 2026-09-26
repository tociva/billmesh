//go:build e2e

package accounts_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestAccounts(t *testing.T) {
	testkit.RunCases(t, "ACC", "E", func(t *testing.T, tc testkit.PlanCase) {
		testkit.ExerciseAccountContract(t, tc)
	})
}
