# Billmesh

Billmesh is a modular Go billing service for Daybook and Taskmesh. PostgreSQL is the authoritative ledger; credit mutations are transactional and idempotent, and external identity/payment systems are replaceable at their HTTP boundaries.

## What is included

- One `billmesh` binary with `api`, `worker`, `migrate`, and `healthcheck` commands.
- `net/http` API with OIDC/JWKS signature, issuer, audience, expiry, organization, application, and permission checks.
- PostgreSQL schema for accounts, products/plans, subscriptions, isolated wallets, grants, reservations, ledger, usage, payments, provider deduplication, outbox events, and webhook delivery.
- Row-locked credit reservations, idempotent grants/reservations/settlement, and append-only ledger writes.
- Durable SSE history with `Last-Event-ID` replay and a retrying webhook worker.
- Unit tests, real-PostgreSQL integration tests, an E2E topology, fake IdNest/Razorpay/webhook services, Docker image, and CI.

## Run locally

Requirements: Go 1.24+, Docker, and Docker Compose.

```sh
cp .env.example .env
make db-bootstrap
go run ./cmd/billmesh api
```

Billmesh automatically loads `.env` when it starts. Existing environment variables take precedence, and `.env` is ignored by Git. Copy `.env.example` for local development; production deployments should supply configuration through their environment or secret manager. Local configuration uses `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER`, `DB_PASSWORD`, and `DB_SSLMODE`; the legacy `DATABASE_URL` remains supported and takes precedence when provided.

`make db-bootstrap` starts the local PostgreSQL and mock services, synchronizes the local PostgreSQL role password with `DB_PASSWORD`, terminates connections to the local `billmesh` database, drops and recreates it, and applies every migration. Local bootstrap deliberately manages only the `billmesh` database configured as `DB_NAME=billmesh`.

Application tables, types, functions, indexes, and sequences live in the dedicated `billmesh` schema. Runtime connections use only that schema; migration connections temporarily retain `public` as a fallback so databases created with older migrations can be upgraded safely.

Generate URL-safe database passwords with OpenSSL:

```sh
openssl rand -hex 32
```

Generate a 32-byte base64url BFF session-encryption key with:

```sh
printf 'v1:'; openssl rand -base64 32 | tr '+/' '-_' | tr -d '=\n'; printf '\n'
```

Hexadecimal database passwords can be placed directly in a PostgreSQL URL without percent-encoding. Replace the checked-in local-only passwords in deployment configuration; do not commit generated secrets.

The API exposes `GET /healthz`, `GET /readyz`, account/product/wallet creation, grants, reservations, settlement, and `GET /v1/events` for SSE. Protected endpoints require a signed token from the configured issuer and the permission named by the handler.

### API documentation

The running API embeds an offline Swagger UI and OpenAPI 3.1 contract. Both are
private by default:

