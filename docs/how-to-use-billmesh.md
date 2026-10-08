# How to use Billmesh

This guide defines the integration boundary between Billmesh and an application
that consumes billing, subscription, entitlement, or credit services. Daybook is
the first consumer, but the same rules apply to every application.

The OpenAPI document is the endpoint-level contract. This document is the
system-responsibility and integration contract. If an application needs billing
behavior that is not described in either document, it must agree that behavior
with the Billmesh team before implementing it locally.

## Current publication status

The v1 consumer contract is implemented as a release candidate. It has one
canonical subscription mutation workflow, a typed snapshot, usable checkout
configuration, ownership-transfer operations, a fixed IdNest token profile,
stable error envelopes, cursor-paginated payment reads, and webhook version 2.

It is ready to give other application teams for client implementation and
staging acceptance. Production publication still requires deployment-specific
work that cannot be completed in this repository alone:

1. configure and verify the IdNest issuer, Billmesh audience, token claims, and
   client-profile registrations in every target environment;
2. configure Razorpay public and secret credentials, webhook secret, and any
   hosted-checkout URL without exposing secrets to consuming applications;
3. seed and review each application's product, billing policy, entitlement
   schema, plans, and optional credit packs;
4. run the repository and consumer acceptance suites, including payment,
   ownership, stale-revision, webhook, and failure scenarios;
5. validate generated clients against `api/openapi.yaml`, tag the accepted v1
   contract, and publish base URLs; and
6. enable dashboards, alerts, reconciliation operations, and named support
   owners before production traffic.

The implementation has been formatted and compile-checked. Test execution and
staging acceptance remain explicit release gates.

## The core rule

Billmesh is the source of truth for billing. A consuming application may cache a
revisioned projection of Billmesh state, but it must not create a second billing
system.

A consumer sends intent, for example “select this opaque plan ID.” Billmesh
decides eligibility, price, payment requirements, timing, resulting
subscription state, credits, limits, and entitlements.

## Ownership and responsibilities

| Area | Billmesh owns | Consuming application owns |
| --- | --- | --- |
| Identity | Resolving a billing customer from verified identity claims and isolating accounts by application, organization, and environment | Organization membership, the current primary owner, obtaining the correct IdNest token, and initiating any supported ownership-change workflow |
| Catalogue | Plan and credit-pack IDs, display data, commercial versions, prices, currency, availability, billing model, and billing policy | Rendering the returned catalogue and preserving opaque IDs only for the current user action |
| Subscriptions | Eligibility, allowed transitions, effective dates, cancellation, renewal, dunning, and authoritative status | Sending user intent, displaying pending states, and never changing access because of a browser redirect |
| Payments | Provider order creation, expected amount and currency, provider webhook verification, payment status, invoice creation, and paid activation | Rendering the approved checkout integration and displaying progress or failure; it never asserts that payment succeeded |
| Entitlements and limits | The effective entitlement values, credit balances, resource limits, snapshot revision, and freshness policy | Counting actual business resources and enforcing the returned limits in application operations |
| Billing data | Authoritative accounts, subscriptions, payments, invoices, credits, ledger, and snapshot | A non-authoritative cached projection, its ETag/revision, and any product-specific display state |
| Events | Durable outbox creation, signed delivery, retry, and secret rotation | A durable webhook inbox, signature verification, event-ID deduplication, snapshot refresh, dead-letter handling, and monitoring |
| Availability | Publishing snapshot freshness and degraded-operation policy | Failing closed after the allowed window and never using stale state to grant or increase a capability |
| Product configuration | Validating and applying product billing policy, plans, entitlements, and credit packs | Supplying product requirements to the Billmesh administrators; no application-side copies of billing rules |

IdNest owns authentication and identity assertions. A payment provider owns the
external payment event. Billmesh verifies both boundaries before changing
billing state.

## What a consuming application must never do

- Do not hard-code plan or credit-pack slugs, prices, currency, Free/Paid
  classification, entitlements, or lifecycle rules.
- Do not send a price, billing model, payment status, or provider secret to
  choose or activate a subscription.
- Do not activate access after a success redirect, client callback, or checkout
  modal result. Only a newer Billmesh snapshot can activate access.
- Do not mutate a cached projection from webhook payload data. Treat the event
  as an invalidation signal and refetch the snapshot.
- Do not let stale state grant a new capability, raise a limit, or restore a
  revoked capability.
- Do not register an application integration as an administrative client.
- Do not implement direct subscription create, plan-change, reactivation, or
  renewal calls. They are not part of the API. Use subscription transitions and
  the current-account cancellation operation.
- Do not treat Billmesh account IDs, customer IDs, plan IDs, or transition IDs
  as application authorization. Authorization comes from the verified token.

## Required identity context

Service API calls use the IdNest Delegated Access profile defined in
[`idnest-auth-development.md`](idnest-auth-development.md). It contains these
verified concepts:

- `iss`: the configured IdNest delegation issuer;
- `aud`: the absolute Billmesh resource audience;
- `authorizer_client_id` plus `client_id`: the signed authorizer/actor pair,
  registered by Billmesh for one fixed scope, API profile, actor type, and
  product/environment;
