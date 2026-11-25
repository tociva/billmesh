# Razorpay zero-downtime key and webhook-secret rotation

API keys and webhook secrets are separate credentials and must be rotated as
separate changes. Use the secured inventory to record environment, credential
identifier (never the value), owner, issue time, activation time, verification,
and retirement time.

## Rotate the webhook secret

1. Generate a new high-entropy webhook secret in the Razorpay dashboard or
   approved secret manager. Keep HMAC verification enabled.
2. Deploy the current secret as `RAZORPAY_PREVIOUS_WEBHOOK_SECRET`, set
   `RAZORPAY_PREVIOUS_WEBHOOK_SECRET_EXPIRES_AT` to a short UTC grace deadline,
   and deploy the new value as `RAZORPAY_WEBHOOK_SECRET`.
3. Update Razorpay to send with the new secret. Deliver a signed Test Mode event
   and confirm success, request/event correlation, and no signature alert.
4. During the grace window, monitor current/previous-secret acceptance and
   reconcile any delayed delivery.
5. After the deadline, remove the previous secret and expiry in a second deploy.
   Confirm an old-secret fixture is rejected and the new secret remains valid.

Rollback before the grace deadline by restoring the old secret as current and
the new one as previous. After expiry, create a new controlled grace window;
never extend an expired credential silently.

## Rotate Razorpay API keys

1. Create a second Test/Live key pair without revoking the active pair. Store it
   in the runtime secret manager; never place it in Git, Helm values, logs, or a
   support ticket.
2. Deploy the new key ID and secret to API and worker together. Keep the old key
   active at Razorpay during validation.
3. Verify readiness, one non-charging provider read, Test Mode order creation,
   receipt recovery, checkout verification, and reconciliation. Monitor provider
   authentication, timeout, and error metrics.
4. Roll back the runtime secret if authentication failures appear. Once the new
   key has remained healthy through the observation window, revoke the old key.
5. Run reconciliation for the rotation window and update the secured inventory.

An emergency suspected-leak rotation skips the normal observation delay: deploy
the replacement, verify the minimum safe flow, revoke the exposed credential,
reconcile from the exposure time, and open a security incident.
