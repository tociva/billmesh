# Razorpay environment and operations contract

Billmesh uses Razorpay Standard Checkout with server-created Orders. The API
and worker load the same validated provider configuration. Checkout is disabled
when all four core values are omitted and application startup fails when only a
subset is supplied.

## Environment configuration

Configure these four values together:

```dotenv
RAZORPAY_BASE_URL=https://api.razorpay.com
RAZORPAY_KEY_ID=rzp_test_or_live_value
RAZORPAY_KEY_SECRET=provider-api-secret
RAZORPAY_WEBHOOK_SECRET=independent-webhook-secret
RAZORPAY_ACCOUNT_ID=acc_your_merchant_account
# Configure both rotation values only for a bounded retry grace period.
RAZORPAY_PREVIOUS_WEBHOOK_SECRET=previous-independent-webhook-secret
RAZORPAY_PREVIOUS_WEBHOOK_SECRET_EXPIRES_AT=2026-12-01T00:00:00Z
```

`RAZORPAY_CHECKOUT_URL` is optional and is not used for modal Standard Checkout.
The key ID may be returned to an approved checkout client. The API key secret and
webhook secret must remain only in the deployment secret manager.

Set `BILLMESH_ENVIRONMENT` to `development`, `staging`, or `production` in a
deployed environment. Development and staging require an `rzp_test_` key;
production requires an `rzp_live_` key. Local and automated-test processes also
accept only Test Mode keys.

## Test and Live isolation

- Test and Live API keys must come from their corresponding Razorpay modes.
- Test and Live webhook endpoints and webhook secrets must be configured
  independently.
- A webhook secret is not the Razorpay API key secret.
- Development and staging must never contain Live Mode credentials.
- Production must never contain Test Mode credentials.
- Provider order, payment, refund, and event identifiers must never be copied
  across Billmesh environments.

## Rotation

Rotate API keys through the deployment secret manager, deploy the new validated
configuration, and revoke the old key only after API and worker health is
confirmed. Webhook-secret rotation must retain the previous secret for the
approved delivery grace period because Razorpay can retry older events signed
with the former secret.

## Reconciliation

Webhooks are the primary asynchronous signal, but the worker must continue to
reconcile pending payments through Razorpay APIs. Finance reconciliation must
compare Razorpay captures and refunds with Billmesh payments, invoices, credit
notes, grants, and ledger mutations. A provider capture without exactly one
Billmesh fulfilment is an incident requiring operator review.

## Readiness and logging

`GET /readyz` reports whether `razorpay_checkout` is `enabled` or `disabled` but
never returns key IDs, secrets, webhook values, or provider responses. Logs and
errors may include safe Billmesh/provider resource identifiers but must never
include credentials or signed checkout/webhook values.
