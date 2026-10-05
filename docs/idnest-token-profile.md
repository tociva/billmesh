# IdNest Token Profile for Billmesh

Billmesh accepts RS256 access tokens issued by the configured IdNest issuer and
addressed to the configured Billmesh audience. ID tokens are not accepted by
the service API.

## Common requirements

Every service access token contains:

| Claim | Requirement |
| --- | --- |
| `iss` | Exact configured `OIDC_ISSUER` for the environment |
| `aud` | Contains the configured `OIDC_AUDIENCE` |
| `sub` | Stable actor subject; never empty |
| `iat` | Required and must not be in the future; Billmesh applies zero additional clock-skew leeway |
| `exp` | Required and no more than one hour after `iat` |
| `app` | Billmesh product/application identity |
| `environment` | Explicit isolated environment such as `production` or `staging` |
| `actor_type` | `user` for delegated users or `service` for application services |
| `permissions` | Array of exact Billmesh permission strings |

Billmesh validates the signature, RS256 algorithm, key ID, issuer, audience,
expiration, issued-at time, maximum one-hour lifetime, application,
environment, and actor type. Organization commands also require `org_id`.

## Application catalogue token

An application catalogue token has `actor_type=service`, does not require an
organization, and receives only `catalogue:read`. It may call protected
catalogue and credit-pack discovery for its own `app` value.

It cannot create accounts, read snapshots, mutate subscriptions, purchase
credits, or manage webhooks.

## Organization billing token

An organization token contains `org_id` and one or more of:

- `billing:read` for the current account, snapshot, transitions, payments,
  invoices, and webhook reads;
- `billing:write` for account onboarding, subscription transitions,
  cancellation, payment orders, and webhook management;
- `billing:ownership` only for the trusted application service that coordinates
  primary-owner changes.

When product customer scope is `identity`, account creation requires a delegated
token with `actor_type=user`; its `sub` is the customer identity. A generic
service token's `sub` is never treated as the human owner.

When customer scope is `external_customer`, a trusted service token supplies the
opaque `billing_customer_id` claim. Billmesh never accepts a customer ID from a
normal account-creation request body.

## Ownership service token

Ownership-transfer creation and confirmation require:

- `actor_type=service`;
- `billing:ownership`;
- the same `app`, `environment`, and `org_id` as the billing account; and
- an opaque `new_owner_ref` that the consuming application obtained from its
  trusted IdNest/ownership data.

The permission must not be issued to browsers, end users, generic background
workers, or ordinary `billing:write` clients.

## Administrative identity

Admin and Console browser sessions use separately configured confidential OIDC
clients. Administrative permissions are not issued to consumer service tokens.
Provider callback routes do not use bearer tokens; they use the provider's
signature contract.

## Rotation and failure behavior

IdNest publishes signing keys through OIDC discovery and JWKS. Billmesh refreshes
the key cache for an unknown key ID and retains a short bounded outage grace only
for a previously validated cached key. Rotation must overlap old and new public
keys for at least the maximum token lifetime plus deployment clock skew.

Invalid, expired, wrongly addressed, or incorrectly scoped tokens receive a
stable authentication/authorization error and never fall back to another
environment or organization.
