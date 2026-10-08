# IdNest authentication configuration for Billmesh (development)

This document is the development authentication contract between Billmesh,
IdNest, the Billmesh Console and Admin applications, and Billmesh consumer
applications. It lists every IdNest OAuth client required by the current
Billmesh authorization model. Billing policy and application authorization
remain outside IdNest.

## Shared provider configuration

```dotenv
OIDC_ISSUER="https://hydra-dev.idnest.cloud/"
OIDC_AUDIENCE="billmesh"
IDNEST_STANDALONE_LOGOUT_URI="https://auth-dev.idnest.cloud/logout"
IDNEST_TOKEN_BROKER_AUDIENCE="urn:idnest:token-broker"
IDNEST_TOKEN_BROKER_SCOPE="billmesh.token.issue"
IDNEST_SERVICE_TOKEN_BROKER_URL="https://auth-dev.idnest.cloud/auth/v1/service-tokens"
```

The issuer includes a trailing slash. IdNest discovery and every access token
must use that value byte-for-byte. Billmesh derives the discovery and JWKS URLs
from the issuer.

`billmesh` is the only resource audience accepted by the Billmesh API. The
broker audience authenticates a consumer service to IdNest and must never be
accepted by Billmesh. Billmesh authorizes the signed `client_id` through its
server-side `OIDC_CLIENT_PROFILES` registry; OAuth scopes and a `permissions`
claim do not select or expand a Billmesh API profile.

## Required client inventory

Development requires two interactive clients and three service clients for
each consuming application:

| Client ID | Kind/profile | Used by and why it is needed | Required |
| --- | --- | --- | --- |
| `billmesh-console-bff-dev` | Confidential Authorization Code; `console` | Billmesh Console signs organization users in through the backend-for-frontend. It is needed to create an isolated Console session, bind it to the selected organization, and call catalogue and billing routes without exposing provider tokens or a client secret to browser code. | Yes |
| `billmesh-admin-bff-dev` | Confidential Authorization Code; `admin` | Billmesh Admin signs explicitly assigned operational administrators in through a separate BFF realm. It is needed for product and plan management, credit adjustments, audit access, webhook replay, and other privileged operations without granting those rights to Console users. | Yes |
| `daybook-billmesh-catalogue-dev` | Confidential M2M; `catalogue` | The Daybook backend reads Daybook plans and credit packs, including before an organization context exists. A separate read-only client prevents catalogue discovery from gaining account, payment, subscription, usage, runtime, or administrative access. | Yes |
| `daybook-billmesh-billing-dev` | Confidential M2M; `billing` | The Daybook backend performs organization billing control-plane work: account onboarding, subscriptions, snapshots, entitlements, payments, invoices, limits, events, webhooks, and ownership coordination. It is separate because these organization-scoped operations are broader than catalogue reads but must not gain runtime or administrative access. | Yes |
| `daybook-billmesh-runtime-dev` | Confidential M2M; `runtime` | Trusted Daybook execution services authorize executions, reserve and settle credits, settle installations, and record usage. It is isolated from the billing client so a compromised runtime worker cannot change subscriptions, initiate payments, manage webhooks, or administer Billmesh. | Yes |
| `taskmesh-billmesh-catalogue-dev` | Confidential M2M; `catalogue` | The Taskmesh backend reads only the Taskmesh catalogue and credit packs. It is a distinct client so its tokens are cryptographically attributable to Taskmesh and cannot read Daybook catalogue data or perform organization mutations. | Yes |
| `taskmesh-billmesh-billing-dev` | Confidential M2M; `billing` | The Taskmesh backend manages Taskmesh organization accounts, subscriptions, snapshots, entitlements, payments, invoices, limits, events, webhooks, and ownership workflows. Its separate identity enforces the Taskmesh application and organization boundaries. | Yes |
| `taskmesh-billmesh-runtime-dev` | Confidential M2M; `runtime` | Trusted Taskmesh execution services authorize work, manage credit reservations and settlement, settle installations, and report usage. It is needed to keep high-frequency runtime credentials least-privileged and separate from Taskmesh billing credentials. | Yes |
| `billmesh-service-admin-dev` | Confidential M2M; `admin` | A Billmesh-owned automation service performs explicitly approved cross-application operational actions such as credit grants, adjustments, audit reads, or webhook replay. It is omitted unless such automation exists because it is highly privileged and cannot replace the interactive Admin client for product or plan mutations. | Conditional |

Do not reuse a client across profiles, applications, or environments. Do not
create one client per organization. An organization is selected by the trusted
`org_id` claim in the short-lived token issued for a service client.

