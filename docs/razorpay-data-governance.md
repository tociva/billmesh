# Razorpay data retention and support access

## Retention schedule

| Data | Retention | Control |
| --- | --- | --- |
| Exact raw Razorpay webhook body | 30 days by default | Worker replaces `raw_payload` and the full JSON payload with normalized references after `RAZORPAY_RAW_PAYLOAD_RETENTION`; the SHA-256 digest remains for replay evidence. |
| Normalized provider-event identity and references | 400 days minimum | Restricted production database access; retain through the dispute/replay window, then delete only through an approved data-maintenance change. |
| Reconciliation attempts and issues | 7 years | Financial-operations access; operator actions and resolution notes are audited. |
| Payments, refunds, invoices, credit notes, and credit ledger | 7 years or the longer applicable statutory period | Finance-controlled, immutable where required; deletion needs legal/finance approval. |
| Admin audit log | 7 years | Append-only database trigger; security/compliance access. |
| Metrics | 90 days high resolution, 13 months recording rules | No customer/account/provider identifiers as labels. |
| Application logs | 30 days searchable, 1 year archive | Request IDs and provider references only; no raw bodies, signatures, keys, tokens, card, or bank data. |

The 7-year defaults require confirmation by legal and finance for every country
in which Billmesh operates. A legal hold overrides deletion. Shortening a
retention period requires documented legal, finance, security, and data-owner
approval.

## Access controls

- Raw payloads have no API route. Production database break-glass access is
  time-bound, approved, logged, and limited to payments engineering/security.
- Support uses the audited admin lookup endpoint and receives only normalized
  payment, provider-event, and reconciliation references.
- Finance can access invoices, credit notes, refunds, settlement evidence, and
  reconciliation outcomes but not API keys, webhook secrets, or signatures.
- Metrics and dashboards use bounded operational labels only. Account IDs,
  customer IDs, payment/order/event/refund IDs, request IDs, receipts, and email
  addresses are forbidden as metric labels.
- Exports, screenshots, and incident attachments follow the same restrictions;
  provider references are shared only in approved support systems.

## Deletion and verification

The hourly worker maintenance pass redacts expired raw webhook content while
preserving the normalized references and digest needed for safe support and
deduplication. Monitor `billmesh_razorpay_raw_payloads_pending_purge`; a non-zero
value after two maintenance intervals is an operational failure. Quarterly,
security verifies retention queries, access grants, audit immutability, dashboard
label cardinality, and a sample support lookup.
