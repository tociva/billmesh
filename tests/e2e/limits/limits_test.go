//go:build e2e

package limits_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"net/http"
	"testing"
)

func TestThresholdsAndLimits(t *testing.T) {
	testkit.RunCases(t, "LIM", "E", func(t *testing.T, tc testkit.PlanCase) {
		testkit.ExerciseEndpointContract(t, tc, testkit.Endpoint{Method: http.MethodGet, Path: "/v1/limits"})
	})
}
