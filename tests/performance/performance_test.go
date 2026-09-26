//go:build performance

package performance_test

import (
	"bytes"
	"net/http"
	"os"
	"testing"

	"github.com/tociva/billmesh/tests/testkit"
)

func BenchmarkPerformancePlan(b *testing.B) {
	base := os.Getenv("BILLMESH_BASE_URL")
	if base == "" {
		b.Fatal("BILLMESH_BASE_URL is required")
	}
	client := http.Client{}
	for _, tc := range testkit.Cases(b, "PERF", "P") {
		tc := tc
		b.Run(tc.ID, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					req, err := http.NewRequest(http.MethodGet, base+"/readyz", bytes.NewReader(nil))
					if err != nil {
						b.Error(err)
						continue
					}
					resp, err := client.Do(req)
					if err != nil {
						b.Error(err)
						continue
					}
					resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						b.Errorf("%s: readiness status %d", tc.ID, resp.StatusCode)
					}
				}
			})
		})
	}
}
