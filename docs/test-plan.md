# Billmesh — Complete Backend Test Case Checklist

The following covers the agreed Billmesh scope: subscriptions, billing, credit wallets, real-time metering, Razorpay payments, notifications, and application integration.

Testing conventions:

* U — Unit: Mock dependencies and test business logic.

* I — Integration: Use real PostgreSQL through Docker Compose.

* E — E2E: Test the actual API and worker with PostgreSQL and mocked external services.

* P — Performance: Concurrency, load, and recovery testing.

All database-dependent integration and E2E tests must use real PostgreSQL. Mock IdNest, Razorpay, Stripe, email, and external application webhook receivers.

## 1. Application Foundation & Configuration

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

FND-001

|

U

|

Validate required environment variables and reject missing configuration.

|
|

FND-002

|

U

|

Reject invalid database URLs, ports, and configuration values.

|
|

FND-003

|

E

|

Verify API starts successfully with valid configuration.

|
|

FND-004

|

E

|

Verify health endpoint returns the expected response.

|
|

FND-005

|

I

|

Verify readiness fails when PostgreSQL is unavailable.

|
|

FND-006

|

I

|

Verify database migrations run successfully on an empty database.

|
|

FND-007

|

I

|

Verify repeated migration execution does not corrupt the schema.

|
|

FND-008

|

I

|

Verify failed migrations roll back safely.

|
|

FND-009

|

E

|

Verify graceful shutdown completes or safely interrupts active operations.

|
|

FND-010

|

E

|

Verify API and worker can restart without losing committed transactions.

|

## 2. Authentication & Authorization

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

AUTH-001

|

E

|

Accept a valid IdNest JWT.

|
|

AUTH-002

|

E

|

Reject requests without authentication.

|
|

AUTH-003

|

E

|

Reject expired JWTs.

|
|

AUTH-004

|

E

|

Reject JWTs with invalid signatures.

|
|

AUTH-005

|

E

|

Reject tokens issued by an untrusted issuer.

|
|

AUTH-006

|

E

|

Reject tokens with an incorrect audience.

|
|

AUTH-007

|

E

|

Verify JWT validation using the configured JWKS endpoint.

|
|

AUTH-008

|

E

|

Verify authentication continues correctly after signing-key rotation.

|
|

AUTH-009

|

E

|

Validate machine-to-machine service authentication.

|
|

AUTH-010

|

E

|

Reject unauthorized service-to-service requests.

|
|

AUTH-011

|

E

|

Reject users accessing another organization's billing records.

|
|

AUTH-012

|

E

|

Reject users modifying another organization's wallet.

|
|

AUTH-013

|

E

|

Restrict administrative endpoints to authorized roles.

|
|

AUTH-014

|

E

|

Allow billing viewers to read without modifying records.

|
|

AUTH-015

|

E

|

Prevent runtime clients from modifying prices or subscriptions.

|
|

AUTH-016

|

E

|

Verify tenant and environment isolation across all relevant APIs.

|

## 3. Billing Accounts & Customers

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

ACC-001

|

E

|

Create a billing account successfully.

|
|

ACC-002

|

E

|

Retrieve an existing billing account.

|
|

ACC-003

|

E

|

Update authorized billing account details.

|
|

ACC-004

|

I

|

Prevent duplicate billing accounts for the same external account reference.

|
|

ACC-005

|

E

|

Link a Daybook organization to a billing account.

|
|

ACC-006

|

E

|

Link a Taskmesh organization to a billing account.

|
|

ACC-007

|

E

|

Support multiple products under one billing account.

|
|

ACC-008

|

E

|

Reject unauthorized changes to organization mappings.

|
|

ACC-009

|

E

|

Preserve subscriptions and transactions when customer details change.

|

## 4. Products, Plans & Pricing

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

PLAN-001

|

E

|

Create a product with valid configuration.

|
|

PLAN-002

|

E

|

Create a free subscription plan.

|
|

PLAN-003

|

E

|

Create monthly and annual paid plans.

|
|

PLAN-004

|

E

|

Retrieve publicly available plans.

|
|

PLAN-005

|

U

|

Validate subscription prices and currencies.

|
|

PLAN-006

|

