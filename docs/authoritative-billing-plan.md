# Billmesh Authoritative Billing Plan

Status notation:

- `[X]` — completed or explicitly agreed.
- `[ ]` — pending decision, implementation, verification, or rollout.

## Objective

Make Billmesh the single source of truth for plans, prices, billing customers,
eligibility, subscriptions, transitions, checkout, payments, invoices, credits,
limits, and entitlements.

Consumer applications such as Daybook send commands and consume a revisioned
projection. They must not infer billing state from plan slugs, prices, browser
redirects, or locally maintained subscription rules.

## Locked architectural decisions

- [X] Billmesh is the authoritative billing system.
- [X] Daybook stores only a non-authoritative, revisioned billing projection.
- [X] Daybook uses opaque Billmesh plan and credit-pack IDs, not slugs.
- [X] Billmesh determines eligibility and whether a transition requires payment.
- [X] Billmesh controls plan prices and validates payment amount and currency.
- [X] Browser success or cancellation redirects never activate a subscription.
- [X] Paid entitlements become active only after Billmesh verifies a provider
  payment event.
- [X] Billmesh owns subscription lifecycle and transition policy.
- [X] Billmesh owns invoices, credit grants, balances, limits, and entitlements.
- [X] Subscription plan details are snapshotted so retired catalogue plans remain
  reconcilable.
- [X] Billmesh events invalidate Daybook's projection; Daybook then refetches the
  authoritative snapshot.
- [X] Payment, subscription, invoice, credit, revision, and outbox changes must be
  atomic where they represent one billing operation.
- [X] IdNest owns authentication and identity claims, not billing state.
- [X] Daybook owns organization membership, primary ownership, actual business
  resources, and resource counts.
- [X] Product-specific billing behavior is represented by a validated, versioned
  Billmesh product billing policy.
- [X] Administrators configure the billing policy through structured fields when
  creating or editing a product.
- [X] Product-policy changes apply to future decisions; pending and historical
  transitions retain the policy version and policy snapshot under which they
  were created.
- [X] Security, payment verification, server-controlled prices, idempotency,
  atomicity, and stale-state safety are platform invariants and cannot be
  weakened by product configuration.
- [X] Consumer deployment details such as webhook inbox storage, dead-letter
  handling, and monitoring ownership are integration configuration, not product
  billing policy.

## Existing Billmesh foundations

- [X] Organization-scoped billing accounts and account links exist.
- [X] Plans have opaque IDs and server-controlled prices.
- [X] Subscriptions snapshot price, currency, billing interval, included credits,
  and entitlements.
- [X] A unique active subscription per account and product is enforced.
- [X] Credit-pack payment orders resolve price and credits on the server.
- [X] Razorpay webhook signatures are verified.
- [X] Provider event IDs are persisted and deduplicated.
- [X] Credit-pack payment capture, invoicing, and credit allocation are
  transactional.
- [X] Subscription and wallet mutations can emit durable outbox events.
- [X] Webhook deliveries are persisted and retried.
- [X] Payment and invoice read APIs exist.
- [X] Entitlement and individual feature-check APIs exist.

The v1 publication work adds caller-trust, identity, checkout, reconciliation,
and typed-projection safeguards to these foundations. Unchecked items below are
longer-term policy breadth, verification, or rollout work and do not imply an
alternate public subscription API.

## Generic product billing policy

The original Daybook-specific decisions are generalized as product policy. A
product receives a policy when it is created and administrators may update that
policy later. Billmesh validates and evaluates the policy; consumer applications
only submit intent and render the resulting catalogue, transition, and snapshot
contracts.

### Policy storage and lifecycle

- [X] Add `billing_policy` and `billing_policy_version` to products.
- [X] Define a versioned, typed policy schema with documented defaults.
- [X] Reject unknown fields, invalid enum values, unsafe values, and incompatible
  combinations instead of accepting arbitrary JSON.
- [X] Make product creation materialize a complete policy from explicit values
  and server defaults.
