# Billmesh API Error Codes

Every JSON error contains `error`, stable machine-readable `code`, and
`request_id`. Consumers branch on HTTP status and `code`, never on message text.
Every response also returns `X-Request-ID`.

## Common codes

| Code | Typical status | Meaning |
| --- | --- | --- |
| `invalid_request` | 400 | The request is malformed or violates the base contract |
| `unauthenticated` | 401 | The bearer token or provider signature is missing or invalid |
| `forbidden` | 403 | Authenticated client has the wrong API profile, context, or resource ownership |
| `not_found` | 404 | Resource is absent or intentionally hidden by isolation |
| `method_not_allowed` | 405 | Route exists but does not accept this method |
| `conflict` | 409 | Current resource or policy state rejects the operation |
| `stale_revision` | 412 | `If-Match` does not identify the current billing revision |
| `precondition_required` | 428 | A required catalogue or billing precondition is missing |
| `rate_limited` | 429 | Caller exceeded an enforced request limit |
| `dependency_invalid_response` | 502 | An external dependency returned an invalid response |
| `dependency_unavailable` | 503 | Required external dependency is unavailable |
| `internal_error` | 500 | Unexpected internal failure with no sensitive detail |

## Billing and command codes

- `idempotency_key_required`
- `idempotency_conflict`
- `billing_account_not_found`
- `credit_pack_id_required`
- `credit_pack_unavailable`
- `checkout_not_configured`
- `checkout_redirect_not_allowed`
- `invalid_provider_response`
- `transition_timing_not_allowed`
- `transition_not_cancellable`
- `immediate_cancellation_disabled`
- `cancellation_withdrawal_disabled`

## Ownership codes

- `trusted_service_required`
- `invalid_new_owner_ref`
- `invalid_ownership_transfer_id`
- `delegated_owner_identity_required`
- `ownership_transfer_not_applicable`
- `ownership_transfer_unsupported`
- `billing_customer_missing`
- `owner_unchanged`
- `ownership_transfer_expired`
- `ownership_transfer_not_cancellable`
- `ownership_transfer_not_authorized`
- `paid_transition_required`
- `new_owner_free_allowance_exhausted`

Ownership-transfer responses also contain a typed `decision_code` such as
`owner_eligible` or `new_owner_free_allowance_exhausted`; that field is a
business decision, not an HTTP error.

## Webhook codes

- `unsupported_webhook_version`
- `unsupported_webhook_event_type`

Additional codes may be added without changing existing meanings. Removing or
changing a published code requires a new incompatible API version.