U

|

Reject negative prices and invalid credit allowances.

|
|

PLAN-007

|

E

|

Configure included workflow credits for a plan.

|
|

PLAN-008

|

E

|

Configure feature entitlements and usage limits.

|
|

PLAN-009

|

E

|

Prevent customers from modifying plan configuration.

|
|

PLAN-010

|

I

|

Preserve historical subscription prices after plan changes.

|
|

PLAN-011

|

E

|

Prevent new subscriptions to inactive plans.

|
|

PLAN-012

|

E

|

Configure purchasable credit packs independently of subscription plans.

|

## 5. Subscription Management

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

SUB-001

|

E

|

Create a free subscription without payment.

|
|

SUB-002

|

E

|

Create a paid subscription with a pending payment.

|
|

SUB-003

|

E

|

Activate a subscription after verified payment.

|
|

SUB-004

|

E

|

Reject duplicate active subscriptions where the product policy prohibits them.

|
|

SUB-005

|

E

|

Retrieve the current subscription for an organization.

|
|

SUB-006

|

E

|

Upgrade a subscription to another plan.

|
|

SUB-007

|

E

|

Downgrade a subscription according to the configured effective-date policy.

|
|

SUB-008

|

U

|

Calculate subscription billing periods correctly.

|
|

SUB-009

|

U

|

Calculate annual and monthly renewal dates correctly.

|
|

SUB-010

|

E

|

Cancel a subscription immediately when requested.

|
|

SUB-011

|

E

|

Schedule cancellation at the billing-period end.

|
|

SUB-012

|

E

|

Reactivate an eligible cancelled subscription.

|
|

SUB-013

|

E

|

Renew an existing subscription after successful payment.

|
|

SUB-014

|

E

|

Apply configured grace-period behavior after failed payment.

|
|

SUB-015

|

E

|

Suspend paid entitlements when the grace period expires.

|
|

SUB-016

|

I

|

Prevent duplicate renewals for the same billing period.

|
|

SUB-017

|

I

|

Prevent duplicate monthly credit allocation during renewal.

|
|

SUB-018

|

E

|

Preserve subscription history after cancellation.

|
|

SUB-019

|

E

|

Allow independent Daybook and Taskmesh subscriptions.

|
|

SUB-020

|

E

|

Ensure cancellation of Taskmesh does not cancel Daybook.

|

## 6. Entitlements & Feature Access

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

ENT-001

|

E

|

Retrieve effective entitlements for a subscription.

|
|

ENT-002

|

U

|

Evaluate boolean feature entitlements.

|
|

ENT-003

|

U

|

Evaluate numeric usage limits.

|
|

ENT-004

|

E

|

Reject access to unavailable premium features.

|
|

ENT-005

|

E

|

Update entitlements after subscription upgrade.

|
|

ENT-006

|

E

|

Apply entitlement changes after downgrade.

|
|

ENT-007

|

E

|

Revoke paid entitlements after subscription expiration.

|
|

ENT-008

|

E

|

Allow Daybook-funded execution without a paid Taskmesh subscription.

|
|

ENT-009

|

E

|

Prevent Daybook credits from granting standalone Taskmesh creation rights.

|
|

ENT-010

|

E

|

Preserve valid Daybook entitlements after Taskmesh cancellation.

|

## 7. Credit Wallets & Grants

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

WAL-001

|

E

|

Create a wallet for a billing account and product.

|
|

WAL-002

|

E

|

Retrieve the current wallet balance.

|
|

WAL-003

|

E

|

Allocate included subscription credits.

|
|

WAL-004

|

E

|

Allocate purchased credits after verified payment.

|
|

WAL-005

|

E

|

Apply an authorized promotional or administrative credit grant.

|
|

WAL-006

|

I

|

Prevent duplicate grants using idempotency keys.

|
|

WAL-007

|

I

|

Reject invalid or negative grant amounts.

|
|

WAL-008

|

U

|

Consume included credits before purchased credits.

|
|

WAL-009

|

U

|

Consume grants according to expiration and priority rules.

|
|

WAL-010

|

I

|

Expire unused monthly credits correctly.

|
|

WAL-011

|

I

|

Preserve purchased credits during monthly allowance reset.