- [X] Apply optimistic concurrency and catalogue audit records to policy edits.
- [X] Increment `billing_policy_version` only when policy content changes.
- [X] Snapshot the policy version and decision-relevant policy content onto each
  subscription transition.
- [X] Preserve the policy snapshot for invoices, subscription history,
  reconciliation, and administrative explanation.
- [X] Apply edited policy only to new commands; do not reinterpret completed or
  pending transitions.
- [X] Backfill every existing product with a validated policy matching its
  current behavior.

### Policy API and evaluation

- [X] Include the complete policy and policy version in administrative product
  create, read, and update contracts.
- [X] Add a metadata contract that supplies policy defaults, enum options,
  constraints, and conditional-field rules to the Admin UI.
- [X] Expose only the safe, consumer-relevant policy projection through catalogue
  and billing-snapshot contracts.
- [ ] Implement one domain policy evaluator used by eligibility, onboarding,
  transitions, checkout, cancellation, renewal, and projection generation.
- [ ] Remove product-specific policy branches and lifecycle decisions from HTTP
  handlers.
- [X] Return stable machine-readable validation and policy-decision reason codes.
- [X] Keep OpenAPI schemas synchronized with the typed policy model.
- [ ] Add unit, contract, integration, migration, authorization, concurrency, and
  end-to-end tests for every supported policy combination.

### Customer identity and Free eligibility policy

- [X] Configure customer scope as `identity`, `organization`, or
  `external_customer`.
- [X] Configure the stable external identity claim mapping used by that scope.
- [X] Configure the number of Free subscriptions allowed per customer, including
  zero for products without a Free allowance.
- [X] Configure whether eligibility is retained or reevaluated after account
  ownership changes.
- [X] Configure ownership-transfer behavior as `unsupported`, `retain`, or
  `preauthorized_recheck`.
- [X] Configure rejection or Paid-checkout requirements when a proposed owner is
  not eligible for Free.
- [X] Keep the ownership-transfer protocol itself transactional, expiring,
  confirmable, cancellable, and reconcilable whenever enabled.

### Account onboarding policy

- [X] Configure whether an account may exist without an active subscription.
- [X] Configure initial-plan selection as `automatic_default` or
  `explicit_transition`.
- [X] Configure ineligible onboarding as `reject`, `restricted`, or
  `require_paid_checkout`.
- [ ] Configure account deletion, restoration, and billing-record retention.

### Catalogue and trial policy

- [X] Configure catalogue access as `public` or `application_token`.
- [X] Configure whether catalogue access is required before account creation.
- [ ] Configure the safe presentation fields exposed to consumer applications.
- [ ] Configure whether trials are disabled or enabled with an explicit duration,
  conversion behavior, and expiry policy.

### Subscription lifecycle policy

- [X] Configure Free-to-Paid upgrades as immediate after verified capture.
- [ ] Configure Paid-to-Paid upgrades as `checkout`, `mandate_proration`, or
  `period_end`.
- [X] Configure downgrades as `period_end` or another explicitly supported safe
  strategy.
- [X] Configure the default cancellation timing and whether immediate
  cancellation is allowed.
- [ ] Configure refund handling for immediate cancellation.
- [X] Configure whether scheduled cancellation can be withdrawn.
- [ ] Configure reactivation as `resume` or `new_transition`.
- [ ] Configure post-downgrade over-limit behavior as `block_new`, `grace_period`,
  or `reject_transition`.
- [ ] Configure renewal, grace-period, dunning, expiration, refund, chargeback,
  and dispute behavior.

### Entitlement and projection policy

- [X] Products already define a structured entitlement schema through the Admin
  UI.
- [ ] Extend entitlement definitions with units, enforcement mode, and resource
  count semantics where required.
- [ ] Configure which entitlement checks require a fresh snapshot and which may
  use a non-expired cached projection.
- [X] Configure maximum fresh and degraded snapshot ages.
- [X] Configure fail-closed operation classes during a Billmesh outage.
- [ ] Enforce the invariant that stale state may retain or reduce an existing
  capability but may never grant or increase one.
