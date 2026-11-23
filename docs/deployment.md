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

## Bootstrap sequence

Complete these steps in order. GitHub environment names are part of the AWS
OIDC trust policy, and the final GitHub configuration requires both Terraform
outputs and a namespace-scoped Kubernetes credential.

### 1. Prepare GitHub

Authenticate the GitHub CLI with an account that can administer the repository:

```sh
gh auth login -h github.com
gh auth status -h github.com
```

Read the immutable numeric owner and repository IDs. These are REST API `id`
values, not names or `node_id` values:

```sh
gh api repos/tociva/billmesh \
  --jq '{github_repository_owner_id: (.owner.id|tostring), github_repository_id: (.id|tostring)}'
```

For `tociva/billmesh`, the expected values are:

```hcl
github_repository_owner_id = "217876362"
github_repository_id       = "1388804484"
```

Prepare `terraform.tfvars` and the known values in the ignored
`tmp/development.env` file. The script preserves external secrets and can be
rerun safely as later values become available:

```sh
./tools/api/ci/bootstrap-development-config.sh
```

Create the two repository environments before creating their OIDC roles:

```sh
gh api --method PUT repos/tociva/billmesh/environments/ecr-build
gh api --method PUT \
  repos/tociva/billmesh/environments/billmesh-development-api
```

Configure the desired reviewers and deployment protection rules for both
environments in **GitHub → Settings → Environments**. Variables and secrets are
uploaded after AWS and Kubernetes bootstrap is complete.

### 2. Authenticate and open the shared Kubernetes tunnel

The development Kubernetes API uses the same protected access path as Daybook.
It is not reachable directly from a workstation. Install `cloudflared` and
`kubectl`, then use two terminals.

In **Terminal 1**, authenticate explicitly. The command opens a browser; do not
share the temporary login URL if it is printed in the terminal:

```sh
cloudflared access login https://k8s-dev.tociva.com
```

After authentication succeeds, start the local TCP proxy and leave it running:

```sh
cloudflared access tcp \
  --hostname k8s-dev.tociva.com \
  --url 127.0.0.1:6443
```

Do not start a second proxy on port `6443`. This process is only the workstation
administrative path; it is separate from the server-side tunnel serving the
public Billmesh API.

In **Terminal 2**, select the existing development-cluster administrator
kubeconfig and verify TCP, TLS, and authenticated Kubernetes access:

```sh
export KUBECONFIG="$HOME/.kube/tociva-dev-admin.yaml"

nc -vz 127.0.0.1 6443
kubectl config current-context
kubectl config view --minify \
  -o jsonpath='{.clusters[0].cluster.server}{"\n"}'
curl --noproxy '*' --insecure --verbose \
  --connect-timeout 5 --max-time 15 \
  https://127.0.0.1:6443/version
kubectl --request-timeout=15s get --raw=/version
kubectl cluster-info
kubectl get nodes -o wide
```

The context may be named `default`; that is normal for the K3s administrator
kubeconfig. The server must be `https://127.0.0.1:6443`. An HTTP `401` or `403`
from `curl` proves TLS forwarding works because `curl` does not present the
client certificate. The authenticated `kubectl get --raw=/version` command must
return Kubernetes version JSON before continuing.

If the administrator kubeconfig is absent, retrieve a fresh K3s administrator
kubeconfig using the secure Daybook procedure, save it as
`$HOME/.kube/tociva-dev-admin.yaml`, and set its mode to `600`. Never commit or
share it.

#### Troubleshoot a TLS handshake timeout

A successful `nc` check proves only that the local proxy is listening. If
`curl` or `kubectl` reports `TLS handshake timeout`, stop the Terminal 1 proxy
with `Ctrl-C`, authenticate again, and restart one proxy with diagnostics:

```sh
cloudflared access login https://k8s-dev.tociva.com
cloudflared access tcp \
  --hostname k8s-dev.tociva.com \
  --url 127.0.0.1:6443 \
  --loglevel debug \
  --logfile "${TMPDIR:-/tmp}/tociva-k8s-access.log"
```

Repeat the TLS and Kubernetes checks from Terminal 2, then inspect the log:

```sh
curl --noproxy '*' --insecure --verbose \
  --connect-timeout 5 --max-time 15 \
  https://127.0.0.1:6443/version
kubectl --request-timeout=15s get --raw=/version
tail -n 100 "${TMPDIR:-/tmp}/tociva-k8s-access.log"
```

If TLS still does not complete, inspect the Cloudflare Access policy, the
shared connector's health, and the `k8s-dev.tociva.com` route to K3s. The
Billmesh namespace does not need to exist for `/version` to succeed.

### 3. Provision AWS with Terraform

From the repository root, create the ignored variable file without overwriting
an existing one:

```sh
cd tools/api/infrastructure/aws-development
test -f terraform.tfvars || cp terraform.tfvars.example terraform.tfvars
```

Review `terraform.tfvars`; the bootstrap script fills its GitHub IDs. Keep
`deploy_environment_name = "billmesh-development-api"` so the IAM trust policy
matches the deployment and rollback workflows. Then run:

```sh
terraform fmt terraform.tfvars
terraform init
terraform plan
terraform apply
terraform output
cd -
```

The configuration creates or references the immutable ECR repository and
creates environment-bound GitHub OIDC roles. Keep Terraform state in a secured
remote backend; populated variable files and local state must not be committed.
Set `create_github_oidc_provider=true` only if this state is the designated
owner of the account-wide GitHub OIDC provider.

### 4. Create the Billmesh namespace and CI identity

Keep Terminal 1's Cloudflare proxy running. From Terminal 2 or another terminal
using the administrator kubeconfig, create the namespace idempotently and apply
the namespace-scoped deployment identity:

```sh
kubectl create namespace billmesh-development \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f deploy/environments/development/deployer-rbac.yaml
```

Interactive administrators can continue using the existing administrator
kubeconfig. The following one-time step creates the least-privilege credential
used by the GitHub Actions deploy and rollback workflows:

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

Verify the restricted identity while the proxy is still running:

```sh
kubectl --kubeconfig "$HOME/.kube/restricted-billmesh-kubeconfig.yaml" \
  get deployments --namespace billmesh-development
kubectl --kubeconfig "$HOME/.kube/restricted-billmesh-kubeconfig.yaml" \
  auth can-i create deployments.apps --namespace billmesh-development
```

The second command must print `yes`. Stop the Terminal 1 proxy with `Ctrl-C`
after Kubernetes setup and verification are complete.

### 5. Populate the GitHub environments

The ignored `tmp/development.env` file is the local source of truth for
`ecr-build` and `billmesh-development-api`. Rerun the bootstrap script now that
Terraform outputs and the restricted kubeconfig exist:

```sh
./tools/api/ci/bootstrap-development-config.sh
```

The script fills AWS outputs, fixed Billmesh development values, and
`KUBECONFIG_BASE64` without printing secret values. Manually replace the
remaining placeholders for database credentials, Cloudflare Access service
tokens, IdNest clients, and session-encryption keys. It never invents or
overwrites those external secrets.

Validate the complete configuration without displaying secret values:

```sh
./tools/api/ci/upload-development-github-environment.sh \
  --check tmp/development.env
```

Optionally generate the exact variable and secret files for reviewing key
names, then upload them:

```sh
./tools/api/ci/generate-development-github-environments.sh \
  tmp/development.env tmp/github-environments
gh auth status -h github.com
./tools/api/ci/upload-development-github-environment.sh tmp/development.env
```

Use `--replace` only when both GitHub environments must be made identical to
the local source of truth. It validates first, then deletes existing variables
and secrets from exactly `ecr-build` and `billmesh-development-api` before
uploading the replacement set.

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