|
|

WAL-012

|

I

|

Expire purchased credits according to their validity.

|
|

WAL-013

|

I

|

Prevent expired credits from funding reservations.

|
|

WAL-014

|

E

|

Maintain separate Daybook and Taskmesh wallets.

|
|

WAL-015

|

E

|

Prevent cross-product credit consumption without explicit authorization.

|
|

WAL-016

|

I

|

Verify wallet totals match ledger and active reservation records.

|
|

WAL-017

|

E

|

Retrieve complete wallet transaction history.

|
|

WAL-018

|

I

|

Prevent unauthorized modification or deletion of ledger records.

|

## 8. Credit Reservations & Settlement

Critical financial correctness

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

RES-001

|

E

|

Reserve credits when sufficient balance exists.

|
|

RES-002

|

E

|

Reject reservations exceeding available credits.

|
|

RES-003

|

I

|

Prevent simultaneous requests from reserving the same credits.

|
|

RES-004

|

I

|

Verify PostgreSQL row-level locking during reservation.

|
|

RES-005

|

I

|

Roll back the complete reservation when any database operation fails.

|
|

RES-006

|

E

|

Settle a reservation using actual execution consumption.

|
|

RES-007

|

E

|

Release unused credits after settlement.

|
|

RES-008

|

E

|

Release the full reservation after eligible execution cancellation.

|
|

RES-009

|

I

|

Prevent duplicate settlement of the same reservation.

|
|

RES-010

|

I

|

Prevent duplicate release of the same reservation.

|
|

RES-011

|

E

|

Reject settlement of an unknown reservation.

|
|

RES-012

|

E

|

Reject settlement belonging to another organization.

|
|

RES-013

|

E

|

Reject invalid reservation state transitions.

|
|

RES-014

|

I

|

Ensure settlement and ledger updates commit atomically.

|
|

RES-015

|

I

|

Preserve grant-allocation information during reservation.

|
|

RES-016

|

E

|

Extend a reservation for a long-running workflow.

|
|

RES-017

|

E

|

Reject reservation extension when credits are insufficient.

|
|

RES-018

|

I

|

Recover expired or abandoned reservations safely.

|
|

RES-019

|

I

|

Prevent negative spendable balances under concurrent execution.

|
|

RES-020

|

I

|

Preserve balances after a database transaction rollback.

|
|

RES-021

|

I

|

Reject reuse of an idempotency key with a different payload.

|

## 9. Usage Metering

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

MTR-001

|

E

|

Record a valid workflow execution event.

|
|

MTR-002

|

E

|

Record LLM input and output token consumption.

|
|

MTR-003

|

E

|

Record agent runtime and compute consumption.

|
|

MTR-004

|

U

|

Calculate configured credit consumption using fixed-point arithmetic.

|
|

MTR-005

|

U

|

Apply the correct meter pricing configuration.

|
|

MTR-006

|

E

|

Reject unsupported meter names.

|
|

MTR-007

|

E

|

Reject negative or invalid usage quantities.

|
|

MTR-008

|

I

|

Deduplicate usage events by event ID.

|
|

MTR-009

|

I

|

Prevent double charging after usage-event retries.

|
|

MTR-010

|

E

|

Accept valid batch usage events.

|
|

MTR-011

|

I

|

Persist valid usage events durably before acknowledgement.

|
|

MTR-012

|

E

|

Retrieve usage filtered by customer, product and meter.

|
|

MTR-013

|

E

|

Retrieve usage within a specified date range.

|
|

MTR-014

|

I

|

Aggregate usage correctly across billing periods.

|
|

MTR-015

|

E

|

Attribute usage to the correct application installation.

|
|

MTR-016

|

E

|

Reject usage submitted for an unauthorized customer.

|
|

MTR-017

|

I

|

Reconcile recorded consumption against wallet settlements.

|

## 10. Razorpay Payments & Credit Purchases

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

PAY-001

|

E

|

Create a Razorpay order using the mock provider.

|
|

PAY-002

|

E

|

Return checkout information to the requesting application.

|
|

PAY-003

|

U

|

Resolve credit-pack amount and price from server configuration.

|
|

PAY-004

|

E