- Browser users with a valid BFF session can open [`http://localhost:8080/docs/`](http://localhost:8080/docs/).
- Bearer-token users can open [`http://localhost:8080/docs/access`](http://localhost:8080/docs/access). The token remains only in that page's memory and is sent to same-origin documentation and `/v1/*` requests.
- The raw contract at [`http://localhost:8080/openapi.yaml`](http://localhost:8080/openapi.yaml) accepts either a valid BFF session or a bearer token, for example `curl -H 'Authorization: Bearer …' http://localhost:8080/openapi.yaml`.

Swagger's static JavaScript and CSS assets remain public, but do not contain the
API contract. Set `PUBLIC_OPENAPI=true` only when intentionally exposing both
the UI and raw contract without authentication. The source contract is also
available in the repository at [`api/openapi.yaml`](api/openapi.yaml).

The service endpoints under `/v1/*` use bearer tokens. When the browser BFF is
enabled, the same protected handlers are mirrored under `/bff/v1/*` and use the
`__Host-billmesh-session` cookie plus `X-CSRF-Token` for unsafe requests.

Run `make openapi-check` after changing routes or the API contract. The check
validates the document and fails when a registered route is missing from it or
the contract contains an operation that is not registered by the service.

### Browser BFF

Billmesh can additionally expose a cookie-authenticated browser surface without changing the bearer-only service API:

- `/v1/*` continues to require exactly one `Authorization: Bearer` header.
- `/auth/*` implements OIDC authorization code login with PKCE, session inspection, refresh, and logout.
- `/bff/v1/*` uses an opaque `__Host-billmesh-session` cookie and dispatches to the same protected handlers as `/v1/*`.
- Unsafe BFF requests require the `X-CSRF-Token` returned by `GET /auth/session` and an exact allowed browser origin.
- Access, refresh, and ID tokens stay in encrypted PostgreSQL session records and are never returned to browser code.

Enable it only after registering a confidential Billmesh browser client with the identity provider:

```sh
export BFF_ENABLED=true
export BFF_APP_ORIGIN='https://billmesh.example'
export BFF_ISSUER="$OIDC_ISSUER"
export BFF_CLIENT_ID='billmesh-web'
export BFF_CLIENT_SECRET='replace-with-client-secret'
export BFF_AUDIENCE="$OIDC_AUDIENCE"
export BFF_REDIRECT_URI='https://billmesh.example/auth/callback'
export BFF_POST_LOGOUT_REDIRECT_URI='https://billmesh.example/auth/logout/callback'
export BFF_SESSION_ENCRYPTION_KEYS='v1:replace-with-base64url-encoded-32-byte-key'
export BFF_RETURN_PATH_PREFIXES='/app'
```

The first encryption-key entry encrypts new records; retained entries decrypt older records during key rotation. `BFF_ALLOW_INSECURE_HTTP=true` exists only for isolated local/test deployments. Production configuration requires HTTPS. The BFF issuer and audience must match Billmesh's resource-server issuer and audience so browser requests retain the same authorization contract as service requests.

Get a development token from the test-only fake issuer:

```sh
curl -s http://localhost:8090/test/token -X POST -H 'content-type: application/json' \
  -d '{"org_id":"org-1","app":"daybook","permissions":["billing:read","billing:write","billing:admin","credits:grant","credits:reserve","credits:settle"]}'
```

## Test

```sh
make test-unit         # pure domain and internal unit tests
make test-integration  # provisions, migrates, tests, and removes isolated PostgreSQL
make test-e2e          # migrations + PostgreSQL + running API/worker E2E
make test-performance  # provisions the isolated API/worker stack and runs benchmarks
make test-clean        # remove test containers and volumes
```

Integration, E2E, restore, and performance databases are disposable test infrastructure. Their Compose projects and volumes are removed automatically when each test command completes or is interrupted. Developers only configure and bootstrap `DB_NAME=billmesh`; never point test database URLs at development or production data.

`make test-performance` is also self-contained. It starts an isolated Compose project, waits for the API health check, runs the benchmark container with the required service URLs, and removes its containers and volumes afterward.

`make test-integration` is self-contained and uses the `billmesh-integration` Compose project with the database exposed only on local port `5433`. It always removes its containers and volume through a shell exit trap, including after failures or interruption. Override `INTEGRATION_DATABASE_URL` only when intentionally testing against another disposable PostgreSQL instance.

Tests are grouped under `tests/unit`, `tests/integration`, `tests/e2e`, and `tests/performance`, then by business category. Every checklist ID in `test-plan.md` is exposed as an individually named test or benchmark. The four cross-module business journeys live under `tests/e2e/journeys`.

Test commands use a grouped, Mocha-style reporter. Packages are rendered as suites, nested Go subtests are indented, passing, failing, and skipped cases have distinct colors, and every run ends with a compact test/package summary. Failure logs and assertion details remain visible, followed by a focused diagnostic summary. Set `NO_COLOR=1` to disable ANSI colors or `TEST_OUTPUT_STYLE=raw` to print unformatted `go test` output.

## API transaction guarantees

`POST /v1/wallets/{id}/reservations` locks the wallet and eligible grants, allocates earliest-expiring grants first, changes cached totals, appends a ledger entry, and writes an outbox event in one transaction. The tuple `(wallet_id, execution_id, operation_seq)` is its idempotency key. Settlement is also idempotent and returns unused reserved credits to the latest-expiring allocations first.

Run `sqlc generate` after changing files under `internal/database/queries`. Migrations are embedded into the production binary and managed by Goose.
