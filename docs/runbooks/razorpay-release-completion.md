# Razorpay release completion audit

This gate records that the Razorpay integration is complete; it does not create
credentials, configure Razorpay, deploy software, initiate a payment, issue a
refund, or approve its own evidence. Run it only after the Phase 15 Test Mode
acceptance and Phase 16 production go-live records are independently approved.

## Required inputs

Prepare three local files without printing their contents:

1. the reviewed `docs/razorpay-integration-plan.md`;
2. a secured copy of
   `docs/razorpay-release-completion-evidence.example.env`; and
3. the exact ignored production runtime environment file.

The completion evidence stores references, versions, timestamps, defect count,
and sign-offs. It must never store API/webhook secrets, signatures, raw webhook
payloads, payment-instrument data, tokens, passwords, or database credentials.
The production environment remains in the approved secret workflow and is read
only by the existing secret-safe validator.

## Checklist and future scope

Every applicable checkbox before Phase 17 must be checked. An intentionally
deferred item may remain open only when its checklist line includes a stable
marker such as:

```markdown
- [ ] Add recurring mandates. <!-- future-scope: RECURRING -->
```

The evidence file must then contain a separately approved reference:

```dotenv
FUTURE_SCOPE_RECURRING_REFERENCE=approval://architecture/recurring
```

A marker is not an approval. The referenced record must describe the excluded
scope, owner, risk acceptance, dependencies, and re-entry criteria. Do not use
future scope to waive a defect in the released checkout, payment, webhook,
refund, reconciliation, ledger, invoice, security, or support behavior.

## Evidence requirements

- Retain the complete automated quality-gate output and Phase 15 acceptance
  evidence.
- Record an OpenAPI and consumer-documentation parity review covering checkout,
  confirmation, webhook, refund, and reconciliation behavior.
- Validate the exact production configuration. It must declare `production`,
  use `https://api.razorpay.com`, an `rzp_live_` key, and the approved `acc_`
  merchant identity. Placeholder, Test Mode, and cross-environment credentials
  fail the gate.
- Link the Phase 16 launch decision, controlled payment/refund evidence, release
  version, immutable commit, and completion timestamp.
- Set `P0_P1_OPEN_COUNT=0`. A risk acceptance cannot convert a known P0 or P1
  defect into a passing result.
- Retain independent `ENGINEERING_SIGNOFF_REFERENCE`, security, finance,
  product, operations, and support sign-off references.

## Run the audit

Run the mocked contract first, then the real evidence verifier:

```sh
tools/api/ci/test-razorpay-release-completion-contracts.sh

tools/api/ci/verify-razorpay-release-completion.sh \
  docs/razorpay-integration-plan.md \
  tmp/razorpay-release-completion.env \
  tmp/production.env
```

Share only the final pass/fail lines. Never paste either environment file into
CI logs, tickets, chat, or release evidence. A failure leaves every Phase 17
checkbox open. After a pass, reviewers compare the referenced records, check
the six Phase 17 boxes, record the release approval, and retain the immutable
evidence bundle.
