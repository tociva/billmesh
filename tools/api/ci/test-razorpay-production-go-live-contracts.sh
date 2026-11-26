#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../../.." && pwd)
runbook="$repository_root/docs/runbooks/razorpay-production-go-live.md"
evidence="$repository_root/docs/razorpay-production-go-live-evidence.example.yaml"
validator="$repository_root/tools/api/kubernetes/validate-app-env.sh"
environment_contract="$script_dir/test-razorpay-environment-contracts.sh"
quality_runner="$script_dir/run-razorpay-quality-gates.sh"
manifest="$repository_root/tests/testdata/test-cases.md"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }
require_text() {
  file=$1
  expected=$2
  grep -Fq -- "$expected" "$file" || fail "$file does not contain: $expected"
}

for file in "$runbook" "$evidence" "$validator" "$environment_contract" "$quality_runner" "$manifest"; do
  [ -f "$file" ] || fail "missing production go-live asset: $file"
done

for required in \
  'Phase 15 Test Mode staging-acceptance checkbox is complete' \
  'activation/KYC is complete' \
  'rzp_live_' \
  'RAZORPAY_ACCOUNT_ID' \
  'must differ from the API key secret' \
  'payment.captured' \
  'payment.failed' \
  'order.paid' \
  'refund.processed' \
  'Automatic Capture' \
  'database backup' \
  'rollback drill' \
  'daily captures, settlements, refunds' \
  'tax/invoice compliance approval' \
  'Controlled minimum-value smoke payment' \
  'Full smoke refund' \
  'rollback decision deadline' \
  'pause only new checkout creation'; do
  require_text "$runbook" "$required"
done
pass 'runbook covers every production decision, smoke, refund, and rollback boundary'

for field in \
  status launch_window_start rollback_decision_deadline release_version image_digest \
  launch_commander independent_approver activation_kyc_reference live_mode_reference \
  approved_payment_methods_reference billmesh_environment provider_mode provider_base_url \
  key_id_suffix api_secret_manager_reference webhook_secret_manager_reference webhook_id \
  exact_url enabled_events backup_reference restore_drill_reference rollback_drill_reference \
  reconciliation_readiness_reference dashboard_alert_reference paging_drill_reference \
  finance_daily_reconciliation_reference tax_invoice_approval_reference \
  checkout_containment_reference billmesh_payment_id provider_order_id provider_payment_id \
  provider_refund_id engineering security finance product operations support final_decision_reference; do
  require_text "$evidence" "$field:"
done
if grep -Eq '(^|[[:space:]])(key_secret|webhook_secret|signature|raw_payload):' "$evidence"; then
  fail 'evidence template must not define fields for secret or raw payment values'
fi
pass 'evidence template records approvals and safe references without secret-value fields'

require_text "$validator" "production:rzp_live_*"
require_text "$validator" "production RAZORPAY_BASE_URL must be https://api.razorpay.com"
require_text "$validator" "production requires RAZORPAY_ACCOUNT_ID"
require_text "$validator" "RAZORPAY_KEY_SECRET and RAZORPAY_WEBHOOK_SECRET must be distinct"
require_text "$environment_contract" 'live key and merchant identity in production'
require_text "$environment_contract" 'alternate provider endpoint in production'
require_text "$environment_contract" 'missing merchant identity in production'
require_text "$environment_contract" 'malformed merchant identity in production'
require_text "$environment_contract" 'shared API and webhook secret'
require_text "$environment_contract" 'shared current and previous webhook secret'
pass 'production runtime configuration fails closed on provider identity and secret separation'

require_text "$quality_runner" './tools/api/ci/test-razorpay-production-go-live-contracts.sh'
require_text "$manifest" 'TEST-005'
pass 'go-live contract is part of the automated contract gate and named test plan'

echo 'Razorpay production go-live contract tests passed.'
