#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../../.." && pwd)
runbook="$repository_root/docs/runbooks/razorpay-dashboard-setup.md"
inventory="$repository_root/docs/razorpay-operational-inventory.example.yaml"
verifier="$repository_root/tools/api/ci/verify-razorpay-webhook-route.sh"
test_compose="$repository_root/deploy/compose.test.yml"
temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/billmesh-razorpay-dashboard-test.XXXXXX")
cleanup() { rm -rf -- "$temporary_dir"; }
trap cleanup EXIT HUP INT TERM

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }
require_text() {
  file=$1
  expected=$2
  grep -Fq -- "$expected" "$file" || fail "$file does not contain: $expected"
}

for file in "$runbook" "$inventory" "$verifier" "$test_compose"; do
  [ -f "$file" ] || fail "missing operational asset: $file"
done

require_text "$runbook" 'https://api-dev.billme.sh/v1/payments/webhook'
require_text "$runbook" 'payment.captured'
require_text "$runbook" 'payment.failed'
require_text "$runbook" 'order.paid'
require_text "$runbook" 'refund.processed'
require_text "$runbook" 'Automatic Capture'
require_text "$runbook" 'RAZORPAY_KEY_SECRET'
require_text "$runbook" 'RAZORPAY_WEBHOOK_SECRET'
require_text "$runbook" 'Real Razorpay account activation, credentials, Dashboard settings, webhooks,'
require_text "$runbook" '`POST` | `/v1/orders`'
require_text "$runbook" '`GET` | `/v1/orders/{id}/payments`'
require_text "$runbook" '`GET` | `/v1/payments/{id}`'
require_text "$runbook" '`GET` | `/v1/payments/{id}/refunds`'
pass 'runbook fixes the endpoint, events, capture policy, and secret boundaries'

require_text "$test_compose" 'RAZORPAY_BASE_URL: https://mock-external:8090'
require_text "$test_compose" 'RAZORPAY_KEY_ID: rzp_test_billmesh'
require_text "$test_compose" 'RAZORPAY_KEY_SECRET: test-provider-secret'
require_text "$test_compose" 'RAZORPAY_WEBHOOK_SECRET: test-webhook-secret'
if grep -Eq 'RAZORPAY_BASE_URL:.*api\.razorpay\.com' "$test_compose"; then
  fail 'automated test Compose configuration must not call the real Razorpay API'
fi
pass 'automated environments use only the local fake provider and deterministic credentials'

for field in merchant_account_id mode billmesh_environment webhook_id webhook_url enabled_events owner alert_email created_at verified_at webhook_secret_rotation_due_at; do
  require_text "$inventory" "$field:"
done
if grep -Eq '(key_secret|webhook_secret):[[:space:]]*[^#[:space:]]+' "$inventory"; then
  fail 'inventory template must not contain secret values'
fi
pass 'inventory records required evidence without secret values'

fake_bin="$temporary_dir/bin"
mkdir -p "$fake_bin"
cat >"$fake_bin/curl" <<'EOF'
#!/bin/sh
set -eu
output=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output) output=$2; shift 2 ;;
    *) shift ;;
  esac
done
[ -n "$output" ] || exit 2
printf '%s\n' '{"error":"invalid webhook signature","code":"unauthenticated","request_id":"route-probe"}' >"$output"
printf '401'
EOF
chmod +x "$fake_bin/curl"

PATH="$fake_bin:$PATH" "$verifier" 'https://api-dev.billme.sh/v1/payments/webhook' >"$temporary_dir/valid.log"
require_text "$temporary_dir/valid.log" 'Billmesh Razorpay webhook route is publicly reachable.'
pass 'route verifier accepts an application-authenticated 401 response'

for invalid in \
  'http://api-dev.billme.sh/v1/payments/webhook' \
  'https://localhost/v1/payments/webhook' \
  'https://api-dev.billme.sh/v1/payments/webhook?debug=true' \
  'https://user@api-dev.billme.sh/v1/payments/webhook' \
  'https://api-dev.billme.sh/v1/other'; do
  if PATH="$fake_bin:$PATH" "$verifier" "$invalid" >"$temporary_dir/invalid.log" 2>&1; then
    fail "route verifier accepted invalid URL: $invalid"
  fi
done
pass 'route verifier rejects insecure, private, credentialed, and incorrect URLs'

echo 'Razorpay Dashboard setup contract tests passed.'
