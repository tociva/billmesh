//go:build e2e

package webhooks_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"net/http"
	"testing"
)

func TestWebhooks(t *testing.T) {
	testkit.RunCases(t, "WH", "E", func(t *testing.T, tc testkit.PlanCase) {
		testkit.ExerciseEndpointContract(t, tc, testkit.Endpoint{Method: http.MethodPost, Path: "/v1/webhooks"})
	})
}
