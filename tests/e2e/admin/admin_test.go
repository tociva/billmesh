//go:build e2e

package admin_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestAdministration(t *testing.T) {
	testkit.RunCases(t, "ADM", "E", func(t *testing.T, tc testkit.PlanCase) {
		testkit.ExerciseAdminContract(t, tc)
	})
}
