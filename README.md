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
docker compose -f deploy/compose.test.yml -f deploy/compose.dev.yml up -d postgres mock-external
export DATABASE_URL='postgres://billmesh:testpassword@localhost:5433/billmesh_e2e?sslmode=disable'
export OIDC_ISSUER='http://localhost:8090'
export OIDC_AUDIENCE='billmesh-test'
export JWKS_URL='http://localhost:8090/.well-known/jwks.json'
go run ./cmd/billmesh migrate up
go run ./cmd/billmesh api
```

The API exposes `GET /healthz`, `GET /readyz`, account/product/wallet creation, grants, reservations, settlement, and `GET /v1/events` for SSE. Protected endpoints require a signed token from the configured issuer and the permission named by the handler.

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

Integration and E2E databases are separate. Never point their `DATABASE_URL` at development or production data.

`make test-performance` is also self-contained. It starts an isolated Compose project, waits for the API health check, runs the benchmark container with the required service URLs, and removes its containers and volumes afterward.

`make test-integration` is self-contained and uses the `billmesh-integration` Compose project with the database exposed only on local port `5433`. It always removes its containers and volume through a shell exit trap, including after failures or interruption. Override `INTEGRATION_DATABASE_URL` only when intentionally testing against another disposable PostgreSQL instance.

Tests are grouped under `tests/unit`, `tests/integration`, `tests/e2e`, and `tests/performance`, then by business category. Every checklist ID in `test-plan.md` is exposed as an individually named test or benchmark. The four cross-module business journeys live under `tests/e2e/journeys`.

Test commands use a grouped, Mocha-style reporter. Packages are rendered as suites, nested Go subtests are indented, passing, failing, and skipped cases have distinct colors, and every run ends with a compact test/package summary. Failure logs and assertion details remain visible, followed by a focused diagnostic summary. Set `NO_COLOR=1` to disable ANSI colors or `TEST_OUTPUT_STYLE=raw` to print unformatted `go test` output.

## API transaction guarantees

`POST /v1/wallets/{id}/reservations` locks the wallet and eligible grants, allocates earliest-expiring grants first, changes cached totals, appends a ledger entry, and writes an outbox event in one transaction. The tuple `(wallet_id, execution_id, operation_seq)` is its idempotency key. Settlement is also idempotent and returns unused reserved credits to the latest-expiring allocations first.

Run `sqlc generate` after changing files under `internal/database/queries`. Migrations are embedded into the production binary and managed by Goose.