|

Reject client-supplied price manipulation.

|
|

PAY-005

|

E

|

Verify a valid Razorpay webhook signature.

|
|

PAY-006

|

E

|

Reject an invalid webhook signature.

|
|

PAY-007

|

E

|

Reject malformed payment webhook payloads.

|
|

PAY-008

|

E

|

Confirm a successful payment and allocate purchased credits.

|
|

PAY-009

|

E

|

Prevent credit allocation after failed payment.

|
|

PAY-010

|

I

|

Prevent duplicate credits from repeated payment webhooks.

|
|

PAY-011

|

E

|

Handle out-of-order Razorpay events correctly.

|
|

PAY-012

|

E

|

Recover safely from payment-provider timeouts.

|
|

PAY-013

|

E

|

Reconcile a pending purchase using provider payment status.

|
|

PAY-014

|

I

|

Link payment, purchase, wallet grant and ledger transaction.

|
|

PAY-015

|

E

|

Prevent one payment from funding multiple independent purchases.

|
|

PAY-016

|

E

|

Process full and partial refunds according to credit-reversal policy.

|
|

PAY-017

|

I

|

Prevent duplicate refund processing.

|
|

PAY-018

|

E

|

Process recurring-subscription payment confirmation.

|
|

PAY-019

|

E

|

Handle failed subscription renewal payments.

|
|

PAY-020

|

E

|

Preserve gateway transaction references for auditing.

|

All E2E payment cases should use a fake Razorpay HTTP service and realistic signed webhook payloads.

## 11. Billing Records & Invoices

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

INV-001

|

E

|

Generate an invoice record for a paid subscription.

|
|

INV-002

|

E

|

Generate a billing record for a credit purchase.

|
|

INV-003

|

U

|

Calculate invoice totals using configured prices and currency precision.

|
|

INV-004

|

E

|

Mark an invoice paid after verified payment.

|
|

INV-005

|

I

|

Prevent duplicate invoice generation for the same billing operation.

|
|

INV-006

|

E

|

Retrieve invoice history for the authorized billing account.

|
|

INV-007

|

E

|

Generate a credit note or adjustment for an eligible refund.

|
|

INV-008

|

I

|

Preserve finalized invoice history against unauthorized modifications.

|

## 12. Webhook Notifications

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

WH-001

|

E

|

Register an application webhook endpoint.

|
|

WH-002

|

E

|

Reject unauthorized webhook configuration changes.

|
|

WH-003

|

E

|

Deliver a subscription activation event.

|
|

WH-004

|

E

|

Deliver a subscription cancellation event.

|
|

WH-005

|

E

|

Deliver wallet credit and debit events.

|
|

WH-006

|

E

|

Deliver a low-balance notification.

|
|

WH-007

|

E

|

Deliver an exhausted-credit notification.

|
|

WH-008

|

E

|

Deliver a usage-limit-exceeded event.

|
|

WH-009

|

E

|

Deliver payment success and failure notifications.

|
|

WH-010

|

E

|

Sign outgoing webhooks using HMAC.

|
|

WH-011

|

E

|

Retry webhook delivery after an HTTP 500 response.

|
|

WH-012

|

E

|

Handle webhook receiver timeouts.

|
|

WH-013

|

I

|

Persist pending webhook events before delivery.

|
|

WH-014

|

I

|

Prevent duplicate logical events for the same threshold crossing.

|
|

WH-015

|

E

|

Record delivery attempts and final status.

|
|

WH-016

|

E

|

Replay a failed webhook without creating duplicate billing transactions.

|
|

WH-017

|

E

|

Stop automatic retries according to the configured retry policy.

|
|

WH-018

|

E

|

Resume pending deliveries after worker restart.

|

## 13. Thresholds & Limit Enforcement

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

LIM-001

|

U

|

Calculate usage percentage correctly.

|
|

LIM-002

|

E

|

Trigger a warning when usage reaches 50%.

|
|

LIM-003

|

E

|

Trigger a warning when usage reaches 80%.

|
|

LIM-004

|

E

|

Trigger a critical warning when usage reaches 90%.

|
|

LIM-005

|

E

|

Trigger an exhaustion event at the configured limit.

