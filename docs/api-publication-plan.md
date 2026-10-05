# Billmesh API Publication Plan

Status notation:

- `[X]` — already implemented and verified.
- `[ ]` — required before the first production-stable API release.

## Objective

Publish one secure, typed, testable Billmesh API that Daybook and future
applications can integrate without embedding billing rules.

Billmesh is a fresh application. There are no external API consumers and no
backward-compatibility requirement. The first published contract will be the
canonical contract: duplicate routes and unsafe request fields will be removed,
not deprecated or supported in parallel.

## Publication decision

The release-candidate API may be shared with application teams for client work
and staging acceptance. It is ready for production publication only when every
P0 verification and operations gate and the release-candidate gate are complete.

### Release-candidate implementation snapshot

The repository now contains the planned v1 API changes: duplicate subscription
mutations are removed; identity context is route-specific; ownership transfer
is preauthorized and rechecked; snapshots and checkout are typed; webhook v2,
stable errors, request IDs, conditional polling, cursor pagination, provider
reconciliation, and the consumer integration documents are implemented.

The unchecked items below remain the full verification and operations ledger,
not an assertion that their corresponding code is absent. The remaining release
decision is driven primarily by test execution, IdNest and Razorpay environment
configuration, product seeding, generated-client proof, staging acceptance,
monitoring/runbooks, and an explicit contract tag. Tests were intentionally not
run as part of this implementation session at the request of the repository
owner.

The initial publication covers the consumer billing control plane:

- catalogue and credit-pack discovery;
- billing-account onboarding;
- subscription transitions and cancellation;
- provider-backed checkout;
- the authoritative billing snapshot;
- payment and invoice reads;
- webhook endpoint management and version 2 event delivery.

Administration, provider callbacks, browser BFF endpoints, and operational
repair endpoints are documented separately and are not part of the consumer API
contract. Wallet reservation, settlement, usage metering, and SSE are published
only if a launch consumer requires them; otherwise they remain an explicitly
deferred contract rather than silently becoming public.

## Locked publication principles

- [X] Billmesh is the authoritative source for catalogue, prices, eligibility,
  subscriptions, payment verification, invoices, credits, limits, and
  entitlements.
- [X] Consumers send intent and store only a revisioned, non-authoritative
  projection.
- [X] Paid access is activated only by a provider event verified by Billmesh.
- [X] Plan and credit-pack IDs are opaque to consumers.
- [X] One operation exists for each consumer intent; no alternate direct
  subscription mutation path is published.
- [ ] Every published request and response is fully described by OpenAPI 3.1.
- [X] Every failure has a stable machine-readable code and request ID.
- [X] Webhooks are invalidation signals; snapshot reconciliation is the recovery
  mechanism.
- [ ] A generated client can integrate without reading handler code or relying
  on undocumented JSON.

## Target public surface

Phase 0 must turn this table into an enforced allowlist. Routes not listed here
are not part of the initial consumer contract.

| Capability | Canonical operation | Decision |
| --- | --- | --- |
| Public catalogue | `GET /v1/public/catalog` | Keep for products whose policy permits public access |
| Protected catalogue | `GET /v1/catalog` | Keep; support an application-scoped catalogue token |
| Account onboarding | `POST /v1/accounts` | Keep; resolve customer and environment from verified identity context |
| Current account | `GET /v1/accounts/current` | Keep |
| Select/change/reactivate plan | `POST /v1/subscription-transitions` | Keep as the only plan-selection mutation |
| Transition status | `GET /v1/subscription-transitions/{id}` | Keep; add conditional polling |
| Cancel transition | `POST /v1/subscription-transitions/{id}/cancel` | Keep; make idempotent |
| Cancel/withdraw subscription cancellation | `POST /v1/subscriptions/current/cancellation` | Keep as the only cancellation mutation |
| Authoritative read model | `GET /v1/billing-snapshot` | Keep and fully type |
| Credit packs | Catalogue `credit_packs` plus `GET /v1/credit-packs` | Keep opaque-ID discovery |
| Credit-pack checkout | `POST /v1/payments/orders` | Keep and align checkout schema with subscription checkout |
| Payments and invoices | `GET /v1/payments`, `GET /v1/invoices` | Keep; add stable pagination before publication |
| Webhook endpoints | `/v1/webhooks` and secret rotation operations | Keep; publish only version 2 delivery semantics |
| Ownership transfer | New `/v1/account-ownership-transfers` operations | Add if ownership can change; otherwise explicitly reject ownership changes in v1 |

