# Product and Plan Administration Implementation Checklist

This document is the implementation and test traceability plan for managing the
global Billmesh Product catalogue and its subscription Plans from Billmesh
Admin.

Checkbox meaning:

- `[X]` — implementation and automated test coverage have been written.
- `[ ]` — implementation or automated test coverage is still outstanding.

Execution constraint for this pass: test and lint commands are intentionally
not run. The final verification commands remain unchecked for the user to run
after implementation is complete.

## 1. Domain and API contract

- [X] Products are global catalogue resources, not tenant-owned resources.
- [X] Products support active and archived lifecycle states without deletion.
- [X] Product slugs are immutable and globally unique.
- [X] Plans remain scoped to exactly one Product.
- [X] Plans support active and inactive lifecycle states without deletion.
- [X] Plan slugs are immutable and unique within a Product.
- [X] Existing subscriptions snapshot price, currency, interval, included
      credits, and entitlements so catalogue edits cannot silently change an
      existing contract.
- [X] Admin Product and Plan mutations use optimistic concurrency.
- [X] Successful and denied Product/Plan mutations are audited.
- [X] Customer catalogue APIs expose only active Products and Plans.
- [X] Admin catalogue APIs expose active and archived/inactive records.

## 2. Database implementation

- [X] Add Product description, active state, updated timestamp, and version.
- [X] Add Plan created/updated timestamps and version.
- [X] Add included-credit and entitlement snapshots to subscriptions.
- [X] Backfill new subscription snapshot columns from current Plans.
- [X] Add indexes supporting Product/Plan status filtering and stable ordering.
- [X] Preserve all existing foreign keys and historical records on archive.
- [X] Provide a reversible down migration.

## 3. Backend Product APIs

- [X] `GET /v1/products` returns only active customer-visible Products.
- [X] `POST /v1/products` remains an admin-authorized compatibility endpoint.
- [X] `GET /v1/admin/products` lists active and archived Products with filters.
- [X] `POST /v1/admin/products` creates a Product.
- [X] `GET /v1/admin/products/{id}` returns Product detail.
- [X] `PATCH /v1/admin/products/{id}` updates allowed fields and lifecycle.
- [X] Product create/update/archive writes an audit event atomically.
- [X] Product responses contain `id`, `slug`, `name`, `description`, `active`,
      `version`, `created_at`, and `updated_at`.

## 4. Backend Plan APIs

- [X] `GET /v1/plans` returns only active Plans under active Products.
- [X] Existing `POST /v1/plans` and `PATCH /v1/plans/{id}` remain compatible.
- [X] `GET /v1/admin/products/{id}/plans` lists active and inactive Plans.
- [X] `POST /v1/admin/products/{id}/plans` creates a Plan for that Product.
- [X] `GET /v1/admin/plans/{id}` returns Plan detail.
- [X] `PATCH /v1/admin/plans/{id}` updates allowed fields and lifecycle.
- [X] Plan mutation validates price, currency, interval, credits, and
      entitlements.
- [X] Plan create/update/archive writes an audit event atomically.
- [X] Product mismatch and cross-Product Plan changes are rejected.
- [X] Inactive Products cannot receive active Plans or new subscriptions.

## 5. Subscription snapshot behavior

- [X] Subscription creation snapshots every commercial and entitlement field.
- [X] Plan change refreshes every subscription snapshot field atomically.
- [X] Entitlement reads use the subscription snapshot.
- [X] Renewals use the subscription included-credit snapshot.
- [X] Product/Plan archive blocks new subscriptions.
- [X] Existing subscriptions continue after Product/Plan archive.

## 6. OpenAPI contract

- [X] Document Product admin list/create/detail/update operations.
- [X] Document Plan admin list/create/detail/update operations.
- [X] Add Product status/filter/pagination parameters and schemas.
- [X] Add Product/Plan version fields and concurrency request fields.
- [X] Document active-only behavior of customer catalogue operations.
- [X] Document admin browser-cookie and CSRF applicability.

