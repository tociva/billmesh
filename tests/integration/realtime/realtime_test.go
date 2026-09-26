//go:build integration

package realtime_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestRealtimeDatabaseCases(t *testing.T) {
	testkit.RunCases(t, "LIVE", "I", func(t *testing.T, tc testkit.PlanCase) { testkit.ExerciseDatabaseContract(t, tc, "outbox_events") })
}
