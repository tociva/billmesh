//go:build e2e

package payments_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"net/http"
	"testing"
)

func TestPayments(t *testing.T) {
	testkit.RunCases(t, "PAY", "E", func(t *testing.T, tc testkit.PlanCase) {
		testkit.ExerciseEndpointContract(t, tc, testkit.Endpoint{Method: http.MethodPost, Path: "/v1/payments/orders"})
	})
}
