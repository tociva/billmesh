# Razorpay integration TDD checklist

This document is the execution tracker for integrating Razorpay with Billmesh.
Every actionable step is a checkbox and should remain unchecked until its
acceptance evidence is available.

## Working agreement

- [ ] Keep the first production scope limited to Razorpay Orders plus Standard
  Checkout for one-time credit-pack purchases and paid-plan transitions.
- [ ] Treat Razorpay Subscriptions, mandates, UPI AutoPay, and automatic renewal
  as a separate phase with a separately approved design.
- [ ] Follow red-green-refactor for every behavior change: add a failing test,
  implement the smallest change that passes, then refactor without changing the
  behavior.
- [ ] Do not accept an implementation PR unless its new test failed for the
  expected reason before the implementation was added.
- [ ] Use unit tests for pure validation and mapping, real PostgreSQL for
  integration tests, and the fake Razorpay HTTP service for automated E2E tests.
- [ ] Keep recorded webhook fixtures structurally identical to Razorpay payloads;
  do not invent a Billmesh-only provider envelope.
- [ ] Use Razorpay Test Mode only for staging acceptance; never put live
  credentials or real customer data in automated tests.
- [ ] Record the test command, result, and relevant evidence against each
  completed milestone.
- [x] Keep development fully offline from Razorpay until every implementation
  and automated-quality phase is complete; use only deterministic fake keys,
  the fake provider, and signed local fixtures until the final external gate.
- [ ] Preserve the rule that only a verified captured payment can change
  entitlements, credits, subscriptions, invoices, or ledger balances.

## 1. Scope and architecture decisions

- [x] Use Standard Checkout in `modal` presentation as the phase-one web
  experience.
- [x] Keep Payment Links and `provider_hosted` checkout outside phase one; never
  construct checkout by appending an order ID to `checkout.razorpay.com`.
- [x] Keep Billmesh as the source of truth for price, currency, payment state,
  subscription state, credits, invoices, and entitlements.
- [x] Treat browser callbacks as UX signals only; they cannot grant access.
- [x] Use both a Billmesh server-side confirmation endpoint and provider polling
  in the phase-one checkout-success path.
- [x] Treat `payment.captured` as the canonical capture event and `order.paid` as
  a redundant recovery signal; both must converge idempotently.
- [x] After signature verification, acknowledge valid but unsupported Razorpay
  events with `204 No Content` and record safe logs and metrics without applying
  financial state.
- [x] Keep the 30-minute checkout expiry. An abandoned checkout grants nothing;
  a later capture grants nothing automatically and enters operator-assisted
  reconciliation and refund handling.
- [x] Limit phase one to INR and reject plans or credit packs in currencies not
  enabled for the integration.
- [ ] Review whether Billmesh invoices and credit notes meet applicable tax/GST
  requirements; document any external invoicing responsibility.
- [ ] Assign engineering, finance/reconciliation, incident-response, and customer
  support owners.

## 2. Test infrastructure and fixtures

- [x] Extend the fake Razorpay server to validate Basic Auth, method, URL, request
  body, receipt constraints, and expected headers.
- [x] Extend the fake provider to persist orders by receipt and expose deterministic
  fetch/recovery behavior.
- [x] Add controls for response delay, connection drop after commit, malformed
  JSON, missing fields, `400`, `401`, `409`, `429`, and `5xx` responses.
- [x] Add controls for multiple payment attempts against one order, including one
  captured attempt among failed attempts.
- [x] Add realistic Razorpay fixtures for `payment.captured`, `payment.failed`,
  `order.paid`, and `refund.processed`.
- [x] Include the real top-level `event` field and the
  `X-Razorpay-Event-Id` header in every relevant fixture.
- [x] Add fixture generation that signs the exact raw bytes using
  `X-Razorpay-Signature` without parsing or reserializing the body.
- [x] Add fixtures with reordered JSON keys and whitespace to prove verification
  uses the raw body.
- [x] Add helpers for current and previous webhook secrets during rotation.
- [x] Add a fake checkout-result fixture containing `razorpay_payment_id`,
  `razorpay_order_id`, and `razorpay_signature`.
- [x] Add a test assertion that no fake-provider secret appears in API responses,
  logs, snapshots, or test failure output.
- [x] Map new scenarios to the existing `PAY-*`, `INV-*`, subscription, security,
  and reliability cases in `tests/testdata/test-cases.md`.

## 3. Provider configuration

### Tests first

- [x] Add a unit test proving that omitting all Razorpay values leaves checkout
  disabled without affecting non-payment APIs.
- [x] Add a unit/deployment test rejecting partial Razorpay configuration.
- [x] Add a unit/deployment test rejecting a non-HTTPS production API base URL.
- [x] Add a unit/deployment test proving modal checkout does not require a
  provider-hosted checkout URL.
- [x] Add a test rejecting accidental use of a live key in a non-production
  environment and a test rejecting a test key in production.
