# Billmesh Authentication, Authorization, and Security Test Gaps

Review date: 2026-09-28

This report compares [test-case-removal-list.md](test-case-removal-list.md), the restored [test-plan.md](test-plan.md), and the current test helpers and implementation. It records missing scenarios and scenarios whose existing tests do not establish the behavior described by their names.

This report began as a static review. The checked rows have since been implemented or strengthened and exercised by the unit, integration, or E2E suites. An unchecked row remains open if any material part of its stated scenario is untested. Absence of a test does not by itself establish absence of a security control.

The earlier test-discovery issue is resolved: `test-plan.md` is present, and the recovered executable cases are listed there. The tests in this report live in the existing suites and the new security suites; this report does not itself register tests.

## Priorities and conventions

- **P1 — High:** Address first because the scenario protects authorization boundaries, tenant data, financial state, or external requests.
- **P2 — Medium:** Add for defensive coverage, failure handling, operational assurance, and policy edge cases.
- **U:** Unit tests with controlled dependencies and clocks.
- **I:** Integration tests using real PostgreSQL.
- **E:** E2E tests through the actual API/worker with mocked external systems.
- **P:** Performance or abuse-resistance tests.
- **D:** Deployment/configuration checks; this is a report-specific category.

The `GAP-*` identifiers below are proposed tracking identifiers, not cases already implemented or loaded by `testkit.RunCases`. Strengthen an existing case when it describes the intended behavior; add a separate case when a distinct scenario or assertion is needed.

Status markers: `[X]` means executable coverage has been added for the full row. `[ ]` means the row is still open or only partially covered.

Verification on 2026-09-29: 44 of 45 tracking rows are checked; 1 remains open. The race-enabled unit suite, all PostgreSQL integration packages, and the full E2E suite passed. The foundation backup/restore integration case ran first against fresh source and restore databases; the remaining integration cases then ran against the source database. Checked pre-existing cases were inspected for their actual assertions and exercised by their suites. The remaining open row covers production deployment checks. All 47 registered API routes now have a successful E2E request; protected routes also have 401/403 gate checks and ownership-denial cases. Authenticated denied admin actions now produce tenant-scoped audit records, and adjustment audit failure rolls back financial changes. The API and worker now use a DML-only role in the test stack, and a real adjustment, invoice, and audit were verified against it. The permission snapshot policy allows an issued token's claims until expiry; the verifier does not perform live IdNest revocation lookups. Browser access is same-origin with bearer tokens; there is no cookie-backed session or cross-origin allowlist. For `GAP-AUTHZ-001`, the route matrix checks missing/invalid tokens and missing permissions for every protected route; the E2E route-coverage gate verifies a successful request for all 47 registered routes, and ownership-denial tests cover protected resources. For `GAP-SEC-007`, tests cover the 1 MiB usage-body limit, 1000-event batch limit, eight concurrent SSE streams per account with capacity release and tenant separation, slow HTTP header/body timeouts, authenticated mutation floods with tenant isolation, and authentication-failure floods while valid tokens remain usable. The limits are enforced by the application and HTTP server, independent of the public proxy. For `GAP-SEC-010`, tests verify missing OIDC settings stop API startup before database access, the API excludes fake issuer routes, and the test stack uses a restricted runtime database role. Live public TLS, private origin exposure, and production database grants still need verification against the deployment actually used.

## 1. Existing cases that need stronger behavioral coverage

