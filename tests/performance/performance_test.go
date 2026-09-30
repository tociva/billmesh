//go:build performance

package performance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tociva/billmesh/tests/testkit"
)

type fixture struct {
	base, mock, database, token, account string
	client                               *http.Client
	wallets                              []string
	tokens                               []string
	sequence                             atomic.Uint64
}

func BenchmarkPerformancePlan(b *testing.B) {
	f := newFixture(b)
	cases := append(testkit.Cases(b, "PERF", "P"), testkit.OptionalCases(b, "LIVE", "P")...)
	for _, tc := range cases {
		tc := tc
		b.Run(tc.ID, func(b *testing.B) {
			switch tc.ID {
			case "PERF-001":
				benchmarkReservations(b, f, false)
			case "PERF-002":
				benchmarkReservations(b, f, true)
			case "PERF-003":
				benchmarkIndependentWallets(b, f)
			case "PERF-004":
				benchmarkUsageBatches(b, f)
			case "PERF-005":
				benchmarkBalanceDuringMetering(b, f)
			case "PERF-006":
				benchmarkSSEConnections(b, f, false)
			case "PERF-007":
				benchmarkSlowWebhookIsolation(b, f)
			case "PERF-008", "PERF-009", "PERF-010", "PERF-011":
				b.Skip("requires an external fault-injection runner with service lifecycle control")
			case "PERF-012":
				benchmarkLedgerReconciliation(b, f)
			case "PERF-013":
				benchmarkReservationAndSettlement(b, f)
			case "LIVE-017":
				benchmarkSSEConnections(b, f, true)
			default:
				b.Fatalf("no performance implementation for %s", tc.ID)
			}
		})
	}
}

func newFixture(b *testing.B) *fixture {
	b.Helper()
	base := strings.TrimRight(os.Getenv("BILLMESH_BASE_URL"), "/")
	mock := strings.TrimRight(os.Getenv("MOCK_SERVER_URL"), "/")
	database := os.Getenv("DATABASE_URL")
	if base == "" || mock == "" || database == "" {
		b.Fatal("BILLMESH_BASE_URL, MOCK_SERVER_URL, and DATABASE_URL are required")
	}
	transport := &http.Transport{MaxIdleConns: 128, MaxIdleConnsPerHost: 128, MaxConnsPerHost: 128, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 5 * time.Second}
	f := &fixture{base: base, mock: mock, database: database, client: &http.Client{Transport: transport, Timeout: 10 * time.Second}}
	b.Cleanup(transport.CloseIdleConnections)
	f.token = f.issueToken(b, "performance-0")
	f.account = f.create(b, http.MethodPost, "/v1/accounts", map[string]any{"name": "Performance Account", "external_ref": f.unique("account"), "application": "daybook", "organization_id": "performance-0"})
	product := f.productID(b, "daybook")
	for i := range 8 {
		account := f.account
		if i > 0 {
			org := fmt.Sprintf("performance-%d", i)
			f.token = f.issueToken(b, org)
			account = f.create(b, http.MethodPost, "/v1/accounts", map[string]any{"name": fmt.Sprintf("Performance Account %d", i+1), "external_ref": f.unique("account"), "application": "daybook", "organization_id": org})
		}
		wallet := f.create(b, http.MethodPost, "/v1/wallets", map[string]any{"account_id": account, "product_id": product})
		f.require(b, http.StatusOK, http.MethodPost, "/v1/wallets/"+wallet+"/grants", map[string]any{"source": "performance", "operation_ref": f.unique("grant"), "amount": int64(1_000_000_000)})
		f.wallets = append(f.wallets, wallet)
		f.tokens = append(f.tokens, f.token)
	}
	f.token = f.tokens[0]
	return f
}

func benchmarkReservations(b *testing.B, f *fixture, parallel bool) {
	var reservationsMu sync.Mutex
	reservations := make([]string, 0, b.N)
	run := func() error {
		id, err := f.reserve(f.wallets[0], 1)
		if err != nil {
			return err
		}
		reservationsMu.Lock()
		reservations = append(reservations, id)
		reservationsMu.Unlock()
		return nil
	}
	benchmarkRequests(b, parallel, run)
	for _, id := range reservations {
		f.require(b, http.StatusOK, http.MethodPost, "/v1/reservations/"+id+"/release", nil)
	}
}

func benchmarkIndependentWallets(b *testing.B, f *fixture) {
	var next atomic.Uint64
	benchmarkRequests(b, true, func() error {
		index := next.Add(1) % uint64(len(f.wallets))
		wallet, token := f.wallets[index], f.tokens[index]
		id, err := f.reserveWithToken(wallet, token, 1)
		if err != nil {
			return err
		}
		return f.expectWithToken(token, http.StatusOK, http.MethodPost, "/v1/reservations/"+id+"/release", nil)
	})
}