- [x] Add a test proving API-key secrets and webhook secrets are redacted from
  structured errors and logs.
- [x] Add a readiness test that reports payment capability accurately without
  exposing credential details.

### Implementation and configuration

- [ ] Set `RAZORPAY_BASE_URL=https://api.razorpay.com` in reviewed environment
  configuration.
- [x] Store `RAZORPAY_KEY_SECRET` and `RAZORPAY_WEBHOOK_SECRET` only in the
  environment secret manager.
- [x] Treat `RAZORPAY_KEY_ID` as a publishable checkout identifier while still
  controlling its environment and merchant-account scope.
- [x] Make `RAZORPAY_CHECKOUT_URL` optional for modal checkout.
- [x] Fail fast on incomplete or environment-mismatched payment configuration.
- [x] Document separate Test and Live credentials, webhooks, and reconciliation
  procedures.

### Verification evidence

- [x] `gotestsum --format testname -- -v ./internal/config ./internal/app
  ./cmd/billmesh ./api` — 160 tests passed on 2026-11-25.
- [x] `./tools/api/ci/test-razorpay-environment-contracts.sh` — all 7 deployment
  contract scenarios passed on 2026-11-25.

## 4. Razorpay Order creation

### Tests first

- [x] Add a unit test mapping a Billmesh amount and ISO currency to Razorpay
  currency subunits without floating-point arithmetic.
- [x] Add a unit test ensuring a provider receipt is non-empty, ASCII-safe,
  deterministic, unique per operation, and no longer than 40 characters.
- [x] Add a unit test proving customer idempotency keys up to Billmesh's limit do
  not make the Razorpay receipt exceed 40 characters.
- [x] Add an E2E test proving credit-pack price, credits, and currency come from
  Billmesh data, not the client request.
- [x] Add an E2E test proving paid-plan transition price and currency come from
  the selected server-side plan version.
- [x] Add an E2E test verifying `POST /v1/orders`, Basic Auth, JSON content type,
  amount, currency, receipt, and approved notes.
- [x] Add an E2E test rejecting a provider response with no order ID.
- [x] Add an E2E test rejecting a provider order whose returned amount, currency,
  receipt, or entity does not match the request.
- [x] Add tests for provider `400`, `401`, `429`, `5xx`, malformed JSON, timeout,
  and connection loss.
- [x] Add an E2E test proving an identical Billmesh idempotency retry returns the
  same payment and provider order.
- [x] Add an E2E test proving reuse of a Billmesh idempotency key with different
  input returns an idempotency conflict.
- [x] Add a concurrency test proving simultaneous identical requests create one
  Billmesh payment and at most one usable Razorpay order.
- [x] Add a recovery test where Razorpay creates the order but its response is
  lost before Billmesh receives it.
- [x] Add a recovery test where Razorpay creates the order but the local payment
  insert or transition update fails.
- [x] Add a test proving retry/recovery cannot attach an order belonging to a
  different account, environment, amount, or currency.
- [x] Add a test proving provider credentials never appear in the checkout
  response.

### Implementation

- [x] Replace long operation references as Razorpay receipts with a fixed-length
  provider-safe identifier while retaining the full local operation reference.
- [x] Persist enough local operation state before calling Razorpay to recover an
  uncertain provider result.
- [x] Validate the complete Razorpay Order response before exposing checkout.
- [x] Implement deterministic recovery for lost create-order responses.
- [x] Add bounded retry/backoff only where the operation can be retried safely.
- [x] Preserve the provider order ID and correlation identifiers for audit and
  support.

### Verification evidence

- [x] `gotestsum --format testname -- -v ./internal/app ./tests/fakeserver` —
  passed on 2026-11-25.
- [x] `make test-e2e` — migrations, integration tests, and all 59 E2E tests
  passed on 2026-11-25.

## 5. Inbound webhook contract

### Tests first

- [x] Add an E2E test accepting an authentic Razorpay payload that uses top-level
  `event` rather than Billmesh's current synthetic `type` field.
- [x] Add an E2E test taking the provider event ID from
  `X-Razorpay-Event-Id` rather than the JSON body.
- [x] Add tests rejecting a missing, blank, malformed, or oversized event ID.
- [x] Add a test verifying HMAC-SHA256 against the exact raw request bytes.
- [x] Add tests rejecting a missing, malformed, wrong, truncated, or uppercase/
  lowercase-invalid signature according to the chosen contract.
- [x] Add a test proving a signature generated for reserialized JSON is rejected.
- [x] Add a test rejecting malformed JSON after signature verification without
  changing financial state.
- [x] Add a test rejecting a request body larger than the configured limit.
- [x] Add realistic tests for `payment.captured`, `payment.failed`, `order.paid`,
  and `refund.processed` entity extraction.
- [x] Add a test for a signed unsupported event according to the approved
  acknowledgement policy.
