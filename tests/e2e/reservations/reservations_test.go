//go:build e2e

package reservations_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"net/http"
	"testing"
)

func TestReservationsAndSettlement(t *testing.T) {
	testkit.RunCases(t, "RES", "E", func(t *testing.T, tc testkit.PlanCase) {
		testkit.ExerciseEndpointContract(t, tc, testkit.Endpoint{Method: http.MethodPost, Path: "/v1/wallets/00000000-0000-0000-0000-000000000000/reservations"})
	})
}
