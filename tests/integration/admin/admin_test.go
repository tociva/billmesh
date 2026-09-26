//go:build integration

package admin_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestAuditDatabaseCases(t *testing.T) {
	testkit.RunCases(t, "ADM", "I", func(t *testing.T, tc testkit.PlanCase) { testkit.ExerciseDatabaseContract(t, tc, "audit_log") })
}
