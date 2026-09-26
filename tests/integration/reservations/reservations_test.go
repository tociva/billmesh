//go:build integration

package reservations_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestReservationDatabaseCases(t *testing.T) {
	testkit.RunCases(t, "RES", "I", func(t *testing.T, tc testkit.PlanCase) { testkit.ExerciseDatabaseContract(t, tc, "reservations") })
}
