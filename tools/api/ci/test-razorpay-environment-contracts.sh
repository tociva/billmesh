#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../../.." && pwd)
validator="$repository_root/tools/api/kubernetes/validate-app-env.sh"
temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/billmesh-razorpay-env-test.XXXXXX")
cleanup() { rm -rf -- "$temporary_dir"; }
trap cleanup EXIT HUP INT TERM

write_base_environment() {
  output=$1
  environment=$2
  cat >"$output" <<EOF
BILLMESH_ENVIRONMENT=$environment
DATABASE_URL=postgres://billmesh:secret@database.example:5432/billmesh
DELEGATION_ISSUER=https://auth.example/delegation
DELEGATION_DISCOVERY_URL=https://auth.example/.well-known/delegation
DELEGATION_AUDIENCE=https://api.example
CONSOLE_APP_ORIGIN=https://console.example
ADMIN_APP_ORIGIN=https://admin.example
BFF_CONSOLE_ISSUER=https://auth.example
BFF_CONSOLE_CLIENT_ID=billmesh-console
BFF_CONSOLE_CLIENT_SECRET=console-secret
BFF_CONSOLE_AUDIENCE=billmesh
BFF_CONSOLE_REDIRECT_URI=https://api.example/api/v1/auth/console/callback
BFF_CONSOLE_POST_LOGOUT_REDIRECT_URI=https://api.example/api/v1/auth/console/logout/callback
BFF_CONSOLE_STANDALONE_LOGOUT_URI=https://auth.example/logout
BFF_ADMIN_ISSUER=https://auth.example
BFF_ADMIN_CLIENT_ID=billmesh-admin
BFF_ADMIN_CLIENT_SECRET=admin-secret
BFF_ADMIN_AUDIENCE=billmesh
BFF_ADMIN_REDIRECT_URI=https://api.example/api/v1/auth/admin/callback
BFF_ADMIN_POST_LOGOUT_REDIRECT_URI=https://api.example/api/v1/auth/admin/logout/callback
BFF_ADMIN_STANDALONE_LOGOUT_URI=https://auth.example/logout
BFF_SESSION_ENCRYPTION_KEYS=v1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
EOF
}

append_razorpay() {
  output=$1
  base_url=$2
  key_id=$3
  cat >>"$output" <<EOF
RAZORPAY_BASE_URL=$base_url
RAZORPAY_KEY_ID=$key_id
RAZORPAY_KEY_SECRET=provider-secret
RAZORPAY_WEBHOOK_SECRET=webhook-secret
EOF
}

expect_valid() {
  name=$1
  file=$2
  if ! "$validator" "$file" >"$temporary_dir/output.log" 2>&1; then
    echo "FAIL: $name unexpectedly failed" >&2
    cat "$temporary_dir/output.log" >&2
    exit 1
  fi
  echo "PASS: $name"
}

expect_invalid() {
  name=$1
  file=$2
  if "$validator" "$file" >"$temporary_dir/output.log" 2>&1; then
    echo "FAIL: $name unexpectedly passed" >&2
    exit 1
  fi
  echo "PASS: $name"
}

disabled="$temporary_dir/disabled.env"
write_base_environment "$disabled" development
expect_valid "all Razorpay values omitted" "$disabled"

modal="$temporary_dir/modal.env"
write_base_environment "$modal" development
append_razorpay "$modal" https://api.razorpay.com rzp_test_billmesh
expect_valid "modal checkout without checkout URL" "$modal"

rotation="$temporary_dir/rotation.env"
write_base_environment "$rotation" development
append_razorpay "$rotation" https://api.razorpay.com rzp_test_billmesh
cat >>"$rotation" <<EOF
RAZORPAY_ACCOUNT_ID=acc_billmesh
RAZORPAY_PREVIOUS_WEBHOOK_SECRET=previous-webhook-secret
RAZORPAY_PREVIOUS_WEBHOOK_SECRET_EXPIRES_AT=2099-01-02T03:04:05Z
EOF
expect_valid "merchant identity and webhook rotation" "$rotation"

incomplete_rotation="$temporary_dir/incomplete-rotation.env"
write_base_environment "$incomplete_rotation" development
append_razorpay "$incomplete_rotation" https://api.razorpay.com rzp_test_billmesh
echo 'RAZORPAY_PREVIOUS_WEBHOOK_SECRET=previous-webhook-secret' >>"$incomplete_rotation"
expect_invalid "rotation secret without expiry" "$incomplete_rotation"

malformed_rotation="$temporary_dir/malformed-rotation.env"
write_base_environment "$malformed_rotation" development
append_razorpay "$malformed_rotation" https://api.razorpay.com rzp_test_billmesh
echo 'RAZORPAY_PREVIOUS_WEBHOOK_SECRET=previous-webhook-secret' >>"$malformed_rotation"
echo 'RAZORPAY_PREVIOUS_WEBHOOK_SECRET_EXPIRES_AT=tomorrow' >>"$malformed_rotation"
expect_invalid "malformed rotation expiry" "$malformed_rotation"

partial="$temporary_dir/partial.env"
write_base_environment "$partial" development
echo 'RAZORPAY_BASE_URL=https://api.razorpay.com' >>"$partial"
expect_invalid "partial Razorpay configuration" "$partial"

insecure="$temporary_dir/insecure.env"
write_base_environment "$insecure" production
append_razorpay "$insecure" http://api.razorpay.com rzp_live_billmesh
expect_invalid "non-HTTPS production base URL" "$insecure"

development_live="$temporary_dir/development-live.env"
write_base_environment "$development_live" development
append_razorpay "$development_live" https://api.razorpay.com rzp_live_billmesh
expect_invalid "live key in development" "$development_live"

production_test="$temporary_dir/production-test.env"
write_base_environment "$production_test" production
append_razorpay "$production_test" https://api.razorpay.com rzp_test_billmesh
expect_invalid "test key in production" "$production_test"

checkout_only="$temporary_dir/checkout-only.env"
write_base_environment "$checkout_only" development
echo 'RAZORPAY_CHECKOUT_URL=https://checkout.razorpay.com' >>"$checkout_only"
expect_invalid "checkout URL without core configuration" "$checkout_only"
