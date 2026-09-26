//go:build e2e

package metering_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"net/http"
	"testing"
)

func TestUsageMetering(t *testing.T) {
	testkit.RunCases(t, "MTR", "E", func(t *testing.T, tc testkit.PlanCase) {
		testkit.ExerciseEndpointContract(t, tc, testkit.Endpoint{Method: http.MethodPost, Path: "/v1/usage-events"})
	})
}
