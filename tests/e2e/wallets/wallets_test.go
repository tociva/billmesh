//go:build e2e

package wallets_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"net/http"
	"testing"
)

func TestWalletsAndGrants(t *testing.T) {
	testkit.RunCases(t, "WAL", "E", func(t *testing.T, tc testkit.PlanCase) {
		testkit.ExerciseEndpointContract(t, tc, testkit.Endpoint{Method: http.MethodPost, Path: "/v1/wallets"})
	})
}
