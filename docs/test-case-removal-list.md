# Billmesh Test Cases: Removal Inventory

Generated from actual `_test.go` files plus checklist cases loaded through `testkit.RunCases`. Use this as the removal checklist.

## `internal/auth/verifier_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestJWKSVerifier` | direct | JWKSVerifier. | Passes when a valid RS256 JWT verifies successfully and expected claims are exposed. |
| `TestJWKSVerifierRejectsWrongAudience` | direct | JWKSVerifier Rejects Wrong Audience. | Passes when a wrong-audience JWT is rejected with an error. |
## `internal/config/config_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestLoadDefaults` | direct | Load Defaults. | Passes when default HTTP address and worker interval are populated from minimal valid config. |
| `TestLoadRequiresDatabase` | direct | Load Requires Database. | Passes when loading config without DATABASE_URL returns an error. |
## `tests/e2e/accounts/accounts_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `ACC-001` | E | Create a billing account successfully. | Passes when the resource is created successfully and the response/persistent state contains the new record. |
| `ACC-002` | E | Retrieve an existing billing account. | Passes when the requested authorized data is returned successfully. |
| `ACC-003` | E | Update authorized billing account details. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `ACC-005` | E | Link a Daybook organization to a billing account. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `ACC-006` | E | Link a Taskmesh organization to a billing account. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `ACC-007` | E | Support multiple products under one billing account. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `ACC-008` | E | Reject unauthorized changes to organization mappings. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `ACC-009` | E | Preserve subscriptions and transactions when customer details change. | Passes when existing valid state remains intact after the scenario. |
## `tests/e2e/admin/admin_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `ADM-001` | E | Allow administrators to create and update plans. | Passes when the authorized operation succeeds and persists the intended state. |
| `ADM-002` | E | Allow authorized administrators to inspect subscriptions. | Passes when the authorized operation succeeds and persists the intended state. |
| `ADM-003` | E | Allow authorized manual credit adjustments. | Passes when the authorized operation succeeds and persists the intended state. |
| `ADM-004` | E | Require a reason for manual financial adjustments. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `ADM-006` | E | Allow administrators to inspect payment history. | Passes when the authorized operation succeeds and persists the intended state. |
| `ADM-007` | E | Allow administrators to inspect failed webhooks. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `ADM-008` | E | Restrict webhook replay to authorized administrators. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `ADM-010` | E | Prevent support/viewer roles from performing privileged financial actions. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
## `tests/e2e/auth/auth_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `AUTH-001` | E | Accept a valid IdNest JWT. | Passes when the valid request/token/event is accepted and returns a successful result. |
| `AUTH-002` | E | Reject requests without authentication. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `AUTH-003` | E | Reject expired JWTs. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `AUTH-004` | E | Reject JWTs with invalid signatures. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `AUTH-005` | E | Reject tokens issued by an untrusted issuer. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `AUTH-006` | E | Reject tokens with an incorrect audience. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `AUTH-007` | E | Verify JWT validation using the configured JWKS endpoint. | Passes when the stated behavior is observed without data loss or contract violation. |
| `AUTH-008` | E | Verify authentication continues correctly after signing-key rotation. | Passes when the stated behavior is observed without data loss or contract violation. |
| `AUTH-009` | E | Validate machine-to-machine service authentication. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `AUTH-010` | E | Reject unauthorized service-to-service requests. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `AUTH-011` | E | Reject users accessing another organization's billing records. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `AUTH-012` | E | Reject users modifying another organization's wallet. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `AUTH-013` | E | Restrict administrative endpoints to authorized roles. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `AUTH-014` | E | Allow billing viewers to read without modifying records. | Passes when the authorized operation succeeds and persists the intended state. |
| `AUTH-015` | E | Prevent runtime clients from modifying prices or subscriptions. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `AUTH-016` | E | Verify tenant and environment isolation across all relevant APIs. | Passes when the stated behavior is observed without data loss or contract violation. |
| `AUTH-017` | E | Reject an OIDC ID token presented as an API access token. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `AUTH-018` | E | Reject a valid service token that lacks permission for the requested billing operation. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `AUTH-019` | E | Reject requests that manipulate nested resource IDs to access another billing account. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
## `tests/e2e/entitlements/entitlements_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `ENT-001` | E | Retrieve effective entitlements for a subscription. | Passes when the requested authorized data is returned successfully. |
| `ENT-004` | E | Reject access to unavailable premium features. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `ENT-005` | E | Update entitlements after subscription upgrade. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `ENT-006` | E | Apply entitlement changes after downgrade. | Passes when the configured policy/calculation is applied exactly. |
| `ENT-007` | E | Revoke paid entitlements after subscription expiration. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `ENT-008` | E | Allow Daybook-funded execution without a paid Taskmesh subscription. | Passes when the authorized operation succeeds and persists the intended state. |
| `ENT-009` | E | Prevent Daybook credits from granting standalone Taskmesh creation rights. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `ENT-010` | E | Preserve valid Daybook entitlements after Taskmesh cancellation. | Passes when existing valid state remains intact after the scenario. |
## `tests/e2e/foundation/foundation_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `FND-003` | E | Verify API starts successfully with valid configuration. | Passes when the stated behavior is observed without data loss or contract violation. |
| `FND-004` | E | Verify health endpoint returns the expected response. | Passes when the stated behavior is observed without data loss or contract violation. |
| `FND-009` | E | Verify graceful shutdown completes or safely interrupts active operations. | Passes when the stated behavior is observed without data loss or contract violation. |
| `FND-010` | E | Verify API and worker can restart without losing committed transactions. | Passes when the stated behavior is observed without data loss or contract violation. |
## `tests/e2e/foundation/health_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestHealthAndReadiness` | direct | Health And Readiness. | Passes when /healthz and /readyz return HTTP 200 with OK JSON. |
## `tests/e2e/installations/installations_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `INST-001` | E | Reject workflow execution using a forged or nonexistent installation ID. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `INST-002` | E | Reject new executions after the application installation is revoked. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `INST-003` | E | Reject an installation whose Daybook organization does not match the authorized billing account. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `INST-004` | E | Prevent the caller from changing the execution billing source to another wallet. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `INST-005` | E | Preserve the defined settlement behavior when an installation is revoked during an active workflow. | Passes when existing valid state remains intact after the scenario. |
## `tests/e2e/invoices/invoices_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `INV-001` | E | Generate an invoice record for a paid subscription. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `INV-002` | E | Generate a billing record for a credit purchase. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `INV-004` | E | Mark an invoice paid after verified payment. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `INV-006` | E | Retrieve invoice history for the authorized billing account. | Passes when the requested authorized data is returned successfully. |
| `INV-007` | E | Generate a credit note or adjustment for an eligible refund. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
## `tests/e2e/journeys/journeys_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestJourney1DaybookWithoutTaskmeshPremium` | direct | Journey1 Daybook Without Taskmesh Premium. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestJourney2ExhaustionAndTopUp` | direct | Journey2 Exhaustion And Top Up. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestJourney3IndependentTaskmeshSubscription` | direct | Journey3 Independent Taskmesh Subscription. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestJourney4ConcurrentExecution` | direct | Journey4 Concurrent Execution. | Passes when the assertions encoded in this test function hold for the scenario. |
## `tests/e2e/limits/limits_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `LIM-002` | E | Trigger a warning when usage reaches 50%. | Passes when the expected notification/event is emitted once to the authorized target. |
| `LIM-003` | E | Trigger a warning when usage reaches 80%. | Passes when the expected notification/event is emitted once to the authorized target. |
| `LIM-004` | E | Trigger a critical warning when usage reaches 90%. | Passes when the expected notification/event is emitted once to the authorized target. |
| `LIM-005` | E | Trigger an exhaustion event at the configured limit. | Passes when the expected notification/event is emitted once to the authorized target. |
| `LIM-007` | E | Reject execution when insufficient spendable credits exist. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `LIM-008` | E | Allow execution after a successful credit top-up. | Passes when the authorized operation succeeds and persists the intended state. |
| `LIM-009` | E | Reset period-specific notification state on renewal. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `LIM-010` | E | Send notifications to the application owning the billing context. | Passes when the expected notification/event is emitted once to the authorized target. |
| `LIM-012` | E | Apply policy when a long-running execution exhausts credits mid-process. | Passes when the configured policy/calculation is applied exactly. |
## `tests/e2e/metering/metering_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `MTR-001` | E | Record a valid workflow execution event. | Passes when the event/action is persisted in the expected table or audit trail. |
| `MTR-002` | E | Record LLM input and output token consumption. | Passes when the event/action is persisted in the expected table or audit trail. |
| `MTR-003` | E | Record agent runtime and compute consumption. | Passes when the event/action is persisted in the expected table or audit trail. |
| `MTR-006` | E | Reject unsupported meter names. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `MTR-007` | E | Reject negative or invalid usage quantities. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `MTR-010` | E | Accept valid batch usage events. | Passes when the valid request/token/event is accepted and returns a successful result. |
| `MTR-012` | E | Retrieve usage filtered by customer, product and meter. | Passes when the requested authorized data is returned successfully. |
| `MTR-013` | E | Retrieve usage within a specified date range. | Passes when the requested authorized data is returned successfully. |
| `MTR-015` | E | Attribute usage to the correct application installation. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `MTR-016` | E | Reject usage submitted for an unauthorized customer. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `MTR-019` | E | Prevent charging an execution through both reservation settlement and a usage event. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `MTR-021` | E | Enforce batch atomicity when valid and invalid usage events are submitted together. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `MTR-023` | E | Reject usage whose execution or billing context cannot be authenticated. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `MTR-024` | E | Apply policy to future-dated and excessively late usage events. | Passes when the configured policy/calculation is applied exactly. |
| `MTR-026` | E | Reject oversized or invalid usage metadata without affecting other customers. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
## `tests/e2e/payments/payments_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `PAY-001` | E | Create a Razorpay order using the mock provider. | Passes when the resource is created successfully and the response/persistent state contains the new record. |
| `PAY-002` | E | Return checkout information to the requesting application. | Passes when the response matches the previously committed/original state. |
| `PAY-004` | E | Reject client-supplied price manipulation. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `PAY-005` | E | Verify a valid Razorpay webhook signature. | Passes when the stated behavior is observed without data loss or contract violation. |
| `PAY-006` | E | Reject an invalid webhook signature. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `PAY-007` | E | Reject malformed payment webhook payloads. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `PAY-008` | E | Confirm a successful payment and allocate purchased credits. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `PAY-009` | E | Prevent credit allocation after failed payment. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `PAY-011` | E | Handle out-of-order Razorpay events correctly. | Passes when the edge condition is handled without duplication, loss, or incorrect state. |
| `PAY-012` | E | Recover safely from payment-provider timeouts. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `PAY-013` | E | Reconcile a pending purchase using provider payment status. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `PAY-015` | E | Prevent one payment from funding multiple independent purchases. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `PAY-016` | E | Process full and partial refunds according to credit-reversal policy. | Passes when the worker/provider flow completes and records the intended final state. |
| `PAY-018` | E | Process recurring-subscription payment confirmation. | Passes when the worker/provider flow completes and records the intended final state. |
| `PAY-019` | E | Handle failed subscription renewal payments. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `PAY-020` | E | Preserve gateway transaction references for auditing. | Passes when existing valid state remains intact after the scenario. |
| `PAY-021` | E | Reject a signed payment event whose amount or currency differs from the order. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `PAY-022` | E | Prevent a payment for one billing account from crediting another account wallet. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `PAY-023` | E | Reconcile a capture received after checkout timed out and was retried. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `PAY-025` | E | Prevent a delayed capture from reversing an already processed refund. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `PAY-026` | E | Handle provider rate limits and outages without duplicating payment orders. | Passes when the edge condition is handled without duplication, loss, or incorrect state. |
| `PAY-027` | E | Reject a signed webhook for an unknown payment operation. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
## `tests/e2e/plans/plans_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `PLAN-001` | E | Create a product with valid configuration. | Passes when the resource is created successfully and the response/persistent state contains the new record. |
| `PLAN-002` | E | Create a free subscription plan. | Passes when the resource is created successfully and the response/persistent state contains the new record. |
| `PLAN-003` | E | Create monthly and annual paid plans. | Passes when the resource is created successfully and the response/persistent state contains the new record. |
| `PLAN-004` | E | Retrieve publicly available plans. | Passes when the requested authorized data is returned successfully. |
| `PLAN-007` | E | Configure included workflow credits for a plan. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `PLAN-008` | E | Configure feature entitlements and usage limits. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `PLAN-009` | E | Prevent customers from modifying plan configuration. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `PLAN-011` | E | Prevent new subscriptions to inactive plans. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `PLAN-012` | E | Configure purchasable credit packs independently of subscription plans. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
## `tests/e2e/realtime/realtime_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `LIVE-001` | E | Establish an authenticated SSE connection. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `LIVE-002` | E | Reject unauthenticated SSE connections. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `LIVE-003` | E | Publish balance updates after committed credit transactions. | Passes when the expected notification/event is emitted once to the authorized target. |
| `LIVE-004` | E | Publish reservation and settlement updates. | Passes when the expected notification/event is emitted once to the authorized target. |
| `LIVE-005` | E | Publish subscription status changes. | Passes when the expected notification/event is emitted once to the authorized target. |
| `LIVE-006` | E | Deliver only events authorized for the connected organization. | Passes when the expected notification/event is emitted once to the authorized target. |
| `LIVE-007` | E | Resume an SSE stream using the last received event ID. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `LIVE-008` | E | Deliver missed events after reconnection. | Passes when the expected notification/event is emitted once to the authorized target. |
| `LIVE-009` | E | Prevent duplicate logical events during replay. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `LIVE-010` | E | Handle disconnected and slow clients without blocking billing operations. | Passes when the edge condition is handled without duplication, loss, or incorrect state. |
| `LIVE-012` | E | Verify usage and balance APIs remain available when SSE is disconnected. | Passes when the stated behavior is observed without data loss or contract violation. |
| `LIVE-013` | E | Reject SSE replay using an event ID belonging to another billing account. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `LIVE-014` | E | Enforce session expiration for an already connected SSE client. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `LIVE-015` | E | Return the resynchronization response when an SSE event is no longer retained. | Passes when the response matches the previously committed/original state. |
| `LIVE-016` | E | Preserve SSE heartbeat and reconnection behavior through the reverse proxy. | Passes when existing valid state remains intact after the scenario. |
## `tests/e2e/reliability/reliability_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TEST-002` | E | Verify E2E tests use the actual API and worker rather than mocked application logic. | Passes when the stated behavior is observed without data loss or contract violation. |
| `TEST-004` | E | Prevent fixture and database state from leaking between independent scenarios. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
## `tests/e2e/reservations/reservations_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `RES-001` | E | Reserve credits when sufficient balance exists. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `RES-002` | E | Reject reservations exceeding available credits. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `RES-006` | E | Settle a reservation using actual execution consumption. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `RES-007` | E | Release unused credits after settlement. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `RES-008` | E | Release the full reservation after eligible execution cancellation. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `RES-011` | E | Reject settlement of an unknown reservation. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `RES-012` | E | Reject settlement belonging to another organization. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `RES-013` | E | Reject invalid reservation state transitions. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `RES-016` | E | Extend a reservation for a long-running workflow. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `RES-017` | E | Reject reservation extension when credits are insufficient. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `RES-024` | E | Reject settlement above the reserved amount unless an authorized extension succeeds. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
## `tests/e2e/subscriptions/subscriptions_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `SUB-001` | E | Create a free subscription without payment. | Passes when the resource is created successfully and the response/persistent state contains the new record. |
| `SUB-002` | E | Create a paid subscription with a pending payment. | Passes when the resource is created successfully and the response/persistent state contains the new record. |
| `SUB-003` | E | Activate a subscription after verified payment. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `SUB-004` | E | Reject duplicate active subscriptions where the product policy prohibits them. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `SUB-005` | E | Retrieve the current subscription for an organization. | Passes when the requested authorized data is returned successfully. |
| `SUB-006` | E | Upgrade a subscription to another plan. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `SUB-007` | E | Downgrade a subscription according to the configured effective-date policy. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `SUB-010` | E | Cancel a subscription immediately when requested. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `SUB-011` | E | Schedule cancellation at the billing-period end. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `SUB-012` | E | Reactivate an eligible cancelled subscription. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `SUB-013` | E | Renew an existing subscription after successful payment. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `SUB-014` | E | Apply configured grace-period behavior after failed payment. | Passes when the configured policy/calculation is applied exactly. |
| `SUB-015` | E | Suspend paid entitlements when the grace period expires. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `SUB-018` | E | Preserve subscription history after cancellation. | Passes when existing valid state remains intact after the scenario. |
| `SUB-019` | E | Allow independent Daybook and Taskmesh subscriptions. | Passes when the authorized operation succeeds and persists the intended state. |
| `SUB-020` | E | Ensure cancellation of Taskmesh does not cancel Daybook. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `SUB-022` | E | Preserve entitlement state when a subscription upgrade payment fails. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `SUB-023` | E | Prevent a late renewal notification from reactivating a cancelled subscription. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `SUB-027` | E | Preserve included and purchased credit treatment after a plan change. | Passes when existing valid state remains intact after the scenario. |
| `SUB-028` | E | Resolve out-of-order cancellation, payment, and renewal events correctly. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
## `tests/e2e/wallets/wallets_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `WAL-001` | E | Create a wallet for a billing account and product. | Passes when the resource is created successfully and the response/persistent state contains the new record. |
| `WAL-002` | E | Retrieve the current wallet balance. | Passes when the requested authorized data is returned successfully. |
| `WAL-003` | E | Allocate included subscription credits. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `WAL-004` | E | Allocate purchased credits after verified payment. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `WAL-005` | E | Apply an authorized promotional or administrative credit grant. | Passes when the configured policy/calculation is applied exactly. |
| `WAL-014` | E | Maintain separate Daybook and Taskmesh wallets. | Passes when existing valid state remains intact after the scenario. |
| `WAL-015` | E | Prevent cross-product credit consumption without explicit authorization. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `WAL-017` | E | Retrieve complete wallet transaction history. | Passes when the requested authorized data is returned successfully. |
## `tests/e2e/webhooks/webhooks_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `WH-001` | E | Register an application webhook endpoint. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `WH-002` | E | Reject unauthorized webhook configuration changes. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `WH-003` | E | Deliver a subscription activation event. | Passes when the expected notification/event is emitted once to the authorized target. |
| `WH-004` | E | Deliver a subscription cancellation event. | Passes when the expected notification/event is emitted once to the authorized target. |
| `WH-005` | E | Deliver wallet credit and debit events. | Passes when the expected notification/event is emitted once to the authorized target. |
| `WH-006` | E | Deliver a low-balance notification. | Passes when the expected notification/event is emitted once to the authorized target. |
| `WH-007` | E | Deliver an exhausted-credit notification. | Passes when the expected notification/event is emitted once to the authorized target. |
| `WH-008` | E | Deliver a usage-limit-exceeded event. | Passes when the expected notification/event is emitted once to the authorized target. |
| `WH-009` | E | Deliver payment success and failure notifications. | Passes when the expected notification/event is emitted once to the authorized target. |
| `WH-010` | E | Sign outgoing webhooks using HMAC. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `WH-011` | E | Retry webhook delivery after an HTTP 500 response. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `WH-012` | E | Handle webhook receiver timeouts. | Passes when the edge condition is handled without duplication, loss, or incorrect state. |
| `WH-015` | E | Record delivery attempts and final status. | Passes when the event/action is persisted in the expected table or audit trail. |
| `WH-016` | E | Replay a failed webhook without creating duplicate billing transactions. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `WH-017` | E | Stop automatic retries according to the configured retry policy. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `WH-018` | E | Resume pending deliveries after worker restart. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `WH-020` | E | Reject webhook destinations using private, loopback, or metadata-service addresses. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `WH-021` | E | Accept active webhook signing secrets during rotation and reject retired secrets. | Passes when the valid request/token/event is accepted and returns a successful result. |
| `WH-022` | E | Prevent a webhook endpoint from receiving another tenant billing events. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `WH-023` | E | Honor webhook retry policy when a destination returns HTTP 429. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `WH-024` | E | Suspend delivery to a permanently disabled webhook endpoint according to policy. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
## `tests/e2e/worker/worker_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `WRK-001` | E | Process pending outbox events. | Passes when the worker/provider flow completes and records the intended final state. |
| `WRK-004` | E | Retry failed jobs with the configured backoff policy. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `WRK-005` | E | Stop retrying permanently invalid jobs. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
| `WRK-006` | E | Process credit expiration jobs correctly. | Passes when the worker/provider flow completes and records the intended final state. |
| `WRK-007` | E | Process subscription renewal jobs correctly. | Passes when the worker/provider flow completes and records the intended final state. |
| `WRK-010` | E | Recover pending jobs when PostgreSQL becomes available again. | Passes when the live API/worker scenario returns the expected HTTP result and state. |
## `tests/integration/accounts/accounts_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `ACC-004` | I | Prevent duplicate billing accounts for the same external account reference. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `ACC-010` | I | Keep identical external organization IDs separate across applications. | Passes when existing valid state remains intact after the scenario. |
## `tests/integration/accounts/review_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestACC010SameOrganizationIDRemainsDistinctAcrossApplications` | direct | ACC010 Same Organization IDRemains Distinct Across Applications. | Passes when the assertions encoded in this test function hold for the scenario. |
## `tests/integration/admin/admin_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `ADM-005` | I | Record every manual adjustment in the ledger. | Passes when the event/action is persisted in the expected table or audit trail. |
| `ADM-009` | I | Record actor, timestamp, action and affected resource for audited changes. | Passes when the event/action is persisted in the expected table or audit trail. |
## `tests/integration/foundation/foundation_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `FND-005` | I | Verify readiness fails when PostgreSQL is unavailable. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `FND-006` | I | Verify database migrations run successfully on an empty database. | Passes when the stated behavior is observed without data loss or contract violation. |
| `FND-007` | I | Verify repeated migration execution does not corrupt the schema. | Passes when the stated behavior is observed without data loss or contract violation. |
| `FND-008` | I | Verify failed migrations roll back safely. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `FND-011` | I | Preserve subscriptions, ledger entries, and pending reservations through backup and restore. | Passes when existing valid state remains intact after the scenario. |
| `FND-012` | I | Keep schema migrations compatible with the supported deployment sequence. | Passes when existing valid state remains intact after the scenario. |
## `tests/integration/foundation/recovery_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestFND011RestoredDatabasePreservesFinancialState` | direct | FND011 Restored Database Preserves Financial State. | Passes when the assertions encoded in this test function hold for the scenario. |
## `tests/integration/invoices/invoices_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `INV-005` | I | Prevent duplicate invoice generation for the same billing operation. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `INV-008` | I | Preserve finalized invoice history against unauthorized modifications. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `INV-009` | I | Reconcile invoice totals with subscription, credit purchase, and payment records. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
## `tests/integration/limits/limits_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `LIM-006` | I | Prevent repeated notifications for the same threshold crossing. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `LIM-011` | I | Re-arm a low-balance warning after top-up and emit it once on a new crossing. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
## `tests/integration/metering/metering_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `MTR-008` | I | Deduplicate usage events by event ID. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `MTR-009` | I | Prevent double charging after usage-event retries. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `MTR-011` | I | Persist valid usage events durably before acknowledgement. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `MTR-014` | I | Aggregate usage correctly across billing periods. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `MTR-017` | I | Reconcile recorded consumption against wallet settlements. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `MTR-018` | I | Reject an existing event ID submitted again with different usage data. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `MTR-020` | I | Attribute delayed usage to the correct period without changing a finalized period. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `MTR-022` | I | Scope usage-event deduplication across products, tenants, and environments. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `MTR-025` | I | Handle out-of-order incremental usage updates without lost or duplicate consumption. | Passes when the edge condition is handled without duplication, loss, or incorrect state. |
## `tests/integration/payments/payments_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `PAY-010` | I | Prevent duplicate credits from repeated payment webhooks. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `PAY-014` | I | Link payment, purchase, wallet grant and ledger transaction. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `PAY-017` | I | Prevent duplicate refund processing. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `PAY-024` | I | Prevent concurrent checkout requests from creating duplicate credit grants. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
## `tests/integration/plans/plans_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `PLAN-010` | I | Preserve historical subscription prices after plan changes. | Passes when existing valid state remains intact after the scenario. |
## `tests/integration/realtime/realtime_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `LIVE-011` | I | Verify SSE replay retrieves committed events from PostgreSQL. | Passes when the stated behavior is observed without data loss or contract violation. |
## `tests/integration/reliability/reliability_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestTEST003RequiredPostgresSuiteCannotSilentlySkip` | direct | TEST003 Required Postgres Suite Cannot Silently Skip. | Passes when required PostgreSQL integration tests cannot be silently skipped. |
## `tests/integration/reservations/reservations_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `RES-003` | I | Prevent simultaneous requests from reserving the same credits. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `RES-004` | I | Verify PostgreSQL row-level locking during reservation. | Passes when the stated behavior is observed without data loss or contract violation. |
| `RES-005` | I | Roll back the complete reservation when any database operation fails. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `RES-009` | I | Prevent duplicate settlement of the same reservation. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `RES-010` | I | Prevent duplicate release of the same reservation. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `RES-014` | I | Ensure settlement and ledger updates commit atomically. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `RES-015` | I | Preserve grant-allocation information during reservation. | Passes when existing valid state remains intact after the scenario. |
| `RES-018` | I | Recover expired or abandoned reservations safely. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `RES-019` | I | Prevent negative spendable balances under concurrent execution. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `RES-020` | I | Preserve balances after a database transaction rollback. | Passes when existing valid state remains intact after the scenario. |
| `RES-021` | I | Reject reuse of an idempotency key with a different payload. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `RES-022` | I | Return the original reservation for an identical idempotent retry without reserving credits twice. | Passes when the response matches the previously committed/original state. |
| `RES-023` | I | Prevent concurrent settlement and release from producing contradictory ledger movements. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `RES-025` | I | Avoid a second reservation when a successful commit is retried after a lost response. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `RES-026` | I | Preserve spendable balance during simultaneous top-up, expiration, and reservation operations. | Passes when existing valid state remains intact after the scenario. |
| `RES-027` | I | Apply the configured policy when credits expire while an active reservation uses them. | Passes when the configured policy/calculation is applied exactly. |
## `tests/integration/reservations/review_postgres_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestFND012MigrationVersionTableIsPresentAndReadable` | direct | FND012 Migration Version Table Is Present And Readable. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestINV009InvoiceTotalsReconcileWithPayment` | direct | INV009 Invoice Totals Reconcile With Payment. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestLIM011LowBalanceWarningRearmsAfterTopUp` | direct | LIM011 Low Balance Warning Rearms After Top Up. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestMTR018ConflictingDuplicateUsageEventIsRejected` | direct | MTR018 Conflicting Duplicate Usage Event Is Rejected. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestMTR020DelayedUsageKeepsItsOriginalOccurrencePeriod` | direct | MTR020 Delayed Usage Keeps Its Original Occurrence Period. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestMTR022UsageDeduplicationIsScopedByTenantAndProduct` | direct | MTR022 Usage Deduplication Is Scoped By Tenant And Product. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestMTR025OutOfOrderUsageUpdatesDoNotLoseOrDuplicateUnits` | direct | MTR025 Out Of Order Usage Updates Do Not Lose Or Duplicate Units. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestPAY024ConcurrentCheckoutIdempotencyCreatesOnePayment` | direct | PAY024 Concurrent Checkout Idempotency Creates One Payment. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestRES022IdenticalReservationRetryReturnsOriginal` | direct | RES022 Identical Reservation Retry Returns Original. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestRES023ConcurrentSettlementAndReleaseHaveOneOutcome` | direct | RES023 Concurrent Settlement And Release Have One Outcome. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestRES025RetryAfterLostResponseDoesNotReserveTwice` | direct | RES025 Retry After Lost Response Does Not Reserve Twice. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestRES026ConcurrentTopUpExpirationAndReservationReconcile` | direct | RES026 Concurrent Top Up Expiration And Reservation Reconcile. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestRES027ReservedCreditsRemainSettleableAfterGrantExpiry` | direct | RES027 Reserved Credits Remain Settleable After Grant Expiry. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestSUB021CompetingLifecycleUpdatesUseOneSubscriptionVersion` | direct | SUB021 Competing Lifecycle Updates Use One Subscription Version. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestSUB024RetriedRenewalAllocatesIncludedCreditsOnce` | direct | SUB024 Retried Renewal Allocates Included Credits Once. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestSUB026RetriedPlanChangeCreatesOneHistoryRecord` | direct | SUB026 Retried Plan Change Creates One History Record. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestWAL019ReservationSpansMultipleGrants` | direct | WAL019 Reservation Spans Multiple Grants. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestWAL020ConsumedPurchaseRefundCannotCreateNegativeBalance` | direct | WAL020 Consumed Purchase Refund Cannot Create Negative Balance. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestWAL021PersistedWalletReconcilesWithLedgerAndReservations` | direct | WAL021 Persisted Wallet Reconciles With Ledger And Reservations. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestWAL022RejectsOverflowingCreditQuantity` | direct | WAL022 Rejects Overflowing Credit Quantity. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestWAL023ConcurrentTopUpAndAllowanceResetPreserveBothGrants` | direct | WAL023 Concurrent Top Up And Allowance Reset Preserve Both Grants. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestWH019RolledBackTransactionProducesNoWebhookEvent` | direct | WH019 Rolled Back Transaction Produces No Webhook Event. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestWRK011RecurringGrantCanOnlyBeClaimedOnce` | direct | WRK011 Recurring Grant Can Only Be Claimed Once. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestWRK012CommittedFinancialJobRemainsRecoverableBeforeAck` | direct | WRK012 Committed Financial Job Remains Recoverable Before Ack. | Passes when the assertions encoded in this test function hold for the scenario. |
## `tests/integration/reservations/wallets_postgres_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestConcurrentReservationsCannotOverspend` | direct | Concurrent Reservations Cannot Overspend. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestSettlementIsIdempotent` | direct | Settlement Is Idempotent. | Passes when the assertions encoded in this test function hold for the scenario. |
## `tests/integration/subscriptions/subscriptions_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `SUB-016` | I | Prevent duplicate renewals for the same billing period. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `SUB-017` | I | Prevent duplicate monthly credit allocation during renewal. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `SUB-021` | I | Resolve simultaneous cancellation and renewal without contradictory subscription states. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `SUB-024` | I | Allocate included credits exactly once when renewal confirmation is retried. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `SUB-026` | I | Prevent duplicate plan changes when a request is retried. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
## `tests/integration/wallets/wallets_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `WAL-006` | I | Prevent duplicate grants using idempotency keys. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `WAL-007` | I | Reject invalid or negative grant amounts. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `WAL-010` | I | Expire unused monthly credits correctly. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `WAL-011` | I | Preserve purchased credits during monthly allowance reset. | Passes when existing valid state remains intact after the scenario. |
| `WAL-012` | I | Expire purchased credits according to their validity. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `WAL-013` | I | Prevent expired credits from funding reservations. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `WAL-016` | I | Verify wallet totals match ledger and active reservation records. | Passes when the stated behavior is observed without data loss or contract violation. |
| `WAL-018` | I | Prevent unauthorized modification or deletion of ledger records. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `WAL-019` | I | Reserve credits across multiple grants when one grant cannot cover the requested amount. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `WAL-020` | I | Refund consumed purchased credits without silently creating an invalid negative balance. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `WAL-021` | I | Recalculate wallet balances from grants, ledger entries, and reservations and detect inconsistencies. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `WAL-023` | I | Preserve purchased credits during a concurrent top-up and allowance reset. | Passes when existing valid state remains intact after the scenario. |
| `WAL-024` | I | Apply deterministic allocation order when grants expire at the same time. | Passes when the configured policy/calculation is applied exactly. |
## `tests/integration/webhooks/webhooks_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `WH-013` | I | Persist pending webhook events before delivery. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `WH-014` | I | Prevent duplicate logical events for the same threshold crossing. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `WH-019` | I | Do not create a deliverable webhook for a rolled-back billing transaction. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
## `tests/integration/worker/worker_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `WRK-002` | I | Prevent two workers from claiming the same job concurrently. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `WRK-003` | I | Release or recover jobs abandoned by crashed workers. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
| `WRK-008` | I | Prevent duplicate processing after worker restart. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `WRK-009` | I | Preserve committed jobs during API shutdown. | Passes when existing valid state remains intact after the scenario. |
| `WRK-011` | I | Prevent multiple workers from allocating the same recurring grant. | Passes when the operation is refused with the expected client/error response and no invalid state change is committed. |
| `WRK-012` | I | Recover after commit when a worker crashes before acknowledging its job. | Passes when the PostgreSQL-backed contract/constraint is present and behaves as required. |
## `tests/unit/entitlements/evaluate_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestENT002BooleanFeatures` | direct | ENT002 Boolean Features. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestENT003NumericLimits` | direct | ENT003 Numeric Limits. | Passes when the assertions encoded in this test function hold for the scenario. |
## `tests/unit/foundation/config_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestFND001RequiredEnvironment` | direct | FND001 Required Environment. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestFND002InvalidConfiguration` | direct | FND002 Invalid Configuration. | Passes when the assertions encoded in this test function hold for the scenario. |
## `tests/unit/invoices/calculation_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestINV003InvoiceTotals` | direct | INV003 Invoice Totals. | Passes when the assertions encoded in this test function hold for the scenario. |
## `tests/unit/invoices/review_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestINV010CurrencyMinorUnitCalculationsRemainExact` | direct | INV010 Currency Minor Unit Calculations Remain Exact. | Passes when the assertions encoded in this test function hold for the scenario. |
## `tests/unit/limits/threshold_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestLIM001UsagePercentage` | direct | LIM001 Usage Percentage. | Passes when the assertions encoded in this test function hold for the scenario. |
## `tests/unit/metering/pricing_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestMTR004FixedPointCredits` | direct | MTR004 Fixed Point Credits. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestMTR005ConfiguredMeterPrice` | direct | MTR005 Configured Meter Price. | Passes when the assertions encoded in this test function hold for the scenario. |
## `tests/unit/payments/catalog_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestPAY003ResolveServerSideCreditPack` | direct | PAY003 Resolve Server Side Credit Pack. | Passes when the assertions encoded in this test function hold for the scenario. |
## `tests/unit/plans/rules_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestPLAN005ValidatePricesAndCurrencies` | direct | PLAN005 Validate Prices And Currencies. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestPLAN006RejectNegativeValues` | direct | PLAN006 Reject Negative Values. | Passes when the assertions encoded in this test function hold for the scenario. |
## `tests/unit/subscriptions/period_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestSUB008BillingPeriod` | direct | SUB008 Billing Period. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestSUB009MonthlyAndAnnualRenewals` | direct | SUB009 Monthly And Annual Renewals. | Passes when the assertions encoded in this test function hold for the scenario. |
## `tests/unit/subscriptions/review_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestSUB025RenewalBoundariesAreTimezoneIndependent` | direct | SUB025 Renewal Boundaries Are Timezone Independent. | Passes when the assertions encoded in this test function hold for the scenario. |
## `tests/unit/testplan/coverage_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestEveryChecklistCaseIsDiscovered` | direct | Every Checklist Case Is Discovered. | Passes when every checklist prefix has discoverable cases in test-plan.md. |
## `tests/unit/testplan/review_implementation_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestTEST001EveryReviewCaseHasBehavioralImplementation` | direct | TEST001 Every Review Case Has Behavioral Implementation. | Passes when every review-added checklist case has an executable implementation mapping. |
## `tests/unit/wallets/priority_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestWAL008IncludedBeforePurchased` | direct | WAL008 Included Before Purchased. | Passes when the assertions encoded in this test function hold for the scenario. |
| `TestWAL009ExpirationPriority` | direct | WAL009 Expiration Priority. | Passes when the assertions encoded in this test function hold for the scenario. |
## `tests/unit/wallets/review_test.go`

| Test case | Type | One-line description | Expected output / pass condition |
| --- | --- | --- | --- |
| `TestWAL024EqualExpiryUsesDeterministicCreationOrder` | direct | WAL024 Equal Expiry Uses Deterministic Creation Order. | Passes when the assertions encoded in this test function hold for the scenario. |
