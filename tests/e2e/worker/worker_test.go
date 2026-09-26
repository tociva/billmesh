//go:build e2e

package worker_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"net/http"
	"testing"
)

func TestBackgroundWorker(t *testing.T) {
	testkit.RunCases(t, "WRK", "E", func(t *testing.T, tc testkit.PlanCase) {
		testkit.ExerciseEndpointContract(t, tc, testkit.Endpoint{Method: http.MethodGet, Path: "/readyz"})
	})
}