- [x] Add a test rejecting an event from an unexpected Razorpay merchant account
  if merchant-account validation is enabled.
- [x] Add a test proving invalid events do not create provider-event records or
  financial mutations.

### Deduplication and ordering tests

- [x] Add an integration test proving the same event ID and identical raw payload
  is acknowledged repeatedly but applied once.
- [x] Add an integration test proving the same event ID with different content is
  treated as a provider-event conflict.
- [x] Add a test proving different event IDs for the same captured payment cannot
  fulfil twice.
- [x] Add a test receiving `order.paid` and `payment.captured` in both orders.
- [x] Add a test receiving `payment.failed` followed by a successful late capture.
- [x] Add a test receiving capture followed by a stale failure event.
- [x] Add a concurrent-delivery test for the same event and the same payment.
- [x] Add a test proving event deduplication survives API and worker restarts.

### Secret rotation and delivery tests

- [x] Add tests accepting the current webhook secret.
- [x] Add tests accepting the previous secret only during an explicit grace
  period for older Razorpay retries.
- [x] Add tests rejecting the previous secret after the grace period.
- [x] Add a performance test proving the receiver durably accepts valid events
  and responds within Razorpay's five-second deadline under expected load.
- [x] Add a failure test proving non-durable receipt never returns success.
- [x] Add a retry test proving a response timeout cannot cause duplicate
  fulfilment.

### Implementation

- [x] Map Razorpay's wire payload into an internal typed provider-event model.
- [x] Use `X-Razorpay-Event-Id` as the provider event identity.
- [x] Preserve the verified raw payload for audit and replay.
- [x] Separate durable webhook receipt from business processing if synchronous
  processing cannot reliably meet the response deadline.
- [x] Support safe webhook-secret rotation without weakening signature checks.
- [x] Return a 2xx only after the event is safely accepted according to the
  approved delivery design.

### Verification evidence

- [x] `GOCACHE=/private/tmp/billmesh-go-cache GOFLAGS=-mod=readonly go build ./...`
  — completed successfully on 2026-11-25 (Go emitted only a sandbox stat-cache
  warning after the successful build).
- [x] `gotestsum --format testname -- -v ./internal/config ./internal/app ./tests/testkit`
  — all tests passed on 2026-11-25 after correcting the empty-secret test assertion.
- [x] `make test-e2e` — full E2E suite passed on 2026-11-25.

## 6. Capture validation and atomic fulfilment

### Tests first

- [x] Add a test proving `authorized` is not sufficient to grant credits or paid
  access.
- [x] Add a test proving capture requires the exact local provider order ID.
- [x] Add tests rejecting zero, negative, missing, or mismatched captured amount.
- [x] Add tests rejecting missing or mismatched currency.
- [x] Add a test rejecting an unknown provider order.
- [x] Add a test rejecting a provider payment ID already attached to another
  Billmesh payment or account.
- [x] Add a cross-environment test proving a Test/Live or development/production
  reference cannot cross the isolation boundary.
- [x] Add an integration test proving payment capture, credit grant, wallet
  balance, ledger entry, invoice, billing revision, and outbox event commit
  atomically.
- [x] Add failure-injection tests at every step of the fulfilment transaction and
  prove the entire transaction rolls back.
- [x] Add a duplicate-capture test proving one payment creates one grant, ledger
  mutation, invoice, and subscription transition result.
- [x] Add a race test between webhook processing and provider reconciliation.
- [x] Add a late-capture test for an expired or cancelled transition.
- [x] Add a test proving a late capture is retained for operator resolution and
  never silently discarded.
- [x] Add a test proving a paid subscription transition becomes effective only
  after verified capture.
- [x] Add a test proving the browser callback or success URL alone cannot change
  the billing snapshot.

### Implementation

- [x] Apply all financial fulfilment through one idempotent database transaction.
- [x] Keep business-operation uniqueness in addition to provider-event
  deduplication.
- [x] Record late captures and emit an actionable event without granting an
  expired transition automatically.
- [x] Ensure a newer billing snapshot is the only source used by consumers to
  enable paid capabilities.

### Verification evidence

- [x] `GOCACHE=/private/tmp/billmesh-go-cache GOFLAGS=-mod=readonly go build ./...`
  — completed successfully on 2026-11-25; Go emitted only the known sandbox
  stat-cache warning after the build.
- [x] `gotestsum --format testname -- -v ./internal/app ./tests/fakeserver ./tests/testkit`
  — all focused tests passed on 2026-11-25.
- [x] `make test-e2e` — all 67 E2E tests passed on 2026-11-25 after correcting
  the UUID cast in the rollback assertion helper.

## 7. Checkout confirmation and consumer integration

### Backend tests first

- [x] Add a unit test for the checkout-success signature message
  `server_order_id + "|" + razorpay_payment_id`.
- [x] Add tests accepting a valid `razorpay_signature` and rejecting a wrong,
  missing, malformed, or replayed signature.