| Status | Existing case | Current limitation | Required improvement | Priority |
| --- | --- | --- | --- | --- |
| [X] | `TestJWKSVerifierRejectsWrongAudience` | Passes `not-a-token`, so it establishes malformed-token rejection rather than audience validation. | Use a correctly signed, otherwise valid JWT with the wrong audience. Keep a separate malformed-token case. `AUTH-006` already exercises wrong-audience rejection through the API. | P2 |
| [X] | `AUTH-004` | Uses a token with an unknown signing-key ID. | Also test a recognized key ID with an invalid signature, including payload/permission tampering after signing. | P1 |
| [X] | `AUTH-009` | Falls through to the ordinary token fixture and product-list request. | Establish a distinct service principal and verify the actual service-token contract and allowed operations. | P1 |
| [X] | `AUTH-010`, `AUTH-013` | Use a read-only token against `/v1/admin/audit`. | Cover the applicable service restrictions and each privileged endpoint, using otherwise valid requests. | P1 |
| [X] | `AUTH-014` | Falls through to a product-list request using all permissions. | Use only viewer permissions; read billing resources successfully and attempt each prohibited mutation. | P1 |
| [X] | `AUTH-015` | Checks denial on `/v1/admin/audit`. | Attempt actual price, plan, and subscription changes with runtime permissions. | P1 |
| [X] | `AUTH-016` | Checks one staging-to-production account read. | Exercise tenant and environment isolation across resource reads, lists, mutations, events, and administrative actions. | P1 |
| [X] | `AUTH-019` | Checks another tenant's wallet-reservation endpoint. | Substitute subscription, reservation, installation, account, and webhook-delivery identifiers as well. | P1 |
| [X] | `PAY-005`–`PAY-007` | Route through the generic payment-order helper rather than directly exercising the payment webhook. | Send valid, invalid-signature, and malformed-body requests to `/v1/payments/webhook`; assert financial state and provider-event persistence. | P1 |
| [X] | `WH-020` | Tests three literal addresses: loopback IPv4, loopback IPv6, and a metadata address. | Add private address ranges, hostname resolution, redirects, DNS rebinding, and validation at delivery time. | P1 |
| [X] | `WH-021` | Checks registration with current/previous secret fields. | Trigger actual deliveries and verify the documented rotation behavior, signatures, and retired-secret rejection at the appropriate verifier. | P1 |
| [X] | `WH-022` | Checks whether another tenant can see a registered endpoint in a list. | Generate events for two tenants and inspect actual deliveries, payloads, and signing-secret selection. | P1 |
| [X] | `LIVE-006` | The generic SSE helper checks connection status/content type, not emitted event payloads. | Generate events for two tenants, consume the stream, and assert that only authorized events arrive. | P1 |
| [X] | `LIVE-013` | Uses a large synthetic cursor and expects a successful connection; it does not create another tenant's event. | Use an actual foreign event ID and assert the specified rejection behavior and absence of foreign event data. | P1 |
| [X] | `LIVE-014` | Connects with an already expired token; a transport error can also cause the test to return without failure. | Connect with a valid short-lived token, cross its expiry while connected, and verify the defined termination/re-authentication behavior. Unexpected transport errors must fail the test. | P1 |
| [X] | `WAL-018`, `INV-008`, `ADM-009` | The shared database helper checks table existence, not immutable financial records or complete audit entries. | Attempt prohibited updates/deletes using the intended database role and inspect real audit records for representative operations. | P1 |

Evidence: [JWT unit tests](internal/auth/verifier_test.go), [authentication and generic HTTP helpers](tests/testkit/http.go), [payment test entry point](tests/e2e/payments/payments_test.go), [webhook and SSE review helpers](tests/testkit/review.go), and [database test helper](tests/testkit/database.go).

The generic HTTP helper can accept a client error caused by an empty or invalid body. A security test must use a request that would otherwise succeed, so validation failure cannot substitute for an authorization check.

## 2. Missing authentication scenarios

| Status | ID | Scenario and test inputs | Expected outcome | Priority | Level |
| --- | --- | --- | --- | --- | --- |
| [X] | GAP-AUTH-001 | Unsupported JWT algorithms: unsigned tokens, `alg=none`, HS256 substitution, and other unsupported algorithms. | Reject with `401`; do not execute the protected handler or cause side effects. | P1 | U, E |
| [X] | GAP-AUTH-002 | Missing/malformed claims: `exp`, `iss`, `aud`, and identity/context claims required by the token contract; wrong JSON types for permissions and claims. | Enforce required claims and types. Missing identity/context must not accidentally grant broader access. Cover valid multi-audience tokens if the issuer supports them. | P1 | U, E |
| [X] | GAP-AUTH-003 | Time boundaries: future `nbf`, exact expiry, clock-skew boundaries, and future `iat` if restricted by policy. | Accept/reject deterministically according to the documented time policy, using a controlled clock where possible. | P2 | U, E |
| [X] | GAP-AUTH-004 | Empty bearer values, malformed JWT segments/encoding, ambiguous duplicate authorization headers, unsupported schemes, and oversized tokens. | Reject safely without panic or ambiguous credential selection. Oversized requests may be rejected by the HTTP server/proxy according to its configured limit. | P2 | U, E, D |
| [X] | GAP-AUTH-005 | An ID token without `token_use`, or another token type whose claims resemble an access token. | Enforce the actual IdNest access-token contract; test issuer-defined type/audience distinctions rather than relying only on a fixture-specific marker. | P1 | U, E |
| [X] | GAP-AUTH-006 | JWKS timeout, unavailable endpoint, malformed/empty keyset, missing/unknown `kid`, and unusable key material. | Never accept a token that cannot be verified. Define bounded cached-key behavior during an outage and verify recovery after the endpoint returns. | P1 | U, E |
| [X] | GAP-AUTH-007 | Warm the key cache, retire/remove an old signing key, then continue presenting tokens signed by that key. | Reject retired keys within the defined retirement window. Also verify the permitted overlap period and successful use of the replacement key. | P1 | U, E |
| [X] | GAP-AUTH-008 | Disable a user/service, remove organization membership, or reduce permissions while previously issued tokens remain unexpired. | Enforce the documented revocation/staleness window. If access remains valid until token expiry by design, test and document that limit explicitly. | P1 | E |
| [X] | GAP-AUTH-009 | Repeated or concurrent requests with random unknown key IDs. | Bound JWKS fetch frequency, concurrency, and resource use; preserve capacity for legitimate authentication. | P2 | U, P |