Remove from routing, handlers, tests, and OpenAPI before publication:

- [X] `POST /v1/subscriptions`;
- [X] `POST /v1/subscriptions/{id}/change-plan`;
- [X] `POST /v1/subscriptions/{id}/cancel`;
- [X] `POST /v1/subscriptions/current/cancel`;
- [X] `POST /v1/subscriptions/{id}/reactivate`;
- [X] `POST /v1/subscriptions/{id}/renew`;
- [X] public request fields that accept a plan slug or `payment_status`;
- [X] any account-linking operation that allows a normal consumer to choose an
  environment or cross an application boundary.

Renewal, expiration, dunning, and scheduled transitions are Billmesh state
machine and worker responsibilities, not commands by which a consumer asserts a
billing outcome.

## Delivery order

| Phase | Priority | Depends on | Outcome |
| --- | --- | --- | --- |
| 0. Freeze the surface | P0 | None | One route allowlist and no duplicate subscription mutations |
| 1. Lock identity | P0 | Phase 0 | Issuer, audience, token types, claims, permissions, and route policies are testable |
| 2. Complete customer ownership | P0 | Phase 1 | Initial ownership and ownership changes cannot bypass Free eligibility |
| 3. Type the snapshot | P0 | Phases 0–1 | Generated clients can safely enforce the complete read model |
| 4. Complete checkout | P0 | Phases 1 and 3 | A consumer can launch checkout without secrets or hard-coded provider data |
| 5. Freeze webhooks | P0 | Phase 3 | Signed, versioned invalidation events are implementable from documentation |
| 6. Standardize commands and errors | P0 | Phases 0–5 | Idempotency, concurrency, errors, and request tracing are uniform |
| 7. Reconciliation and operations | P0 | Phases 4–6 | Missed events and stuck work converge automatically and alert operators |
| 8. Contract quality and client proof | P0 | Phases 0–7 | Strict OpenAPI and a generated reference client pass conformance tests |
| 9. Staging acceptance and publication | P0 | All prior phases | Daybook passes the release-candidate gate and the contract is tagged |
| 10. Deferred usage/credit surface | P1 | Publication or explicit launch need | Workload billing APIs receive the same contract treatment before exposure |

Phases with the same satisfied dependencies may be implemented in parallel, but
their exit criteria remain independent.

## Phase 0 — Freeze the consumer API surface

### Implementation

- [X] Create a route inventory that classifies every registered operation as
  consumer, admin, provider callback, browser BFF, internal operations, or
  deferred.
- [X] Delete the duplicate direct subscription handlers and OpenAPI operations
  listed above.
- [X] Delete slug and `payment_status` fields from all consumer request schemas
  and Go inputs.
- [X] Make transitions cover activation, plan change, upgrade, downgrade, and
  reactivation based on current state and target plan.
- [X] Keep subscription renewal, dunning, expiry, and provider-driven completion
  inside the Billmesh state machine and worker.
- [X] Replace ID-addressed consumer account mutations with current-account
  operations where the account is derivable from the token.
- [X] Gate admin, payment-provider, and repair operations with distinct security
  schemes and tags so generated consumer clients exclude them.
- [ ] Add a CI test asserting that the registered consumer allowlist and the
  OpenAPI consumer operations are identical.

### Tests

- [ ] Route tests prove removed operations return `404`.
- [ ] Security tests prove a consumer token cannot call admin, provider, repair,
  or cross-tenant operations.
- [ ] Transition E2E tests cover every supported consumer intent after the
  direct handlers are deleted.

### Exit criteria

- [X] There is exactly one public mutation path for each subscription intent.
- [X] No published request accepts price, billing model, plan slug, environment,
  or payment-verification status from a normal consumer.

## Phase 1 — Lock the IdNest token profile

### Token types

Define and document three non-overlapping token profiles:

1. Application catalogue token: `iss`, Billmesh `aud`, stable service `sub`,
   `app`, `environment`, actor type `service`, and `catalogue:read`; it does not
   require an organization.
2. Organization billing token: the application claims plus `org_id` and
   `billing:read` and/or `billing:write`. A delegated owner token identifies a
   human customer; a service token uses only an explicitly trusted customer
   reference agreed with IdNest.
