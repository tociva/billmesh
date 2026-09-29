package app

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tociva/billmesh/internal/auth"
)

type failureWindow struct {
	mu      sync.Mutex
	entries map[string]failureCount
	limit   int
	now     func() time.Time
}

type failureCount struct {
	count int
	reset time.Time
}

func (f *failureWindow) record(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	entry := f.entries[key]
	if !now.Before(entry.reset) {
		entry = failureCount{reset: now.Add(time.Minute)}
	}
	entry.count++
	f.entries[key] = entry
	if len(f.entries) > 8192 {
		for k, v := range f.entries {
			if !now.Before(v.reset) {
				delete(f.entries, k)
			}
		}
		for k := range f.entries {
			if len(f.entries) <= 4096 {
				break
			}
			delete(f.entries, k)
		}
	}
	return entry.count > f.limit
}

type keyedBuckets struct {
	mu      sync.Mutex
	entries map[string]tokenBucket
	rate    float64
	burst   float64
	now     func() time.Time
}

type tokenBucket struct {
	tokens  float64
	updated time.Time
}

func (b *keyedBuckets) allow(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	entry, exists := b.entries[key]
	if !exists {
		entry = tokenBucket{tokens: b.burst, updated: now}
	} else {
		entry.tokens += now.Sub(entry.updated).Seconds() * b.rate
		if entry.tokens > b.burst {
			entry.tokens = b.burst
		}
		entry.updated = now
	}
	if len(b.entries) > 8192 {
		for k, v := range b.entries {
			if now.Sub(v.updated) > time.Minute {
				delete(b.entries, k)
			}
		}
		for k := range b.entries {
			if len(b.entries) <= 4096 {
				break
			}
			delete(b.entries, k)
		}
	}
	allowed := entry.tokens >= 1
	if allowed {
		entry.tokens--
	}
	b.entries[key] = entry
	return allowed
}

func (a *API) ConfigureRequestLimits(authFailuresPerMinute, mutationRatePerSecond, mutationBurst int) {
	a.authFailures = &failureWindow{entries: make(map[string]failureCount), limit: authFailuresPerMinute, now: time.Now}
	a.mutations = &keyedBuckets{entries: make(map[string]tokenBucket), rate: float64(mutationRatePerSecond), burst: float64(mutationBurst), now: time.Now}
}

func (a *API) requestLimitHooks() auth.MiddlewareHooks {
	return auth.MiddlewareHooks{
		Rejected: func(r *http.Request) bool {
			return a.authFailures.record(remoteHost(r.RemoteAddr))
		},
		Authenticated: func(w http.ResponseWriter, r *http.Request, claims *auth.Claims) bool {
			if r.Method != http.MethodPost && r.Method != http.MethodPatch {
				return true
			}
			key := claims.OrgID + "|" + claims.Environment
			if a.mutations.allow(key) {
				return true
			}
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "request rate limit exceeded")
			return false
		},
	}
}

func remoteHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return strings.TrimSpace(remoteAddr)
}
