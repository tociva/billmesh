//go:build integration

package payments_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestPaymentDatabaseCases(t *testing.T) {
	testkit.RunCases(t, "PAY", "I", func(t *testing.T, tc testkit.PlanCase) { testkit.ExerciseDatabaseContract(t, tc, "provider_events") })
}
