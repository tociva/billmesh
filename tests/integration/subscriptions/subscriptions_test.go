//go:build integration

package subscriptions_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestSubscriptionDatabaseCases(t *testing.T) {
	testkit.RunCases(t, "SUB", "I", func(t *testing.T, tc testkit.PlanCase) { testkit.ExerciseDatabaseContract(t, tc, "subscriptions") })
}
