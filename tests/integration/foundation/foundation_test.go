//go:build integration

package foundation_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestFoundationDatabaseCases(t *testing.T) {
	testkit.RunCases(t, "FND", "I", func(t *testing.T, tc testkit.PlanCase) { testkit.ExerciseDatabaseContract(t, tc, "goose_db_version") })
}
