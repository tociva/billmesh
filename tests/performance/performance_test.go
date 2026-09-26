//go:build performance

package performance_test

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tociva/billmesh/tests/testkit"
)

func BenchmarkPerformancePlan(b *testing.B) {
	base := os.Getenv("BILLMESH_BASE_URL")
	if base == "" {
		b.Fatal("BILLMESH_BASE_URL is required")
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          128,
		MaxIdleConnsPerHost:   128,
		MaxConnsPerHost:       128,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
	}
	client := http.Client{Transport: transport, Timeout: 10 * time.Second}
	b.Cleanup(transport.CloseIdleConnections)
	for _, tc := range testkit.Cases(b, "PERF", "P") {
		tc := tc
		b.Run(tc.ID, func(b *testing.B) {
			var failures atomic.Int64
			var firstFailure error
			var failureMu sync.Mutex
			recordFailure := func(err error) {
				failures.Add(1)
				failureMu.Lock()
				if firstFailure == nil {
					firstFailure = err
				}
				failureMu.Unlock()
			}
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					req, err := http.NewRequest(http.MethodGet, base+"/readyz", nil)
					if err != nil {
						recordFailure(err)
						continue
					}
					resp, err := client.Do(req)
					if err != nil {
						recordFailure(err)
						continue
					}
					_, readErr := io.Copy(io.Discard, resp.Body)
					closeErr := resp.Body.Close()
					if readErr != nil {
						recordFailure(fmt.Errorf("read readiness response: %w", readErr))
						continue
					}
					if closeErr != nil {
						recordFailure(fmt.Errorf("close readiness response: %w", closeErr))
						continue
					}
					if resp.StatusCode != http.StatusOK {
						recordFailure(fmt.Errorf("readiness status %d", resp.StatusCode))
					}
				}
			})
			b.StopTimer()
			if count := failures.Load(); count > 0 {
				b.Fatalf("%s: %d benchmark requests failed; first failure: %v", tc.ID, count, firstFailure)
			}
		})
	}
}
