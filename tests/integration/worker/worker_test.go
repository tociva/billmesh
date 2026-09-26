//go:build integration

package worker_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestWorkerDatabaseCases(t *testing.T) {
	testkit.RunCases(t, "WRK", "I", func(t *testing.T, tc testkit.PlanCase) { testkit.ExerciseDatabaseContract(t, tc, "webhook_deliveries") })
}