- [X] Configure projection refresh and reconciliation intervals exposed as
  consumer guidance.
- [ ] Define atomic reservation or compare-and-consume contracts for concurrent
  creation of limited resources.

### Checkout policy

- [X] Configure allowed success and cancellation redirect origins.
- [X] Configure checkout presentation as `inline`, `modal`, or
  `provider_hosted` when supported by the provider.
- [X] Configure whether recurring mandates are required.
- [X] Expose transition-state and delayed-confirmation presentation hints without
  allowing UI behavior to change billing state.
- [ ] Expose safe customer-facing reason codes for refunds, chargebacks,
  disputes, and renewal failures.

### Product policy Admin UI

- [X] Add a structured billing-policy section to the existing create-product and
  edit-product forms.
- [X] Use selects, switches, numeric constraints, duration fields, and conditional
  sections rather than an unrestricted JSON editor.
- [X] Start from safe server-provided defaults and show a review summary before
  saving.
- [X] Explain which changes affect only future commands and display the current
  policy version.
- [ ] Display validation errors against the relevant field.
- [ ] Add UI tests for defaults, conditional fields, invalid combinations,
  creation, editing, version conflicts, and read-only historical policy views.

## Non-configurable Billmesh platform invariants

- [X] Billmesh controls plan and credit-pack prices and validates amount and
  currency.
- [X] A consumer cannot assert payment status or provider verification.
- [X] Paid entitlements require a matching verified provider capture.
- [X] Browser redirects cannot activate or otherwise mutate billing state.
- [X] Commands and provider events are idempotent and concurrency-safe.
- [X] Related payment, subscription, invoice, credit, revision, and event changes
  are atomic.
- [X] Provider webhook signatures and replay controls cannot be disabled by a
  product policy.
- [X] Product policies cannot broaden token permissions or cross tenant, product,
  or environment boundaries.
- [X] Stale projections cannot grant or increase capabilities.

## Consumer integration configuration

These settings and tasks belong to each consuming application, initially
Daybook. They are not switches in the product billing policy.

- [ ] Identify the service that receives Billmesh webhooks.
- [ ] Persist a durable webhook inbox and deduplicate event IDs.
- [ ] Define retry and dead-letter handling.
- [ ] Treat events as invalidation signals and conditionally refetch the
  authoritative snapshot instead of directly mutating billing state.
- [ ] Choose durable and cache storage for the non-authoritative projection.
- [ ] Define login, account-switching, expired-projection, and outage behavior
  within the limits published by Billmesh.
- [ ] Run staging conformance checks between the cached consumer projection and
  the live authoritative snapshot.
- [ ] Define operational ownership for projection lag and reconciliation alerts.
- [ ] Define customer-facing UI for transition states and delayed provider
  confirmation.

## Phase 1 — Lock identity and token contracts

- [ ] Define application-scoped catalogue token claims.
- [ ] Define organization-scoped billing token claims.
- [ ] Fix the Billmesh issuer and audience contract.
- [ ] Define stable actor subject, organization ID, application/product identity,
  environment, and permissions.
- [ ] Distinguish user, Daybook service, and Billmesh administrative identities.
- [ ] Define token expiry, signing-key rotation, and clock-skew policy.
- [X] Add a `billing_customers` model with internal IDs and external identity
  links if required by the Free-eligibility decision.
- [ ] Add account-to-customer ownership history.
- [X] Add customer-level Free-eligibility constraints and transactional checks.
- [ ] Add ownership-transfer intent and confirmation APIs if required.
- [ ] Add authorization and cross-tenant security tests for every identity type.

### Phase 1 exit criteria

- [ ] Daybook can read the catalogue before organization creation using the
  agreed token type.
- [X] Daybook can issue organization-scoped commands without supplying account
  identity that Billmesh can derive from claims.
- [X] Billmesh can identify the billing customer across multiple organizations.
- [ ] Free eligibility cannot be bypassed by creating or transferring an
  organization concurrently.