A single shared client such as `daybook-billmesh-dev` is not compatible with
this fixed-profile contract. Replace it with the three profile-specific
Daybook clients above; apply the same rule to every other consumer.

The optional service-admin client is not a substitute for the interactive
Admin client. Product and plan catalogue mutations reject service subjects and
require an interactive administrator.

## Console login

The Console BFF uses a confidential Authorization Code client with PKCE. The
browser never receives the client secret or provider tokens.

| Setting | Development value |
| --- | --- |
| Client ID | `billmesh-console-bff-dev` |
| Grant types | `authorization_code`, `refresh_token` |
| Response type | `code` |
| Token endpoint authentication | `client_secret_basic` |
| Audience | `billmesh` |
| Scopes | `openid profile email offline_access` |
| Callback | `https://api-dev.billme.sh/api/v1/auth/console/callback` |
| Logout callback | `https://api-dev.billme.sh/api/v1/auth/console/logout/callback` |
| Browser origin | `https://console-dev.billme.sh` |

The access token must contain the exact Console client ID plus `sub`, `app`,
`environment`, `actor_type`, and the user's selected `org_id`. Billmesh assigns
the `console` profile only after the signed `client_id` matches
`BFF_CONSOLE_CLIENT_ID`. The ID token audience is the Console client ID.

IdNest must allow refresh tokens for this first-party client and rotate or
revoke them normally. Reusing remembered consent for `offline_access` must not
bypass login, expiry, rotation, or revocation.

## Admin login

The Admin BFF uses a separate confidential Authorization Code client with
PKCE. Only identities explicitly assigned to this client in IdNest may finish
the login flow.

| Setting | Development value |
| --- | --- |
| Client ID | `billmesh-admin-bff-dev` |
| Grant types | `authorization_code`, `refresh_token` |
| Response type | `code` |
| Token endpoint authentication | `client_secret_basic` |
| Audience | `billmesh` |
| Scopes | `openid profile email offline_access` |
| Callback | `https://api-dev.billme.sh/api/v1/auth/admin/callback` |
| Logout callback | `https://api-dev.billme.sh/api/v1/auth/admin/logout/callback` |
| Browser origin | `https://admin-dev.billme.sh` |

Billmesh recognizes an Admin session by an exact match between the signed
access-token `client_id` and `BFF_ADMIN_CLIENT_ID`; no administrative OAuth
scope or permission claim is used. The ID token audience is the Admin client
ID. Removing an authorized identity must revoke that identity's outstanding
Admin grants and refresh tokens in IdNest.

## Consumer service clients

Each consumer has one confidential M2M client for each fixed Billmesh API
profile. Every client uses the two-stage IdNest service-token flow:

| Setting | Development value |
| --- | --- |
| Grant type | `client_credentials` |
| Token endpoint authentication | `client_secret_basic` |
| Hydra token audience | `urn:idnest:token-broker` |
| Hydra token scope | `billmesh.token.issue` |
| Broker endpoint | `https://auth-dev.idnest.cloud/auth/v1/service-tokens` |
| Resulting resource audience | `billmesh` |
| Resulting signing algorithm | `RS256` |
| Maximum resulting lifetime | Five minutes |

1. The consumer authenticates to Hydra with `client_credentials`, audience
   `urn:idnest:token-broker`, and scope `billmesh.token.issue`.
2. The consumer asks the IdNest broker for a short-lived token with audience
   `billmesh`, its fixed application, the environment `development`, and—when
   required—the authorized organization ID.

The broker must derive the output profile from the authenticated client. It
must not accept a caller-selected profile or emit another client's
`client_id`. The resulting RS256 JWT must include the exact OAuth `client_id`
that Billmesh registers below.

### Catalogue clients

`daybook-billmesh-catalogue-dev` and
`taskmesh-billmesh-catalogue-dev` may obtain only `catalogue` tokens for their
own application. The protected catalogue and credit-pack discovery routes do
not require `org_id`; all account, billing, runtime, and administrative routes
remain unavailable. Consumers may use the public catalogue instead when the
product policy explicitly permits it.

### Billing clients

`daybook-billmesh-billing-dev` and `taskmesh-billmesh-billing-dev` obtain
organization-scoped `billing` tokens. They cover account onboarding,
subscription transitions and cancellation, snapshots, entitlements, payments,
invoices, billing webhooks, limits, events, usage reads, and ownership
coordination. The consumer must authorize the user or persisted system action
before requesting a token for an `org_id`.

