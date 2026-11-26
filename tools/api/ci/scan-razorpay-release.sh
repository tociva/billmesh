#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../../.." && pwd)
image_ref=${BILLMESH_QUALITY_IMAGE_REF:-billmesh-razorpay-quality:local}

fail() { echo "Razorpay release scan failed: $*" >&2; exit 1; }
for command in docker git go gofmt; do
  command -v "$command" >/dev/null 2>&1 || fail "missing required command: $command"
done
cd "$repository_root"

[ -z "$(git ls-files -- .env tmp)" ] || fail 'local credential files must not be tracked'
[ -z "$(gofmt -l .)" ] || fail 'gofmt found unformatted Go files'
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...

docker run --rm \
  --volume "$repository_root:/repo:ro" \
  ghcr.io/gitleaks/gitleaks:v8.30.1 \
  git /repo --config /repo/.gitleaks.toml --redact --no-banner --verbose

docker run --rm \
  --volume "$repository_root:/work:ro" \
  ghcr.io/aquasecurity/trivy:0.76.2 \
  fs --scanners vuln,misconfig,secret --severity HIGH,CRITICAL --exit-code 1 \
  --helm-values /work/deploy/environments/development/api.values.yaml \
  --helm-set-string image.repository=registry.invalid/billmesh/api \
  --helm-set-string image.digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  --skip-dirs /work/.git --skip-dirs /work/deploy/test-certs /work

docker build --target runtime --tag "$image_ref" --file deploy/Dockerfile .
docker run --rm \
  --volume /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/aquasecurity/trivy:0.76.2 \
  image --severity HIGH,CRITICAL --exit-code 1 "$image_ref"

echo "Razorpay release security scans passed for $image_ref."