|
|

LIM-006

|

I

|

Prevent repeated notifications for the same threshold crossing.

|
|

LIM-007

|

E

|

Reject execution when insufficient spendable credits exist.

|
|

LIM-008

|

E

|

Allow execution after a successful credit top-up.

|
|

LIM-009

|

E

|

Reset period-specific notification state on renewal.

|
|

LIM-010

|

E

|

Send notifications to the application owning the billing context.

|

Threshold percentages are illustrative defaults; the platform should support configurable values.

## 14. Real-Time Monitoring & SSE

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

LIVE-001

|

E

|

Establish an authenticated SSE connection.

|
|

LIVE-002

|

E

|

Reject unauthenticated SSE connections.

|
|

LIVE-003

|

E

|

Publish balance updates after committed credit transactions.

|
|

LIVE-004

|

E

|

Publish reservation and settlement updates.

|
|

LIVE-005

|

E

|

Publish subscription status changes.

|
|

LIVE-006

|

E

|

Deliver only events authorized for the connected organization.

|
|

LIVE-007

|

E

|

Resume an SSE stream using the last received event ID.

|
|

LIVE-008

|

E

|

Deliver missed events after reconnection.

|
|

LIVE-009

|

E

|

Prevent duplicate logical events during replay.

|
|

LIVE-010

|

E

|

Handle disconnected and slow clients without blocking billing operations.

|
|

LIVE-011

|

I

|

Verify SSE replay retrieves committed events from PostgreSQL.

|
|

LIVE-012

|

E

|

Verify usage and balance APIs remain available when SSE is disconnected.

|

## 15. Background Worker & Recovery

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

WRK-001

|

E

|

Process pending outbox events.

|
|

WRK-002

|

I

|

Prevent two workers from claiming the same job concurrently.

|
|

WRK-003

|

I

|

Release or recover jobs abandoned by crashed workers.

|
|

WRK-004

|

E

|

Retry failed jobs with the configured backoff policy.

|
|

WRK-005

|

E

|

Stop retrying permanently invalid jobs.

|
|

WRK-006

|

E

|

Process credit expiration jobs correctly.

|
|

WRK-007

|

E

|

Process subscription renewal jobs correctly.

|
|

WRK-008

|

I

|

Prevent duplicate processing after worker restart.

|
|

WRK-009

|

I

|

Preserve committed jobs during API shutdown.

|
|

WRK-010

|

E

|

Recover pending jobs when PostgreSQL becomes available again.

|

## 16. Administrative Operations & Audit Logging

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

ADM-001

|

E

|

Allow administrators to create and update plans.

|
|

ADM-002

|

E

|

Allow authorized administrators to inspect subscriptions.

|
|

ADM-003

|

E

|

Allow authorized manual credit adjustments.

|
|

ADM-004

|

E

|

Require a reason for manual financial adjustments.

|
|

ADM-005

|

I

|

Record every manual adjustment in the ledger.

|
|

ADM-006

|

E

|

Allow administrators to inspect payment history.

|
|

ADM-007

|

E

|

Allow administrators to inspect failed webhooks.

|
|

ADM-008

|

E

|

Restrict webhook replay to authorized administrators.

|
|

ADM-009

|

I

|

Record actor, timestamp, action and affected resource for audited changes.

|
|

ADM-010

|

E

|

Prevent support/viewer roles from performing privileged financial actions.

|

## 17. Performance, Concurrency & Reliability

|
ID

|

Type

|

Test case

|
| --- | --- | --- |
|

PERF-001

|

P

|

Benchmark credit reservation latency under normal load.

|
|

PERF-002

|

P

|

Run concurrent reservations against the same wallet.

|
|

PERF-003

|

P

|

Run concurrent reservations against independent wallets.

|
|

PERF-004

|

P

|

Benchmark batch usage-event ingestion.

|
|

PERF-005

|

P

|

Benchmark wallet balance queries during active metering.

|
|

PERF-006

|

P

|

Test concurrent SSE connections and event delivery.

|
|

PERF-007

|

P

|

Verify slow webhook receivers do not block API operations.

|
|

PERF-008

|

P

|

Simulate PostgreSQL connection exhaustion and verify safe failures.

