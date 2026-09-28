//go:build e2e

package reliability_test

import (
	"testing"

	"github.com/tociva/billmesh/tests/testkit"
)

func TestE2EReliabilityContracts(t *testing.T) {
	testkit.RunCases(t, "TEST", "E", func(t *testing.T, tc testkit.PlanCase) {
		if !testkit.ExerciseReviewE2ECase(t, tc) {
			t.Fatalf("%s has no executable E2E implementation", tc.ID)
		}
	})
}