Evidence: [JWT verifier and middleware](internal/auth/verifier.go). The verifier now requires access-token use and periodically refreshes cached keys. The tests document an expiry-only permission snapshot policy and a bounded JWKS outage grace period. The exact external IdNest revocation contract still needs confirmation before deployment.

## 3. Missing authorization scenarios

| Status | ID | Scenario and test inputs | Expected outcome | Priority | Level |
| --- | --- | --- | --- | --- | --- |
| [X] | GAP-AUTHZ-001 | Build a method/path/permission matrix for every protected route, including authentication-only catalog routes and explicitly public routes. | Protected routes reject missing/invalid tokens with `401`. Permission-gated routes reject valid tokens lacking permission with `403`. Correct permission and ownership allow a valid operation. Public routes follow their explicit contract. | P1 | U, E |
| [X] | GAP-AUTHZ-002 | Valid mutation requests from viewers, runtime clients, service principals, and other defined roles; include accounts, plans, credit packs, subscriptions, grants, adjustments, installations, and webhook management. | No role can acquire privileges merely by calling a different endpoint. Unknown/empty permissions do not grant access. Positive controls prove the same request succeeds for the intended role. | P1 | E |
| [X] | GAP-AUTHZ-003 | Substitute another tenant's account, wallet, subscription, reservation, installation, and webhook-delivery IDs in paths and bodies. | Deny reads and writes according to the documented `403`/`404` policy. Test reservation settle/release/extend, subscription change/cancel/renew/reactivate, and installation revoke/settle-active. | P1 | E |
| [X] | GAP-AUTHZ-004 | Use tenant A's administrator token to adjust tenant B's wallet, inspect its audit data, or replay its deliveries. | Tenant-scoped administration remains tenant-scoped. If a global administrator exists, give it a separately explicit contract and tests. | P1 | E |
| [X] | GAP-AUTHZ-005 | Create/link accounts using another organization, application, or environment; also test omitted identity claims. | Possession of billing-write access alone cannot claim an unrelated identity or create an unauthorized bridge. Legitimate cross-application linking follows an explicit trusted workflow. | P1 | E |
| [X] | GAP-AUTHZ-006 | Use Daybook and Taskmesh identities against different products under the same billing account. | Enforce the documented product boundary for subscriptions, usage, entitlements, payments, installations, and events. Test explicitly shared access as a positive case. | P1 | E |
| [X] | GAP-AUTHZ-007 | Staging/production resources with identical organization identifiers; omitted environment claims; explicit cross-environment link creation. | Preserve environment isolation. Default-environment behavior must match the contract and must not unexpectedly expand access. | P1 | E |
| [X] | GAP-AUTHZ-008 | Two populated tenants; altered product/application filters, pagination parameters where supported, and actual foreign replay cursors. | Lists, errors, and event streams disclose no unauthorized tenant data. Inspect returned records and payloads, not just response codes. | P1 | E |
| [X] | GAP-AUTHZ-009 | Snapshot state before rejected financial, configuration, and lifecycle operations. | Rejection leaves balances, ledger, subscriptions, provider calls, and queued business events unchanged. Any expected security audit entry is allowed and asserted separately. | P1 | I, E |

Use the route definitions in [API registration](internal/app/api.go) as the starting point for the endpoint matrix. Relevant ownership logic also lives in [resource handlers](internal/app/resources.go), [installation handlers](internal/app/installations.go), and [payment/webhook handlers](internal/app/payments_webhooks.go).

