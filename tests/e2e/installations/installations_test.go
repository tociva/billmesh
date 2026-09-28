//go:build e2e

package installations_test

import (
	"testing"

	"github.com/tociva/billmesh/tests/testkit"
)

func TestApplicationInstallationSecurity(t *testing.T) {
	testkit.RunCases(t, "INST", "E", func(t *testing.T, tc testkit.PlanCase) {
		if !testkit.ExerciseReviewE2ECase(t, tc) {
			t.Fatalf("%s has no executable E2E implementation", tc.ID)
		}
	})
}