- `sub`: a stable actor or owner subject when the product's customer scope is
  `identity`;
- structured `org_id` and optional `billing_customer_id` context asserted by
  the trusted authorizer;
- `app`, `environment`, and `actor_type`, derived by Billmesh from the trusted
  pair registration rather than caller-selected claims;
- `billing_customer_id`: a stable trusted customer reference only when the
  product policy uses `external_customer` scope.

The `sub` of a delegated service actor is never used as a human owner.
Identity-scoped account creation requires a pair registered with
`actor_type=user`.

An application must use its registered authorizer and billing actor with an
organization-scoped context for account commands. For catalogue discovery
before an organization context exists, use the registered catalogue actor or
the public catalogue when product policy explicitly permits it.

## Canonical integration flow

### 1. Obtain deployment configuration

Before making requests, the application team obtains the following from the
Billmesh team:

- the Billmesh base URL for each environment;
- the IdNest delegation issuer and discovery URL, Billmesh audience, context
  profile, and registered authorizer/actor pair;
- the configured product identity and billing policy;
- seeded plans, entitlements, and optional credit packs;
- the supported Razorpay test checkout configuration;
- the webhook API version, supported event types, and a signing secret;
- the error-code catalogue and support/escalation contact.

Production and non-production identities, accounts, provider orders, webhooks,
and data must remain isolated.

### 2. Fetch the catalogue

Use one of these operations, according to the product's catalogue policy:

- `GET /v1/public/catalog?product={application}` for an explicitly public
  catalogue; or
- `GET /v1/catalog?product={application}` with the product's
  `billmesh.catalogue` actor.

Store the response `ETag` and use `If-None-Match` on refresh. Render the returned
plans and credit packs. Submit only the selected opaque `plan_id` or
`credit_pack_id`; do not convert the selection into a local slug or price.

### 3. Create or resolve the billing account

Call `POST /v1/accounts` with only `name` and optional `external_ref`.
Application, organization, environment, and customer identity come from the
verified token. When product policy requires catalogue acknowledgement, send
the current catalogue ETag in `If-Match`.

Billmesh derives the application, environment, actor type, and API profile from
the trusted pair registration and the billing customer from verified context,
creates or resolves the account, applies onboarding policy, and checks Free-plan
eligibility. Repeating account creation for the same application, organization,
and environment resolves the existing account.

The application must not send an internal Billmesh customer ID. It must stop and
surface an eligibility or onboarding rejection; it must not create another
organization or identity to bypass the decision.

For ownership changes, a registered service actor with billing or administrative
authority creates
`POST /v1/account-ownership-transfers` using the current snapshot ETag,
receives an eligibility decision, commits the application-side ownership change
only when allowed, and confirms the intent with its ID and current ETag. It must
cancel unused intents. A `requires_paid_transition` decision means the current
Free subscription must be moved to a Paid plan before confirmation. Billmesh
rechecks eligibility atomically at confirmation.

### 4. Read and store the billing snapshot

Call `GET /v1/billing-snapshot?product={application}` with the product's
`billmesh.billing` actor.
Persist, at minimum:

- the response body as a non-authoritative projection;
- its `ETag` and monotonic `revision`;
- `verified_at`, `expires_at`, and `degraded_until`;
- the application organization and environment to which it belongs.

Use `If-None-Match` for conditional refreshes. A `304 Not Modified` means the
stored projection remains current; it does not create a new billing revision.
Reject a response whose revision moves backwards for the same account and
product.

Use the effective plan embedded in the subscription snapshot. Do not join it to
the current catalogue: a subscribed plan may have been retired or replaced.

For enforcement, compare application-owned resource counts with limits and
entitlements from the snapshot. At or after `expires_at`, refresh before any
operation that could grant or increase access. During an allowed degraded
window, retain or reduce existing capability only. After `degraded_until`, or
when no degraded window exists, fail closed for protected operations.

### 5. Request a subscription transition

Call `POST /v1/subscription-transitions` with:

- the opaque `plan_id` selected from the current catalogue;
- an optional supported `effective` intent;
- allowed success and cancellation URLs when checkout presentation needs them;
- a unique `Idempotency-Key`; and
- the last snapshot ETag in `If-Match` when available.

Reuse an idempotency key only to retry the identical request. A different body
with the same key is a conflict. On a stale-state response, fetch a fresh
snapshot, reevaluate the UI, and ask the user to confirm again when the outcome
has materially changed.

Interpret the returned transition status as follows:

- `completed`: fetch the snapshot; do not update access directly from the
  transition response;
- `requires_payment`: open only the checkout mechanism returned by Billmesh;
- `processing`: show a pending state and poll or wait for invalidation;
- `failed`, `expired`, or `cancelled`: show a safe failure state and fetch the
  snapshot before offering another action.

The typed `checkout` response is directly usable. For `modal` or `inline`
presentation, use `client_config.public_key`, `order_id`, `amount_minor`, and
`currency`. For `provider_hosted`, open `checkout_url`. Never construct a
checkout from catalogue data or store a provider secret in the consumer.

### 6. Confirm effective access

