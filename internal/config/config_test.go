package config

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("WORKER_INTERVAL", "250ms")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, ":8080", cfg.HTTPAddr)
	require.Equal(t, 250*time.Millisecond, cfg.WorkerInterval)
	require.Equal(t, 30, cfg.AuthFailuresPerMinute)
	require.Equal(t, 10, cfg.MutationRatePerSecond)
	require.Equal(t, 50, cfg.MutationBurst)
}

func TestLoadRejectsInvalidRequestLimits(t *testing.T) {
	for _, key := range []string{"AUTH_FAILURES_PER_MINUTE", "MUTATION_RATE_PER_SECOND", "MUTATION_BURST"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://example")
			t.Setenv(key, "0")
			_, err := Load()
			require.ErrorContains(t, err, key)
		})
	}
}

func TestLoadClientProfiles(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("DELEGATION_CLIENT_PROFILES", `[{"authorizer_client_id":"daybook-authorizer","actor_client_id":"daybook-billing","scope":"billmesh.billing","type":"billing","actor_type":"user","app":"daybook","environment":"production"}]`)
	cfg, err := Load()
	require.NoError(t, err)
	require.Len(t, cfg.DelegationClients, 1)
}

func TestLoadRejectsInvalidClientProfiles(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("DELEGATION_CLIENT_PROFILES", `[{"authorizer_client_id":"authorizer","actor_client_id":"duplicate","scope":"billmesh.billing","type":"billing","actor_type":"user","app":"daybook","environment":"production"},{"authorizer_client_id":"authorizer","actor_client_id":"duplicate","scope":"billmesh.runtime","type":"runtime","actor_type":"service","app":"daybook","environment":"production"}]`)
	_, err := Load()
	require.ErrorContains(t, err, "duplicate authorizer/actor pair")
}
func TestLoadRequiresDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	clearDatabaseComponents(t)
	_, err := Load()
	require.ErrorContains(t, err, "DATABASE_URL or DB_HOST")
}

func TestLoadBuildsDatabaseURLFromComponents(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_PORT", "5433")
	t.Setenv("DB_NAME", "billmesh_e2e")
	t.Setenv("DB_USER", "billmesh")
	t.Setenv("DB_PASSWORD", "special/@ password")
	t.Setenv("DB_SSLMODE", "disable")

	cfg, err := Load()
	require.NoError(t, err)
	parsed, err := url.Parse(cfg.DatabaseURL)
	require.NoError(t, err)
	require.Equal(t, "localhost", parsed.Hostname())
	require.Equal(t, "5433", parsed.Port())
	require.Equal(t, "billmesh_e2e", parsed.Path[1:])
	require.Equal(t, "billmesh", parsed.User.Username())
	password, present := parsed.User.Password()
	require.True(t, present)
	require.Equal(t, "special/@ password", password)
	require.Equal(t, "disable", parsed.Query().Get("sslmode"))
}

func TestLoadDatabaseURLTakesPrecedenceOverComponents(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://override.example/billmesh")
	t.Setenv("DB_HOST", "component.example")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "postgres://override.example/billmesh", cfg.DatabaseURL)
}

func TestLoadRejectsInvalidDatabaseComponents(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_NAME", "billmesh")
	t.Setenv("DB_USER", "billmesh")
	t.Setenv("DB_PASSWORD", "secret")
	t.Setenv("DB_PORT", "70000")
	_, err := Load()
	require.ErrorContains(t, err, "DB_PORT")

	t.Setenv("DB_PORT", "5432")
	t.Setenv("DB_SSLMODE", "invalid")
	_, err = Load()
	require.ErrorContains(t, err, "DB_SSLMODE")
}

func TestLoadBFFConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	setBFFRealmEnvironment(t, "CONSOLE", "https://console.billmesh.example")
	setBFFRealmEnvironment(t, "ADMIN", "https://admin.billmesh.example")
	t.Setenv("BFF_SESSION_ENCRYPTION_KEYS", "v1:key")
	cfg, err := Load()
	require.NoError(t, err)
	require.Len(t, cfg.BFF.Realms, 2)
	require.Equal(t, "console", cfg.BFF.Realms[0].Realm)
	require.Equal(t, "/app", cfg.BFF.Realms[0].DefaultReturnPath)
	require.Equal(t, []string{"/app"}, cfg.BFF.Realms[0].ReturnPathPrefixes)
	require.Equal(t, 30, cfg.BFF.Realms[0].LoginAttemptsPerMinute)
	require.Equal(t, "admin", cfg.BFF.Realms[1].Realm)
	require.Equal(t, "/app", cfg.BFF.Realms[1].DefaultReturnPath)
	require.Equal(t, []string{"/app"}, cfg.BFF.Realms[1].ReturnPathPrefixes)
}

func TestLoadBFFAllowsNonAPIProcessesWithoutCredentials(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	cfg, err := Load()
	require.NoError(t, err)
	require.Len(t, cfg.BFF.Realms, 2)
}

func TestLoadBFFRejectsInvalidLifetimes(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("BFF_SESSION_IDLE_TTL", "2h")
	t.Setenv("BFF_SESSION_ABSOLUTE_TTL", "1h")
	_, err := Load()
	require.ErrorContains(t, err, "must be at least")
}

func setBFFRealmEnvironment(t *testing.T, realm, appOrigin string) {
	t.Helper()
	prefix := "BFF_" + realm
	t.Setenv(realm+"_APP_ORIGIN", appOrigin)
	t.Setenv(prefix+"_ISSUER", "https://identity.example")
	t.Setenv(prefix+"_CLIENT_ID", "billmesh-"+strings.ToLower(realm))
	t.Setenv(prefix+"_CLIENT_SECRET", "secret")
	t.Setenv(prefix+"_AUDIENCE", "billmesh")
	t.Setenv(prefix+"_REDIRECT_URI", "https://api.billmesh.example/api/v1/auth/"+strings.ToLower(realm)+"/callback")
	t.Setenv(prefix+"_POST_LOGOUT_REDIRECT_URI", "https://api.billmesh.example/api/v1/auth/"+strings.ToLower(realm)+"/logout/callback")
	t.Setenv(prefix+"_STANDALONE_LOGOUT_URI", "https://auth.idnest.example/logout")
}

func clearDatabaseComponents(t *testing.T) {
	t.Helper()
	for _, key := range []string{"DB_HOST", "DB_PORT", "DB_NAME", "DB_USER", "DB_PASSWORD", "DB_SSLMODE"} {
		t.Setenv(key, "")
	}
}
