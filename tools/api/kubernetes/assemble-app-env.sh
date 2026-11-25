#!/bin/sh
set -eu

fail() { echo "Billmesh environment assembly failed: $*" >&2; exit 1; }
[ "$#" -eq 1 ] || fail 'usage: assemble-app-env.sh OUTPUT'
output_file=$1
[ ! -L "$output_file" ] || fail "refusing to replace symbolic link: $output_file"

safe_value() {
  key=$1
  value=$2
  [ -n "$value" ] || fail "$key is missing"
  single_line=$(printf '%s' "$value" | tr -d '\015\012')
  [ "$single_line" = "$value" ] || fail "$key must be a single-line value"
}

write_required() {
  key=$1
  eval "value=\${$key:-}"
  safe_value "$key" "$value"
  printf '%s=%s\n' "$key" "$value"
}

write_optional() {
  key=$1
  eval "value=\${$key:-}"
  [ -n "$value" ] || return 0
  safe_value "$key" "$value"
  printf '%s=%s\n' "$key" "$value"
}

umask 077
temporary=$(mktemp "${output_file}.XXXXXX")
cleanup() { rm -f -- "${temporary:-}"; }
trap cleanup EXIT HUP INT TERM

{
  for key in \
    BILLMESH_ENVIRONMENT \
    DATABASE_URL \
    DELEGATION_ISSUER \
    DELEGATION_DISCOVERY_URL \
    DELEGATION_AUDIENCE \
    CONSOLE_APP_ORIGIN \
    ADMIN_APP_ORIGIN \
    BFF_CONSOLE_ISSUER \
    BFF_CONSOLE_CLIENT_ID \
    BFF_CONSOLE_CLIENT_SECRET \
    BFF_CONSOLE_AUDIENCE \
    BFF_CONSOLE_REDIRECT_URI \
    BFF_CONSOLE_POST_LOGOUT_REDIRECT_URI \
    BFF_CONSOLE_STANDALONE_LOGOUT_URI \
    BFF_ADMIN_ISSUER \
    BFF_ADMIN_CLIENT_ID \
    BFF_ADMIN_CLIENT_SECRET \
    BFF_ADMIN_AUDIENCE \
    BFF_ADMIN_REDIRECT_URI \
    BFF_ADMIN_POST_LOGOUT_REDIRECT_URI \
    BFF_ADMIN_STANDALONE_LOGOUT_URI \
    BFF_SESSION_ENCRYPTION_KEYS; do
    write_required "$key"
  done

  for key in \
    DELEGATION_PROFILE_REFRESH_INTERVAL \
    DELEGATION_PROFILE_MAX_STALENESS \
    WEBHOOK_TIMEOUT \
    WORKER_INTERVAL \
    WEBHOOK_ALLOWED_PRIVATE_HOSTS \
    AUTH_FAILURES_PER_MINUTE \
    MUTATION_RATE_PER_SECOND \
    MUTATION_BURST \
    BFF_CONSOLE_SCOPE \
    BFF_ADMIN_SCOPE \
    BFF_SESSION_IDLE_TTL \
    BFF_SESSION_ABSOLUTE_TTL \
    BFF_LOGIN_TTL \
    BFF_LOGOUT_TTL \
    BFF_REFRESH_SKEW \
    BFF_LOGIN_ATTEMPTS_PER_MINUTE \
    RAZORPAY_BASE_URL \
    RAZORPAY_KEY_ID \
    RAZORPAY_KEY_SECRET \
    RAZORPAY_CHECKOUT_URL \
    RAZORPAY_WEBHOOK_SECRET \
    SSL_CERT_FILE; do
    write_optional "$key"
  done
} >"$temporary"

chmod 600 "$temporary"
mv "$temporary" "$output_file"
temporary=
trap - EXIT
