//go:build integration

package limits_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestLimitDatabaseCases(t *testing.T) {
	testkit.RunCases(t, "LIM", "I", func(t *testing.T, tc testkit.PlanCase) { testkit.ExerciseDatabaseContract(t, tc, "outbox_events") })
}
