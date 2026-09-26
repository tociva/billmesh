//go:build e2e

package foundation_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"net/http"
	"testing"
)

func TestFoundation(t *testing.T) {
	testkit.RunCases(t, "FND", "E", func(t *testing.T, tc testkit.PlanCase) {
		testkit.ExerciseEndpointContract(t, tc, testkit.Endpoint{Method: http.MethodGet, Path: "/healthz"})
	})
}
