#!/bin/sh
set -eu

fail() { echo "Kubernetes deployment contract failed: $*" >&2; exit 1; }
command -v helm >/dev/null 2>&1 || fail 'helm is required'

root=$(CDPATH= cd -- "$(dirname "$0")/../../.." && pwd)
chart=$root/deploy/charts/billmesh-api
values=$root/deploy/environments/development/api.values.yaml
routing=$root/deploy/environments/development/routing-services.yaml
rbac=$root/deploy/environments/development/deployer-rbac.yaml
workflow=$root/.github/workflows/api-development-deploy.yml
repository=123456789012.dkr.ecr.ap-south-1.amazonaws.com/billmesh/api
digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
rendered=$(mktemp)
trap 'rm -f "$rendered"' EXIT HUP INT TERM

helm lint "$chart" --values "$values" --set-string image.repository="$repository" --set-string image.digest="$digest" >/dev/null
helm template billmesh-api "$chart" --namespace billmesh-development --values "$values" --set-string image.repository="$repository" --set-string image.digest="$digest" --set-string revision=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa >"$rendered"

[ "$(grep -c '^kind: Deployment$' "$rendered")" -eq 2 ] || fail 'API and worker Deployments are required'
grep -Fq 'kind: Job' "$rendered" || fail 'migration Job is missing'
! grep -Eq '^kind: Service$' "$rendered" || fail 'Helm must not own development routing Services'
[ "$(grep -c '^kind: Service$' "$routing")" -eq 2 ] || fail 'administrator bootstrap must own both routing Services'
grep -Fq 'name: billmesh-api' "$routing" || fail 'API Service is missing'
grep -Fq 'name: billmesh-api-worker-metrics' "$routing" || fail 'worker metrics Service is missing'
[ "$(grep -c 'type: ClusterIP' "$routing")" -eq 2 ] || fail 'routing Services must be ClusterIP'
[ "$(grep -c 'app.kubernetes.io/component: api' "$routing")" -eq 2 ] || fail 'API Service labels and selector do not match'
[ "$(grep -c 'app.kubernetes.io/component: worker' "$routing")" -eq 2 ] || fail 'worker Service labels and selector do not match'
grep -Fq 'resources: [services]' "$rbac" || fail 'deployer cannot read routing Services'
grep -Fq 'verbs: [get, list, watch]' "$rbac" || fail 'routing Service access is not read-only'
! grep -Fq 'resources: [networkpolicies]' "$rbac" || fail 'deployer must not mutate NetworkPolicies'
! grep -Fq "'create services'" "$workflow" || fail 'deployment workflow must not require Service creation'
! grep -Fq "'patch services'" "$workflow" || fail 'deployment workflow must not require Service mutation'
grep -Fq 'helm.sh/hook: pre-install,pre-upgrade' "$rendered" || fail 'migration does not block installs and upgrades'
grep -Fq "image: \"$repository@$digest\"" "$rendered" || fail 'image is not digest pinned'
[ "$(grep -c 'name: billmesh-api-runtime' "$rendered")" -eq 3 ] || fail 'runtime Secret must be referenced by API, worker, and migration workloads'
grep -Fq 'runAsUser: 10001' "$rendered" || fail 'container UID contract is missing'
grep -Fq 'automountServiceAccountToken: false' "$rendered" || fail 'service-account tokens are enabled'
grep -Fq 'readOnlyRootFilesystem: true' "$rendered" || fail 'read-only root filesystem is missing'
grep -Fq 'allowPrivilegeEscalation: false' "$rendered" || fail 'privilege escalation is enabled'
grep -Fq 'drop:' "$rendered" || fail 'capability drop is missing'

for forbidden in 'kind: Ingress' 'kind: HTTPRoute' 'type: LoadBalancer' 'type: NodePort' 'hostPath:'; do
  ! grep -Fq "$forbidden" "$rendered" || fail "forbidden manifest content: $forbidden"
done
echo 'Kubernetes deployment contracts passed.'
