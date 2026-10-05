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

### Local HTTPS with Nginx

The API can be exposed at `https://api-local.billme.sh` through a
developer-managed Nginx reverse proxy, following the same convention as the
Daybook local environment. Nginx terminates TLS and forwards requests to the
local API at `http://127.0.0.1:5001`.

The repository provides a proposed configuration at
[`docs/nginx/api-local.billme.sh.conf.example`](docs/nginx/api-local.billme.sh.conf.example).
It does not install Nginx, create certificates, edit `/etc/hosts`, copy files
outside the repository, or reload Nginx.

The examples below assume Homebrew on Apple Silicon, where Nginx uses
`/opt/homebrew/etc/nginx`. If `brew --prefix` prints another prefix, update the
certificate paths in the proposed configuration before installing it.

1. Install the local prerequisites:

   ```sh
   brew install nginx mkcert
   mkcert -install
   ```

   `mkcert -install` adds the local development CA to this machine's trust
   stores. Run it only on a developer workstation.

2. Create one wildcard certificate covering the Billmesh local subdomains.
   Keep the certificate and private key outside the repositories:

   ```sh
   NGINX_ROOT="$(brew --prefix)/etc/nginx"
   mkdir -p "$NGINX_ROOT/ssl"
   mkcert \
     -cert-file "$NGINX_ROOT/ssl/local.billme.sh.pem" \
     -key-file "$NGINX_ROOT/ssl/local.billme.sh-key.pem" \
     "*.billme.sh"
   chmod 600 "$NGINX_ROOT/ssl/local.billme.sh-key.pem"
   ```

3. Add the local names to `/etc/hosts` (once, without duplicating an existing
   entry):

   ```text
   127.0.0.1 api-local.billme.sh console-local.billme.sh admin-local.billme.sh
   ```

4. Copy the proposed API server block into the Homebrew Nginx `servers`
   directory:

   ```sh
   NGINX_ROOT="$(brew --prefix)/etc/nginx"
   mkdir -p "$NGINX_ROOT/servers"
   cp docs/nginx/api-local.billme.sh.conf.example \
     "$NGINX_ROOT/servers/api-local.billme.sh.conf"
   ```

   Ensure the `http` block in `$NGINX_ROOT/nginx.conf` contains
   `include servers/*;`. Install the Console and Admin server blocks from the
   sibling `billmesh-web` repository when those applications are needed.

5. Start Billmesh normally, then validate and reload the developer-managed
   proxy:

   ```sh
   HTTP_ADDR=:5001 go run ./cmd/billmesh api
   nginx -t
   brew services restart nginx
   curl https://api-local.billme.sh/healthz
   ```

`api-local.billme.sh` owns the Console and Admin session cookies. Register the
realm-specific callback and logout URLs from `.env.example` with IdNest. The
API allows credentialed CORS only from `CONSOLE_APP_ORIGIN` and
`ADMIN_APP_ORIGIN`; all browser-authentication URLs must use HTTPS.

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

The running API embeds an offline Swagger UI and OpenAPI 3.1 contract. The UI
and raw contract always require authentication. With the local HTTPS gateway
configured:

