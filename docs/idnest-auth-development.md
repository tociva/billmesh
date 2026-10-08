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

| Client ID | Kind | Billmesh profile | Application | Required |
| --- | --- | --- | --- | --- |
| `billmesh-console-bff-dev` | Confidential Authorization Code | `console` | Billmesh Console | Yes |
| `billmesh-admin-bff-dev` | Confidential Authorization Code | `admin` | Billmesh Admin | Yes |
| `daybook-billmesh-catalogue-dev` | Confidential M2M | `catalogue` | Daybook | Yes |
| `daybook-billmesh-billing-dev` | Confidential M2M | `billing` | Daybook | Yes |
| `daybook-billmesh-runtime-dev` | Confidential M2M | `runtime` | Daybook | Yes |
| `taskmesh-billmesh-catalogue-dev` | Confidential M2M | `catalogue` | Taskmesh | Yes |
| `taskmesh-billmesh-billing-dev` | Confidential M2M | `billing` | Taskmesh | Yes |
| `taskmesh-billmesh-runtime-dev` | Confidential M2M | `runtime` | Taskmesh | Yes |
| `billmesh-service-admin-dev` | Confidential M2M | `admin` | Billmesh operations | Only if non-browser administrative automation is deployed |

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

Billmesh rejects ID tokens on the service API, unknown clients, a client whose
application or environment does not match its registry entry, tokens addressed
to the broker audience, and tokens that attempt to select access with scopes or
permissions.

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

The detailed resource-token validation rules are defined in
[IdNest Token Profile for Billmesh](./idnest-token-profile.md).
