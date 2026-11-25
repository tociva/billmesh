# Razorpay security and abuse-resistance review

This document is the Phase 11 security boundary for Billmesh's Razorpay
integration. HMAC verification is mandatory for every webhook. Network origin,
TLS termination, and IP filtering are defence in depth and never replace it.

## Trust boundaries and controls

| Boundary | Required control |
| --- | --- |
| Browser to Billmesh | Authenticated account and environment scope; verify the checkout signature over the server-stored order ID; re-fetch payment state from Razorpay before fulfilment. |
| Razorpay to public webhook | Public HTTPS on TCP 443 only; exactly one event-ID and signature header; 1 MiB body limit; HMAC over the exact raw body before parsing or database access. |
| Webhook parser to financial mutation | Valid UTF-8; maximum JSON nesting depth 64; allowlisted event types; merchant, amount, currency, provider environment, and stored-order checks. Unknown signed fields are ignored for forward compatibility and never copied into financial decisions. |
| Billmesh to Razorpay | HTTPS base URL validation; five-second overall timeout; bounded TLS, response-header, idle-connection, and per-host connection limits. |
| Billmesh to PostgreSQL | Maximum 20 and minimum 2 connections; finite connection lifetime and idle lifetime; transaction locks and uniqueness constraints for idempotency. |

The public webhook has an independent per-source token bucket (50 requests per
second with a 200-request burst by default) and a 64-request concurrent-work
gate. A rejected request returns `429` and `Retry-After: 1`. Provider retry and
durable event idempotency remain the recovery mechanisms. Operators should tune
these values only after comparing them with observed legitimate Razorpay bursts.

## Logging and secrets

Webhook logs contain only the Billmesh request ID and Razorpay account, event,
payment, order, and refund references. Do not log raw webhook bodies, signature
headers, API or webhook secrets, checkout tokens, or card/bank data. The generic
request log includes the request ID, method, path, and duration. Error responses
remain stable and contain no provider body.

Secret rotation accepts the previous webhook secret only until its configured
UTC expiry. API keys and webhook secrets must come from the runtime secret,
must not be placed in Helm values or logs, and must be rotated separately.

## Production exposure and TLS policy

The Helm chart creates only a `ClusterIP` service. It must not be changed to a
`NodePort` or `LoadBalancer`, and the application service must not be exposed
directly. The existing Cloudflare Tunnel or equivalent edge is responsible for:

- accepting only HTTPS on public TCP 443 for `/v1/payments/webhook`;
- redirecting or rejecting public plaintext HTTP rather than forwarding it;
- using TLS 1.2 or newer with a currently trusted certificate;
- forwarding internally to the ClusterIP service on HTTP/TCP 80;
- limiting the route to `POST /v1/payments/webhook` where the edge supports
  path and method policy; and
- preserving duplicate header fields so Billmesh can reject ambiguity.

Enable the chart NetworkPolicy in production and restrict ingress to the tunnel
namespace and pod labels. No UDP, alternate public port, or direct pod/service
address is supported for Razorpay delivery.

## Razorpay source-IP allowlisting decision

Source-IP allowlisting is optional defence in depth at the edge, not an
application authentication mechanism. Do not hard-code an address list in
Billmesh: provider ranges may change, proxying can replace the source address,
and a stale list can drop legitimate financial events.

If operations enables an allowlist, it must use Razorpay's then-current official
published ranges, run at the public edge before the tunnel, be monitored for
rejections, and have a tested emergency rollback. Exact-body HMAC verification,
event deduplication, and merchant/account checks remain mandatory even for an
allowlisted address.

## Threat-model review

| Threat | Mitigation and verification |
| --- | --- |
| Replay or duplicate delivery | Unique provider event identity plus raw-body digest; financial idempotency keys and transaction locks; concurrent replay, checkout, reconciliation, and refund tests. |
| Payload/header tampering | Strict single-value headers; lowercase 64-hex signature; constant-time HMAC comparison over exact raw bytes; signature verification before JSON parsing or financial lookup. |
| Parser or resource exhaustion | Body, depth, UTF-8, request-rate, concurrency, HTTP timeout, connection-pool, and database-pool limits. Unknown fields cannot affect normalized financial fields. |
| Cross-account, product, application, or environment fulfilment | Stored account ownership and product relations, provider environment, merchant account, provider order, amount, and currency are checked at each read/mutation boundary; isolation suites cover lists, checkout, capture, and reconciliation. |
| Leaked credentials or sensitive payment data | Runtime-secret injection, bounded previous-secret grace, redacted errors, correlation-only logs, no raw payload/signature logging. Rotate the affected API key and webhook secret, expire the previous secret, and reconcile the exposure window. |
| Provider or dependency compromise | Do not trust a signed event alone: bind it to locally created order state and expected merchant/amount/currency/environment; reconciliation reports disagreement rather than silently fulfilling it. |
| Operator misuse | Admin actions require scoped authorization and create audit records; reconciliation resolution is explicit and reviewable; raw financial data access follows production database access controls. |

Residual risk is accepted only for provider-controlled unknown fields and
provider availability. Unknown fields are ignored deliberately for compatible
schema evolution, while only normalized allowlisted fields can drive a mutation.
Provider outages result in retries or reconciliation, never speculative
fulfilment.
