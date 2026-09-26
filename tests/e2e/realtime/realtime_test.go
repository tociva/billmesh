//go:build e2e

package realtime_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestRealtimeSSE(t *testing.T) {
	testkit.RunCases(t, "LIVE", "E", func(t *testing.T, tc testkit.PlanCase) {
		testkit.ExerciseSSEContract(t, tc)
	})
}
