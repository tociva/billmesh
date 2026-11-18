# Billmesh development deployment

Billmesh development runs on Kubernetes and is released in two explicit phases:

1. Build and publish a tested, scanned, immutable API image.
2. Promote an already-published version to the development environment.

The deployment contains three workloads from the same image:

- `billmesh migrate up` runs as a blocking Helm hook Job.
- `billmesh api` runs as the HTTP API Deployment and owns the ClusterIP Service.
- `billmesh worker` runs as a separate background-worker Deployment.

The application chart does not create an Ingress, HTTPRoute, LoadBalancer, or
NodePort. The cluster's existing Cloudflare Tunnel must route the public API
hostname to the `billmesh-api` Service in the Billmesh namespace.

## Required infrastructure

- An ARM64 Kubernetes node pool.
- A namespace dedicated to Billmesh, normally `billmesh-development`.
- A restricted kubeconfig whose API server is `https://127.0.0.1:6443`.
- An interactive Cloudflare Access identity for workstation administration and
  a service token for CI, both allowed to reach the protected Kubernetes access
  hostname.
- An ECR repository with scan-on-push enabled.
- GitHub OIDC build and deployment roles. No static AWS access keys are used.
- A PostgreSQL runtime role and a separate migration/schema-owner role.
- A Cloudflare Tunnel route from the API hostname to
  `http://billmesh-api.billmesh-development.svc.cluster.local`.

The deployment role needs ECR read and scan permissions. The restricted
Kubernetes identity needs access to the Billmesh namespace to manage Helm
releases, Deployments, Jobs, Services, ServiceAccounts, and the three Secrets
used by the release (`billmesh-ecr`, `billmesh-api-runtime`, and
`billmesh-api-migration`).

### Provision AWS resources

The Terraform configuration creates or references the immutable ECR repository
and creates environment-bound GitHub OIDC roles:

```sh
cd tools/api/infrastructure/aws-development
cp terraform.tfvars.example terraform.tfvars
terraform init
terraform plan
terraform apply
terraform output
cd -
```

Fill the immutable numeric GitHub owner and repository IDs in `terraform.tfvars`.
They are REST API `id` values, not the `tociva`/`billmesh` names or GitHub
`node_id` values. Authenticate the GitHub CLI and retrieve both from the
repository response:

```sh
gh auth login -h github.com
gh api repos/tociva/billmesh \
  --jq '{github_repository_owner_id: (.owner.id|tostring), github_repository_id: (.id|tostring)}'
```

Copy the returned numbers into the variable file as strings, replacing the
placeholders entirely:

```hcl
github_repository_owner_id = "12345678"
github_repository_id       = "87654321"
```

The repository owner ID comes from `.owner.id`; the repository ID comes from
the top-level `.id`. Keep Terraform state in a secured remote backend;
populated variable files and local state are ignored and must not be committed.
Set `create_github_oidc_provider=true` only if this state is the designated
owner of the account-wide GitHub OIDC provider.

### Create the restricted Kubernetes identity

The development Kubernetes API uses the same protected access path as Daybook.
It is not reachable directly from a workstation. Install `cloudflared` and
`kubectl`, then use two terminals for the entire bootstrap session.

In **Terminal 1**, open the shared Cloudflare Access TCP proxy and leave it
running:

```sh
cloudflared access tcp \
  --hostname k8s-dev.tociva.com \
  --url 127.0.0.1:6443
```

Cloudflare opens the Access login in a browser when authentication is required.
This local process is only the workstation-to-Kubernetes administrative path;
it is separate from the server-side tunnel that serves the public Billmesh API.

In **Terminal 2**, select the existing Daybook development-cluster administrator
kubeconfig and verify that the proxy and cluster are reachable:

```sh
export KUBECONFIG="$HOME/.kube/tociva-dev-admin.yaml"

nc -vz 127.0.0.1 6443
kubectl config current-context
kubectl cluster-info
kubectl get nodes -o wide
```

The administrator kubeconfig must use `https://127.0.0.1:6443`. If the shared
file is not already present, retrieve a fresh K3s administrator kubeconfig using
the same secure procedure used for Daybook, save it as
`$HOME/.kube/tociva-dev-admin.yaml`, and set its mode to `600`. Do not commit or
share this file.

Still in Terminal 2, create the Billmesh namespace idempotently and apply its
namespace-scoped deployment identity:

```sh
kubectl create namespace billmesh-development \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f deploy/environments/development/deployer-rbac.yaml
```

Wait for the service-account token, then build the restricted CI kubeconfig.
Its local API endpoint is reached through the same Terminal 1 proxy:

```sh
until kubectl get secret billmesh-api-deployer-token \
  --namespace billmesh-development -o jsonpath='{.data.token}' | grep -q .; do
  sleep 1
done

mkdir -p "$HOME/.kube/billmesh-ci"
kubectl config view --raw --minify \
  -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | \
  openssl base64 -d -A > "$HOME/.kube/billmesh-ci/ca.crt"

CI_TOKEN="$(kubectl get secret billmesh-api-deployer-token \
  --namespace billmesh-development \
  -o jsonpath='{.data.token}' | openssl base64 -d -A)"

kubectl config set-cluster billmesh-development \
  --server=https://127.0.0.1:6443 \
  --certificate-authority="$HOME/.kube/billmesh-ci/ca.crt" \
  --embed-certs=true \
  --kubeconfig="$HOME/.kube/restricted-billmesh-kubeconfig.yaml"
kubectl config set-credentials billmesh-api-deployer \
  --token="$CI_TOKEN" \
  --kubeconfig="$HOME/.kube/restricted-billmesh-kubeconfig.yaml"
unset CI_TOKEN
kubectl config set-context billmesh-development \
  --cluster=billmesh-development \
  --user=billmesh-api-deployer \
  --namespace=billmesh-development \
  --kubeconfig="$HOME/.kube/restricted-billmesh-kubeconfig.yaml"
kubectl config use-context billmesh-development \
  --kubeconfig="$HOME/.kube/restricted-billmesh-kubeconfig.yaml"
chmod 600 "$HOME/.kube/restricted-billmesh-kubeconfig.yaml"
```