- [x] Add a test proving the server uses its stored order ID rather than trusting
  `razorpay_order_id` from the browser.
- [x] Add a test proving a valid checkout signature does not by itself grant
  access before captured status is verified.
- [x] Add a test proving confirmation is account-scoped and cannot confirm
  another customer's order.
- [x] Add a test for confirmation racing with webhook and reconciliation paths.

### Consumer tests first

- [x] Add a consumer unit test loading Razorpay Checkout once and handling script
  load failure.
- [x] Add a consumer unit test mapping only Billmesh-returned public key, order
  ID, amount, and currency into Checkout options.
- [x] Add a consumer test proving catalogue price or client-edited state cannot
  override the returned checkout configuration.
- [x] Add a consumer test for successful handler response followed by a
  processing state until Billmesh confirms capture.
- [x] Add tests for checkout failure, dismissal, abandonment, retry, offline
  interruption, and duplicate button clicks.
- [x] Add a test proving a success redirect, modal handler, or query parameter
  cannot directly enable paid UI or API access.
- [x] Add a test polling the transition/snapshot with bounded backoff and stopping
  at completed, failed, expired, or cancelled states.
- [x] Add an accessibility test for focus restoration, keyboard operation,
  progress announcements, error messages, and retry controls.
- [x] Add a responsive test for supported mobile viewport and WebView behavior.
- [x] Add a CSP test allowing only the explicitly required Razorpay checkout
  origins.

### Implementation

- [x] Add the approved server-side confirmation/status path if selected in the
  architecture decision.
- [x] Load `https://checkout.razorpay.com/v1/checkout.js` in the consuming product,
  not in the Billmesh Admin application.
- [x] Display payment as processing until the authoritative snapshot advances.
- [x] Refresh the billing snapshot after callback, webhook invalidation, or
  terminal transition status.
- [x] Never expose `RAZORPAY_KEY_SECRET` or `RAZORPAY_WEBHOOK_SECRET` to browser
  code, runtime configuration, analytics, or error reporting.

### Verification evidence

- [x] `gotestsum --format testname -- -v ./internal/app ./tests/fakeserver
  ./tests/testkit` — all focused backend tests passed on 2026-11-25.
- [x] `make test-e2e` — all end-to-end tests passed on 2026-11-25.
- [x] `pnpm nx test console --skip-nx-cache` in `billmesh-web` — all consumer
  unit, component, accessibility, responsive, and CSP tests passed on
  2026-11-25.
- [x] `GOCACHE=/private/tmp/billmesh-go-cache GOFLAGS=-mod=readonly go build
  ./...` and `NX_DAEMON=false pnpm nx build console --skip-nx-cache` — backend
  and console production builds completed successfully on 2026-11-25.

## 8. Reconciliation and recovery

### Tests first

- [x] Add an E2E test recovering a captured payment when the webhook never
  arrives.
- [x] Add a test selecting only a captured payment whose order, amount, and
  currency exactly match Billmesh.
- [x] Add a test with multiple failed attempts followed by one captured attempt.
- [x] Add a test rejecting a captured provider payment with a mismatched order,
  amount, or currency.
- [x] Add a test proving an already fulfilled payment is a no-op during
  reconciliation.
- [x] Add a race test between two workers reconciling the same payment.
- [x] Add restart tests proving candidates remain recoverable after worker or
  database interruption.
- [x] Add tests for provider timeout, `401`, `404`, `429`, `5xx`, malformed JSON,
  and partial responses.
- [x] Add a test for bounded exponential backoff with jitter after rate limiting
  or transient provider failure.
- [x] Add a test proving permanent authentication/configuration errors raise an
  alert rather than retrying silently forever.
- [x] Add a test detecting a provider order that exists without a usable local
  payment mapping.
- [x] Add a test detecting a captured Razorpay payment absent from Billmesh's
  fulfilled ledger.

### Implementation

- [x] Keep scheduled provider reconciliation enabled even when webhooks appear
  healthy.
- [x] Add recovery handling for orphaned orders and ambiguous create-order
  results.
- [x] Add a periodic finance reconciliation comparing Razorpay captures and
  refunds with Billmesh payments, invoices, credit notes, and ledger operations.
- [x] Store reconciliation timestamps, outcomes, and safe provider references.
- [x] Provide an operator workflow for unresolved mismatches without direct
  database mutation.

### Verification evidence

- [x] `gotestsum --format testname -- -v ./internal/app ./tests/fakeserver
  ./tests/testkit` — all 173 focused tests passed on 2026-11-25.
- [x] `make test-e2e` — all 73 end-to-end tests passed on 2026-11-25,
  including reconciliation, restart, concurrency, orphan, finance, and operator
  workflow scenarios.
- [x] `GOCACHE=/private/tmp/billmesh-go-cache GOFLAGS=-mod=readonly go build
  ./...` — production build completed successfully on 2026-11-25; Go emitted
  only the known sandbox stat-cache warning.