|
|

PERF-009

|

P

|

Restart the API during active financial operations.

|
|

PERF-010

|

P

|

Restart the worker during webhook processing.

|
|

PERF-011

|

P

|

Verify recovery from temporary database unavailability.

|
|

PERF-012

|

P

|

Verify ledger reconciliation after sustained concurrent activity.

|

Performance thresholds should be established from measured baseline results on your intended Hetzner deployment configuration.

# 18. Complete Business E2E Journeys

These tests combine multiple modules and should run against the real Billmesh API, worker and PostgreSQL.

P0

Journey 1 — Daybook without Taskmesh Premium

The central business requirement.

1. Register a Daybook organization.

2. Create an active Daybook subscription.

3. Allocate the monthly workflow allowance.

4. Link the authorized Taskmesh installation.

5. Execute a Daybook workflow using the Daybook wallet.

6. Confirm successful execution without Taskmesh Professional.

P0

Journey 2 — Exhaustion and Top-Up

1. Consume the available Daybook allowance.

2. Verify further unfunded execution is rejected.

3. Verify Daybook receives the exhaustion webhook.

4. Purchase additional credits through mock Razorpay.

5. Verify payment and grant credits exactly once.

6. Resume workflow execution using purchased credits.

P0

Journey 3 — Independent Taskmesh Subscription

1. Purchase Taskmesh Professional.

2. Activate standalone workflow entitlements.

3. Execute a standalone workflow using Taskmesh credits.

4. Verify Daybook credits remain unchanged.

5. Cancel Taskmesh Professional.

6. Verify authorized Daybook workflows continue working.

P0

Journey 4 — Concurrent Execution

1. Allocate 100 credits.

2. Start two workflows simultaneously, each requesting 80 credits.

3. Verify only one initial reservation succeeds.

4. Settle the successful execution.

5. Verify the ledger, reservations and available balance reconcile.

## Recommended testing priority

|
Priority

|

Coverage

|

Release requirement

|
| --- | --- | --- |
|

P0 — Critical

|

Credits, reservations, payments, authorization, idempotency

|

Mandatory before production

|
|

P1 — Core

|

Subscriptions, entitlements, metering, renewals, webhooks

|

Mandatory before production

|
|

P2 — Operational

|

SSE, administration, recovery and reporting

|

Mandatory for the corresponding feature release

|
|

P3 — Extended

|

Sustained load and resilience benchmarking

|

Required before scaling or changing deployment topology

|

The most important release gate for Billmesh is financial correctness, not simply achieving a high unit-test coverage percentage.

Every P0 scenario should pass against real PostgreSQL, including concurrency, rollback, duplicate webhook, and repeated settlement tests.

## Recovered executable cases

These cases are exercised by the current test suites and were omitted from the checklist above.