func benchmarkUsageBatches(b *testing.B, f *fixture) {
	benchmarkRequests(b, false, func() error {
		reservation, err := f.reserve(f.wallets[0], 1)
		if err != nil {
			return err
		}
		events := make([]map[string]any, 10)
		for i := range events {
			events[i] = map[string]any{"event_id": f.unique("usage"), "reservation_id": reservation, "meter": "workflow.execution", "quantity": 0, "application": "daybook"}
		}
		if err := f.expect(http.StatusAccepted, http.MethodPost, "/v1/usage-events", events); err != nil {
			return err
		}
		return f.expect(http.StatusOK, http.MethodPost, "/v1/reservations/"+reservation+"/release", nil)
	})
}

func benchmarkBalanceDuringMetering(b *testing.B, f *fixture) {
	reservation, err := f.reserve(f.wallets[0], 1)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = f.expect(http.StatusOK, http.MethodPost, "/v1/reservations/"+reservation+"/release", nil) })
	benchmarkRequests(b, true, func() error {
		if err := f.expect(http.StatusAccepted, http.MethodPost, "/v1/usage-events", map[string]any{"event_id": f.unique("meter"), "reservation_id": reservation, "meter": "workflow.execution", "quantity": 0, "application": "daybook"}); err != nil {
			return err
		}
		return f.expect(http.StatusOK, http.MethodGet, "/v1/wallets/"+f.wallets[0], nil)
	})
}

func benchmarkSSEConnections(b *testing.B, f *fixture, slow bool) {
	count := b.N
	if slow && count < 8 {
		count = 8
	}
	b.ResetTimer()
	var wg sync.WaitGroup
	errCh := make(chan error, count)
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.base+"/v1/events", nil)
			req.Header.Set("Authorization", "Bearer "+f.token)
			resp, err := f.client.Do(req)
			if err != nil {
				errCh <- err
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errCh <- fmt.Errorf("SSE status %d", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
	b.StopTimer()
	close(errCh)
	for err := range errCh {
		if err != nil {
			b.Fatal(err)
		}
	}
	if slow {
		f.require(b, http.StatusOK, http.MethodGet, "/v1/wallets/"+f.wallets[0], nil)
	}
}

func benchmarkSlowWebhookIsolation(b *testing.B, f *fixture) {
	f.requireMock(b, http.StatusNoContent, "/test/receiver-delay", map[string]any{"milliseconds": 250})
	b.Cleanup(func() {
		_ = f.expectMock(http.StatusNoContent, "/test/receiver-delay", map[string]any{"milliseconds": 0})
	})
	f.require(b, http.StatusCreated, http.MethodPost, "/v1/webhooks", map[string]any{"application": "daybook", "target_url": "https://mock-external:8090/receivers/daybook", "secret": "performance-secret"})
	benchmarkRequests(b, false, func() error {
		if err := f.expect(http.StatusOK, http.MethodPost, "/v1/wallets/"+f.wallets[0]+"/grants", map[string]any{"source": "performance", "operation_ref": f.unique("webhook"), "amount": 1}); err != nil {
			return err
		}
		return f.expect(http.StatusOK, http.MethodGet, "/v1/wallets/"+f.wallets[0], nil)
	})
}

func benchmarkLedgerReconciliation(b *testing.B, f *fixture) {
	benchmarkRequests(b, true, func() error {
		id, err := f.reserve(f.wallets[0], 1)
		if err != nil {
			return err
		}
		return f.expect(http.StatusOK, http.MethodPost, "/v1/reservations/"+id+"/settle", map[string]any{"actual": 1})
	})
	pool, err := pgxpool.New(context.Background(), f.database)
	if err != nil {
		b.Fatal(err)
	}
	defer pool.Close()
	var available, reserved, ledgerAvailable, ledgerReserved int64
	err = pool.QueryRow(context.Background(), `SELECT w.available,w.reserved,COALESCE(sum(l.available_delta),0),COALESCE(sum(l.reserved_delta),0) FROM wallets w LEFT JOIN credit_ledger l ON l.wallet_id=w.id WHERE w.id=$1 GROUP BY w.id`, f.wallets[0]).Scan(&available, &reserved, &ledgerAvailable, &ledgerReserved)
	if err != nil {
		b.Fatal(err)
	}
	if available != ledgerAvailable || reserved != ledgerReserved {
		b.Fatalf("ledger mismatch: wallet=(%d,%d) ledger=(%d,%d)", available, reserved, ledgerAvailable, ledgerReserved)
	}
}

func benchmarkReservationAndSettlement(b *testing.B, f *fixture) {
	benchmarkRequests(b, false, func() error {
		id, err := f.reserve(f.wallets[0], 1)
		if err != nil {
			return err
		}
		return f.expect(http.StatusOK, http.MethodPost, "/v1/reservations/"+id+"/settle", map[string]any{"actual": 1})
	})
}

func benchmarkRequests(b *testing.B, parallel bool, operation func() error) {
	b.Helper()
	var failures atomic.Int64
	var first error
	var mu sync.Mutex
	run := func() {
		if err := operation(); err != nil {
			failures.Add(1)
			mu.Lock()
			if first == nil {
				first = err
			}
			mu.Unlock()
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	if parallel {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				run()
			}
		})
	} else {
		for range b.N {
			run()
		}
	}
	b.StopTimer()
	if failures.Load() != 0 {
		b.Fatalf("%d benchmark requests failed; first failure: %v", failures.Load(), first)
	}
}

