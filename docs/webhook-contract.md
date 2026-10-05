# Billmesh Webhook Version 2 Contract

Billmesh webhook version 2 delivers cache-invalidation events. Event data may
help with diagnostics and presentation, but a consumer changes effective access
only after fetching a newer authoritative billing snapshot.

## Delivery protocol

Billmesh sends an HTTP `POST` with `Content-Type: application/json` and:

- `X-Billmesh-Event-ID` — immutable event UUID;
- `X-Billmesh-Event-Type` — registered event type;
- `X-Billmesh-Webhook-Version: 2`;
- `X-Billmesh-Timestamp` — Unix timestamp in seconds;
- `X-Billmesh-Signature` — current-secret signature; and
- `X-Billmesh-Previous-Signature` during a configured rotation grace period.

The signature is lowercase hexadecimal HMAC-SHA256 over the UTF-8 timestamp,
one period byte, and the exact raw request body:

```text
HMAC_SHA256(secret, timestamp + "." + raw_body)
```

Receivers verify the signature against raw bytes before parsing JSON, compare in
constant time, and reject timestamps more than five minutes in the past or
future. During rotation they accept a valid current or previous signature until
the published grace period ends.

## Envelope

The `BillmeshWebhookEventV2` schema in OpenAPI is authoritative. It contains the
event ID and type, occurrence time, account and product identifiers, aggregate,
billing revision, snapshot URL, and event-specific `data`.

Supported event types are enumerated by `WebhookEventType` in OpenAPI. Endpoint
registration rejects unknown filters. New event types are additive only when a
receiver that subscribes to all events is required to tolerate unknown types;
otherwise a contract version change is required.

## Delivery semantics

- Delivery is at least once. Duplicates are expected.
- No global ordering is guaranteed. The billing revision determines freshness.
- Billmesh currently attempts delivery up to eight times with exponential
  backoff beginning at one second and a five-second request timeout.
- A non-2xx response or transport failure is retryable until attempts are
  exhausted.
- Terminal failures are retained for operator replay and raise an operational
  alert.
- Secret rotation may overlap current and previous signatures for up to seven
  days; one day is the default.

## Receiver requirements

The receiver must:

1. validate timestamp and signature;
2. durably store and uniquely constrain the event ID before returning 2xx;
3. acknowledge duplicates without applying an effect twice;
4. enqueue asynchronous processing;
5. conditionally fetch `/v1/billing-snapshot` using the stored ETag;
6. replace its projection only with an equal or newer revision; and
7. periodically reconcile snapshots even when webhook delivery is healthy.

The receiver must not activate, downgrade, cancel, or otherwise mutate access
directly from event `data`.