### Runtime clients

`daybook-billmesh-runtime-dev` and `taskmesh-billmesh-runtime-dev` obtain
organization-scoped `runtime` tokens. They are limited to execution
authorization, wallet reservation lifecycle, installation settlement, and
usage recording. Runtime credentials must not be exposed to browsers or reused
for billing-control-plane calls.

### Optional service administrator

If Billmesh deploys trusted administrative automation, use
`billmesh-service-admin-dev`. Register it as `admin` with wildcard application
and environment context only when cross-application administration is an
explicit requirement. Keep it out of consumer deployments. It cannot perform
catalogue mutations that require an interactive administrator.

## Billmesh development configuration

The two browser clients are configured independently from the service-token
registry:

```dotenv
CONSOLE_APP_ORIGIN="https://console-dev.billme.sh"
ADMIN_APP_ORIGIN="https://admin-dev.billme.sh"

BFF_CONSOLE_ISSUER="https://hydra-dev.idnest.cloud/"
BFF_CONSOLE_CLIENT_ID="billmesh-console-bff-dev"
BFF_CONSOLE_CLIENT_SECRET="<IDNEST_GENERATED_SECRET>"
BFF_CONSOLE_AUDIENCE="billmesh"
BFF_CONSOLE_SCOPE="openid profile email offline_access"
BFF_CONSOLE_REDIRECT_URI="https://api-dev.billme.sh/api/v1/auth/console/callback"
BFF_CONSOLE_POST_LOGOUT_REDIRECT_URI="https://api-dev.billme.sh/api/v1/auth/console/logout/callback"
BFF_CONSOLE_STANDALONE_LOGOUT_URI="https://auth-dev.idnest.cloud/logout"

BFF_ADMIN_ISSUER="https://hydra-dev.idnest.cloud/"
BFF_ADMIN_CLIENT_ID="billmesh-admin-bff-dev"
BFF_ADMIN_CLIENT_SECRET="<IDNEST_GENERATED_SECRET>"
BFF_ADMIN_AUDIENCE="billmesh"
BFF_ADMIN_SCOPE="openid profile email offline_access"
BFF_ADMIN_REDIRECT_URI="https://api-dev.billme.sh/api/v1/auth/admin/callback"
BFF_ADMIN_POST_LOGOUT_REDIRECT_URI="https://api-dev.billme.sh/api/v1/auth/admin/logout/callback"
BFF_ADMIN_STANDALONE_LOGOUT_URI="https://auth-dev.idnest.cloud/logout"
```

Register the service clients with Billmesh using exact client IDs and fixed
application/environment bindings:

```dotenv
OIDC_ISSUER="https://hydra-dev.idnest.cloud/"
OIDC_AUDIENCE="billmesh"
OIDC_CLIENT_PROFILES='[{"client_id":"daybook-billmesh-catalogue-dev","type":"catalogue","app":"daybook","environment":"development"},{"client_id":"daybook-billmesh-billing-dev","type":"billing","app":"daybook","environment":"development"},{"client_id":"daybook-billmesh-runtime-dev","type":"runtime","app":"daybook","environment":"development"},{"client_id":"taskmesh-billmesh-catalogue-dev","type":"catalogue","app":"taskmesh","environment":"development"},{"client_id":"taskmesh-billmesh-billing-dev","type":"billing","app":"taskmesh","environment":"development"},{"client_id":"taskmesh-billmesh-runtime-dev","type":"runtime","app":"taskmesh","environment":"development"}]'
```

Append this entry only when the optional automation client is provisioned:

```json
{"client_id":"billmesh-service-admin-dev","type":"admin","app":"*","environment":"*"}
```

`BFF_SESSION_ENCRYPTION_KEYS` is a Billmesh secret, not an IdNest credential.
All IdNest-generated client secrets belong only in the development secret store
and protected server runtime. Never place them in either web application,
source control, logs, hook payloads, or screenshots.

## Access-token contract

Every service access token presented to `/v1/*` must be signed with RS256 and
contain:

| Claim | Development requirement |
| --- | --- |
| `iss` | `https://hydra-dev.idnest.cloud/` |
| `aud` | Contains `billmesh` |
| `sub` | Stable user or service subject |
| `iat` | Present and not in the future |
| `exp` | Present and no more than one hour after `iat`; five minutes is recommended for broker tokens |
| `client_id` | Exact IdNest client registered in `OIDC_CLIENT_PROFILES` |
| `app` | `daybook` or `taskmesh`, matching the client registration |
| `environment` | `development` |
| `actor_type` | `user` or `service` |
| `org_id` | Required for organization billing and runtime operations |