Verify the restricted identity while Terminal 1 is still running:

```sh
kubectl --kubeconfig "$HOME/.kube/restricted-billmesh-kubeconfig.yaml" \
  get deployments --namespace billmesh-development
kubectl --kubeconfig "$HOME/.kube/restricted-billmesh-kubeconfig.yaml" \
  auth can-i create deployments.apps --namespace billmesh-development
```

The second command must print `yes`. Stop the Terminal 1 proxy with `Ctrl-C`
only after the restricted kubeconfig has been created and verified.

## GitHub environments

Create two GitHub environments:

- `ecr-build`: ECR publication variables and any build approval rules.
- `billmesh-development-api`: deployment/runtime variables and secrets, with the desired
  reviewer protection rules.

The ignored `tmp/development.env` file is the local source of truth for both
environments. Start from the committed template:

```sh
mkdir -p tmp
cp tools/api/ci/development.env.example tmp/development.env
chmod 600 tmp/development.env
```

`tmp/` is ignored by Git and must never be committed. Fill every required
placeholder, then validate the complete configuration without displaying
secret values:

```sh
./tools/api/ci/upload-development-github-environment.sh \
  --check tmp/development.env
```

Generate the exact variable/secret files without uploading them:

```sh
./tools/api/ci/generate-development-github-environments.sh \
  tmp/development.env tmp/github-environments
```

After reviewing the generated key names, upload them:

```sh
gh auth status
./tools/api/ci/upload-development-github-environment.sh tmp/development.env
```

Use `--replace` only when the two GitHub environments must be made identical to
the local source of truth. It validates first, then deletes existing variables
and secrets from exactly `ecr-build` and `billmesh-development-api` before uploading the
replacement set.

Generate `KUBECONFIG_BASE64` without printing it on macOS:

```sh
base64 < "$HOME/.kube/restricted-billmesh-kubeconfig.yaml" | tr -d '\n' | pbcopy
```

## Database identities

`DATABASE_URL` is the restricted runtime connection used by the API and worker.
`MIGRATION_DATABASE_URL` is available only to the Helm migration Job. Local
development remains backward compatible: when `MIGRATION_DATABASE_URL` is not
set, the migration command falls back to `DATABASE_URL`.

If PostgreSQL uses a private CA, create that CA Secret in the namespace before
deployment and configure `POSTGRES_CA_SECRET_NAME` and
`POSTGRES_CA_SECRET_KEY`. Add the chart mount path to both database URLs, for
example `sslrootcert=/run/billmesh/tls/postgres-ca.crt`.

An independent outbound CA can be mounted with `OUTBOUND_CA_SECRET_NAME` and
`OUTBOUND_CA_SECRET_KEY`. When configured, the workflow sets
`SSL_CERT_FILE=/run/billmesh/tls/outbound-ca.crt`.

## Build a release

Run **Build and publish API image** from the `main` branch and choose a minor or
major increment. The workflow:

1. Runs static checks, unit tests, integration/E2E tests, and Helm contract tests.
2. Creates an annotated `api-v<major>.<minor>.0` tag on the tested revision.
3. Builds the ARM64 image with OCI metadata, provenance, and an SBOM.
4. Publishes both the version tag and `git-<full-revision>` tag.
5. Blocks on ECR HIGH/CRITICAL vulnerability findings.
6. Runs migrations and starts the published image in an isolated smoke stack.

Building does not change the Kubernetes environment.

## Deploy a release

Run **Deploy API image (development)** from `main` with:

- `version`: the version without the `api-v` prefix.
- `confirm`: exactly `DEPLOY`.
- `reason`: a short change or incident reference.

The workflow verifies that the tag belongs to `main`, that both ECR tags resolve
to the same digest, and that the digest still passes the vulnerability gate. It
then opens the authenticated Kubernetes tunnel, synchronizes runtime and
migration Secrets, refreshes ECR pull credentials, and performs an atomic Helm
upgrade pinned to the exact digest.

The migration Job completes before either Deployment is changed. A failed
migration or rollout causes Helm to restore the previous Kubernetes revision.
Database migrations are intentionally not reversed.

## Roll back

Run **Roll back API (development)** with a Helm revision, or `0` for the
immediately preceding revision. Type `ROLLBACK` and provide a reason.

Rollback opens the same authenticated Cloudflare tunnel, refreshes ECR pull
credentials, restores the selected Helm revision, waits for both Deployments,
and verifies public readiness. It does not run down migrations.

Inspect available revisions before choosing a non-zero revision:

```sh
helm history billmesh-api --namespace billmesh-development
```

## Local deployment checks

With Helm installed:

```sh
./tools/api/ci/test-kubernetes-deployment-contracts.sh
go test ./internal/config ./cmd/billmesh
docker build -f deploy/Dockerfile --target runtime .
```

The Compose files under `deploy/` remain the supported local, integration, and
end-to-end test environments. They are not used for Kubernetes promotion.