| ID | Type | Test case |
| --- | --- | --- |
| ACC-010 | I | Keep identical external organization IDs separate across applications. |
| AUTH-017 | E | Reject an OIDC ID token presented as an API access token. |
| AUTH-018 | E | Reject a valid service token that lacks permission for the requested billing operation. |
| AUTH-019 | E | Reject requests that manipulate nested resource IDs to access another billing account. |
| FND-011 | I | Preserve subscriptions, ledger entries, and pending reservations through backup and restore. |
| FND-012 | I | Keep schema migrations compatible with the supported deployment sequence. |
| INST-001 | E | Reject workflow execution using a forged or nonexistent installation ID. |
| INST-002 | E | Reject new executions after the application installation is revoked. |
| INST-003 | E | Reject an installation whose Daybook organization does not match the authorized billing account. |
| INST-004 | E | Prevent the caller from changing the execution billing source to another wallet. |
| INST-005 | E | Preserve the defined settlement behavior when an installation is revoked during an active workflow. |
| INV-009 | I | Reconcile invoice totals with subscription, credit purchase, and payment records. |
| LIM-011 | I | Re-arm a low-balance warning after top-up and emit it once on a new crossing. |
| LIM-012 | E | Apply policy when a long-running execution exhausts credits mid-process. |
| LIVE-013 | E | Reject SSE replay using an event ID belonging to another billing account. |
| LIVE-014 | E | Enforce session expiration for an already connected SSE client. |
| LIVE-015 | E | Return the resynchronization response when an SSE event is no longer retained. |
| LIVE-016 | E | Preserve SSE heartbeat and reconnection behavior through the reverse proxy. |
| MTR-018 | I | Reject an existing event ID submitted again with different usage data. |
| MTR-019 | E | Prevent charging an execution through both reservation settlement and a usage event. |
| MTR-020 | I | Attribute delayed usage to the correct period without changing a finalized period. |
| MTR-021 | E | Enforce batch atomicity when valid and invalid usage events are submitted together. |
| MTR-022 | I | Scope usage-event deduplication across products, tenants, and environments. |
| MTR-023 | E | Reject usage whose execution or billing context cannot be authenticated. |
| MTR-024 | E | Apply policy to future-dated and excessively late usage events. |
| MTR-025 | I | Handle out-of-order incremental usage updates without lost or duplicate consumption. |
| MTR-026 | E | Reject oversized or invalid usage metadata without affecting other customers. |
| PAY-021 | E | Reject a signed payment event whose amount or currency differs from the order. |
| PAY-022 | E | Prevent a payment for one billing account from crediting another account wallet. |
| PAY-023 | E | Reconcile a capture received after checkout timed out and was retried. |
| PAY-024 | I | Prevent concurrent checkout requests from creating duplicate credit grants. |
| PAY-025 | E | Prevent a delayed capture from reversing an already processed refund. |
| PAY-026 | E | Handle provider rate limits and outages without duplicating payment orders. |
| PAY-027 | E | Reject a signed webhook for an unknown payment operation. |
| RES-022 | I | Return the original reservation for an identical idempotent retry without reserving credits twice. |
| RES-023 | I | Prevent concurrent settlement and release from producing contradictory ledger movements. |
| RES-024 | E | Reject settlement above the reserved amount unless an authorized extension succeeds. |
| RES-025 | I | Avoid a second reservation when a successful commit is retried after a lost response. |
| RES-026 | I | Preserve spendable balance during simultaneous top-up, expiration, and reservation operations. |
| RES-027 | I | Apply the configured policy when credits expire while an active reservation uses them. |
| SUB-021 | I | Resolve simultaneous cancellation and renewal without contradictory subscription states. |
| SUB-022 | E | Preserve entitlement state when a subscription upgrade payment fails. |
| SUB-023 | E | Prevent a late renewal notification from reactivating a cancelled subscription. |
| SUB-024 | I | Allocate included credits exactly once when renewal confirmation is retried. |
| SUB-026 | I | Prevent duplicate plan changes when a request is retried. |
| SUB-027 | E | Preserve included and purchased credit treatment after a plan change. |
| SUB-028 | E | Resolve out-of-order cancellation, payment, and renewal events correctly. |
| TEST-002 | E | Verify E2E tests use the actual API and worker rather than mocked application logic. |
| TEST-004 | E | Prevent fixture and database state from leaking between independent scenarios. |
| WAL-019 | I | Reserve credits across multiple grants when one grant cannot cover the requested amount. |
| WAL-020 | I | Refund consumed purchased credits without silently creating an invalid negative balance. |
| WAL-021 | I | Recalculate wallet balances from grants, ledger entries, and reservations and detect inconsistencies. |
| WAL-023 | I | Preserve purchased credits during a concurrent top-up and allowance reset. |
| WAL-024 | I | Apply deterministic allocation order when grants expire at the same time. |
| WRK-011 | I | Prevent multiple workers from allocating the same recurring grant. |
| WRK-012 | I | Recover after commit when a worker crashes before acknowledging its job. |
| WH-019 | I | Do not create a deliverable webhook for a rolled-back billing transaction. |
| WH-020 | E | Reject webhook destinations using private, loopback, or metadata-service addresses. |
| WH-021 | E | Accept active webhook signing secrets during rotation and reject retired secrets. |
| WH-022 | E | Prevent a webhook endpoint from receiving another tenant billing events. |
| WH-023 | E | Honor webhook retry policy when a destination returns HTTP 429. |
| WH-024 | E | Suspend delivery to a permanently disabled webhook endpoint according to policy. |