3. Administrative token/session: separate client, permissions, audience rules,
   and route group; never accepted as an ordinary consumer token by accident.

### Implementation

- [ ] Agree exact claim names and encodings with IdNest, including `actor_type`,
  environment, permissions/scopes, and any `billing_customer_id` reference.
- [ ] Publish issuer and audience per environment, maximum token lifetime,
  allowed clock skew, signing algorithm, JWKS refresh behavior, and key-rotation
  procedure.
- [ ] Refactor authentication so required context is route-specific. Catalogue
  tokens must not be forced to invent an `org_id`; organization commands must
  reject tokens without one.
- [ ] Use dedicated permissions: `catalogue:read`, `billing:read`,
  `billing:write`, and, if Phase 2 requires it, `billing:ownership`.
- [ ] Stop accepting multiple undocumented permission claim shapes before
  publication; publish the one canonical encoding.
- [ ] Add the token profiles and examples to OpenAPI security descriptions and
  integration documentation.

### Tests

- [ ] Contract tests for each token type, issuer, audience, expiry, not-before,
  clock skew, signing algorithm, missing claim, wrong app, wrong organization,
  wrong environment, and unknown key.
- [ ] Cross-product, cross-tenant, and cross-environment authorization matrices.
- [ ] Key-rotation and temporary JWKS-outage tests.

### Exit criteria

- [ ] IdNest can mint each documented token profile in staging.
- [ ] Every consumer route states which token type and permissions it accepts.
- [ ] A service token can never be mistaken for a human owner.

## Phase 2 — Complete customer identity and ownership

### Decision gate

- [ ] Confirm whether organization ownership can change in every consuming
  application. If it cannot change in v1, document and enforce rejection. If it
  can change, implement the protocol below before publication.
- [ ] Select one trusted new-owner representation: a short-lived IdNest-signed
  ownership assertion is preferred; an opaque `billing_customer_id` supplied by
  a service token with `billing:ownership` is acceptable. Do not accept an
  untrusted arbitrary subject from a normal `billing:write` request.

### Proposed ownership-transfer API

- [ ] `POST /v1/account-ownership-transfers` pre-authorizes a proposed owner,
  serializes eligibility checks, and returns an intent ID, decision code,
  required action, and expiry.
- [ ] `GET /v1/account-ownership-transfers/{id}` returns typed status without
  exposing private identity data.
- [ ] `POST /v1/account-ownership-transfers/{id}/confirm` atomically changes the
  Billmesh customer link after the application commits the ownership change.
- [ ] `POST /v1/account-ownership-transfers/{id}/cancel` idempotently cancels an
  unused intent.
- [ ] Use `Idempotency-Key` on create/confirm/cancel and `If-Match` with the
  billing revision on create and confirm.
- [ ] Define statuses `authorized`, `requires_paid_transition`, `ineligible`,
  `confirmed`, `cancelled`, and `expired`, with stable reason codes.
- [ ] Persist immutable ownership history and audit events.
- [ ] Define recovery when the application changes ownership but confirmation
  fails, including an expiring reconciliation job and operator runbook.

### Tests

- [ ] Concurrent account creation cannot obtain multiple Free allowances for one
  customer.
- [ ] Concurrent transfer intents serialize correctly.
- [ ] Ineligible, expired, replayed, cancelled, and cross-tenant transfers fail
  safely.
- [ ] A transfer cannot retain or grant Free access contrary to product policy.
- [ ] Audit and billing revision changes are atomic with confirmation.

### Exit criteria

- [ ] Initial customer resolution and ownership change use verified identity.
- [ ] Creating or transferring organizations cannot bypass customer-level Free
  eligibility.

## Phase 3 — Fully type the authoritative snapshot

### Schemas

- [ ] Define `BillingSnapshotAccount`.
- [ ] Define `BillingSnapshotSubscription`, including status, period,
  cancellation schedule, billing-policy version, and typed effective plan.
- [ ] Define `BillingSnapshotEffectivePlan`, including immutable plan ID and
  version, display fields, billing model, price, currency, interval, included
  credits, and entitlement-schema version.
- [ ] Define `BillingSnapshotTransition`, including target plan, operation,
  timing, status, effective/expiry timestamps, failure code, and policy version.
