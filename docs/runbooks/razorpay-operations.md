# Razorpay operations runbook

Use the Billmesh payment ID as the primary incident reference. Record the
request ID, provider order/payment/event/refund references, environment, first
observed time, dashboard snapshot, and incident owner. Never paste API keys,
webhook secrets, signatures, raw webhook bodies, card details, bank details, or
checkout tokens into tickets or chat.

The support-safe lookup endpoint is:

```text
GET /v1/admin/payments/lookup?payment_id=<uuid>
GET /v1/admin/payments/lookup?provider_order_id=<order_id>
GET /v1/admin/payments/lookup?provider_payment_id=<payment_id>
GET /v1/admin/payments/lookup?event_id=<event_id>
GET /v1/admin/payments/lookup?request_id=<request_id>
```

It requires the admin client, accepts exactly one key, returns normalized
references and reconciliation issues without raw payloads, and writes an audit
record for every successful lookup.

## Webhook disablement or signature failures

1. Confirm `BillmeshRazorpayInvalidSignatureSpike` and compare failures with a
   known Test/Live environment and the configured merchant account.
2. Do not disable HMAC verification. Check whether Razorpay is using the current
   or still-valid previous webhook secret and whether the edge altered the body
   or combined duplicate headers.
3. If the endpoint must be disabled because it is under active abuse, return a
   deliberate non-2xx response at the edge, record the start time, and keep the
   worker/reconciliation running. Do not acknowledge undurable events.
4. Restore the route after rotation or edge repair, request redelivery from the
   provider dashboard, and reconcile the entire disabled interval.

## Delayed or failed delivery

1. Check webhook receipt rate, processing failures, acknowledgement p95, API
   pod saturation, PostgreSQL availability, and the 64-request admission gate.
2. Look up a delayed event ID. A stored/processed event can be safely replayed;
   the same event ID and exact body are idempotent.
3. For events absent from Billmesh, use provider redelivery. Never synthesize a
   captured event from a support ticket.
4. Confirm the payment, invoice/credit note, ledger, wallet, and outbox event
   converge before closing the incident.

## Provider outage

1. Confirm provider status classes, timeouts, rate limits, and whether failures
   affect create-order, payment verification, or reconciliation operations.
2. Keep checkout failures explicit (`503`) and do not invent provider order IDs.
   Ambiguous order attempts must be recovered using their deterministic receipt.
3. Keep webhook ingestion and reconciliation enabled when they remain healthy.
4. After recovery, monitor order recovery, reconciliation backlog, orphan-order
   issues, and captured-but-unfulfilled payments until they return to baseline.

## Ambiguous order creation

1. Search by Billmesh payment/request ID and inspect the deterministic receipt.
2. Let automatic receipt-based recovery finish. Do not ask the customer to retry
   with a new idempotency key while the original outcome is unknown.
3. If multiple provider orders match one receipt, stop fulfilment, acknowledge
   the reconciliation issue, and escalate to payments engineering and finance.
4. Resume or refund only after the provider dashboard and Billmesh audit trail
   identify the authoritative charge.

## Missed capture or captured but unfulfilled

1. Page immediately when `BillmeshRazorpayCapturedUnfulfilled` is non-zero.
2. Search by payment/order/payment-reference and compare provider amount,
   currency, merchant account, environment, and status with the stored payment.
3. Allow reconciliation to apply an exact captured payment. A mismatch must
   remain unfulfilled and become an operator-visible reconciliation issue.
4. Verify exactly one invoice, grant/subscription transition, ledger mutation,
   billing revision, and outbox event before resolution.

## Duplicate charge

1. Identify every provider payment and order using the deterministic receipt,
   account, amount, currency, and time window.
2. Do not fulfil the second charge. Preserve both provider references and open
   a reconciliation issue.
3. Finance chooses the charge to retain and performs the duplicate refund in
   Razorpay. Billmesh consumes the resulting `refund.processed` event.
4. Confirm one fulfilment and the matching refund/credit note before notifying
   the customer.

## Refund or ledger mismatch

1. Search by provider refund or payment reference and inspect the open issue.
2. Compare captured total, all provider refunds, Billmesh refunds, credit notes,
   entitlement-reversal policy, grant remaining value, wallet balance, and
   refund ledger entries.
3. Never edit the append-only ledger or finalized invoice directly. Correct the
   upstream record through an approved, audited remediation.
4. Acknowledge while investigating; resolve only with a support-safe note and
   finance evidence.

## Reconciliation failure

1. Check worker health, provider API outcomes, backlog, authentication failures,
   checkpoints, and the newest reconciliation attempt.
2. Authentication or invalid-response failures require operator attention;
   transient failures use bounded retry with jitter.
3. Use the audited `retry` action only after fixing the cause. Never repeatedly
   retry a permanent mismatch.
4. Escalate open finance mismatches to finance and payments engineering. Close
   only after the backlog and all affected issues converge.

## Escalation and closure

Page payments engineering for signature spikes, captured-but-unfulfilled value,
multiple charges, provider authentication failure, or ledger mismatch. Include
finance for refunds, settlement, invoices, credit notes, or ledger corrections.
Close only with an audited lookup, reconciled financial state, customer-impact
assessment, and a recorded prevention or follow-up owner.