Billmesh validates the signature, RS256 algorithm, key ID, issuer, audience,
expiration, issued-at time, maximum lifetime, client registration, application,
environment, and actor type. It applies zero additional clock-skew leeway. A
legacy `permissions` claim, when present, never expands the registered client's
profile.

Billmesh rejects ID tokens on the service API, unknown clients, missing or
malformed required claims, a client whose application or environment does not
match its registry entry, tokens addressed to the broker audience, and tokens
that attempt to select access with scopes or permissions.

## Profile-specific token requirements

### Catalogue tokens

A catalogue token has `actor_type=service`, uses a client registered with the
`catalogue` profile, and does not require `org_id`. It may call protected
catalogue and credit-pack discovery only for the client registration's `app`
and `environment`.

It cannot create accounts, read billing snapshots, mutate subscriptions,
purchase credits, manage webhooks, report usage, reserve credits, or invoke
administrative operations.

### Billing tokens and customer identity

A billing token uses a client registered with the `billing` profile and
contains `org_id`. The consuming application must authorize the current user or
persisted system operation before it requests this token. Billmesh does not use
the OAuth scope or a `permissions` claim as application authorization.

When the product customer scope is `identity`, account creation requires a
delegated token with `actor_type=user`; `sub` is the stable customer identity.
The `sub` of a generic service token is never treated as a human owner.

When the customer scope is `external_customer`, a trusted service token may
contain the opaque `billing_customer_id` assigned by the consuming application.
Billmesh never accepts that identifier from a normal account-creation request
body.

### Ownership service tokens

Ownership-transfer creation and confirmation require all of the following:

- `actor_type=service`;
- a client registered with the `billing` profile;
- the same `app`, `environment`, and `org_id` as the billing account; and
- an opaque `new_owner_ref` in the request body, obtained by the consuming
  application from trusted IdNest or ownership data.

Ownership operations must not be exposed directly to browsers, end users, or
generic background workers.

### Runtime tokens

A runtime token has `actor_type=service`, uses a client registered with the
`runtime` profile, and contains `org_id`. Its `app` and `environment` must match
the client registration and every affected installation, wallet, reservation,
execution, and usage event.

### Interactive Console and Admin tokens

Console and Admin use their separately configured confidential OIDC clients.
The BFF verifies the access token, the ID token, the exact signed `client_id`,
the ID-token nonce, and the ID-token access-token hash when present. Access and
refresh tokens remain in encrypted server-side sessions and are never returned
to browser code.

Admin access is established by the configured Admin client ID and IdNest's
authorized-identity assignment, not by a token scope or permission claim.
OIDC callback routes do not accept normal Billmesh bearer tokens; the BFF
validates the login transaction, authorization-code exchange, access token, and
ID token before it creates a session.

## Signing-key rotation and failure behavior

IdNest publishes signing keys through OIDC discovery and JWKS. Billmesh
refreshes its key cache for an unknown key ID and keeps only a short, bounded
outage grace period for a previously validated cached key. IdNest must overlap
old and new public keys for at least the maximum access-token lifetime plus the
deployment clock-skew allowance before retiring the old key.

An invalid, expired, wrongly addressed, incorrectly signed, unknown-client, or
mismatched client-context token receives a stable authentication or
authorization error. Billmesh never falls back to another application,
environment, organization, client profile, issuer, or audience.

## Development verification checklist

- Discovery publishes issuer `https://hydra-dev.idnest.cloud/` exactly and an
  RS256 access-token JWKS.
- Console and Admin use separate secrets, callbacks, cookies, identity
  assignments, and refresh-token grants.
- A Console or Admin access token contains its exact BFF `client_id`; each ID
  token is addressed to that same client.
- Only identities assigned to `billmesh-admin-bff-dev` can complete Admin
  login, and removing an identity revokes its grants and refresh tokens.
- Each M2M client can request only the broker audience and issuance scope
  needed for its fixed application and profile.
- The broker preserves the authenticated OAuth client as the output
  `client_id`, emits `app` and `environment`, and rejects an unauthorized or
  empty organization for organization-scoped profiles.
- Catalogue, billing, and runtime clients cannot call one another's protected
  route families.
- A token for one application, environment, or organization cannot access
  another boundary.
- `urn:idnest:token-broker` is never accepted as the Billmesh API audience.
- Tokens, client secrets, Basic Authorization values, refresh tokens, session
  encryption keys, and webhook secrets are absent from application logs.