## Phase 2 — Close current paid-activation bypasses

- [X] Fix optional cancellation-body decoding so invalid JSON cannot mutate a
  subscription after a `400` response.
- [X] Add an explicit `json:"immediate"` field tag and contract tests.
- [ ] Change Daybook cancellation requests to use lowercase `immediate`.
- [X] Reject unsupported `effective` values on plan changes.
- [X] Stop treating caller-supplied `payment_status` as provider verification.
- [X] Prevent ordinary `billing:write` callers from directly activating a paid
  subscription.
- [X] Prevent unpaid paid-plan changes.
- [X] Prevent unpaid paid-plan reactivation.
- [X] Prevent caller-asserted paid renewal.
- [ ] Remove the duplicate direct paid-subscription operations before the first
  public release; no compatibility path is required for this fresh application.
- [X] Add negative security tests for creation, plan change, renewal, and
  reactivation bypass attempts.

### Phase 2 exit criteria

- [X] No public caller can obtain paid entitlements without a matching verified
  provider event.
- [X] Invalid cancellation bodies cannot change subscription state.

## Phase 3 — Make the catalogue authoritative

- [X] Add a constrained `billing_model` to plans.
- [X] Support `free` and `paid` initially; add `trial` only with defined trial
  lifecycle fields and policy.
- [X] Add `selectable` independently from administrative `active` state.
- [X] Add `default_for_product` with a unique active default per product.
- [X] Add `checkout_enabled` for controlled rollout.
- [X] Compute transition-specific `requires_payment` instead of trusting price or
  a caller-supplied classification.
- [X] Add plan descriptions and catalogue effective dates.
- [ ] Decide and implement immutable published plan versions or an equivalent
  commercial-version model.
- [X] Preserve historical plan versions for active and past subscriptions.
- [X] Snapshot billing semantics and commercial plan version onto subscriptions.
- [X] Extend admin create, update, archive, audit, and validation paths.
- [ ] Update the Billmesh Admin UI for semantic and versioned plan management.
- [X] Add `GET /v1/catalog?product=daybook` with catalogue revision and ETag.
- [X] Return opaque plan IDs, display fields, price, interval, semantics,
  entitlements for display, availability, and effective dates.
- [X] Add active credit packs to the catalogue response or a dedicated
  `GET /v1/credit-packs` endpoint.
- [X] Accept `credit_pack_id` for payment orders and deprecate slug lookup.
- [X] Add product-scoped catalogue authorization tests.

### Phase 3 exit criteria

- [ ] Daybook can render its billing catalogue without embedded slugs, prices, or
  Free/Paid rules.
- [X] Retiring or publishing a plan does not alter existing subscription terms.
- [X] Billmesh alone determines whether a plan is selectable and whether a
  transition requires payment.

## Phase 4 — Centralize the subscription state machine

- [ ] Define subscription states and allowed transitions in one domain service.
- [ ] Define transition types for initial activation, upgrade, downgrade,
  cancellation, cancellation withdrawal, renewal, reactivation, expiration, and
  administrative correction.
- [ ] Remove direct lifecycle SQL decisions from HTTP handlers.
- [X] Implement Free activation after Billmesh eligibility validation.
- [X] Implement Paid activation only after matching verified capture.
- [X] Implement immediate and period-end transition scheduling.
- [ ] Implement upgrade policy and proration behavior.
- [X] Implement downgrade policy and effective dates.
- [X] Implement cancellation and cancellation withdrawal policy.
- [ ] Implement renewal, grace period, dunning, `past_due`, and expiration rules.
- [ ] Implement refund, chargeback, dispute, and entitlement-revocation policy.
- [ ] Ensure every transition writes history, audit state, billing revision, and
  outbox records atomically.
- [ ] Add table-driven unit tests for every allowed and rejected state transition.

### Phase 4 exit criteria

- [ ] All subscription lifecycle decisions pass through one tested state machine.
- [ ] HTTP handlers translate commands and responses but do not decide billing
  policy.

## Phase 5 — Implement transitions and provider checkout