## 4. Missing or incomplete security scenarios

| Status | ID | Scenario and test inputs | Expected outcome | Priority | Level |
| --- | --- | --- | --- | --- | --- |
| [X] | GAP-SEC-001 | Webhook hostnames resolving to private/link-local/loopback addresses, DNS rebinding, public-to-private redirects, and destination changes between registration and delivery. | Prevent requests to prohibited destinations at connection/delivery time, including redirect hops. Test actual network attempts in a controlled environment. Extends `WH-020`. | P1 | U, E |
| [X] | GAP-SEC-002 | Two tenants register the same webhook URL with different secrets; create events for both and retry deliveries. | Each delivery uses the secret and endpoint belonging to its tenant. URL equality must not select another tenant's secret. | P1 | I, E |
| [X] | GAP-SEC-003 | Incoming webhook with absent/wrong signature or modified raw body; outgoing webhook with current/retired secret configurations. | Verify signatures over the exact delivered bytes. Invalid incoming requests have no financial effects. Outgoing rotation follows the receiver contract, and missing configuration cannot silently weaken authentication. Extends `PAY-005`–`PAY-007`, `WH-010`, and `WH-021`. | P1 | U, E |
| [X] | GAP-SEC-004 | Replay a payment event with conflicting payload under the same event ID; replay references across accounts/environments; repeat concurrent deliveries. | No additional financial effects and no reassignment to another purchase/tenant. Assert ledger, grant, invoice, and payment state. Extends existing duplicate-payment coverage. | P1 | I, E |
| [X] | GAP-SEC-005 | Payment capture with absent/zero amount, absent currency, contradictory status, or mismatched order/payment references. | Apply the supported provider payload contract and verify the payment before granting credits. Optional fields require an explicit trusted verification path; omission must not silently bypass required checks. | P1 | U, E |
| [X] | GAP-SEC-006 | SQL-like input, unexpected writable fields, duplicate JSON keys, trailing JSON values, malformed identifiers, and unsupported content types. | Inputs are rejected or handled deterministically according to the API contract; no query injection, field-level privilege escalation, parser ambiguity, or panic. | P2 | U, E |
| [X] | GAP-SEC-007 | Flood expensive endpoints and unauthenticated requests; oversized batches, excessive SSE connections, and slow clients. | Enforce configured limits and timeouts while preserving legitimate traffic and other tenants' capacity. Include HTTP/proxy limits where controls live outside the application. | P2 | P, D |
| [X] | GAP-SEC-008 | Trigger authentication, validation, database, provider, and delivery errors; inspect responses, logs, and event payloads. | No exposure of bearer tokens, signing secrets, database credentials, or unrelated tenant data. Safe diagnostic identifiers remain available. | P1 | U, E |
| [X] | GAP-SEC-009 | Inspect privileged-operation audits, required denied-action audits, attempted audit/ledger modification, and failures while persisting an adjustment audit. | Record trustworthy actor/resource/action details; enforce tamper resistance. A financial adjustment and its required audit entry follow the documented atomicity guarantee. | P1 | I, E |
| [ ] | GAP-SEC-010 | Missing authentication configuration; production TLS/proxy settings; runtime database-role restrictions; deployment of test-only services/routes. | Fail safely without exposing protected routes. Enforce configured transport policy and least privilege. Production must not expose fake token issuance or key-rotation endpoints. | P1 | I, E, D |
| [X] | GAP-SEC-011 | Browser-origin access from allowed and disallowed origins; cookie-authenticated mutations only if cookies are supported. | CORS follows the browser-client contract. Add CSRF, session fixation, and cookie-attribute checks only when session/cookie authentication applies. | P2 | E, D |

`GAP-SEC-010` has deployment-independent tests for missing OIDC settings, fake API routes, and restricted database behavior in the local stack. The stack applies the reusable [runtime grants](deploy/runtime-grants.sql) with separate migration and runtime roles. Public TLS, origin network exposure, and the actual production database login are properties of the eventual deployment and cannot be established from an undeployed application. The row remains open for those checks without requiring a particular proxy or orchestrator.

## 5. Implementation observations that make particular tests urgent

These were the code observations at the start of the review. A checked marker here means the corresponding implementation was changed and verified; the description preserves the original finding for traceability.

