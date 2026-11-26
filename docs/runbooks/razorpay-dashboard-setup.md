# Razorpay Dashboard setup

This runbook configures Billmesh development with Razorpay Test Mode. It keeps
Test and Live resources independent and records enough evidence for a reviewed
production launch without placing credentials in the repository.

## Offline development rule

Real Razorpay account activation, credentials, Dashboard settings, webhooks,
and network calls are deferred until all implementation and automated-quality
phases are complete. Until that final gate:

- automated tests use only `rzp_test_billmesh`, `test-provider-secret`, and
  `test-webhook-secret` inside isolated test processes;
- `RAZORPAY_BASE_URL` points to the fake external service, never
  `api.razorpay.com`;
- no real credential is generated, stored, uploaded, or requested; and
- a deployed development endpoint returning `razorpay_checkout: disabled` and a
  webhook `503` is expected, not a phase failure.

Billmesh currently invokes this complete provider surface, all of which is
implemented by `tests/fakeserver` and exercised by automated tests:

| Method | Provider path | Purpose |
| --- | --- | --- |
| `POST` | `/v1/orders` | Create an idempotently recoverable order |
| `GET` | `/v1/orders` | Recover orders and scan provider inventory |
| `GET` | `/v1/orders/{id}/payments` | Reconcile all attempts for an order |
| `GET` | `/v1/payments/{id}` | Confirm or reconcile one payment |
| `GET` | `/v1/payments/{id}/refunds` | Reconcile paginated refunds |

Inbound webhooks are not provider REST calls, but their real wire envelopes,
event headers, raw-body HMAC signatures, duplicates, ordering, rotation, and the
four approved event types are also generated locally. The remaining sections
are the final real-environment procedure and must not be performed early.

## Fixed development contract

| Setting | Required value |
| --- | --- |
| Razorpay mode | Test Mode |
| Billmesh environment | `development` |
| API base URL | `https://api.razorpay.com` |
| Public webhook URL | `https://api-dev.billme.sh/v1/payments/webhook` |
| Capture policy | Automatic Capture for all authorised Orders API payments |
| Active events | `payment.captured`, `payment.failed`, `order.paid`, `refund.processed` |
| API secret location | GitHub environment `billmesh-development-api` |
| Webhook secret | A new value, distinct from `RAZORPAY_KEY_SECRET` |

Do not add `payment.authorized`, dispute, subscription, payout, or transfer
events. They are outside the phase-one handler allowlist.

## Before changing the Dashboard

- Use a named Razorpay Owner or Admin account with MFA. Record who performs and
  approves the change.
- Identify the merchant account ID, operational owner, monitored alert email,
  and approved credential-rotation date.
- Confirm `https://api-dev.billme.sh/readyz` is healthy and the deployment has
  complete Razorpay configuration. Do not paste secrets into tickets, chat,
  screenshots, shell history, logs, or the inventory record.
- Create the secured inventory record from
  [`razorpay-operational-inventory.example.yaml`](../razorpay-operational-inventory.example.yaml).
  The populated record belongs in the approved vault or restricted operations
  system, not Git.

## 1. Verify the merchant account

1. Sign in to the Razorpay Dashboard and select the intended merchant account.
2. Record its merchant account ID and confirm the assigned Owner/Admin can view
   **Account & Settings**, **API Keys**, **Payment Capture**, and **Webhooks**.
3. Record the account status in restricted evidence. Test Mode integration can
   proceed while Live activation is pending, but production cannot.
4. Resolve any KYC, website/app verification, or activation action before the
   Live Mode gate.

## 2. Generate and store Test Mode API credentials

1. Switch the Dashboard to **Test Mode** and verify the mode again before
   generating anything.
2. Open **Account & Settings → API Keys → Generate Key**.
3. Transfer the displayed key ID and one-time key secret directly into the
   approved secret workflow. Do not save the downloaded credentials in the
   repository or attach them to the inventory.
4. Populate the ignored `tmp/development.env` with:

   ```dotenv
   RAZORPAY_BASE_URL=https://api.razorpay.com
   RAZORPAY_KEY_ID=rzp_test_replace_with_generated_key_id
   RAZORPAY_KEY_SECRET=replace_with_generated_api_key_secret
   RAZORPAY_WEBHOOK_SECRET=replace_with_independent_webhook_secret
   RAZORPAY_ACCOUNT_ID=acc_replace_with_merchant_account_id
   ```

5. Validate and then upload the full development environment using the existing
   deployment workflow. These are commands for the operator to run; neither
   command should print the secret values:

   ```sh
   tools/api/ci/upload-development-github-environment.sh --check tmp/development.env
   tools/api/ci/upload-development-github-environment.sh tmp/development.env
   ```

