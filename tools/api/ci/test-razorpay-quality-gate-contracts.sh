#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../../.." && pwd)
consumer_root=${BILLMESH_WEB_ROOT:-"$repository_root/../billmesh-web"}
runner="$script_dir/run-razorpay-quality-gates.sh"
policy="$script_dir/verify-razorpay-test-policy.sh"
scanner="$script_dir/scan-razorpay-release.sh"
image_verifier="$script_dir/build-and-verify-api-image.sh"
gitleaks_config="$repository_root/.gitleaks.toml"
dockerfile="$repository_root/deploy/Dockerfile"
plan="$repository_root/docs/razorpay-integration-plan.md"
makefile="$repository_root/Makefile"
ci_workflow="$repository_root/.github/workflows/ci.yml"
pr_template="$repository_root/.github/pull_request_template.md"
consumer_package="$consumer_root/package.json"
consumer_journey="$consumer_root/apps/console/src/app/billing/checkout-journey.e2e.spec.ts"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }
require_text() {
  file=$1
  expected=$2
  grep -Fq -- "$expected" "$file" || fail "$file does not contain: $expected"
}

for file in "$runner" "$policy" "$scanner" "$image_verifier" "$gitleaks_config" "$dockerfile" "$plan" "$makefile" "$ci_workflow" "$pr_template"; do
  [ -f "$file" ] || fail "missing quality-gate asset: $file"
done

for gate in focused integration e2e race failure performance contracts policy security consumer all; do
  require_text "$runner" "$gate)"
done
pass 'quality runner exposes every required gate family'

require_text "$makefile" 'razorpay-quality:'
require_text "$makefile" 'razorpay-quality-contracts:'
require_text "$makefile" 'razorpay-test-policy:'
pass 'Makefile exposes stable quality-gate entry points'

require_text "$scanner" 'govulncheck@v1.7.0'
require_text "$scanner" 'gitleaks/gitleaks:v8.30.1'
require_text "$scanner" "git ls-files -- .env tmp"
require_text "$scanner" 'git /repo'
require_text "$scanner" '--config /repo/.gitleaks.toml'
require_text "$scanner" '--redact --no-banner --verbose'
require_text "$scanner" 'aquasecurity/trivy:0.76.2'
require_text "$scanner" '--helm-values /work/deploy/environments/development/api.values.yaml'
require_text "$scanner" '--helm-set-string image.repository=registry.invalid/billmesh/api'
require_text "$scanner" 'docker build'
require_text "$dockerfile" "'libcrypto3>=3.5.9-r2'"
require_text "$dockerfile" "'libssl3>=3.5.9-r2'"
require_text "$image_verifier" "info --installed 'libcrypto3>=3.5.9-r2' 'libssl3>=3.5.9-r2'"
require_text "$gitleaks_config" 'useDefault = true'
require_text "$gitleaks_config" 'deploy/test-certs/'
require_text "$gitleaks_config" 'targetRules = ["generic-api-key"]'
require_text "$gitleaks_config" 'tests/(e2e|testkit)'
pass 'release scan covers Go dependencies, secrets, filesystem, and runtime image'

require_text "$policy" 'tests/e2e/payments'
require_text "$policy" 'tests/e2e/subscriptions'
require_text "$policy" 'Scenario IDs'
require_text "$policy" "git -C \"\$repository_root\" diff --name-only \"\$base_ref...HEAD\""
require_text "$policy" 'production changes require a changed automated test'
require_text "$policy" 'unknown Scenario ID in PR body'
require_text "$policy" 'PR body must contain an exact executable test command'
require_text "$ci_workflow" 'Verify PR scenario evidence'
require_text "$ci_workflow" 'github.event.pull_request.base.sha'
require_text "$ci_workflow" 'github.event.pull_request.body'
require_text "$ci_workflow" 'verify-razorpay-test-policy.sh "${BASE_SHA}" "${RUNNER_TEMP}/pr-body.md"'
require_text "$pr_template" '## Scenario IDs'
require_text "$pr_template" '## TDD evidence'
require_text "$pr_template" '## Test command'
require_text "$pr_template" 'Failing test observed before implementation:'
if [ -f "$consumer_package" ] && [ -f "$consumer_journey" ]; then
  require_text "$consumer_package" '"quality:checkout"'
  require_text "$consumer_journey" "describe('Razorpay checkout journey'"
elif [ "${REQUIRE_BILLMESH_WEB:-false}" = true ]; then
  fail "consumer quality assets are required but unavailable under $consumer_root"
fi
pass 'payment test, PR scenario/TDD evidence, and composed consumer journey policies are mandatory'

for scenario in PERF-013 PERF-014 PERF-015 PAY-044 PAY-045 PAY-046; do
  require_text "$repository_root/tests/testdata/test-cases.md" "$scenario"
done
pass 'quality and mock-provider scenarios are named in the test manifest'

echo 'Razorpay quality-gate contract tests passed.'
