//go:build e2e

package plans_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestProductsAndPlans(t *testing.T) {
	testkit.RunCases(t, "PLAN", "E", func(t *testing.T, tc testkit.PlanCase) {
		testkit.ExercisePlanContract(t, tc)
	})
}