## 7. Product automated test cases

- [X] PRD-001 customer list returns active Products only.
- [X] PRD-002 admin list returns active and archived Products.
- [X] PRD-003 admin retrieves Product detail by ID.
- [X] PRD-004 administrator creates a valid Product.
- [X] PRD-005 Product name and description are trimmed and validated.
- [X] PRD-006 valid normalized Product slug is accepted.
- [X] PRD-007 invalid, unsafe, empty, or oversized Product slug is rejected.
- [X] PRD-008 Product slug uniqueness is enforced.
- [X] PRD-009 duplicate Product returns conflict without side effects.
- [X] PRD-010 administrator updates allowed Product fields.
- [X] PRD-011 Product slug cannot be changed.
- [X] PRD-012 administrator archives and reactivates Product.
- [X] PRD-013 repeated lifecycle request is idempotent.
- [X] PRD-014 unknown Product returns not found; malformed ID returns bad request.
- [X] PRD-015 Product list supports status, search, limit, and offset filters.
- [X] PRD-016 concurrent duplicate creation commits exactly one Product.
- [X] PRD-017 stale Product update is rejected.
- [X] PRD-018 Product archive preserves Plans and subscription history.
- [X] PRD-019 existing subscription continues after Product archive.
- [X] PRD-020 new subscription is rejected after Product archive.
- [X] PRD-021 successful Product mutation is audited.
- [X] PRD-022 failed Product mutation does not write a success audit.

## 8. Plan automated test cases

- [X] PLAN-013 admin lists active and inactive Plans for one Product.
- [X] PLAN-014 admin filters Plans by lifecycle state.
- [X] PLAN-015 admin retrieves Plan detail by ID.
- [X] PLAN-016 administrator creates free, monthly, and annual Plans.
- [X] PLAN-017 customer list hides inactive Plans and Plans of archived Products.
- [X] PLAN-018 invalid price, credits, currency, or interval is rejected.
- [X] PLAN-019 Plan slug uniqueness is enforced within Product.
- [X] PLAN-020 same Plan slug may exist under different Products.
- [X] PLAN-021 creation under missing or archived Product is rejected.
- [X] PLAN-022 Plan Product and slug cannot be changed.
- [X] PLAN-023 administrator archives and reactivates Plan.
- [X] PLAN-024 new subscription to inactive Plan is rejected.
- [X] PLAN-025 existing subscription continues after Plan archive.
- [X] PLAN-026 omitted PATCH fields remain unchanged.
- [X] PLAN-027 duplicate and unknown JSON fields are rejected.
- [X] PLAN-028 entitlement values accept supported JSON shapes.
- [X] PLAN-029 stale Plan update is rejected.
- [X] PLAN-030 Plan update preserves existing subscription snapshots.
- [X] PLAN-031 new subscribers receive the updated Plan values.
- [X] PLAN-032 Plan mutations are audited atomically.

## 9. Security automated test cases

- [X] CATSEC-001 missing or invalid authentication is rejected.
- [X] CATSEC-002 billing reader/writer cannot mutate Products or Plans.
- [X] CATSEC-003 runtime/service identities cannot mutate the catalogue.
- [X] CATSEC-004 valid admin without tenant context can manage the catalogue.
- [X] CATSEC-005 denied mutations have no business side effects.
- [X] CATSEC-006 denied catalogue mutations are audited.
- [X] CATSEC-007 Customer A cannot read or update Customer B's account.
- [X] CATSEC-008 Customer A cannot subscribe Customer B's account.
- [X] CATSEC-009 Customer A cannot change/cancel/renew Customer B's subscription.
- [X] CATSEC-010 Customer A cannot access Customer B's wallet or ledger.
- [X] CATSEC-011 application and environment boundaries remain isolated.
- [X] CATSEC-012 `billing:admin` alone does not bypass tenant subscription access.
- [X] CATSEC-013 cross-Product Plan assignment and plan changes are rejected.
- [X] CATSEC-014 admin BFF mutations require a valid CSRF token.
- [X] CATSEC-015 a CSRF token cannot be used with another session.
- [X] CATSEC-016 console/admin cookies and origins cannot be confused.
- [X] CATSEC-017 foreign origins and bearer/session ambiguity are rejected.
- [X] CATSEC-018 expired and logged-out admin sessions are rejected.
- [X] CATSEC-019 mass assignment, duplicate keys, and unsupported media fail.
- [X] CATSEC-020 stored markup is returned as data and rendered safely.
- [X] CATSEC-021 errors do not reveal SQL or foreign resource existence.
- [X] CATSEC-022 mutation rate limits do not permit partial writes.