`RAZORPAY_KEY_ID`, `RAZORPAY_KEY_SECRET`, and `RAZORPAY_WEBHOOK_SECRET` are
stored as GitHub environment secrets. `RAZORPAY_BASE_URL` and
`RAZORPAY_ACCOUNT_ID` are stored as environment variables.

## 3. Configure automatic capture

Only the Razorpay account owner can change this setting.

1. In Test Mode, open **Account & Settings → Payment Capture** and choose
   **Change** next to **Automatic Capture**.
2. Select automatic capture for all payments authorised within **3 days**.
3. Select **Refund Automatically**, then **Normal Refund**, and save.
4. Capture restricted evidence showing the selected mode and policy, without
   exposing any credential.

Billmesh creates payments through the Orders API and fulfils value only after a
captured payment. Do not choose Manual Capture for phase one.

## 4. Prove the public route reaches Billmesh

The webhook path must be public. Do not put Cloudflare Access authentication in
front of this path because Razorpay cannot supply Billmesh's service token. Keep
the signature verification, request limits, ingress TLS, and application rate
limits enabled.

Run the secret-free negative probe:

```sh
tools/api/ci/verify-razorpay-webhook-route.sh \
  https://api-dev.billme.sh/v1/payments/webhook
```

Success means the public request reached the Billmesh webhook handler and was
rejected with its expected `401` invalid-signature envelope. The probe does not
use a real secret and cannot persist a provider event. A redirect, Cloudflare
challenge, HTML response, timeout, or any status other than the expected
Billmesh response is a routing failure; fix DNS, Cloudflare, ingress, firewall,
or service routing before creating the webhook.

## 5. Create the Test Mode webhook

1. Confirm the Dashboard still shows **Test Mode**.
2. Open **Account & Settings → Webhooks → Add New Webhook**.
3. Enter the exact URL
   `https://api-dev.billme.sh/v1/payments/webhook`.
4. Enter the independent value stored as `RAZORPAY_WEBHOOK_SECRET`. It must not
   equal `RAZORPAY_KEY_SECRET` and must not be stored in the inventory.
5. Enter the monitored operational alert email.
6. Select only these active events:

   - `payment.captured`
   - `payment.failed`
   - `order.paid`
   - `refund.processed`

7. Create the webhook, confirm it is enabled, and record its webhook ID. If the
   Test Mode operation requests an OTP, follow the current instruction shown by
   Razorpay.
8. Record restricted evidence of URL, mode, enabled status, events, and alert
   email. Redact unrelated account data and never capture the secret.

Razorpay treats a non-2xx response or a response taking longer than five seconds
as a failed delivery and can disable a persistently failing webhook. The alert
email must therefore be a monitored mailbox with a named owner.

## 6. Complete the secured inventory

Fill every field in the secured copy of the inventory template, including the
webhook ID, exact events, owner, alert email, environment, creation and
verification timestamps, rotation due dates, secret-manager references, and
restricted evidence links. Store only the final eight characters of the key ID;
never store either secret value.

The operator and approver should jointly compare the Dashboard with this fixed
contract. Mark Phase 13 tasks complete in the integration plan only from this
evidence, not from screenshots pasted into the repository.

## 7. Live Mode gate

Do not repeat this setup in Live Mode until every Phase 15 staging-acceptance
item is complete and approved. Live Mode requires a separately verified merchant
account, `rzp_live_` credentials, an independent production webhook and secret,
the production endpoint, a production alert owner, a production inventory
record, and an independent route/delivery verification. Never promote Test Mode
keys, secrets, webhook IDs, or provider resource IDs into production.

The full operator sequence, evidence template, minimum-value smoke payment,
refund, approval, containment, and rollback decision are defined in the
[`Razorpay production go-live`](razorpay-production-go-live.md) runbook.

## Operator verification commands

Run these after making the configuration changes and share only their pass/fail
output. The commands must be run by the operator; this runbook does not run them.

```sh
tools/api/ci/test-razorpay-dashboard-setup-contracts.sh

tools/api/ci/upload-development-github-environment.sh \
  --check tmp/development.env

tools/api/ci/verify-razorpay-webhook-route.sh \
  https://api-dev.billme.sh/v1/payments/webhook
```

The first successful Test Mode transaction and signed delivery belong to Phase
15 staging acceptance, not to this configuration-only phase.

## Razorpay references

- [Generate Test and Live API keys](https://razorpay.com/docs/payments/dashboard/account-settings/api-keys/)
- [Configure payment capture](https://razorpay.com/docs/payments/payments/capture-settings/)
- [Set up and edit payment webhooks](https://razorpay.com/docs/webhooks/setup-edit-payments/)
- [Merchant activation checklist](https://razorpay.com/blog/razorpay-merchant-activation-checklist/)