## 9. Failed payments, refunds, and disputes

### Tests first

- [x] Add a realistic `payment.failed` test that records the provider payment ID
  and failure state without granting value.
- [x] Add a test proving a later valid capture can supersede an earlier failed
  attempt according to Razorpay's late-authorization behavior.
- [x] Add realistic full and partial `refund.processed` tests.
- [x] Add tests rejecting missing refund ID, missing payment ID, zero amount,
  negative amount, and over-refund.
- [x] Add duplicate and out-of-order refund-event tests.
- [x] Add a test proving cumulative partial refunds cannot exceed the captured
  amount.
- [x] Add tests for the approved credit-reversal policy when purchased credits
  are unspent, partially spent, or fully spent.
- [x] Add tests for paid-subscription refund policy and entitlement revocation or
  retention.
- [x] Add an integration test proving refund, credit reversal, ledger entry,
  credit note, billing revision, and outbox event commit atomically.
- [x] Add rollback tests for failures during refund processing.
- [x] Record that phase one is webhook-only for refunds; Billmesh does not call
  Razorpay's refund API, so initiation retry, pending, failed, and lost-response
  tests are intentionally not applicable.
- [x] Record the phase-one disputes/chargebacks scope decision.
- [x] Record that dispute fixtures and state-transition tests are deferred until
  a separately approved dispute model enters scope.

### Implementation

- [x] Implement the approved refund initiation and/or webhook-only responsibility
  boundary.
- [x] Make refund processing independently idempotent from webhook-event
  deduplication.
- [x] Surface refund mismatches to finance and support owners; keep disputes out
  of the event allowlist until their model is approved.
- [x] Document manual remediation that preserves ledger auditability.

### Phase-one responsibility boundary

- Billmesh consumes signed, processed Razorpay refund webhooks but does not
  initiate provider refunds. Refund initiation remains an explicitly approved
  external finance operation. Supporting initiation later requires its own TDD
  phase covering provider idempotency, pending and failed states, timeouts, and
  lost responses.
- Disputes and chargebacks are not phase-one events. They require a separate
  lifecycle model, entitlement policy, accounting entries, evidence workflow,
  and recorded fixtures before their event types may enter the allowlist.
- A provider refund is independently idempotent by `provider_refund_id`; webhook
  event deduplication is an additional transport guarantee, not the accounting
  idempotency key.
- Amount, currency, payment, state, cumulative-total, and reused-refund-ID
  mismatches create an operator-visible `refund_mismatch` reconciliation issue.

### Verification evidence

- [x] `gotestsum --format testname -- -v ./internal/app ./tests/fakeserver
  ./tests/testkit` — focused contract and application tests passed on
  2026-11-25.
- [x] `make test-e2e` — all 79 end-to-end tests passed on 2026-11-25,
  including failed-payment, late-capture, refund, entitlement-policy, mismatch,
  atomicity, and rollback scenarios.
- [x] `GOCACHE=/private/tmp/billmesh-go-cache GOFLAGS=-mod=readonly go build
  ./...` — production build completed successfully on 2026-11-25; Go emitted
  only the known sandbox stat-cache warning.

## 10. Subscription lifecycle boundary

### Phase-one tests and behavior

- [x] Add a test documenting that phase-one paid transitions create one-time
  Razorpay Orders, not Razorpay Subscriptions.
- [x] Add a test proving phase-one configuration cannot claim that a recurring
  mandate was created.
- [x] Add a period-end test documenting the current paid-subscription behavior
  when no renewal payment exists.
- [x] Add tests for grace-period, past-due, expiry, cancellation, and late capture.
- [x] Ensure customer-facing copy does not promise automatic renewal before the
  recurring phase is complete.

### Recurring phase design gate

- [x] Record that no recurring provider flow is approved and keep the design gate
  closed until Razorpay Subscriptions, UPI AutoPay, and alternatives are reviewed.
- [x] Define how Billmesh plan versions will map to provider plans without making the provider the
  catalogue source of truth.
- [x] Define provider customer, subscription, mandate, charge, invoice, and
  payment identifiers in the Billmesh schema.
- [x] Define activation, authentication, pending, active, halted, cancelled,
  completed, and expired state transitions.
- [x] Define plan change, proration, pause/resume, cancellation, retry, dunning,
  mandate expiry, and renewal reconciliation behavior.
- [x] Define the mandatory failing unit, integration, E2E, security, and recovery
  test gate for every future approved recurring state transition.
- [x] Require a separate architecture and production-readiness review before
  enabling any recurring policy flag.

The closed-gate design, identifier model, state transitions, behavior matrix,
test requirements, and production-readiness gate are recorded in
[`razorpay-recurring-design-gate.md`](razorpay-recurring-design-gate.md).

### Verification evidence

