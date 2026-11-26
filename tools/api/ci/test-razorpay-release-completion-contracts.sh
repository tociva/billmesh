#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../../.." && pwd)
verifier="$script_dir/verify-razorpay-release-completion.sh"
runbook="$repository_root/docs/runbooks/razorpay-release-completion.md"
evidence_template="$repository_root/docs/razorpay-release-completion-evidence.example.env"
manifest="$repository_root/tests/testdata/test-cases.md"
temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/billmesh-razorpay-completion-test.XXXXXX")
cleanup() { rm -rf -- "$temporary_dir"; }
trap cleanup EXIT HUP INT TERM

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }
[ -x "$verifier" ] || fail "completion verifier is not executable: $verifier"
require_text() {
  file=$1
  expected=$2
  grep -Fq -- "$expected" "$file" || fail "$file does not contain: $expected"
}

for file in "$runbook" "$evidence_template" "$manifest"; do
  [ -f "$file" ] || fail "missing release-completion asset: $file"
done
require_text "$runbook" 'Every applicable checkbox before Phase 17 must be checked.'
require_text "$runbook" 'P0_P1_OPEN_COUNT=0'
require_text "$runbook" 'ENGINEERING_SIGNOFF_REFERENCE'
require_text "$evidence_template" 'STAGING_ACCEPTANCE_EVIDENCE_REFERENCE='
require_text "$evidence_template" 'GO_LIVE_EVIDENCE_REFERENCE='
require_text "$evidence_template" 'SUPPORT_SIGNOFF_REFERENCE='
require_text "$manifest" 'TEST-006'
pass 'completion runbook, evidence template, and named scenario are present'

write_plan() {
  output=$1
  pre_completion_line=$2
  cat >"$output" <<EOF
# Razorpay integration plan

- [x] Implement and verify the applicable integration scope.
$pre_completion_line

## 17. Completion criteria

- [ ] Record final completion after this verifier passes.
EOF
}

write_evidence() {
  output=$1
  extra=${2:-}
  p0_count=${3:-0}
  cat >"$output" <<EOF
AUTOMATED_TEST_EVIDENCE_REFERENCE=ci://quality/all/123
STAGING_ACCEPTANCE_EVIDENCE_REFERENCE=vault://razorpay/staging/acceptance
OPENAPI_CONSUMER_PARITY_REFERENCE=review://contracts/456
PRODUCTION_CONFIGURATION_EVIDENCE_REFERENCE=vault://razorpay/production/configuration
GO_LIVE_EVIDENCE_REFERENCE=vault://razorpay/production/go-live
RELEASE_VERSION=v1.0.0
RELEASE_COMMIT=0123456789abcdef
COMPLETED_AT=2099-01-02T03:04:05Z
P0_P1_OPEN_COUNT=$p0_count
ENGINEERING_SIGNOFF_REFERENCE=approval://engineering/1
SECURITY_SIGNOFF_REFERENCE=approval://security/1
FINANCE_SIGNOFF_REFERENCE=approval://finance/1
PRODUCT_SIGNOFF_REFERENCE=approval://product/1
OPERATIONS_SIGNOFF_REFERENCE=approval://operations/1
SUPPORT_SIGNOFF_REFERENCE=approval://support/1
$extra
EOF
}

