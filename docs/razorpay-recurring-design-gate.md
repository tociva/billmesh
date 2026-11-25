# Razorpay recurring-collection design gate

## Status

The recurring-collection gate is **closed**. Phase one uses Razorpay Orders and
Standard Checkout for a single paid billing period. It does not create a
Razorpay Customer, Subscription, mandate, recurring charge, or automatic
renewal. Product policy is therefore locked to `lifecycle.renewal: manual` and
`checkout.recurring_mandate: false`.

At the end of a paid period, Billmesh does not manufacture a renewal payment.
Without a separately verified payment, the subscription becomes `past_due`,
retains access only through the snapshotted grace period, and then follows its
expiration policy. A scheduled period-end cancellation becomes `cancelled`
instead of entering dunning. A later capture is retained for finance review and
does not reactivate a terminal transition.

## Provider ownership decision

No recurring provider flow is approved yet. Razorpay Subscriptions, UPI AutoPay,
and any alternative mandate flow must be compared in a separate architecture
decision. The decision must cover supported payment methods, authentication,
mandate limits and expiry, retries, dunning controls, plan changes, pause/resume,
webhook guarantees, reconciliation APIs, settlement reporting, refunds,
chargebacks, regional requirements, and exit/migration behavior.

Until that decision is approved, Billmesh remains the only catalogue,
subscription, entitlement, invoice, and ledger authority. A provider may own
collection execution later, but it must never become the catalogue source of
truth.

## Plan-version mapping

Recurring implementation requires an environment-scoped mapping from an
immutable Billmesh plan version to a provider plan. The future mapping must
contain at least:

- Billmesh product, plan family, plan ID, and plan version;
- provider, provider environment, and provider plan ID;
- amount, currency, interval, and provider creation timestamp;
- activation and retirement timestamps; and
- an audit reference to the actor or deployment that created the mapping.

A changed Billmesh price or interval creates a new plan version and provider
mapping. Reusing or mutating an old provider plan must not rewrite an existing
subscription's snapshotted commercial terms.

## Provider identifier model

The recurring phase must add explicit, environment-scoped records rather than
overloading current one-time payment fields:

| Provider object | Required Billmesh relationship |
| --- | --- |
| Customer | Account/customer mapping with provider and environment uniqueness |
| Subscription | Billmesh subscription, provider customer, provider plan mapping, and current provider state |
| Mandate | Provider customer/subscription, payment method class, authorization state, limits, and expiry |
| Charge | Provider subscription and billing period, linked to exactly one Billmesh payment attempt |
| Invoice | Provider invoice reference linked to the authoritative Billmesh invoice without replacing it |
| Payment | Existing Billmesh payment plus immutable provider charge/payment references and environment |

Every provider identifier must be unique inside its provider environment. Test
and Live identifiers must never cross environments.

## State-transition contract

The future adapter must normalize provider states without letting them directly
grant access:

| Normalized state | Required behavior before approval |
| --- | --- |
| activation/authentication | Create no entitlement until provider authentication and Billmesh verification complete |
| pending | Preserve the prior authoritative state and expose a pending transition |
| active | Activate only after a verified collectible mandate and successful initial charge, when required |
| halted | Enter policy-controlled dunning without silently extending the paid period |
| cancelled | Stop future collection and apply the approved effective-date policy |
| completed | Stop collection after the contracted final period without reactivation |
| expired | Remove recurring authority and follow the snapshotted expiration policy |

Duplicate and out-of-order events must converge on one legal state. A late
provider event may create an auditable reconciliation issue but cannot move a
terminal Billmesh state backwards.

## Behavior that must be designed before implementation

The architecture decision must explicitly define plan upgrades and downgrades,
proration ownership, pause/resume, immediate and period-end cancellation,
authentication retries, charge retries, dunning windows, mandate expiry and
replacement, renewal invoice timing, credit allocation, refunds, disputes, and
provider/Billmesh reconciliation. No provider default is accepted implicitly.

## Mandatory test gate

Before any recurring policy flag can be enabled, tests must first fail for the
expected reason across all approved transitions:

- unit tests for provider-state normalization, policy validation, proration, and
  period calculations;
- PostgreSQL integration tests for identifier uniqueness, atomic state changes,
  idempotency, concurrency, rollback, ledger, invoice, and credit allocation;
- E2E tests with recorded provider payloads for authentication, activation,
  pending, active, halted, cancellation, completion, expiry, retry, duplicate,
  out-of-order, and lost-response scenarios;
- security tests for cross-account and Test/Live isolation, forged callbacks,
  replay, leaked identifiers, and operator authorization; and
- recovery tests for webhook loss, provider outage, worker restart, ambiguous
  commit, reconciliation mismatch, and provider migration.

## Architecture and production-readiness review

Enabling recurring collection requires a separate approved review covering the
provider decision, schema migration and rollback, threat model, data retention,
PCI and regional obligations, SLOs and alerting, reconciliation and settlement,
support runbooks, finance sign-off, staged Test Mode acceptance, production
rollout, kill switch, and rollback plan. Until that review is complete, policy
validation and the public schema must continue to reject recurring mandates and
automatic provider renewal.