- [X] Add a `subscription_transitions` table.
- [X] Store account, product, source subscription, target plan version, operation,
  requested timing, status, effective date, expiry, and failure code.
- [X] Add transition statuses: `requires_payment`, `processing`, `completed`,
  `failed`, `expired`, and `cancelled`.
- [X] Add payment purpose and subscription-transition linkage to payments.
- [X] Add a true client idempotency key scoped by account, product, operation,
  and request hash.
- [X] Return `409 Conflict` when a key is reused with a different request.
- [X] Add `POST /v1/subscription-transitions`.
- [X] Add `GET /v1/subscription-transitions/{id}`.
- [X] Add `POST /v1/subscription-transitions/{id}/cancel`.
- [X] Support `If-Match` with the current billing revision.
- [X] Return transition ID, subscription ID, payment ID, checkout expiry,
  effective date, failure code, billing revision, and snapshot URL.
- [X] Resolve account and customer from authenticated identity.
- [X] Resolve plan version, price, currency, and eligibility on the server.
- [X] Create a provider order only when the computed transition requires payment.
- [X] Validate checkout redirect origins against product policy configuration.
- [X] Refactor payment capture so credit-pack and subscription payments use a
  purpose-specific dispatcher.
- [X] On capture, validate provider, order, account, amount, currency, plan
  version, transition status, and expiry.
- [X] Atomically capture payment, complete the transition, update subscription,
  create invoice, allocate credits, increment revision, and write outbox events.
- [X] Handle duplicate, reordered, late, and conflicting provider events.
- [ ] Add reconciliation for transitions or provider orders stuck in processing.
- [ ] Add integration tests for concurrent transitions and webhook replay.
- [ ] Add fake-provider end-to-end tests for successful, failed, expired, partial,
  duplicate, mismatched, refunded, and disputed payments.

### Phase 5 exit criteria

- [ ] Daybook submits only the selected plan ID and optional user timing intent.
- [ ] Daybook cannot submit price, billing classification, or payment status.
- [X] Paid subscription state changes only in the verified provider-event
  transaction.
- [X] Repeated commands or provider events cannot duplicate subscriptions,
  invoices, or credit grants.

## Phase 6 — Add the authoritative billing snapshot

- [X] Define the snapshot schema for account, subscription, effective plan,
  entitlements, limits, pending transition, and timestamps.
- [X] Add a monotonic billing revision per account and product.
- [X] Increment the revision in every transaction that changes snapshot-visible
  state.
- [X] Add `GET /v1/billing-snapshot?product=daybook`.
- [X] Build the response from one consistent database snapshot.
- [X] Embed the subscription's effective plan snapshot rather than requiring an
  active catalogue lookup.
- [X] Return `revision`, `generated_at`, `effective_at`, `verified_at`, and
  `expires_at` as applicable.
- [X] Return an ETag derived from the billing revision.
- [X] Support `If-None-Match` and `304 Not Modified`.
- [X] Represent an account with no subscription explicitly.
- [X] Define whether live credit limits share the billing revision or use a
  separate high-frequency revision.
- [X] Add consistency tests covering plan retirement and concurrent mutations.

### Phase 6 exit criteria

- [ ] Daybook can render and enforce billing using one authoritative projection.
- [X] A retired plan remains fully visible through the subscription snapshot.
- [ ] Conditional requests safely refresh Daybook's projection.

## Phase 7 — Version outbound events and reconciliation

- [X] Define a versioned event envelope with ID, type, schema version, timestamp,
  account ID, product ID, aggregate ID, billing revision, and data.