## 10. Admin frontend implementation

- [X] Add typed Product and Plan API models.
- [X] Add API client PATCH support.
- [X] Add Product/Plan admin data service.
- [X] Add Product list route with search, lifecycle filter, and pagination.
- [X] Add Product create form.
- [X] Add Product detail/edit/archive/reactivate screen.
- [X] Add nested Plan list.
- [X] Add Plan create form with minor-unit price conversion.
- [X] Add Plan detail/edit/archive/reactivate screen.
- [X] Handle loading, empty, validation, conflict, forbidden, and retry states.
- [X] Require confirmation for archive operations.
- [X] Preserve accessible labels, focus behavior, and keyboard operation.
- [X] Render all server-provided names/descriptions as text.

## 11. Admin frontend automated test cases

- [X] ADMUI-001 API service sends correct Product list/detail/create/update calls.
- [X] ADMUI-002 API service sends correct nested Plan calls.
- [X] ADMUI-003 unsafe calls use PATCH/POST and receive CSRF via interceptor.
- [X] ADMUI-004 Product list renders loading, empty, success, and error states.
- [X] ADMUI-005 Product filters and pagination change the request.
- [X] ADMUI-006 Product form validates name and slug.
- [X] ADMUI-007 duplicate slug and stale update conflicts remain recoverable.
- [X] ADMUI-008 Product archive requires confirmation.
- [X] ADMUI-009 Plan form validates price, currency, interval, and credits.
- [X] ADMUI-010 entitlement editor preserves JSON value types.
- [X] ADMUI-011 inactive Plans remain discoverable to administrators.
- [X] ADMUI-012 unauthenticated and non-admin users cannot enter catalogue routes.
- [X] ADMUI-013 server-provided markup renders as text.
- [X] ADMUI-014 forms and dialogs meet keyboard/focus accessibility behavior.

## 12. End-to-end journeys

- [X] JOURNEY-PRD-001 create Product, create Plan, expose it, and subscribe.
- [X] JOURNEY-PRD-002 archive Plan, block new subscription, preserve existing one.
- [X] JOURNEY-PRD-003 update Plan, preserve old subscription snapshot, apply to new subscription.
- [X] JOURNEY-PRD-004 archive Product, hide catalogue, preserve existing subscription, reactivate.
- [X] JOURNEY-PRD-005 two customers share a Plan but cannot access each other's resources.
- [X] JOURNEY-PRD-006 stale administrator update is rejected without lost changes.
- [X] JOURNEY-PRD-007 denied catalogue mutation has no side effects and is audited.
- [X] JOURNEY-PRD-008 admin BFF accepts valid CSRF and rejects invalid origin/session combinations.

## 13. Deferred verification commands

- [ ] Run backend unit tests.
- [ ] Run backend integration tests against PostgreSQL.
- [ ] Run backend E2E tests with mocked external providers.
- [ ] Run frontend unit tests.
- [ ] Run backend and frontend linters.
- [ ] Run backend and frontend builds.
- [ ] Review generated/served OpenAPI documentation.
