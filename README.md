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
local API at `http://127.0.0.1:5000`.

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

2. Create one certificate covering the API and both Billmesh web applications.
   Keep the certificate and private key outside the repositories:

   ```sh
   NGINX_ROOT="$(brew --prefix)/etc/nginx"
   mkdir -p "$NGINX_ROOT/ssl"
   mkcert \
     -cert-file "$NGINX_ROOT/ssl/local.billme.sh.pem" \
     -key-file "$NGINX_ROOT/ssl/local.billme.sh-key.pem" \
     api-local.billme.sh \
     console-local.billme.sh \
     admin-local.billme.sh
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
   HTTP_ADDR=:5000 go run ./cmd/billmesh api
   nginx -t
   brew services restart nginx
   curl https://api-local.billme.sh/healthz
   ```

When the browser BFF is enabled, use the HTTPS application origin that the
browser actually opens. For example, a Console-oriented API process uses
`BFF_APP_ORIGIN=https://console-local.billme.sh` and callback URLs on that
same origin. Register those exact HTTPS callback and logout URLs with the OIDC
provider. Do not set `BFF_ALLOW_INSECURE_HTTP=true` for this setup.

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
private by default. With the local HTTPS gateway configured:

- Browser users with a valid BFF session can open [`https://api-local.billme.sh/docs/`](https://api-local.billme.sh/docs/).
- Bearer-token users can open [`https://api-local.billme.sh/docs/access`](https://api-local.billme.sh/docs/access). The token remains only in that page's memory and is sent to same-origin documentation and `/v1/*` requests.
- The raw contract at [`https://api-local.billme.sh/openapi.yaml`](https://api-local.billme.sh/openapi.yaml) accepts either a valid BFF session or a bearer token, for example `curl -H 'Authorization: Bearer …' https://api-local.billme.sh/openapi.yaml`.

Without the optional gateway, replace the HTTPS origin above with the direct
development origin `http://localhost:5000`.

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

Enable it only after registering a confidential Billmesh browser client in
IdNest. Copy the BFF section from `.env.example` into the backend `.env`, then
replace its placeholders with the values from that IdNest client:

```dotenv
BFF_ENABLED=true
BFF_APP_ORIGIN=https://billmesh.example
BFF_ISSUER=https://idnest.example
BFF_CLIENT_ID=replace-with-idnest-client-id
BFF_CLIENT_SECRET=replace-with-idnest-client-secret
BFF_AUDIENCE=billmesh
BFF_SCOPE=openid profile email offline_access
BFF_REDIRECT_URI=https://billmesh.example/auth/callback
BFF_POST_LOGOUT_REDIRECT_URI=https://billmesh.example/auth/logout/callback
BFF_SESSION_ENCRYPTION_KEYS=v1:replace-with-base64url-encoded-32-byte-key
BFF_RETURN_PATH_PREFIXES=/app
BFF_ALLOW_INSECURE_HTTP=false
```

`BFF_CLIENT_ID` and `BFF_CLIENT_SECRET` are the credentials of the confidential
IdNest client. They belong only in the backend environment or deployment secret
manager; do not put them in the web application's runtime configuration or any
checked-in file. Register `BFF_REDIRECT_URI` and
`BFF_POST_LOGOUT_REDIRECT_URI` as exact allowed callback URLs for that client.

`BFF_ISSUER` and `BFF_AUDIENCE` must exactly match `OIDC_ISSUER` and
`OIDC_AUDIENCE`, respectively, so BFF and service-to-service requests use the
same token validation contract. The default scope requests a refresh token by
including `offline_access`; the IdNest client must be allowed to issue it.

`BFF_SESSION_ENCRYPTION_KEYS` is a Billmesh secret rather than an IdNest
credential. Generate it with the command in the local setup section above. The
first key entry encrypts new session records; retained entries decrypt older
records during key rotation. `BFF_ALLOW_INSECURE_HTTP=true` exists only for
isolated local/test deployments. Production configuration requires HTTPS.

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
