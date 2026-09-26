//go:build integration

package webhooks_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestWebhookDatabaseCases(t *testing.T) {
	testkit.RunCases(t, "WH", "I", func(t *testing.T, tc testkit.PlanCase) { testkit.ExerciseDatabaseContract(t, tc, "webhook_deliveries") })
}
