//go:build e2e

package entitlements_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"net/http"
	"testing"
)

func TestEntitlements(t *testing.T) {
	testkit.RunCases(t, "ENT", "E", func(t *testing.T, tc testkit.PlanCase) {
		testkit.ExerciseEndpointContract(t, tc, testkit.Endpoint{Method: http.MethodGet, Path: "/v1/entitlements"})
	})
}
