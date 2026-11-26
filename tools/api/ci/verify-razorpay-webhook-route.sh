#!/bin/sh
set -eu

fail() { echo "Razorpay webhook route verification failed: $*" >&2; exit 1; }
[ "$#" -eq 1 ] || fail 'usage: verify-razorpay-webhook-route.sh HTTPS_WEBHOOK_URL'

webhook_url=$1
case "$webhook_url" in
  https://*) ;;
  *) fail 'webhook URL must use HTTPS' ;;
esac
case "$webhook_url" in
  *'?'*|*'#'*|*'@'*) fail 'webhook URL must not contain credentials, a query, or a fragment' ;;
esac

authority_and_path=${webhook_url#https://}
authority=${authority_and_path%%/*}
path=${authority_and_path#"$authority"}
[ "$path" = '/v1/payments/webhook' ] || fail 'webhook URL must use the exact /v1/payments/webhook path'
printf '%s\n' "$authority" | grep -Eq '^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$' || fail 'webhook URL must contain a valid hostname without a custom port'
if printf '%s\n' "$authority" | grep -Eiq '^(localhost|.*\.localhost|.*\.local|0\.|10\.|127\.|169\.254\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.)'; then
  fail 'webhook URL must use a public hostname'
fi

command -v curl >/dev/null 2>&1 || fail 'curl is required'
temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/billmesh-razorpay-route.XXXXXX")
cleanup() { rm -rf -- "$temporary_dir"; }
trap cleanup EXIT HUP INT TERM
response_body=$temporary_dir/response.json

# An intentionally invalid signature proves the request reached Billmesh without
# requiring or exposing any real Razorpay secret. The application must identify
# the invalid signature with its standard 401 JSON envelope.
status=$(curl --silent --show-error --max-time 10 \
  --output "$response_body" --write-out '%{http_code}' \
  --request POST \
  --header 'Content-Type: application/json' \
  --header 'X-Razorpay-Event-Id: evt_billmesh_route_probe' \
  --header 'X-Razorpay-Signature: 0000000000000000000000000000000000000000000000000000000000000000' \
  --data '{"event":"billmesh.route.probe"}' \
  "$webhook_url") || fail 'request did not reach the webhook endpoint'

[ "$status" = 401 ] || fail "expected Billmesh 401 response, received HTTP $status"
grep -Eq '"code"[[:space:]]*:[[:space:]]*"unauthenticated"' "$response_body" || fail 'response is not the Billmesh unauthenticated envelope'
grep -Eq '"error"[[:space:]]*:[[:space:]]*"invalid webhook signature"' "$response_body" || fail 'response did not identify Billmesh webhook signature validation'

echo 'Billmesh Razorpay webhook route is publicly reachable.'
echo "Verified endpoint: $webhook_url"