- [ ] Define typed credit balances separately from resource limits.
- [ ] Define entitlement values as a documented JSON value union and include the
  entitlement-schema version needed to validate product-specific keys.
- [ ] Define resource-limit entries with key, value, unit, enforcement mode, and
  count semantics.
- [ ] Set `additionalProperties: false` on fixed structural objects.
- [ ] Describe `revision`, ETag, `effective_at`, `verified_at`, `expires_at`, and
  `degraded_until` semantics precisely.

### Implementation

- [ ] Replace handler `map[string]any` construction with typed response models.
- [ ] Validate stored entitlements against the snapshotted entitlement schema.
- [ ] Ensure every snapshot-visible mutation increments the revision in the
  same transaction.
- [ ] Return explicit `subscription: null`, `pending_transition: null`, and
  empty typed entitlements for an account without a subscription.
- [ ] Define whether high-frequency credit balance changes share the billing
  revision or expose a separate `credit_revision`; implement one consistent
  choice.

### Tests

- [ ] OpenAPI response validation for subscribed, unsubscribed, pending,
  scheduled cancellation, expired, and retired-plan snapshots.
- [ ] Concurrent mutation consistency and monotonic revision tests.
- [ ] Compatibility fixtures consumed by at least two generated client
  languages or by the reference client plus a schema validator.
- [ ] Fresh, degraded, and expired projection enforcement tests.

### Exit criteria

- [ ] No snapshot field required for display or enforcement is undocumented or
  open-ended.
- [ ] A consumer can render and enforce billing from this single read model.

## Phase 4 — Complete the checkout contract

### API design

Return a discriminated `checkout` object from a transition or credit-pack order:

- [ ] `provider`, `presentation`, `expires_at`, amount, and currency;
- [ ] either a provider-hosted `checkout_url`; or
- [ ] an inline/modal `client_config` containing the public provider key, order
  ID, customer-safe display fields, allowed callback/redirect values, and no
  secret;
- [ ] stable states and failure codes for abandoned, expired, failed, delayed,
  mismatched, refunded, and disputed payments.

### Implementation

- [ ] Source every amount, currency, order, and public checkout value from
  Billmesh configuration and the selected catalogue item.
- [ ] Validate redirect origins against product policy.
- [ ] Make checkout creation idempotent with the parent transition/order.
- [ ] Ensure client callbacks and redirects cannot complete a transition.
- [ ] Define how a consumer resumes a still-valid checkout and handles expiry.
- [ ] Use the same checkout abstraction for subscription and credit-pack
  purchases where their semantics overlap.

### Tests

- [ ] Browser/reference-client test can launch Razorpay test checkout using only
  the response contract.
- [ ] Tests prove no secret, internal provider credential, or untrusted amount is
  exposed or accepted.
- [ ] E2E tests cover success, delay, abandonment, duplicate capture, mismatch,
  expiry, failure, refund, and dispute.

### Exit criteria

- [ ] Daybook can complete sandbox checkout without hard-coded Billmesh or
  Razorpay configuration beyond the Billmesh base URL.
- [ ] Only the verified provider-event transaction can activate paid access.

## Phase 5 — Freeze outbound webhook version 2

### Contract

- [ ] Add an OpenAPI `BillmeshWebhookEventV2` schema with `specversion`,
  `schema_version`, event ID, type, occurrence time, account and product IDs,
  billing revision, aggregate reference, and typed/versioned data.
- [ ] Publish the event-type catalogue and the minimum data schema for each
  supported type.
- [ ] Require the version 2 headers documented in the usage guide.
- [ ] Specify lowercase hexadecimal HMAC-SHA256 over
  `timestamp + "." + raw_body`, constant-time comparison, and a fixed timestamp
  tolerance.
- [ ] Specify secret-rotation overlap and validation of current and previous
  signatures.
- [ ] Specify at-least-once delivery, no assumed global ordering, event-ID
  deduplication, and revision-based convergence.
- [ ] Fix and document retry attempts, backoff, request timeout, event retention,
  replay retention, and terminal-failure behavior.

### Implementation

- [ ] Remove version 1 registration for new endpoints, or remove version 1
  entirely because there are no external consumers.
- [ ] Validate event filters against the supported event catalogue.
- [ ] Add the authoritative snapshot URL to the envelope or define its stable
  derivation.
- [ ] Add consumer-visible delivery health or ensure Billmesh operators own and
  alert on it through an explicit operational contract.
