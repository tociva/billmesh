//go:build integration

package invoices_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestInvoiceDatabaseCases(t *testing.T) {
	testkit.RunCases(t, "INV", "I", func(t *testing.T, tc testkit.PlanCase) { testkit.ExerciseDatabaseContract(t, tc, "invoices") })
}