- [x] `gotestsum --format testname -- -v ./internal/products ./tests/unit/plans
  ./internal/app ./tests/fakeserver ./tests/testkit` — focused policy and contract
  tests passed on 2026-11-25.
- [x] `make test-e2e` — all 81 end-to-end tests passed on 2026-11-25,
  including the one-time-order boundary, manual renewal, grace, expiry,
  cancellation, and late-capture scenarios.
- [x] `GOCACHE=/private/tmp/billmesh-go-cache GOFLAGS=-mod=readonly go build
  ./...` — production build completed successfully on 2026-11-25; Go emitted
  only the known sandbox stat-cache warning.

## 11. Security and abuse resistance

- [x] Add a test proving constant-time webhook and checkout-signature comparison.
- [x] Add a test proving signature verification occurs before JSON parsing or
  financial lookup.
- [x] Add tests for missing/duplicate signature and event-ID headers.
- [x] Add tests for oversized bodies, deeply nested JSON, unexpected fields, and
  malformed encodings.
- [x] Add tests for account, product, application, and environment isolation on
  every payment read and mutation.
- [x] Add concurrency tests for event replay, duplicate checkout, reconciliation,
  and refunds.
- [x] Add rate-limit and resource-exhaustion tests for public webhook traffic.
- [x] Add a test proving request IDs and provider references are logged without
  card, bank, token, signature, or secret data.
- [x] Add dependency timeouts and connection-pool limits appropriate for provider
  calls and webhook bursts.
- [x] Verify production TLS policy and restrict the webhook endpoint to ports and
  protocols supported by Razorpay.
- [x] Evaluate Razorpay webhook IP allowlisting as defence in depth while keeping
  HMAC verification mandatory.
- [x] Complete threat-model review for replay, tampering, cross-account fulfilment,
  leaked credentials, dependency compromise, and operator misuse.

The implemented trust boundaries, safe logging contract, production TLS and
source-IP policy, and completed threat model are recorded in
[`razorpay-security.md`](razorpay-security.md).

### Verification evidence

- [x] `gotestsum --format testname -- -v ./internal/app ./internal/database
  ./tests/fakeserver ./tests/testkit` — focused security, transport, database,
  fake-provider, and fixture tests passed on 2026-11-25.
- [x] `make test-e2e` — the complete end-to-end suite passed on 2026-11-25,
  including webhook abuse controls, isolation, concurrency, and refund coverage.
- [x] `GOCACHE=/private/tmp/billmesh-go-cache GOFLAGS=-mod=readonly go build
  ./...` — production build completed successfully on 2026-11-25; Go emitted
  only the known sandbox stat-cache warning.

## 12. Observability and operations

- [x] Add metrics for order-create attempts, successes, failures, latency, and
  ambiguous outcomes.
- [x] Add metrics for webhook receipt, signature failure, duplicate, processing
  failure, processing latency, and acknowledgement latency.
- [x] Add metrics for captured, failed, late-captured, refunded, unreconciled, and
  mismatched payments.
- [x] Add metrics for provider API latency, status classes, timeouts, and rate
  limits without high-cardinality customer labels.
- [x] Add dashboards for checkout health, webhook health, reconciliation backlog,
  fulfilment failures, refunds, and subscription transitions.
- [x] Add alerts for sustained provider failures, invalid-signature spikes,
  webhook processing failures, acknowledgement latency, reconciliation backlog,
  captured-but-unfulfilled payments, and ledger mismatches.
- [x] Add a runbook for webhook disablement, delayed delivery, key rotation,
  provider outage, ambiguous order creation, missed capture, duplicate charge,
  refund mismatch, and reconciliation failure.
- [x] Add a runbook for rotating API keys and webhook secrets without downtime.
- [x] Define retention and access controls for raw provider payloads and financial
  audit data.
- [x] Define support-safe lookup by Billmesh payment ID, provider order ID,
  provider payment ID, event ID, and request ID.

Prometheus assets are in [`deploy/observability`](../deploy/observability), the
incident procedures are in [`runbooks/razorpay-operations.md`](runbooks/razorpay-operations.md)
and [`runbooks/razorpay-key-rotation.md`](runbooks/razorpay-key-rotation.md), and
retention/access rules are in
[`razorpay-data-governance.md`](razorpay-data-governance.md).

### Verification evidence

- [x] `gotestsum --format testname -- -v ./internal/app ./internal/config
  ./internal/database ./cmd/billmesh ./tests/fakeserver ./tests/testkit` — focused
  metrics, operational assets, configuration, database, provider, and fixture
  tests passed on 2026-11-25.
- [x] `make test-e2e` — the complete end-to-end suite passed on 2026-11-26,
  including concurrent refund delivery, audited support lookup, and raw-payload
  retention scenarios.
- [x] `GOCACHE=/private/tmp/billmesh-go-cache GOFLAGS=-mod=readonly go build
  ./...` — production build completed successfully on 2026-11-25; Go emitted
  only the known sandbox stat-cache warning.
