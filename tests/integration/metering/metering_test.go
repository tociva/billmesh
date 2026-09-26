//go:build integration

package metering_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestMeteringDatabaseCases(t *testing.T) {
	testkit.RunCases(t, "MTR", "I", func(t *testing.T, tc testkit.PlanCase) { testkit.ExerciseDatabaseContract(t, tc, "usage_events") })
}
