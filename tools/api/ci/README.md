# API deployment helpers

These helpers implement the Billmesh image and Kubernetes deployment contract.

- `bootstrap-development-config.sh` discovers immutable GitHub IDs, prepares
  Terraform variables, imports matching values from the root `.env` when the
  deployment value is still missing, fills known development defaults, and
  imports available Terraform outputs and the restricted kubeconfig without
  replacing populated values.
- `upload-development-github-environment.sh` validates the ignored canonical
  configuration, splits variables from secrets, and optionally uploads them.
- `generate-development-github-environments.sh` performs generation only.
- `gate-ecr-image-scan.sh` blocks images with HIGH or CRITICAL ECR findings.
- `smoke-test-api-image.sh` migrates a disposable database and verifies the
  published image's readiness and liveness endpoints.
- `test-kubernetes-deployment-contracts.sh` lints and renders the Helm chart and
  checks digest pinning, workload separation, and pod hardening.

See the [development deployment runbook](../../../docs/deployment.md) for the
complete operator procedure.