- [ ] Ensure a disabled/deleted endpoint cannot receive newly enqueued events.

### Tests

- [ ] Golden request fixtures for every event envelope and signature.
- [ ] Tests for tampering, stale/future timestamp, duplicate delivery,
  out-of-order revision, rotation overlap, disabled endpoint, retry exhaustion,
  and replay.
- [ ] A reference receiver durably records, deduplicates, acknowledges, and
  conditionally refetches the snapshot.

### Exit criteria

- [ ] A consumer can implement a secure receiver from OpenAPI and this contract
  without reading worker code.
- [ ] Missing, duplicate, or reordered events converge through snapshot refresh.

## Phase 6 — Standardize commands, concurrency, and errors

### Error contract

- [ ] Make `Error.code` required and publish a registry grouped by validation,
  authentication, authorization, not-found, conflict, policy, payment,
  concurrency, rate-limit, and dependency failures.
- [ ] Add required `request_id` and optional safe `details` with a typed field
  error shape; never expose internal database/provider messages.
- [ ] Return `X-Request-ID` on every response and accept a valid caller
  correlation ID without trusting it as authorization.
- [ ] Document explicit non-default responses for every public operation,
  including `400`, `401`, `403`, `404`, `409`, `412`, `428`, `429`, and `503`
  where applicable.

### Idempotency and optimistic concurrency

- [ ] Require `Idempotency-Key` for every externally retryable mutation,
  including transition cancellation and ownership operations.
- [ ] Define key scope, maximum length, minimum retention, identical replay
  response, concurrent duplicate behavior, and payload-conflict response.
- [ ] Persist status code and response body so a replay returns the original
  semantic result.
- [ ] Use `If-Match` consistently for commands whose decision depends on a
  snapshot revision; return a typed stale-revision error.
- [ ] Add `If-None-Match` and `304` to transition polling.

### Tests

- [ ] Table-driven error-code tests for every public endpoint.
- [ ] Concurrent identical and conflicting idempotency-key tests.
- [ ] Stale, missing-required, malformed, and current ETag tests.
- [ ] Request-ID propagation and error-redaction tests.

### Exit criteria

- [ ] Consumers branch only on HTTP status and stable error code, never message
  text.
- [ ] Retrying any documented command cannot duplicate a billing effect.

## Phase 7 — Reconciliation, observability, and operations

### Automated reconciliation

- [ ] Reconcile provider orders and transitions stuck in `requires_payment` or
  `processing` by querying the provider and applying the same verified event
  transaction.
- [ ] Detect captured provider payments without a completed Billmesh transition
  and vice versa.
- [ ] Expire abandoned checkout and ownership intents deterministically.
- [ ] Document the consumer snapshot-reconciliation interval and maximum
  projection age from product policy.
- [ ] Verify that replay and reconciliation are idempotent.

### Observability

- [ ] Metrics and dashboards for transition duration, checkout abandonment,
  payment failures, provider mismatches, outbox age, webhook lag/failure,
  reconciliation repairs, and snapshot age.
- [ ] Alerts for stuck transitions, repeated delivery failure, provider mismatch,
  revision divergence, reconciliation failure, and unauthorized mutation
  attempts.
- [ ] Structured logs carry request ID, event ID, transition ID, payment ID,
  account ID, application, and environment while excluding secrets and unsafe
  identity/payment data.

### Runbooks

- [ ] Missed or delayed provider event.
- [ ] Stuck or mismatched transition.
- [ ] Webhook delivery replay and secret rotation.
- [ ] Incorrect customer ownership or eligibility decision.
- [ ] Refund, dispute, and manual correction.
- [ ] IdNest/JWKS outage and Billmesh degraded operation.

### Exit criteria

- [ ] Missed provider and Billmesh webhooks converge without granting duplicate
  subscriptions, invoices, or credits.
- [ ] Every alert has an owner, severity, and tested runbook.

## Phase 8 — Contract quality and reference-client proof

### OpenAPI and documentation

- [ ] Quote permission extensions and pass strict YAML 1.2 and OpenAPI 3.1
  validation.
- [ ] Remove undocumented operations and document every registered public
  operation, parameter, header, request, response, and security requirement.
- [ ] Add examples for catalogue, onboarding, transition, checkout, snapshot,
  cancellation, errors, and webhooks.
