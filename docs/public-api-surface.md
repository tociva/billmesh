# Billmesh v1 API Surface

This inventory defines which routes belong to the first consumer-facing API.
The repository OpenAPI also documents administrative, callback, BFF, and
deferred first-party operations; consumer client generation must select only
the allowlist below.

## Consumer control plane

| Capability | Operations | Token context |
| --- | --- | --- |
| Catalogue | `GET /v1/public/catalog`, `GET /v1/catalog`, `GET /v1/credit-packs` | Public when policy permits, otherwise `billmesh.catalogue` delegated token |
| Account | `POST /v1/accounts`, `GET /v1/accounts/current` | Organization context with `billmesh.billing`; identity onboarding also requires a user actor |
| Ownership | `POST /v1/account-ownership-transfers`, `GET /v1/account-ownership-transfers/{id}`, `POST .../{id}/confirm`, `POST .../{id}/cancel` | `billmesh.billing` or administrative authority; mutations require a registered service actor |
| Subscription | `POST /v1/subscription-transitions`, `GET /v1/subscription-transitions/{id}`, `POST .../{id}/cancel`, `POST /v1/subscriptions/current/cancellation` | Organization context with `billmesh.billing` |
| Authoritative state | `GET /v1/billing-snapshot` | Organization context with `billmesh.billing` |
| Purchases | `POST /v1/payments/orders`, `GET /v1/payments`, `GET /v1/invoices` | Organization context with `billmesh.billing` |
| Webhooks | `POST /v1/webhooks`, `GET /v1/webhooks`, `PATCH /v1/webhooks/{id}`, `DELETE /v1/webhooks/{id}`, `POST /v1/webhooks/{id}/rotate-secret` | Organization context with `billmesh.billing` |

The snapshot is the canonical enforcement read model. Separate entitlement,
limit, wallet, reservation, installation, usage, and SSE operations remain
available to existing first-party workloads but are deferred from the initial
general consumer SDK until their publication contract is explicitly approved.

## Non-consumer routes

- `/v1/admin/*` is the administrative control plane and requires administrative
  authorization. It must not appear in a normal application client.
- `POST /v1/payments/webhook` is a Razorpay callback authenticated with the
  provider signature, not an application operation.
- `/api/v1/*` is the browser BFF surface and uses an opaque browser session plus
  CSRF protection for unsafe requests.
- `/healthz`, `/readyz`, `/docs/*`, and `/openapi.yaml` are health and
  documentation operations, not billing commands.

## Deliberately absent

There is no direct subscription create, plan-change, reactivation, renewal, or
ID-addressed cancellation route. A consumer cannot submit a plan slug, price,
billing model, environment, or payment-verification status. Those outcomes are
derived and verified by Billmesh.

Because Billmesh is a fresh application, this is the only v1 contract; there is
no legacy compatibility surface.
