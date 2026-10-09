# IdNest Delegated Access for Billmesh

Billmesh accepts IdNest Delegated Access tokens as its only service-to-service
authentication contract. It does not use a proprietary service-token broker or
accept Hydra client-credentials tokens directly on `/v1/*`.

Browser Console and Admin sessions remain separate confidential OIDC flows.
Their RS256 access and ID tokens are verified only by the BFF managers and are
not part of the delegated service contract described here.

## Development resource

Configure one active IdNest delegation resource:

| Setting | Development value |
| --- | --- |
| Resource key | `billmesh-api-dev` |
| Audience | `https://api-dev.billme.sh` |
| Signing algorithm | `ES256` |
| Maximum token lifetime | Five minutes |
| Authorization detail type | `urn:idnest:delegation` |
| Context profile | `urn:billmesh:context:v1` |

Every consuming application owns an authorizer client. Each authorizer is
paired explicitly with approved actor clients and scopes. An authorizer for one
application must never be able to nominate another application's actors.

Example Daybook clients:

| Client | Broker scope | Billmesh scope | Billmesh actor type |
| --- | --- | --- | --- |
| `daybook-billmesh-authorizer-dev` | `delegation.grant` | — | — |
| `daybook-billmesh-catalogue-dev` | `delegation.exchange` | `billmesh.catalogue` | `service` |
| `daybook-billmesh-billing-user-dev` | `delegation.exchange` | `billmesh.billing` | `user` |
| `daybook-billmesh-billing-service-dev` | `delegation.exchange` | `billmesh.billing` | `service` |
| `daybook-billmesh-runtime-dev` | `delegation.exchange` | `billmesh.runtime` | `service` |

Create equivalent authorizer/actor policies for Taskmesh or another consumer.
The actor credential alone never chooses its application, environment, API
profile, actor type, or scope.

### Create the Daybook catalogue actor

Create `daybook-billmesh-catalogue-dev` in IdNest before adding it to the
delegation resource:

1. Open **Security Administration → OAuth clients** and choose **Create OAuth
   client**.
2. Select **Machine-to-machine** and enter these values:

   | OAuth client field | Value |
   | --- | --- |
   | Client ID | `daybook-billmesh-catalogue-dev` |
   | Client name | `Daybook Billmesh catalogue (development)` |
   | Grant type | `client_credentials` (provided by the machine-to-machine profile) |
   | Token endpoint authentication | `client_secret_basic` (provided by the machine-to-machine profile) |
   | Scope | `delegation.exchange` only |
   | Audience | `urn:idnest:delegation` |
   | Redirect, logout, and CORS URIs | None |

   `delegation.exchange` is a custom scope in the OAuth-client editor; enter it
   and choose **Add scope**. Do not add `billmesh.catalogue` to this OAuth
   client. That is a delegated resource scope and is assigned by the approved
   actor policy below.
3. Create the client and immediately copy its one-time secret to the Daybook
   development secret store. Configure the Daybook backend with:

   ```dotenv
   BILLMESH_CATALOGUE_ACTOR_CLIENT_ID="daybook-billmesh-catalogue-dev"
   BILLMESH_CATALOGUE_ACTOR_CLIENT_SECRET="<IDNEST_GENERATED_SECRET>"
   ```

   The secret is a server-side credential. Do not put it in either web
   application, source control, logs, or this document.
4. Open **Security Administration → Delegated Access → Billmesh API
   (development) → Approved actors** and choose **Approve an actor**. Select
   `daybook-billmesh-catalogue-dev`, allow only `billmesh.catalogue`, leave the
   policy **Active**, and save it. The current IdNest contract validates on
   save that the selected client allows `client_credentials`, the
   `urn:idnest:delegation` audience, and `delegation.exchange`.
5. Ensure the same authorizer/actor pair exists and is enabled in Billmesh's
   `billmesh.delegation_client_profiles` registry. For local development the
   matching record is maintained by `deploy/delegation-profiles.dev.sql` and
   loaded by `make db-bootstrap`.

The catalogue actor can exchange only a one-time grant created by
`daybook-billmesh-authorizer-dev` for `billmesh-api-dev` and
`billmesh.catalogue`. Its Hydra client-credentials token is broker
infrastructure and must never be sent to a Billmesh `/v1/*` endpoint.

## Grant and exchange

The consuming application authenticates its authorizer and actor clients to the
delegation broker with the audience `urn:idnest:delegation`. It then:

1. creates a grant through `POST /auth/v1/delegation/grants`;
2. exchanges the one-time grant through `POST /auth/v1/delegation/token`; and
3. sends the resulting delegated access token to Billmesh.

An organization-scoped grant resembles:

```json
{
  "resource": "billmesh-api-dev",
  "subject": "user-123",
  "actorClientId": "daybook-billmesh-billing-user-dev",
  "scope": ["billmesh.billing"],
  "authorizationContext": {
    "type": "urn:billmesh:context:v1",
    "org_id": "org-123"
  },
  "correlationId": "request-123"
}
```

