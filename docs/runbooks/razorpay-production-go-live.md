# Razorpay production go-live

This is the operator gate for enabling Billmesh payments with Razorpay Live
Mode. It is deliberately evidence-driven: documentation and automated tests
prepare the launch, but they do not authorize production traffic.

## Hard stop prerequisites

Do not generate Live Mode credentials or configure a Live webhook until every
Phase 15 Test Mode staging-acceptance checkbox is complete and its engineering,
finance, security, product, operations, and support approvals are recorded.
Stop if any P0 or P1 payment, ledger, invoice, refund, reconciliation, security,
or checkout defect is open.

Create a secured copy of
[`razorpay-production-go-live-evidence.example.yaml`](../razorpay-production-go-live-evidence.example.yaml).
The populated record belongs in the approved restricted evidence system, never
in Git. It may contain safe identifiers and links, but never API key secrets,
webhook secrets, signatures, raw webhook bodies, card/bank details, tokens, or
database credentials.

Assign one launch commander and one independent approver. Record the release
version and image digest, the launch window, the rollback decision deadline,
the customer-support owner, finance owner, security owner, and incident channel.

## 1. Merchant and payment-method approval

1. Sign in using a named Razorpay Owner or Admin account with MFA and select the
   intended merchant account.
2. Record restricted evidence that activation/KYC is complete and Live Mode is
   available.
3. Record each required payment method and currency as approved. A method that
   is pending, restricted, or untested remains disabled at launch.
4. Compare the merchant account ID with the separately approved production
   identity. A mismatch is a hard stop.

## 2. Generate isolated Live Mode credentials

1. Switch to **Live Mode**, verify the mode a second time, and generate a new
   Live API key pair. Transfer the one-time secret directly to the production
   secret manager.
2. Generate a separate high-entropy `RAZORPAY_WEBHOOK_SECRET`.
   It must differ from the API key secret and every Test Mode secret.
3. Use separate production secret-manager references and access policy. Never
   copy Test Mode keys, secrets, webhook IDs, orders, payments, refunds, events,
   or merchant resources into production.
4. Prepare the ignored production environment file. It must use
   `BILLMESH_ENVIRONMENT=production`, `https://api.razorpay.com`, an
   `rzp_live_` key ID, and the approved `acc_` merchant account ID in
   `RAZORPAY_ACCOUNT_ID`.
5. Run the secret-safe validator. It prints only pass/fail output:

   ```sh
   tools/api/kubernetes/validate-app-env.sh tmp/production.env
   ```

The API and worker must receive the same immutable secret version. Do not place
secret values in Helm values, GitHub variables, tickets, screenshots, shell
history, logs, analytics, or the evidence record.

## 3. Configure the independent Live webhook

1. Approve the exact production HTTPS URL ending in
   `/v1/payments/webhook`. The route must be public on TCP 443 and must not be
   protected by an interactive access challenge.
2. Before saving the Dashboard webhook, run the secret-free route probe against
   the exact approved URL:

   ```sh
   tools/api/ci/verify-razorpay-webhook-route.sh "$PRODUCTION_WEBHOOK_URL"
   ```

3. In **Live Mode**, create a new webhook using the independent production
   secret and a monitored production alert email.
4. Enable only `payment.captured`, `payment.failed`, `order.paid`, and
   `refund.processed`. Confirm the webhook is enabled and record its safe ID.
5. Independently compare mode, merchant, URL, events, alert owner, and secret
   manager reference with the secured evidence record. Never promote the Test
   Mode webhook.

## 4. Review capture and product configuration

1. Confirm **Automatic Capture** for all authorised Orders API payments and the
   reviewed late-authorisation/refund policy. Manual capture is not approved for
   phase one.
2. Confirm production plans, credit packs, prices, currencies, included credits,
   validity, billing-policy versions, checkout presentation, redirect origins,
   and refund-entitlement policy using a four-eyes review.
3. Export or record safe revision identifiers so the reviewed configuration can
   be compared after launch. Do not record customer or credential data.

## 5. Prove operational readiness

Every item below needs a dated evidence link and owner before traffic is enabled:

- a recent production database backup and a successful restore drill into an
  isolated database;
- an immutable application rollback drill, with the migration compatibility
  decision recorded;
- the payment incident-response and Razorpay key-rotation runbooks reviewed;
- reconciliation worker health, backlog visibility, safe support lookup, and
  audited issue remediation verified;