- [x] `jq empty deploy/observability/razorpay-dashboard.json` and `helm lint
  deploy/charts/billmesh-api` — dashboard JSON and Helm chart validation passed
  on 2026-11-25.

## 13. Offline Razorpay and Dashboard contract

This phase proves the complete integration contract without a Razorpay account
or network call. Real Test/Live keys and Dashboard changes are intentionally
deferred until all implementation phases and automated quality gates are green.

- [x] Document the offline-only development rule and keep real provider
  activation outside the current development loop.
- [x] Use deterministic fake API-key and webhook-secret values that cannot be
  mistaken for deployable credentials.
- [x] Mock `POST /v1/orders`, including Basic Auth, validation, persistence,
  duplicate receipt, timeout, malformed response, and lost response.
- [x] Mock paginated `GET /v1/orders` recovery and inventory queries.
- [x] Mock `GET /v1/orders/{id}/payments` with multiple payment attempts.
- [x] Mock `GET /v1/payments/{id}` for checkout confirmation and reconciliation.
- [x] Mock paginated `GET /v1/payments/{id}/refunds` for refund reconciliation.
- [x] Generate authentic signed fixtures for `payment.captured`,
  `payment.failed`, `order.paid`, and `refund.processed` without parsing or
  reserializing the signed bytes.
- [x] Apply deterministic delay, malformed JSON, `400`, `401`, `409`, `429`, and
  `5xx` controls across the provider surface used by Billmesh.
- [x] Run the focused fake-provider, fixture, and provider-client tests and retain
  the passing output — `gotestsum --format testname -- -v ./tests/fakeserver
  ./tests/testkit ./internal/app` and
  `tools/api/ci/test-razorpay-dashboard-setup-contracts.sh` passed on 2026-11-26.

### Deferred final real-environment execution

These actions do not block Phase 13 offline completion and must remain deferred
until all development phases are complete:

- [ ] Activate and verify the Razorpay merchant account required for the target
  environment.
- [ ] Generate separate Test Mode API keys and store them in the development or
  staging secret manager.
- [ ] Configure automatic payment capture for the intended phase-one flow.
- [ ] Create the Test Mode webhook with the exact public HTTPS Billmesh endpoint.
- [ ] Configure a webhook secret that is distinct from the API key secret.
- [ ] Enable `payment.captured`, `payment.failed`, `order.paid`, and
  `refund.processed`.
- [ ] Configure a monitored alert email and operational owner.
- [ ] Verify Cloudflare, ingress, firewall, and application routing from the real
  Razorpay service.
- [ ] Store webhook identity, events, owner, environment, creation date, and
  rotation date in the secured operational inventory.
- [ ] Repeat independently for Live Mode only after Test Mode acceptance.

The future operator procedure is documented in
[`runbooks/razorpay-dashboard-setup.md`](runbooks/razorpay-dashboard-setup.md),
with the secret-free inventory template in
[`razorpay-operational-inventory.example.yaml`](razorpay-operational-inventory.example.yaml).

## 14. Automated quality gates

The reproducible command and evidence matrix is documented in
[`razorpay-quality-gates.md`](razorpay-quality-gates.md). The gate runner keeps
all provider traffic local and exposes focused, integration, E2E, race,
failure/recovery, load, contract, test-policy, security, and consumer groups.
The checkboxes remain open until the operator supplies passing output.

- [x] Run all new unit tests and retain the passing result. Focused backend
  suites and repaired OpenAPI/test-manifest contracts passed on 2026-11-26.
- [x] Run all payment and invoice integration tests against real PostgreSQL.
  All 17 payment and invoice integration tests passed with the race detector on
  2026-11-26.
- [x] Run all Razorpay E2E tests against the realistic fake provider. The
  complete Docker E2E gate passed on 2026-11-26.
- [x] Run the complete backend unit, integration, E2E, security, and reliability
  suites. Focused unit, PostgreSQL integration, Docker E2E, race, provider
  failure/recovery, and release-security gates passed on 2026-11-26.
- [x] Run consumer checkout unit, component, accessibility, and E2E suites.
  Formatting and linting passed; all 27 console checkout tests across 7 files,
  including the composed checkout journey, passed; and the production console
  build completed successfully on 2026-11-26.
- [x] Run race/concurrency tests for webhook, fulfilment, order creation,
  reconciliation, and refund paths. All 231 focused application, database,
  fake-provider, and fixture tests passed with the race detector on 2026-11-26.
- [x] Run provider-failure and restart recovery tests. All 113 focused provider
  failure/recovery tests passed on 2026-11-26; the complete E2E suite also
  passed the persisted reconciliation restart-convergence scenario.
- [x] Run load tests for expected checkout and webhook peaks. The Docker-backed
  performance gate passed 11 benchmarks with zero failures on 2026-11-26,
  including financial-operation `PERF-013`, concurrent checkout-order
  `PERF-014`, and captured-webhook `PERF-015`.