write_production_environment() {
  output=$1
  key_id=${2:-rzp_live_completion_fixture}
  base_url=${3:-https://api.razorpay.com}
  cat >"$output" <<EOF
BILLMESH_ENVIRONMENT=production
DATABASE_URL=postgres://billmesh:fixture@database.example:5432/billmesh
DELEGATION_ISSUER=https://auth.example/delegation
DELEGATION_DISCOVERY_URL=https://auth.example/.well-known/delegation
DELEGATION_AUDIENCE=https://api.example
CONSOLE_APP_ORIGIN=https://console.example
ADMIN_APP_ORIGIN=https://admin.example
BFF_CONSOLE_ISSUER=https://auth.example
BFF_CONSOLE_CLIENT_ID=billmesh-console
BFF_CONSOLE_CLIENT_SECRET=console-fixture-value
BFF_CONSOLE_AUDIENCE=billmesh
BFF_CONSOLE_REDIRECT_URI=https://api.example/api/v1/auth/console/callback
BFF_CONSOLE_POST_LOGOUT_REDIRECT_URI=https://api.example/api/v1/auth/console/logout/callback
BFF_CONSOLE_STANDALONE_LOGOUT_URI=https://auth.example/logout
BFF_ADMIN_ISSUER=https://auth.example
BFF_ADMIN_CLIENT_ID=billmesh-admin
BFF_ADMIN_CLIENT_SECRET=admin-fixture-value
BFF_ADMIN_AUDIENCE=billmesh
BFF_ADMIN_REDIRECT_URI=https://api.example/api/v1/auth/admin/callback
BFF_ADMIN_POST_LOGOUT_REDIRECT_URI=https://api.example/api/v1/auth/admin/logout/callback
BFF_ADMIN_STANDALONE_LOGOUT_URI=https://auth.example/logout
BFF_SESSION_ENCRYPTION_KEYS=v1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
RAZORPAY_BASE_URL=$base_url
RAZORPAY_KEY_ID=$key_id
RAZORPAY_KEY_SECRET=provider-fixture-value
RAZORPAY_WEBHOOK_SECRET=webhook-fixture-value
RAZORPAY_ACCOUNT_ID=acc_completion_fixture
EOF
}

expect_valid() {
  name=$1
  plan=$2
  evidence=$3
  environment=$4
  "$verifier" "$plan" "$evidence" "$environment" >"$temporary_dir/output.log" 2>&1 || {
    cat "$temporary_dir/output.log" >&2
    fail "$name unexpectedly failed"
  }
  pass "$name"
}

expect_invalid() {
  name=$1
  plan=$2
  evidence=$3
  environment=$4
  if "$verifier" "$plan" "$evidence" "$environment" >"$temporary_dir/output.log" 2>&1; then
    fail "$name unexpectedly passed"
  fi
  pass "$name"
}

complete_plan="$temporary_dir/complete-plan.md"
complete_evidence="$temporary_dir/complete-evidence.env"
production_env="$temporary_dir/production.env"
write_plan "$complete_plan" ''
write_evidence "$complete_evidence"
write_production_environment "$production_env"
expect_valid 'complete mocked release evidence' "$complete_plan" "$complete_evidence" "$production_env"

open_plan="$temporary_dir/open-plan.md"
write_plan "$open_plan" '- [ ] Verify an applicable payment behavior.'
expect_invalid 'open applicable checklist item' "$open_plan" "$complete_evidence" "$production_env"

indented_open_plan="$temporary_dir/indented-open-plan.md"
write_plan "$indented_open_plan" '  - [ ] Verify a nested applicable payment behavior.'
expect_invalid 'indented open applicable checklist item' "$indented_open_plan" "$complete_evidence" "$production_env"

future_plan="$temporary_dir/future-plan.md"
future_evidence="$temporary_dir/future-evidence.env"
write_plan "$future_plan" '- [ ] Provider subscriptions are deferred. <!-- future-scope: RECURRING -->'
write_evidence "$future_evidence" 'FUTURE_SCOPE_RECURRING_REFERENCE=approval://architecture/recurring'
expect_valid 'approved future-scope checklist item' "$future_plan" "$future_evidence" "$production_env"
expect_invalid 'future scope without approval evidence' "$future_plan" "$complete_evidence" "$production_env"

test_env="$temporary_dir/test-key.env"
write_production_environment "$test_env" rzp_test_completion_fixture
expect_invalid 'Test Mode key in production evidence' "$complete_plan" "$complete_evidence" "$test_env"

alternate_provider_env="$temporary_dir/alternate-provider.env"
write_production_environment "$alternate_provider_env" rzp_live_completion_fixture https://provider-proxy.example
expect_invalid 'alternate provider endpoint in production evidence' "$complete_plan" "$complete_evidence" "$alternate_provider_env"

open_defects="$temporary_dir/open-defects.env"
write_evidence "$open_defects" '' 1
expect_invalid 'open P0 or P1 defect' "$complete_plan" "$open_defects" "$production_env"

missing_signoff="$temporary_dir/missing-signoff.env"
grep -v '^FINANCE_SIGNOFF_REFERENCE=' "$complete_evidence" >"$missing_signoff"
expect_invalid 'missing required owner sign-off' "$complete_plan" "$missing_signoff" "$production_env"

placeholder="$temporary_dir/placeholder.env"
sed 's|GO_LIVE_EVIDENCE_REFERENCE=.*|GO_LIVE_EVIDENCE_REFERENCE=pending|' "$complete_evidence" >"$placeholder"
expect_invalid 'placeholder completion evidence' "$complete_plan" "$placeholder" "$production_env"

echo 'Razorpay release completion contract tests passed.'
