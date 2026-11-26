# API deployment helpers

These helpers implement the Billmesh image and Kubernetes deployment contract.

- `bootstrap-development-config.sh` discovers immutable GitHub IDs, prepares
  Terraform variables, imports matching values from the root `.env` when the
  deployment value is still missing, fills known development defaults (with
  `KUBERNETES_ACCESS_HOSTNAME` available as an environment override), and imports
  available Terraform outputs and the restricted kubeconfig without replacing
  populated values.
- `upload-development-github-environment.sh` validates the ignored canonical
  configuration, splits variables from secrets, and optionally uploads them.
- `generate-development-github-environments.sh` performs generation only.
- `build-and-verify-api-image.sh` builds the local ARM64 runtime image, checks
  its platform, non-root user, and patched OpenSSL and zlib floors, then runs
  migrations and the API smoke test against a disposable PostgreSQL database.
- `gate-ecr-image-scan.sh` blocks images with HIGH or CRITICAL ECR findings.
- `smoke-test-api-image.sh` migrates a disposable database and verifies the
  published image's readiness and liveness endpoints.
- `test-kubernetes-deployment-contracts.sh` lints and renders the Helm chart and
  checks digest pinning, workload separation, and pod hardening.
- `verify-razorpay-webhook-route.sh` sends a secret-free invalid-signature probe
  and accepts only the expected Billmesh webhook response, proving that the
  public HTTPS route reaches the application.
- `test-razorpay-dashboard-setup-contracts.sh` verifies the fixed Dashboard
  runbook, secret-safe operational inventory, and route verifier contracts.
- `test-razorpay-production-go-live-contracts.sh` verifies the Live/Test
  isolation, production evidence, smoke-payment/refund, and rollback gates.
- `test-razorpay-release-completion-contracts.sh` uses mock evidence to verify
  the final checklist, future-scope, configuration, defect, and sign-off gate.
- `verify-razorpay-release-completion.sh` validates the reviewed plan,
  secret-free completion evidence, and exact production environment without
  printing either environment file.
- `run-razorpay-quality-gates.sh` is the release-order entry point for focused,
  PostgreSQL, E2E, race, failure, load, contract, policy, scan, and consumer
  checks.
- `test-razorpay-quality-gate-contracts.sh` prevents required quality-gate
  commands, scenarios, or consumer assets from silently disappearing.
- `verify-razorpay-test-policy.sh` rejects skipped payment tests and requires
  named scenario plus TDD evidence for relevant production pull requests.
- `scan-razorpay-release.sh` runs pinned Go dependency, secret, filesystem,
  misconfiguration, and runtime-image vulnerability gates.

See the [development deployment runbook](../../../docs/deployment.md) for the
complete operator procedure.