- [x] Run OpenAPI validation and route-coverage checks. The complete contracts
  gate passed on 2026-11-26, covering OpenAPI references and registered routes,
  Razorpay environment isolation, Dashboard setup, quality-gate, Helm, and
  Kubernetes deployment contracts.
- [x] Run static analysis, dependency vulnerability scanning, secret scanning,
  and container scanning. Formatting, vet, govulncheck, Gitleaks, Trivy
  filesystem/misconfiguration, and Trivy runtime-image scans passed on
  2026-11-26 with zero high or critical findings.
- [x] Confirm every new production branch has a named test scenario and every
  scenario is linked to its implementation PR. The quality contract passed on
  2026-11-26 and confirms CI requires a changed automated test, known scenario
  IDs, TDD evidence, and exact test commands for production payment changes.
- [x] Confirm no skipped, quarantined, or flaky payment test remains without an
  approved owner and resolution date. The skipped/focused test policy passed on
  2026-11-26 across backend payment paths and the consumer checkout suite.

## 15. Razorpay Test Mode staging acceptance

Offline prerequisite: the console credit-pack flow and composed `PAY-047`
journey cover server-owned pricing, idempotent order creation, duplicate-click
protection, dismissal, provider failure, retry, and post-capture confirmation
with mocked Razorpay Checkout. This does not complete any staging checkbox;
each item below remains open until it is exercised with separately generated
Test Mode credentials in the staging environment.

- [ ] Create a credit-pack order and complete a successful Test Mode payment.
- [ ] Verify the Razorpay order is paid and payment is captured.
- [ ] Verify Billmesh records exactly one payment, grant, ledger mutation, invoice,
  billing revision, and success event.
- [ ] Complete a paid-plan transition and verify access changes only after the
  authoritative snapshot advances.
- [ ] Exercise a failed payment and verify no value is granted.
- [ ] Close and abandon Checkout and verify no value is granted.
- [ ] Retry checkout and verify the approved idempotency behavior.
- [ ] Replay the same webhook and verify fulfilment remains single.
- [ ] Send `order.paid` and `payment.captured` in both orders and verify the final
  state remains correct.
- [ ] Temporarily make the receiver fail, observe Razorpay retry delivery, restore
  it, and verify one fulfilment.
- [ ] Suppress a webhook and verify scheduled reconciliation completes the
  captured payment.
- [ ] Exercise a late capture after checkout expiry and verify the approved
  operator/customer outcome.
- [ ] Process partial and full Test Mode refunds and verify credit/entitlement and
  credit-note behavior.
- [ ] Rotate the webhook secret and verify current and in-flight older deliveries
  follow the approved grace policy.
- [ ] Verify dashboards, alerts, logs, traces, reconciliation reports, and support
  lookup during the staging journeys.
- [ ] Verify no secret or sensitive payment data appears in browser storage,
  frontend bundles, logs, analytics, or error reports.
- [ ] Capture acceptance evidence and obtain engineering, finance, security, and
  product sign-off.

## 16. Production go-live gate

- [ ] Confirm Razorpay account activation/KYC and required payment methods are
  approved for Live Mode.
- [ ] Generate Live Mode keys and store them separately from Test Mode.
- [ ] Configure and independently verify the Live Mode webhook and alert email.
- [ ] Confirm automatic capture and currency/payment-method settings match the
  reviewed design.
- [ ] Confirm plans, credit packs, prices, currencies, redirect origins, and
  billing policies are reviewed in production.
- [ ] Confirm database backup, rollback, incident response, reconciliation, and
  customer-support procedures are ready.
- [ ] Confirm dashboards and paging alerts are active before accepting payment.
- [ ] Confirm finance has an agreed daily settlement and refund reconciliation
  process.
- [ ] Confirm tax/invoice compliance sign-off is recorded.
- [ ] Perform a controlled minimum-value live payment with an approved internal
  account.
- [ ] Verify capture, webhook, Billmesh fulfilment, invoice, snapshot, provider
  dashboard, and reconciliation records for the live smoke payment.
- [ ] Refund the live smoke payment and verify the full reverse path.
- [ ] Record go-live approval, timestamp, release version, owners, and rollback
  decision window.

## 17. Completion criteria

- [ ] Every applicable checkbox in this document is complete or explicitly moved
  to a separately approved future-scope document.
- [ ] All automated and staging acceptance tests pass with retained evidence.
- [ ] The OpenAPI contract and consumer documentation match the implemented
  checkout, confirmation, webhook, refund, and reconciliation behavior.
- [ ] Production configuration contains no placeholder, Test Mode, or
  cross-environment credential.
- [ ] No known P0 or P1 payment, security, reconciliation, or ledger defect remains
  open.
- [ ] Engineering, security, finance, product, operations, and support owners have
  signed off.

