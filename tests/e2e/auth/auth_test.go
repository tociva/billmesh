//go:build e2e

package auth_test

import (
	"github.com/tociva/billmesh/tests/testkit"
	"testing"
)

func TestAuthenticationAndAuthorization(t *testing.T) {
	testkit.RunCases(t, "AUTH", "E", func(t *testing.T, tc testkit.PlanCase) {
		testkit.ExerciseAuthContract(t, tc)
	})
}