A provider redirect is only a user-experience signal. After redirect, webhook,
or polling completion, conditionally fetch the billing snapshot. Grant paid
access only when the snapshot has a newer applicable revision and reports the
subscription and entitlements as effective.

### 7. Cancel or withdraw cancellation

Use `POST /v1/subscriptions/current/cancellation` with a unique
`Idempotency-Key`. The `effective` value is `immediate`, `period_end`, or
`withdraw`, subject to product policy. Fetch the snapshot after the command.

There are no alternate consumer cancellation routes.

### 8. Receive webhooks and reconcile

Register a consumer endpoint with `target_url`, a 32-character-or-longer secret,
optional event filters, and webhook API version `2`. Billmesh derives the
application from the token; the request cannot select another application. The
wire contract is
defined by `BillmeshWebhookEventV2` in OpenAPI and
[`webhook-contract.md`](webhook-contract.md). Billmesh sends these headers:

- `X-Billmesh-Event-ID`;
- `X-Billmesh-Event-Type`;
- `X-Billmesh-Webhook-Version`;
- `X-Billmesh-Timestamp`;
- `X-Billmesh-Signature`;
- `X-Billmesh-Previous-Signature` during secret-rotation grace, when applicable.

For version 2, the signature is the lowercase hexadecimal HMAC-SHA256
of `timestamp + "." + raw_request_body`. Verify it against the raw bytes before
JSON parsing, enforce the five-minute timestamp tolerance, and compare
signatures in constant time.

The receiver must durably record the event ID before acknowledging it, safely
deduplicate retries, and return a 2xx response only after the inbox write. It
then conditionally refetches the snapshot and applies a newer revision. It must
tolerate duplicate, delayed, missing, and out-of-order events.

Webhooks improve freshness but are not the only recovery mechanism. The
consumer must periodically reconcile active organizations by conditionally
fetching snapshots. Billmesh and the consumer must alert on repeated delivery
failure or projection age beyond policy.

## Command and error behavior

Every externally retryable billing command documented with `Idempotency-Key`
requires a non-empty key of at most 200 characters. Keys are scoped to the
authenticated account, product or target resource, and operation. Replaying the
same key and payload returns the existing semantic result; reusing it for a
different payload returns `409` with `code=idempotency_conflict`. Billmesh keeps
these keys with the associated billing records; there is no short automatic
expiry on published v1 command keys.

Use `If-Match` when a command depends on the snapshot revision. A `412` with
`code=stale_revision` means the consumer must refresh the snapshot before
asking the user to reconfirm. Read operations that advertise `If-None-Match`
return `304` when the stored representation remains current.

Every JSON error has `error`, stable `code`, and `request_id`, and every response
has `X-Request-ID`. Messages are for people; application logic branches on the
HTTP status and code listed in
[`api-error-codes.md`](api-error-codes.md).

`GET /v1/payments` and `GET /v1/invoices` return `{items, next_cursor}`. Pass the
opaque non-null `next_cursor` back as `cursor` and do not parse or persist its
internal representation as business data.

## Consumer storage model

A consumer may store a projection similar to:

```text
organization_id
billmesh_environment
snapshot_revision
snapshot_etag
snapshot_body
verified_at
expires_at
degraded_until
last_refresh_attempt_at
```

The projection should be replace-only by revision, not a collection of locally
interpreted billing tables. Product resources and resource counts remain in the
consumer's own domain model.

The webhook inbox should store at least the event ID, received timestamp, raw
body or integrity-preserving equivalent, processing state, attempt count, last
error, and resulting snapshot revision.

## Expected consumer behavior

Every consuming application must:

1. use least-privilege service credentials and keep tokens and webhook secrets
   out of browser storage and logs;
2. use opaque catalogue IDs and the authoritative transition API;
3. send idempotency keys for commands and handle conflict responses;
4. cache snapshots by ETag/revision and honor their freshness boundaries;
5. enforce entitlements at the server-side business-operation boundary, not
   only in the UI;
6. maintain a durable, deduplicating webhook inbox;
7. reconcile snapshots periodically even when webhooks appear healthy;
8. send a valid `X-Request-ID` when correlating calls and always record the
   returned `X-Request-ID` and error `request_id`;
9. test stale snapshots, timeouts, duplicate events, checkout abandonment,
   payment delay, cancellation, and ownership-change scenarios; and
10. provide operational owners for integration alerts and customer support.

## Billmesh publication checklist for a new application

Before approving an application for production, the Billmesh team must confirm:

- the product, policy, entitlement schema, plans, and credit packs are reviewed
  and seeded independently in each environment;
- the chosen customer scope and stable identity mapping are documented;
- each authorizer/actor pair is registered for exactly one Billmesh scope and
  API profile, and no consumer pair is registered as an administrator;
- catalogue, onboarding, transitions, cancellation, snapshot, checkout, and
  webhook scenarios pass against the consumer's staging deployment;
- snapshot and webhook schemas used by the consumer are versioned and frozen;
- secret rotation and provider/payment failure tests pass;
- dashboards, alerts, reconciliation jobs, runbooks, and escalation owners are
  active; and
- duplicate direct subscription mutation routes have been removed.
