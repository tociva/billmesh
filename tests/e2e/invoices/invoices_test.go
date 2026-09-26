//go:build e2e

package invoices_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"net/http"
	"testing"
)

func TestInvoices(t *testing.T) {
	testkit.RunCases(t, "INV", "E", func(t *testing.T, tc testkit.PlanCase) {
		testkit.ExerciseEndpointContract(t, tc, testkit.Endpoint{Method: http.MethodGet, Path: "/v1/invoices"})
	})
}