IdNest validates the exact authorizer–actor–scope policy and the context schema
when creating the grant. During exchange it revalidates the active resource,
policy, effective reduced scopes, and context-profile version before consuming
the grant and signing a token.

## Token contract

Billmesh accepts only an ES256 JWT with `typ=at+jwt` and:

| Claim | Requirement |
| --- | --- |
| `iss` | Exact configured IdNest delegation issuer |
| `aud` | Exactly the configured absolute Billmesh audience |
| `sub` | Stable user or service subject selected by the trusted authorizer |
| `iat`, `nbf`, `exp`, `jti` | Required; lifetime is at most five minutes |
| `client_id` | Exchanging actor client ID |
| `act.sub` | Must equal `client_id` |
| `scope` | Exactly one registered Billmesh scope initially |
| `authorization_details` | Exactly one entry described below |

The authorization detail is:

```json
{
  "type": "urn:idnest:delegation",
  "grant_id": "grant-123",
  "authorizer_client_id": "daybook-billmesh-authorizer-dev",
  "context_profile_version": 1,
  "context": {
    "type": "urn:billmesh:context:v1",
    "org_id": "org-123",
    "billing_customer_id": "customer-456"
  }
}
```

`billing_customer_id` is optional in the schema and required by Billmesh only
when the selected product uses `external_customer` customer scope. Catalogue
tokens may omit the context. Billing and runtime routes require `org_id`.
`application` and `environment` are accepted only to resolve a wildcard
administrative registration; ordinary registrations derive both values from
the trusted authorizer/actor pair. A wildcard administrator must provide both
fields. Any other context field is rejected.

The context is signed but not encrypted. It must not contain secrets or private
display data. Billmesh rejects unknown fields, non-canonical whitespace,
context values over 512 bytes, and delegated JWTs over 16 KiB.

## Billmesh trust registry

Billmesh registers the authenticated pair, not the actor alone:

```dotenv
DELEGATION_ISSUER="https://auth-dev.idnest.cloud/auth/v1/delegation"
DELEGATION_DISCOVERY_URL="https://auth-dev.idnest.cloud/.well-known/idnest-delegation-configuration"
DELEGATION_AUDIENCE="https://api-dev.billme.sh"
DELEGATION_PROFILE_REFRESH_INTERVAL="10s"
DELEGATION_PROFILE_MAX_STALENESS="1m"
```

The authorizer/actor registrations live in `billmesh.delegation_client_profiles`.
For local development, `make db-bootstrap` loads the idempotent records in
`deploy/delegation-profiles.dev.sql`. Production onboarding must write profiles
with a migration or control-plane database role; the API/worker runtime role
cannot create, change, or delete them.

The API loads the complete enabled set before serving traffic and atomically
refreshes it on the configured interval. A failed refresh retains the last
known-good snapshot. Once that snapshot exceeds the maximum staleness, delegated
requests fail closed with HTTP 503 until a valid snapshot is loaded. An empty or
invalid enabled set also prevents API startup.

From the database registry Billmesh derives the application, environment, API
profile, actor type, and context-profile version. Token input cannot override
those values. The initial contract requires the token's one scope to equal the
registered scope.

Administrative automation may use an `admin` registration with wildcard
application/environment only when the signed context names the target
application and environment. Normal consumer registrations cannot use
wildcards.

Disable a registration by setting `enabled = false`; avoid deleting it during
normal operations. Every insert, update, and delete is recorded in
`billmesh.delegation_client_profile_events`, including the database principal
that performed the change and the before/after records.

## Customer identity

Identity-scoped customers are keyed by:

```text
(delegation issuer, authorizer_client_id, sub)
```

Billmesh never assumes that `sub` is globally unique across authorizers.

- `identity` customer scope requires a `user` actor and uses the tuple above.
- `organization` customer scope uses the registered application/environment
  plus `org_id`.
- `external_customer` scope uses the registered application/environment plus
  the signed `billing_customer_id`.
- Ownership-transfer operations require a delegated `service` actor with a
  billing or administrative profile.

## Caching and secrets

Consumers may cache the Hydra client-credentials tokens used to authenticate
their authorizer and actor clients. They must not share or cache final
user-delegated tokens across operations. A final delegated service token, if
ever cached, must be isolated by audience, authorizer, actor, subject,
normalized scopes, exact context, and principal type.

Client secrets belong only in protected server runtimes and secret stores.
They must not appear in browser applications, source control, logs, webhook
payloads, screenshots, or support exports.

## Acceptance checks

Before enabling a consumer, verify:

- discovery advertises the exact issuer, delegation JWKS, ES256, and the
  delegation authorization-detail type;
- an authorizer cannot nominate an actor outside its policy;
- policy or context-profile disablement after grant creation blocks exchange;
- exchange may reduce but never expand scope;
- malformed, oversized, unknown-field, or wrong-version contexts fail;
- catalogue succeeds without organization context;
- billing and runtime fail without `org_id`;
- identity onboarding rejects a service actor;
- ownership operations reject a user actor; and
- identical `sub` values from different authorizers remain different billing
  customers.
