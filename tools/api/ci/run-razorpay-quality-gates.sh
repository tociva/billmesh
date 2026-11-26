#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../../.." && pwd)
consumer_root=${BILLMESH_WEB_ROOT:-"$repository_root/../billmesh-web"}

fail() { echo "Razorpay quality gate failed: $*" >&2; exit 1; }
usage() {
  cat >&2 <<'EOF'
usage: run-razorpay-quality-gates.sh GATE

GATE is one of:
  focused      Go unit, fake-provider, fixture, and provider-client tests
  integration  PostgreSQL integration suites
  e2e          complete backend E2E suite against the fake provider
  race         race detector on payment concurrency components
  failure      provider failure, refund, reconciliation, and recovery tests
  performance  load benchmarks, including checkout and webhook peaks
  contracts    OpenAPI, deployment, Dashboard, and route/test contracts
  policy       skipped-test and PR scenario-evidence policy
  security     static, dependency, secret, filesystem, and image scans
  consumer     console checkout unit, component, accessibility, journey, build
  all          every gate above in release order
EOF
  exit 2
}

[ "$#" -eq 1 ] || usage
gate=$1
cd "$repository_root"

case "$gate" in
  focused)
    TEST_SUITE_NAME='Razorpay focused tests' ./scripts/test-output.sh \
      go test -v -count=1 ./api ./internal/... ./tests/unit/... ./tests/fakeserver ./tests/testkit
    ;;
  integration)
    make test-integration
    ;;
  e2e)
    make test-e2e
    ;;
  race)
    TEST_SUITE_NAME='Razorpay race tests' ./scripts/test-output.sh \
      go test -v -race -count=1 ./internal/app ./internal/database ./tests/fakeserver ./tests/testkit
    ;;
  failure)
    TEST_SUITE_NAME='Razorpay failure and recovery tests' ./scripts/test-output.sh \
      go test -v -count=1 -run 'Test.*(Razorpay|Provider|Reconciliation|Refund|Capture).*' \
      ./internal/app ./tests/fakeserver ./tests/testkit
    ;;
  performance)
    make test-performance
    ;;
  contracts)
    make openapi-check
    ./tools/api/ci/test-razorpay-environment-contracts.sh
    ./tools/api/ci/test-razorpay-dashboard-setup-contracts.sh
    ./tools/api/ci/test-razorpay-production-go-live-contracts.sh
    ./tools/api/ci/test-razorpay-quality-gate-contracts.sh
    ./tools/api/ci/test-kubernetes-deployment-contracts.sh
    ;;
  policy)
    ./tools/api/ci/verify-razorpay-test-policy.sh
    ;;
  security)
    ./tools/api/ci/scan-razorpay-release.sh
    ;;
  consumer)
    [ -f "$consumer_root/package.json" ] || fail "consumer repository not found: $consumer_root"
    (cd "$consumer_root" && pnpm run quality:checkout)
    ;;
  all)
    for required_gate in focused integration e2e race failure performance contracts policy security consumer; do
      "$0" "$required_gate"
    done
    ;;
  *) usage ;;
esac
