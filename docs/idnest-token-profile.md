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
| `client_id` | OAuth client that obtained the token; mapped by Billmesh to one fixed API profile |
| `app` | Billmesh product/application identity |
| `environment` | Explicit isolated environment such as `production` or `staging` |
| `actor_type` | `user` for delegated users or `service` for application services |

Billmesh validates the signature, RS256 algorithm, key ID, issuer, audience,
expiration, issued-at time, maximum one-hour lifetime, client registration,
application, environment, and actor type. Organization commands also require
`org_id`. A legacy `permissions` claim, when present during migration, never
expands the registered client's access.

## Application catalogue token

An application catalogue token uses a client registered with the `catalogue`
profile, has `actor_type=service`, and does not require an organization. It may
call protected catalogue and credit-pack discovery for its own `app` value.

It cannot create accounts, read snapshots, mutate subscriptions, purchase
credits, or manage webhooks.

## Organization billing token

An organization billing token uses a client registered with the `billing`
profile and contains `org_id`. That profile covers the current account,
subscription transitions, snapshots, entitlements, payments, invoices,
billing webhooks, and ownership coordination. Application-level authorization
decides which user may initiate those calls before the trusted backend invokes
Billmesh.

When product customer scope is `identity`, account creation requires a delegated
token with `actor_type=user`; its `sub` is the customer identity. A generic
service token's `sub` is never treated as the human owner.

When customer scope is `external_customer`, a trusted service token supplies the
opaque `billing_customer_id` claim. Billmesh never accepts a customer ID from a
normal account-creation request body.

## Ownership service token

Ownership-transfer creation and confirmation require:

- `actor_type=service`;
- a client registered with the `billing` profile;
- the same `app`, `environment`, and `org_id` as the billing account; and
- an opaque `new_owner_ref` that the consuming application obtained from its
  trusted IdNest/ownership data.

The ownership operation must not be exposed directly to browsers, end users,
or generic background workers.

## Administrative identity

Admin and Console browser sessions use separately configured confidential OIDC
clients. Admin access is established by the configured Admin client ID, not by
a token scope or permission claim.
Provider callback routes do not use bearer tokens; they use the provider's
signature contract.

## Rotation and failure behavior

IdNest publishes signing keys through OIDC discovery and JWKS. Billmesh refreshes
the key cache for an unknown key ID and retains a short bounded outage grace only
for a previously validated cached key. Rotation must overlap old and new public
keys for at least the maximum token lifetime plus deployment clock skew.

Invalid, expired, wrongly addressed, or mismatched client-context tokens receive a
stable authentication/authorization error and never fall back to another
environment or organization.