- Bearer-token users can open [`https://api-local.billme.sh/docs/access`](https://api-local.billme.sh/docs/access). The token remains only in that page's memory and is sent to same-origin documentation and `/v1/*` requests.
- The raw contract at [`https://api-local.billme.sh/openapi.yaml`](https://api-local.billme.sh/openapi.yaml) accepts a bearer token, for example `curl -H 'Authorization: Bearer …' https://api-local.billme.sh/openapi.yaml`. A configured Console or Admin origin may also fetch it with its BFF session.

Without the optional gateway, replace the HTTPS origin above with the direct
development origin `http://localhost:5001`.

Swagger's static JavaScript and CSS assets remain public, but do not contain the
API contract. The source contract is also available in the repository at
[`api/openapi.yaml`](api/openapi.yaml).

Consumer applications should also follow
[`docs/how-to-use-billmesh.md`](docs/how-to-use-billmesh.md), which defines the
Billmesh/application responsibility boundary, the supported integration flow,
and the remaining production-publication gates. The exact consumer route
allowlist is documented in
[`docs/public-api-surface.md`](docs/public-api-surface.md). The ordered
implementation and release work is tracked in
[`docs/api-publication-plan.md`](docs/api-publication-plan.md).

The service identity, event-delivery, and failure contracts are documented in
[`docs/idnest-token-profile.md`](docs/idnest-token-profile.md),
[`docs/webhook-contract.md`](docs/webhook-contract.md), and
[`docs/api-error-codes.md`](docs/api-error-codes.md).

The service endpoints under `/v1/*` use bearer tokens. The browser-facing
protected handlers are mirrored under `/api/v1/*` and use a realm-specific
`__Host-billmesh-*-session` cookie plus `X-CSRF-Token` for unsafe requests.

Run `make openapi-check` after changing routes or the API contract. The check
validates the document and fails when a registered route is missing from it or
the contract contains an operation that is not registered by the service.

### Browser BFF

Billmesh exposes a cookie-authenticated browser surface without changing the bearer-only service API:

- `/v1/*` continues to require exactly one `Authorization: Bearer` header.
- `/api/v1/auth/console/*` and `/api/v1/auth/admin/*` implement independent OIDC authorization-code flows with PKCE, session inspection, refresh, and logout.
- `/api/v1/*` uses the opaque session associated with the exact request origin and dispatches to the same protected handlers as `/v1/*`.
- Unsafe BFF requests require the `X-CSRF-Token` returned by the realm's session endpoint and an exact allowed browser origin.
- Access, refresh, and ID tokens stay in encrypted PostgreSQL session records and are never returned to browser code.

Register separate confidential Billmesh Console and Admin clients in IdNest.
Copy the BFF section from `.env.example` into the backend `.env`, then replace
its placeholders with the client values:

```dotenv
CONSOLE_APP_ORIGIN=https://console.billme.sh
ADMIN_APP_ORIGIN=https://admin.billme.sh
BFF_CONSOLE_ISSUER=https://idnest.example
BFF_CONSOLE_CLIENT_ID=replace-with-idnest-console-client-id
BFF_CONSOLE_CLIENT_SECRET=replace-with-idnest-console-client-secret
BFF_CONSOLE_AUDIENCE=billmesh
BFF_CONSOLE_SCOPE=openid profile email offline_access
BFF_CONSOLE_REDIRECT_URI=https://api.billme.sh/api/v1/auth/console/callback
BFF_CONSOLE_POST_LOGOUT_REDIRECT_URI=https://api.billme.sh/api/v1/auth/console/logout/callback
BFF_CONSOLE_STANDALONE_LOGOUT_URI=https://auth.idnest.example/logout
BFF_ADMIN_ISSUER=https://idnest.example
BFF_ADMIN_CLIENT_ID=replace-with-idnest-admin-client-id
BFF_ADMIN_CLIENT_SECRET=replace-with-idnest-admin-client-secret
BFF_ADMIN_AUDIENCE=billmesh
BFF_ADMIN_SCOPE=openid profile email offline_access billing:admin
BFF_ADMIN_REDIRECT_URI=https://api.billme.sh/api/v1/auth/admin/callback
BFF_ADMIN_POST_LOGOUT_REDIRECT_URI=https://api.billme.sh/api/v1/auth/admin/logout/callback
BFF_ADMIN_STANDALONE_LOGOUT_URI=https://auth.idnest.example/logout
BFF_SESSION_ENCRYPTION_KEYS=v1:replace-with-base64url-encoded-32-byte-key
```

The client IDs and secrets are credentials of the confidential IdNest clients.
They belong only in the backend environment or deployment secret manager; do
not put them in the web application's runtime configuration or any checked-in
file. Register each realm's redirect and post-logout redirect URI exactly.

Each realm has its own issuer, audience, client, scope, and token verifier. The
default scope requests a refresh token by including `offline_access`; both
IdNest clients must be allowed to issue it. The Admin client should include the
`billing:admin` permission required by privileged Billmesh operations.

`BFF_SESSION_ENCRYPTION_KEYS` is a Billmesh secret rather than an IdNest
credential. Generate it with the command in the local setup section above. The
first key entry encrypts new session records; retained entries decrypt older
records during key rotation. Browser-authentication origins, issuer endpoints,
and callbacks always require HTTPS. Console and Admin return paths default to
and are restricted to `/app` by application policy.

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
docker builder prune
```

Integration, E2E, restore, and performance databases are disposable test infrastructure. Their Compose projects and volumes are removed automatically when each test command completes or is interrupted. Developers only configure and bootstrap `DB_NAME=billmesh`; never point test database URLs at development or production data.

`make test-performance` is also self-contained. It starts an isolated Compose project, waits for the API health check, runs the benchmark container with the required service URLs, and removes its containers and volumes afterward.

`make test-integration` is self-contained and uses the `billmesh-integration` Compose project with a memory-backed disposable database exposed only on local port `5434`, avoiding the development database on port `5433`. It always removes its container through a shell exit trap, including after failures or interruption. Override `INTEGRATION_DB_PORT` if port `5434` is unavailable, or override `INTEGRATION_DATABASE_URL` only when intentionally testing against another disposable PostgreSQL instance.

Tests are grouped under `tests/unit`, `tests/integration`, `tests/e2e`, and `tests/performance`, then by business category. Every checklist ID in `test-plan.md` is exposed as an individually named test or benchmark. The four cross-module business journeys live under `tests/e2e/journeys`.

Test commands use a grouped, Mocha-style reporter. Packages are rendered as suites, nested Go subtests are indented, passing, failing, and skipped cases have distinct colors, and every run ends with a compact test/package summary. Failure logs and assertion details remain visible, followed by a focused diagnostic summary. Set `NO_COLOR=1` to disable ANSI colors or `TEST_OUTPUT_STYLE=raw` to print unformatted `go test` output.

## API transaction guarantees

`POST /v1/wallets/{id}/reservations` locks the wallet and eligible grants, allocates earliest-expiring grants first, changes cached totals, appends a ledger entry, and writes an outbox event in one transaction. The tuple `(wallet_id, execution_id, operation_seq)` is its idempotency key. Settlement is also idempotent and returns unused reserved credits to the latest-expiring allocations first.

Run `sqlc generate` after changing files under `internal/database/queries`. Migrations are embedded into the production binary and managed by Goose.
