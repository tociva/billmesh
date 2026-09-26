//go:build integration

package plans_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestPlanDatabaseCases(t *testing.T) {
	testkit.RunCases(t, "PLAN", "I", func(t *testing.T, tc testkit.PlanCase) { testkit.ExerciseDatabaseContract(t, tc, "plans") })
}