- the Razorpay dashboard and paging alerts active, routed, and test-paged;
- finance's daily captures, settlements, refunds, invoices, credit notes, and
  ledger reconciliation procedure, including discrepancy ownership and cutoff;
- tax/invoice compliance approval for the legal entity, invoice sequence,
  currency, tax treatment, retention, and refund credit notes; and
- customer-support escalation, status messaging, and duplicate-charge/refund
  handling reviewed.

The containment plan must stop new checkout-order creation while leaving the
webhook route, API verification, worker reconciliation, support lookup, and
refund processing available for already-created payments. Do not contain an
incident by deleting credentials or blocking signed webhook delivery.

## 6. Preflight and launch decision

Immediately before the launch window:

1. Deploy the approved immutable digest and run database migrations under the
   reviewed backup/rollback plan.
2. Verify `/livez`, `/readyz`, and the readiness response showing
   `razorpay_checkout: enabled`; confirm no credential material is returned.
3. Verify API and worker metrics, reconciliation freshness, database health,
   webhook acknowledgement latency, captured-but-unfulfilled count, signature
   failures, and paging delivery.
4. Re-run the production environment validator and public route probe. Compare
   the production key suffix, merchant account, webhook ID, events, and product
   revisions with the secured record.
5. The launch commander and independent approver record `GO` or `STOP`. Silence,
   missing evidence, an expired approval, or a failed check means `STOP`.

## 7. Controlled minimum-value smoke payment

Use an approved internal production billing account and the smallest supported
non-zero credit pack. This is a real charge; record consent, amount, currency,
and owner before starting.

1. Create one Billmesh checkout and record only safe Billmesh payment, Razorpay
   order, and Razorpay payment references.
2. Complete payment once. Do not retry with a new idempotency key while the
   outcome is unknown.
3. Verify Razorpay shows the order paid and payment captured.
4. Verify Billmesh has exactly one payment fulfilment, grant or subscription
   transition, ledger mutation, invoice, billing revision, and success event.
5. Verify the authoritative billing snapshot advanced only after capture, the
   signed webhook was acknowledged, and scheduled reconciliation reports no
   mismatch or orphan.
6. Confirm logs, traces, analytics, browser storage, support lookup, and evidence
   contain no credential, signature, raw payload, card/bank, or checkout-token
   data.

Any mismatch, duplicate value, missing invoice, unfulfilled capture, alert, or
unknown state triggers `STOP`, containment of new checkout creation, and the
incident procedure. Preserve webhook ingestion and reconciliation.

## 8. Full smoke refund

After the captured smoke payment is fully reconciled, finance issues a full
refund from the Razorpay Dashboard using its approved role. A refund cannot be
cancelled merely because the launch is stopped.

Verify the signed `refund.processed` delivery, one Billmesh refund, the approved
credit/entitlement reversal, ledger mutation, credit note, billing revision,
outbox event, provider dashboard state, and reconciliation result. A refund or
ledger mismatch is a launch-stopping finance incident.

## 9. Approval and rollback window

Record the final approval timestamp, release version and digest, all owners,
smoke payment/refund references, evidence links, known residual risks, and the
rollback decision deadline. During the decision window, watch the payment,
webhook, fulfilment, reconciliation, refund, database, and latency indicators.

Application rollback must preserve compatible migrations, production secrets,
the Live webhook, worker reconciliation, and financial evidence. If containment
is invoked, pause only new checkout creation. Resume traffic only after the
incident is resolved, affected payments are reconciled, alerts are clear, and a
new independent `GO` decision is recorded.

## Operator commands

These commands are run by the operator and must share only pass/fail output:

```sh
tools/api/ci/test-razorpay-production-go-live-contracts.sh
tools/api/kubernetes/validate-app-env.sh tmp/production.env
tools/api/ci/verify-razorpay-webhook-route.sh "$PRODUCTION_WEBHOOK_URL"
```

## Razorpay references

- [API keys](https://razorpay.com/docs/payments/dashboard/account-settings/api-keys/)
- [Payment capture settings](https://razorpay.com/docs/payments/payments/capture-settings/)
- [Payment webhooks](https://razorpay.com/docs/webhooks/setup-edit-payments/)
- [Payment Dashboard actions and refunds](https://razorpay.com/docs/payments/payments/dashboard/)
- [Security checklist](https://razorpay.com/security/checklist/)