func (f *fixture) issueToken(b *testing.B, org string) string {
	raw, _ := json.Marshal(map[string]any{"org_id": org, "app": "daybook", "permissions": []string{"billing:read", "billing:write", "billing:admin", "credits:grant", "credits:reserve", "credits:settle"}})
	resp, err := f.client.Post(f.mock+"/test/token", "application/json", bytes.NewReader(raw))
	if err != nil {
		b.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil || out.AccessToken == "" {
		b.Fatal("fake issuer did not return a token")
	}
	return out.AccessToken
}

func (f *fixture) productID(b *testing.B, slug string) string {
	status, raw, err := f.request(http.MethodGet, "/v1/products", nil)
	if err != nil || status != http.StatusOK {
		b.Fatalf("list products: status=%d err=%v", status, err)
	}
	var products []struct{ ID, Slug string }
	if json.Unmarshal(raw, &products) != nil {
		b.Fatal("decode products")
	}
	for _, product := range products {
		if product.Slug == slug {
			return product.ID
		}
	}
	b.Fatalf("product %s not found", slug)
	return ""
}

func (f *fixture) create(b *testing.B, method, path string, body any) string {
	status, raw, err := f.request(method, path, body)
	if err != nil || status != http.StatusCreated {
		b.Fatalf("%s %s: status=%d err=%v body=%s", method, path, status, err, raw)
	}
	var out struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &out) != nil || out.ID == "" {
		b.Fatalf("%s %s returned no id: %s", method, path, raw)
	}
	return out.ID
}

func (f *fixture) reserve(wallet string, amount int64) (string, error) {
	return f.reserveWithToken(wallet, f.token, amount)
}

func (f *fixture) reserveWithToken(wallet, token string, amount int64) (string, error) {
	status, raw, err := f.requestWithToken(token, http.MethodPost, "/v1/wallets/"+wallet+"/reservations", map[string]any{"execution_id": f.unique("execution"), "operation_seq": 0, "amount": amount})
	if err != nil {
		return "", err
	}
	if status != http.StatusCreated {
		return "", fmt.Errorf("reserve status %d: %s", status, raw)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.ID == "" {
		return "", fmt.Errorf("decode reservation: %w", err)
	}
	return out.ID, nil
}

func (f *fixture) require(b *testing.B, want int, method, path string, body any) {
	b.Helper()
	if err := f.expect(want, method, path, body); err != nil {
		b.Fatal(err)
	}
}

func (f *fixture) expect(want int, method, path string, body any) error {
	return f.expectWithToken(f.token, want, method, path, body)
}

func (f *fixture) expectWithToken(token string, want int, method, path string, body any) error {
	status, raw, err := f.requestWithToken(token, method, path, body)
	if err != nil {
		return err
	}
	if status != want {
		return fmt.Errorf("%s %s: want %d, got %d: %s", method, path, want, status, raw)
	}
	return nil
}

func (f *fixture) request(method, path string, body any) (int, []byte, error) {
	return f.requestWithToken(f.token, method, path, body)
}

func (f *fixture) requireMock(b *testing.B, want int, path string, body any) {
	b.Helper()
	if err := f.expectMock(want, path, body); err != nil {
		b.Fatal(err)
	}
}

func (f *fixture) expectMock(want int, path string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := f.client.Post(f.mock+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != want {
		return fmt.Errorf("POST %s: want %d, got %d: %s", path, want, resp.StatusCode, data)
	}
	return nil
}

func (f *fixture) requestWithToken(token, method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, f.base+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, err
}

func (f *fixture) unique(prefix string) string {
	return fmt.Sprintf("perf-%s-%d-%d", prefix, time.Now().UnixNano(), f.sequence.Add(1))
}
