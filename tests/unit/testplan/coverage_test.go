package testplan_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tociva/billmesh/tests/testkit"
)

func TestEveryChecklistCaseIsDiscovered(t *testing.T) {
	prefixes := []string{"FND", "AUTH", "ACC", "PLAN", "SUB", "ENT", "WAL", "RES", "MTR", "PAY", "INV", "WH", "LIM", "LIVE", "WRK", "ADM", "PERF"}
	counts := map[string]int{}
	for _, prefix := range prefixes {
		for _, tc := range testkit.Cases(t, prefix, "") {
			counts[tc.Kind]++
		}
	}
	require.Equal(t, 15, counts["U"])
	require.Equal(t, 47, counts["I"])
	require.Equal(t, 159, counts["E"])
	require.Equal(t, 12, counts["P"])
}
