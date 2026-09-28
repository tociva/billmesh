package testplan_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestTEST001EveryReviewCaseHasBehavioralImplementation(t *testing.T) {
	prefixes := []string{"FND", "AUTH", "INST", "ACC", "SUB", "WAL", "RES", "MTR", "PAY", "INV", "WH", "LIM", "LIVE", "WRK", "TEST", "PERF"}
	for _, prefix := range prefixes {
		for _, tc := range testkit.Cases(t, prefix, "") {
			if reviewAddition(tc.ID) {
				require.Truef(t, testkit.IsReviewCaseImplemented(tc.ID), "%s has no executable behavioral implementation", tc.ID)
			}
		}
	}
}

func reviewAddition(id string) bool {
	parts := strings.Split(id, "-")
	if len(parts) != 2 {
		return false
	}
	minimum := map[string]string{"INST": "001", "RES": "022", "MTR": "018", "PAY": "021", "SUB": "021", "WH": "019", "LIVE": "013", "LIM": "011", "TEST": "001"}
	start, ok := minimum[parts[0]]
	return ok && parts[1] >= start
}