- [ ] Publish a versioning and compatibility policy for `/v1`, including what is
  additive and what requires `/v2`.
- [ ] Publish environment URLs, rate limits, pagination, timeouts, support
  contacts, and change-log policy.

### Client proof

- [ ] Generate a typed client from `api/openapi.yaml` in CI.
- [ ] Build a small reference integration that performs catalogue fetch, account
  onboarding, snapshot cache, transition, checkout handoff, cancellation, and
  webhook invalidation without importing Billmesh internals.
- [ ] Add provider-agnostic contract tests that run against a deployed staging
  Billmesh instance.
- [ ] Fail CI on OpenAPI drift, breaking schema changes, invalid examples, or a
  generated-client compile failure.

### Exit criteria

- [ ] The reference integration requires no undocumented fields, routes,
  permissions, or operational assumptions.
- [ ] A breaking-change detector reports no unapproved break from the release
  candidate contract.

## Phase 9 — Staging acceptance and publication

### Billmesh staging setup

- [ ] Provide isolated staging base URL, IdNest configuration, Razorpay test
  account, worker, database backups, and monitoring.
- [ ] Seed the Daybook product, billing policy, entitlement schema, Free and Paid
  plans, and credit packs through repeatable deployment data.
- [ ] Register and rotate a Daybook webhook secret through the published API.

### Daybook acceptance

- [ ] Render catalogue without embedded slugs, prices, or billing classes.
- [ ] Create/resolve billing account with the agreed owner identity.
- [ ] Persist snapshot body, ETag, revision, and freshness timestamps.
- [ ] Enforce entitlements and resource limits server-side.
- [ ] Complete Free activation, paid checkout, upgrade, downgrade, cancellation,
  cancellation withdrawal, and reactivation through canonical operations.
- [ ] Confirm redirects never activate access before the snapshot changes.
- [ ] Receive, verify, persist, deduplicate, and process webhook version 2.
- [ ] Reconcile snapshots after dropped and reordered webhook tests.
- [ ] Demonstrate safe behavior during Billmesh, IdNest, and provider outages.

### Release-candidate gate

- [ ] All P0 phase exit criteria are complete.
- [ ] Full unit, integration, E2E, security, concurrency, migration, restore, and
  performance suites pass from a clean environment.
- [ ] No open critical/high security finding or unresolved paid-activation path.
- [ ] OpenAPI is tagged as the release candidate and its checksum is recorded.
- [ ] Billmesh, IdNest, Daybook, payments, and operations owners approve their
  responsibilities and runbooks.
- [ ] Production deployment and rollback are rehearsed in staging.

### Publication

- [ ] Tag the first stable API release.
- [ ] Publish OpenAPI, the usage guide, token profile, webhook contract, error
  catalogue, changelog, and support policy together.
- [ ] Enable production credentials only for applications that pass the same
  conformance checklist.
- [ ] Monitor the first production application with tightened alerts and a
  documented review window.

## Phase 10 — Deferred workload credit and metering APIs

Before wallets, grants, reservations, settlement, installations, usage events,
limits, or SSE are advertised to other applications:

- [ ] classify the caller and permission model for each operation;
- [ ] replace caller-supplied account/product IDs where claims can resolve them;
- [ ] fully type wallet, ledger, reservation, usage, limit, and event schemas;
- [ ] specify idempotency, ordering, pagination, retention, and concurrency;
- [ ] add cross-tenant, overspend, replay, and partial-failure tests;
- [ ] prove the flow with a separate generated reference client; and
- [ ] add the approved operations to the public allowlist and compatibility
  policy.

These operations are not implicitly public merely because they already exist in
the service.

## Definition of ready to publish

Billmesh is ready to publish when:

- [ ] the route allowlist exposes only canonical consumer operations;
- [ ] the IdNest token profile and ownership rules are implemented and tested;
- [ ] catalogue, transition, checkout, snapshot, error, and webhook contracts are
  fully typed and versioned;
- [ ] no consumer can submit price, plan slug, environment, or payment status;
- [ ] paid access requires a matching verified provider event;
- [ ] commands are idempotent and stale-state safe;
- [ ] snapshots and reconciliation recover from missing or duplicate events;
- [ ] generated clients and the Daybook staging integration pass conformance;
- [ ] monitoring, alerts, backups, recovery, and runbooks are operational; and
- [ ] the complete release-candidate gate is approved.