1. [X] **Webhook replay ownership:** `replayWebhook` updates `webhook_deliveries` by delivery ID without a tenant ownership condition. The route requires `billing:admin`. Test tenant-scoped versus global administrator behavior explicitly (`GAP-AUTHZ-004`). See [payment/webhook handlers](internal/app/payments_webhooks.go).

2. [X] **Webhook signing-secret selection:** The worker selects an active endpoint secret using `target_url` and `LIMIT 1`, without matching the endpoint's tenant in that subquery. Shared destination URLs need a dedicated isolation test (`GAP-SEC-002`). See [worker](internal/app/worker.go).

3. [X] **Webhook SSRF validation:** Registration checks literal IP addresses with `net.ParseIP`. This does not establish protection for DNS resolution or redirects during delivery. The default worker HTTP client has no custom redirect restriction (`GAP-SEC-001`). See [registration](internal/app/payments_webhooks.go) and [worker](internal/app/worker.go).

4. [X] **Cached signing-key retirement:** A known cached key does not trigger JWKS refresh. Adding a replacement key is not enough to demonstrate retirement of an existing cached key (`GAP-AUTH-007`). See [verifier](internal/auth/verifier.go).

5. [X] **Token type and context defaults:** The verifier now requires `token_use=access`. Omitted environment claims use the documented `production` default, and explicit cross-environment linking has dedicated tests (`GAP-AUTH-005`, `GAP-AUTHZ-007`). See [verifier](internal/auth/verifier.go), [API](internal/app/api.go), and [resources](internal/app/resources.go).

6. [X] **Account linking:** `linkAccount` checks access to the source account and then accepts the submitted application, organization, and environment for the new link. Test authorization to establish the target relationship, not only access to the source account (`GAP-AUTHZ-005`). See [resources](internal/app/resources.go).

7. [X] **Long-lived SSE authentication:** Authentication occurs when the connection is opened; the streaming loop does not visibly re-check expiry or membership. Test the documented connected-session policy (`LIVE-014`, `GAP-AUTH-008`). See [SSE handler](internal/app/api.go).

8. [X] **Adjustment/audit atomicity:** `adminAdjustment` calls the wallet grant service before inserting its audit record. Inject audit persistence failure and inspect the resulting financial state (`GAP-SEC-009`). See [adjustment handler](internal/app/payments_webhooks.go).

9. [X] **Payment field omissions:** Capture validation compares amount only when nonzero and currency only when nonempty. Test supported provider payload variants and the required verification path for omitted fields (`GAP-SEC-005`). See [payment webhook handler](internal/app/payments_webhooks.go).

## 6. Test design and acceptance criteria

- For each denial case, create real owned and foreign resources and use an otherwise valid request. Include a positive control where useful.
- Assert the specific authentication/authorization result, not an arbitrary `4xx` produced by invalid JSON, missing fields, or nonexistent fixtures.
- Inspect both returned data and persistent effects. A denied request must not change financial state or enqueue a business event.
- For isolation, populate both tenants/products/environments with distinguishable records and inspect payload contents.
- For webhook tests, trigger the worker and inspect requests received by the controlled receiver, including raw bodies, signatures, tenant selection, and delivery attempts.
- For SSE tests, consume actual events and observe behavior across expiry/reconnection rather than checking only the handshake.
- For database security, exercise attempted operations with the production-equivalent runtime role; table/index existence alone is insufficient.
- For revocation, token type, global administration, cross-product access, and environment defaults, document the intended policy and test its allowed and denied paths.
- Fail tests on unexpected transport errors or missing fixtures; do not allow incidental failures to count as successful security rejection.
- Keep this report synchronized with implemented case IDs. A tracking row or implementation registry entry alone is not evidence that its behavior is tested.

## 7. Suggested implementation order

1. Repair the existing authentication, payment-webhook, SSE, and database security tests that currently overstate their coverage.
2. Add the endpoint permission matrix, cross-tenant administrative checks, account-link escalation cases, and no-side-effect assertions.
3. Add webhook SSRF, shared-URL secret isolation, real signature/rotation checks, and adversarial payment verification cases.
4. Add JWT claim/algorithm cases, JWKS failure/retirement handling, and the agreed revocation/session policy tests.
5. Complete abuse-resistance, parser robustness, deployment, and applicable browser-origin coverage.

Login, password reset, MFA, and identity-provider session security belong primarily to IdNest if Billmesh only consumes its tokens. Billmesh coverage should establish the resulting token-validation, principal, revocation, and authorization contracts; add identity-provider journey tests separately if that system is in scope.
