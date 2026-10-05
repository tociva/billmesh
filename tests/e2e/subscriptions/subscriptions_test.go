//go:build e2e

package subscriptions_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"net/http"
	"testing"
)

func TestSubscriptions(t *testing.T) {
	testkit.RunCases(t, "SUB", "E", func(t *testing.T, tc testkit.PlanCase) {
		testkit.ExerciseEndpointContract(t, tc, testkit.Endpoint{Method: http.MethodPost, Path: "/v1/subscription-transitions"})
	})
}