- [ ] Include a snapshot URL or sufficient keys to refetch the snapshot.
- [X] Add a timestamp header and sign `timestamp + "." + raw_body`.
- [ ] Define signature tolerance and replay protection.
- [ ] Document at-least-once delivery and duplicate handling.
- [ ] Document ordering guarantees and retention.
- [ ] Document retry schedule and terminal delivery failure behavior.
- [X] Add webhook contract-version negotiation or endpoint version selection.
- [X] Add subscribed event-type filters.
- [X] Add endpoint update, disable, and delete operations.
- [X] Add signing-secret rotation without delivery interruption.
- [ ] Add delivery observability, lag metrics, and administrative replay.
- [ ] Add Billmesh provider reconciliation for missed payment webhooks.
- [ ] Add Daybook snapshot reconciliation for missed Billmesh events.
- [X] Add compatibility and security tests for signatures, replay, rotation, and
  duplicate delivery.

### Phase 7 exit criteria

- [ ] Daybook treats events only as cache invalidation signals.
- [ ] Missing or duplicate events converge to the authoritative Billmesh state.
- [X] Webhook consumers can rotate secrets without downtime.

## Phase 8 — Adopt the canonical Daybook contract

- [X] Backfill plan semantics and commercial versions for existing plans.
- [ ] Deploy additive catalogue, transition, snapshot, and webhook contracts.
- [ ] Change Daybook catalogue reads to use opaque Billmesh IDs.
- [ ] Change Daybook subscription mutations to use transition commands.
- [ ] Change Daybook activation to wait for Billmesh snapshot confirmation.
- [ ] Add Daybook projection storage with revision, ETag, verified time, and
  expiry.
- [ ] Add Daybook webhook inbox, conditional refetch, retry, and reconciliation.
- [ ] Remove slug-based subscription mutations before Daybook integrates.
- [ ] Remove public `payment_status` fields.
- [ ] Remove direct paid change, renew, and reactivate operations.
- [ ] Remove Daybook plan slugs, prices, classifications, and local lifecycle
  rules.

### Phase 8 exit criteria

- [ ] Daybook contains no authoritative billing rules or payment-verification
  logic.
- [ ] All Daybook billing reads derive from the Billmesh snapshot.
- [ ] All Daybook billing mutations are Billmesh commands.
- [ ] The public contract exposes no alternate path around the state machine.

## Cross-cutting contract and operational work

- [ ] Add stable machine-readable error codes while retaining safe messages.
- [ ] Add correlation and request IDs across Daybook, Billmesh, and provider
  operations.
- [ ] Add cursor pagination to payment, invoice, usage, and audit histories.
- [ ] Normalize wallet and reservation JSON through a compatible versioned
  contract.
- [X] Keep the repository OpenAPI contract synchronized with registered routes.
- [ ] Add migration up/down and restore tests for every schema change.
- [ ] Add audit records for customer ownership, eligibility decisions, catalogue
  publishing, transitions, payments, refunds, and administrative corrections.
- [ ] Add metrics for transition duration, checkout abandonment, payment failure,
  webhook lag, reconciliation repairs, projection age, and outbox backlog.
- [ ] Add alerts for stuck transitions, provider mismatches, repeated delivery
  failure, revision divergence, and unauthorized paid-activation attempts.
- [ ] Document operational runbooks for payment reconciliation, webhook replay,
  incorrect customer ownership, disputed payments, and manual recovery.

## Final definition of done

- [ ] Daybook knows no plan or credit-pack slugs.
- [ ] Daybook never submits or stores an authoritative price.
- [ ] Daybook never asserts payment verification.
- [ ] Daybook never independently activates, renews, changes, or reactivates a
  subscription.
- [X] Billmesh enforces customer-level Free eligibility.
- [ ] Billmesh owns every subscription transition and effective date.
- [ ] No paid entitlement can become active without a matching verified payment.
- [X] Retired plan versions remain reconcilable for current and historical
  subscriptions.
- [X] Billing commands and provider events are idempotent and auditable.
- [X] Browser redirects cannot change billing state.
- [ ] Stale Daybook projections cannot grant or increase capabilities.
- [X] Duplicate or reordered events cannot duplicate subscriptions, invoices, or
  credits.
- [ ] Daybook and Billmesh recover automatically from missed webhooks through
  reconciliation.
- [ ] Billmesh's revisioned snapshot is the sole authoritative billing read model
  consumed by Daybook.
