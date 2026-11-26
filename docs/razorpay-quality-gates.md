# Razorpay automated quality gates

These gates keep Razorpay development fully offline. They use deterministic
credentials, the fake provider, signed local webhook fixtures, PostgreSQL test
containers, and the consumer's simulated browser journey. They never require a
Razorpay account, real API key, Dashboard webhook, or public endpoint.

## Gate matrix

| Gate | Command | Acceptance evidence |
| --- | --- | --- |
| Focused backend | `tools/api/ci/run-razorpay-quality-gates.sh focused` | Unit, provider-client, fake-provider, fixture, payment, invoice, and configuration tests pass |
| PostgreSQL integration | `tools/api/ci/run-razorpay-quality-gates.sh integration` | Financial integration tests pass against disposable PostgreSQL with the race detector |
| Complete E2E | `tools/api/ci/run-razorpay-quality-gates.sh e2e` | Integration, route coverage, security, reliability, and E2E suites pass against the fake provider |
| Race/concurrency | `tools/api/ci/run-razorpay-quality-gates.sh race` | Webhook, fulfilment, order, reconciliation, refund, database, and fixture race checks pass |
| Provider failure/recovery | `tools/api/ci/run-razorpay-quality-gates.sh failure` | Provider failure classification and recovery-focused tests pass |
| Load | `tools/api/ci/run-razorpay-quality-gates.sh performance` | Checkout-order and captured-webhook peak benchmarks pass with the existing performance plan |
| Contracts | `tools/api/ci/run-razorpay-quality-gates.sh contracts` | OpenAPI, environment, Dashboard, quality, Helm, and deployment contracts pass |
| Test policy | `tools/api/ci/run-razorpay-quality-gates.sh policy` | No skipped/focused Razorpay test exists; PRs must link named scenarios and TDD evidence |
| Security scans | `tools/api/ci/run-razorpay-quality-gates.sh security` | Formatting, vet, govulncheck, Gitleaks, Trivy filesystem, and Trivy runtime-image scans pass |
| Consumer | `tools/api/ci/run-razorpay-quality-gates.sh consumer` | Console formatting, lint, unit, component, accessibility, composed subscription and credit-pack checkout journeys, and production build pass |

`tools/api/ci/run-razorpay-quality-gates.sh all` runs the complete matrix in
release order. Run the individual gates while diagnosing failures; retain the
final `all` output for release evidence.

## Named scenario and PR policy

The pull-request template requires scenario IDs, the initial failing-test
reason, the final passing result, and exact commands. On a pull request that
changes production API, application, configuration, database, or fake-provider
surfaces, CI calls:

```sh
tools/api/ci/verify-razorpay-test-policy.sh BASE_SHA PR_BODY_FILE
```

The gate rejects production changes without a changed automated test, rejects
unknown scenario IDs, and requires the test-command evidence section. The same
policy rejects `Skip`, focused, or quarantined tests in backend payment paths and
the consumer checkout directory.

## Security scanner versions

The release scan pins govulncheck `v1.7.0`, Gitleaks `v8.30.1`, and Trivy
`0.76.2`. It scans called Go dependency vulnerabilities, repository secrets and
misconfiguration, then builds the runtime stage and scans the resulting image
for high or critical findings. The release image retains the independent ECR
scan gate during publication. Gitleaks extends its default rules and exempts
only `deploy/test-certs/`, whose intentionally committed key is documented as a
public fixture for the disposable IdNest E2E server. Failed secret scans print
redacted rule, file, and line evidence so false positives can be reviewed
without exposing candidate values. The scan reads Git-tracked history, rejects
tracked `.env` or `tmp/` credential files, and therefore neither reads nor
allowlists ignored developer-local credentials. Rule-specific exceptions
require both an exact deterministic test-secret pattern and an executable test
path; the Docker `api:8080` false positive is similarly bound to its exact test
line and file.

Trivy renders the Helm chart with the development values and deterministic
non-secret image placeholders, so schema validation cannot silently skip the
chart. Development routing Services are administrator-owned bootstrap
resources; the application deployer can read them but cannot mutate Services
or NetworkPolicies.

## Evidence handling

- Record command, date, commit, pass/fail result, and CI/run reference.
- Do not paste raw secrets, provider payloads, tokens, or environment files into
  evidence.
- Any failure keeps its Phase 14 checkbox open until the defect is fixed and the
  exact gate is rerun.
- A skipped checkout/payment scenario is a failed gate unless it has been moved
  to an approved future-scope document with an owner and resolution date.
