package testplan_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestEveryChecklistCaseIsDiscovered(t *testing.T) {
	prefixes := []string{"FND", "AUTH", "INST", "ACC", "PLAN", "SUB", "ENT", "WAL", "RES", "MTR", "PAY", "INV", "WH", "LIM", "LIVE", "WRK", "ADM", "TEST", "PERF"}
	counts := map[string]int{}
	for _, prefix := range prefixes {
		for _, tc := range testkit.Cases(t, prefix, "") {
			counts[tc.Kind]++
		}
	}
	require.Equal(t, 15, counts["U"])
	require.Equal(t, 73, counts["I"])
	require.Equal(t, 195, counts["E"])
	require.Equal(t, 12, counts["P"])
}
