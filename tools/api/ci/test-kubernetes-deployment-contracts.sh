#!/bin/sh
set -eu

fail() { echo "Kubernetes deployment contract failed: $*" >&2; exit 1; }
command -v helm >/dev/null 2>&1 || fail 'helm is required'

root=$(CDPATH= cd -- "$(dirname "$0")/../../.." && pwd)
chart=$root/deploy/charts/billmesh-api
values=$root/deploy/environments/development/api.values.yaml
repository=123456789012.dkr.ecr.ap-south-1.amazonaws.com/billmesh/api
digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
rendered=$(mktemp)
trap 'rm -f "$rendered"' EXIT HUP INT TERM

helm lint "$chart" --values "$values" --set-string image.repository="$repository" --set-string image.digest="$digest" >/dev/null
helm template billmesh-api "$chart" --namespace billmesh-development --values "$values" --set-string image.repository="$repository" --set-string image.digest="$digest" --set-string revision=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa >"$rendered"

[ "$(grep -c '^kind: Deployment$' "$rendered")" -eq 2 ] || fail 'API and worker Deployments are required'
grep -Fq 'kind: Job' "$rendered" || fail 'migration Job is missing'
grep -Fq 'kind: Service' "$rendered" || fail 'Service is missing'
grep -Fq 'type: ClusterIP' "$rendered" || fail 'Service is not ClusterIP'
grep -Fq 'helm.sh/hook: pre-install,pre-upgrade' "$rendered" || fail 'migration does not block installs and upgrades'
grep -Fq "image: \"$repository@$digest\"" "$rendered" || fail 'image is not digest pinned'
grep -Fq 'name: billmesh-api-runtime' "$rendered" || fail 'runtime Secret is not referenced'
grep -Fq 'name: billmesh-api-migration' "$rendered" || fail 'migration Secret is not referenced'
grep -Fq 'runAsUser: 10001' "$rendered" || fail 'container UID contract is missing'
grep -Fq 'automountServiceAccountToken: false' "$rendered" || fail 'service-account tokens are enabled'
grep -Fq 'readOnlyRootFilesystem: true' "$rendered" || fail 'read-only root filesystem is missing'
grep -Fq 'allowPrivilegeEscalation: false' "$rendered" || fail 'privilege escalation is enabled'
grep -Fq 'drop:' "$rendered" || fail 'capability drop is missing'

for forbidden in 'kind: Ingress' 'kind: HTTPRoute' 'type: LoadBalancer' 'type: NodePort' 'hostPath:'; do
  ! grep -Fq "$forbidden" "$rendered" || fail "forbidden manifest content: $forbidden"
done
echo 'Kubernetes deployment contracts passed.'
