#!/bin/sh
set -eu

fail() { echo "Billmesh environment validation failed: $*" >&2; exit 1; }
[ "$#" -eq 1 ] || fail 'usage: validate-app-env.sh ENV_FILE'
env_file=$1
[ -f "$env_file" ] && [ ! -L "$env_file" ] || fail 'environment file must be a regular file'
[ -s "$env_file" ] || fail 'environment file is empty'

for command in awk cmp grep iconv sort tr wc; do
  command -v "$command" >/dev/null 2>&1 || fail "missing required command: $command"
done

[ "$(wc -c <"$env_file" | tr -d ' ')" -le 65536 ] || fail 'environment file exceeds 64 KiB'
LC_ALL=C tr -d '\000\015' <"$env_file" | cmp -s - "$env_file" || fail 'environment file contains NUL or carriage-return bytes'
iconv -f UTF-8 -t UTF-8 "$env_file" >/dev/null 2>&1 || fail 'environment file is not valid UTF-8'

allowed='DATABASE_URL DELEGATION_ISSUER DELEGATION_DISCOVERY_URL DELEGATION_AUDIENCE DELEGATION_PROFILE_REFRESH_INTERVAL DELEGATION_PROFILE_MAX_STALENESS WEBHOOK_TIMEOUT WORKER_INTERVAL WEBHOOK_ALLOWED_PRIVATE_HOSTS AUTH_FAILURES_PER_MINUTE MUTATION_RATE_PER_SECOND MUTATION_BURST CONSOLE_APP_ORIGIN ADMIN_APP_ORIGIN BFF_CONSOLE_ISSUER BFF_CONSOLE_CLIENT_ID BFF_CONSOLE_CLIENT_SECRET BFF_CONSOLE_AUDIENCE BFF_CONSOLE_SCOPE BFF_CONSOLE_REDIRECT_URI BFF_CONSOLE_POST_LOGOUT_REDIRECT_URI BFF_CONSOLE_STANDALONE_LOGOUT_URI BFF_ADMIN_ISSUER BFF_ADMIN_CLIENT_ID BFF_ADMIN_CLIENT_SECRET BFF_ADMIN_AUDIENCE BFF_ADMIN_SCOPE BFF_ADMIN_REDIRECT_URI BFF_ADMIN_POST_LOGOUT_REDIRECT_URI BFF_ADMIN_STANDALONE_LOGOUT_URI BFF_SESSION_ENCRYPTION_KEYS BFF_SESSION_IDLE_TTL BFF_SESSION_ABSOLUTE_TTL BFF_LOGIN_TTL BFF_LOGOUT_TTL BFF_REFRESH_SKEW BFF_LOGIN_ATTEMPTS_PER_MINUTE RAZORPAY_BASE_URL RAZORPAY_KEY_ID RAZORPAY_KEY_SECRET RAZORPAY_CHECKOUT_URL RAZORPAY_WEBHOOK_SECRET SSL_CERT_FILE'
required='DATABASE_URL DELEGATION_ISSUER DELEGATION_DISCOVERY_URL DELEGATION_AUDIENCE CONSOLE_APP_ORIGIN ADMIN_APP_ORIGIN BFF_CONSOLE_ISSUER BFF_CONSOLE_CLIENT_ID BFF_CONSOLE_CLIENT_SECRET BFF_CONSOLE_AUDIENCE BFF_CONSOLE_REDIRECT_URI BFF_CONSOLE_POST_LOGOUT_REDIRECT_URI BFF_CONSOLE_STANDALONE_LOGOUT_URI BFF_ADMIN_ISSUER BFF_ADMIN_CLIENT_ID BFF_ADMIN_CLIENT_SECRET BFF_ADMIN_AUDIENCE BFF_ADMIN_REDIRECT_URI BFF_ADMIN_POST_LOGOUT_REDIRECT_URI BFF_ADMIN_STANDALONE_LOGOUT_URI BFF_SESSION_ENCRYPTION_KEYS'

awk -v allowed="$allowed" -v required="$required" '
  BEGIN {
    n=split(allowed,a," "); for(i=1;i<=n;i++) permitted[a[i]]=1
    n=split(required,a," "); for(i=1;i<=n;i++) mandatory[a[i]]=1
  }
  {
    separator=index($0,"=")
    if(separator==0){printf "invalid line %d\n",NR > "/dev/stderr"; failed=1; next}
    key=substr($0,1,separator-1); value=substr($0,separator+1)
    if(key !~ /^[A-Z][A-Z0-9_]*$/ || !(key in permitted)){printf "unexpected key on line %d\n",NR > "/dev/stderr"; failed=1}
    if(seen[key]++){printf "duplicate key %s\n",key > "/dev/stderr"; failed=1}
    if(length(value)==0){printf "empty value for %s\n",key > "/dev/stderr"; failed=1}
    lowered=tolower(value)
    if(lowered ~ /<[^>]+>/ || lowered ~ /replace-with/ || lowered ~ /change-me/ || lowered ~ /todo/){printf "placeholder value for %s\n",key > "/dev/stderr"; failed=1}
  }
  END {for(key in mandatory) if(!(key in seen)){printf "missing key %s\n",key > "/dev/stderr"; failed=1}; exit failed}
' "$env_file" || fail 'environment contract failed'

value_for() {
  awk -v wanted="$1" 'index($0,"=")>0 && substr($0,1,index($0,"=")-1)==wanted {print substr($0,index($0,"=")+1); exit}' "$env_file"
}
key_present() { grep -Eq "^$1=" "$env_file"; }
require_https() { case "$(value_for "$1")" in https://?*) ;; *) fail "$1 must be an absolute HTTPS URL" ;; esac; }

case "$(value_for DATABASE_URL)" in postgres://*|postgresql://*) ;; *) fail 'DATABASE_URL must be a PostgreSQL URL' ;; esac
for key in DELEGATION_ISSUER DELEGATION_DISCOVERY_URL DELEGATION_AUDIENCE CONSOLE_APP_ORIGIN ADMIN_APP_ORIGIN BFF_CONSOLE_ISSUER BFF_CONSOLE_REDIRECT_URI BFF_CONSOLE_POST_LOGOUT_REDIRECT_URI BFF_CONSOLE_STANDALONE_LOGOUT_URI BFF_ADMIN_ISSUER BFF_ADMIN_REDIRECT_URI BFF_ADMIN_POST_LOGOUT_REDIRECT_URI BFF_ADMIN_STANDALONE_LOGOUT_URI; do
  require_https "$key"
done

for realm in CONSOLE ADMIN; do
  redirect=$(value_for "BFF_${realm}_REDIRECT_URI")
  logout=$(value_for "BFF_${realm}_POST_LOGOUT_REDIRECT_URI")
  lower=$(printf '%s' "$realm" | tr '[:upper:]' '[:lower:]')
  case "$redirect" in */api/v1/auth/$lower/callback) ;; *) fail "BFF_${realm}_REDIRECT_URI has the wrong callback path" ;; esac
  case "$logout" in */api/v1/auth/$lower/logout/callback) ;; *) fail "BFF_${realm}_POST_LOGOUT_REDIRECT_URI has the wrong callback path" ;; esac
done

printf '%s\n' "$(value_for BFF_SESSION_ENCRYPTION_KEYS)" | grep -Eq '^[A-Za-z0-9_-]+:[A-Za-z0-9_-]{43}(,[A-Za-z0-9_-]+:[A-Za-z0-9_-]{43})*$' || fail 'BFF_SESSION_ENCRYPTION_KEYS must contain base64url 32-byte keys'

payment_keys='RAZORPAY_BASE_URL RAZORPAY_KEY_ID RAZORPAY_KEY_SECRET RAZORPAY_CHECKOUT_URL RAZORPAY_WEBHOOK_SECRET'
payment_count=0
for key in $payment_keys; do key_present "$key" && payment_count=$((payment_count + 1)); done
[ "$payment_count" -eq 0 ] || [ "$payment_count" -eq 5 ] || fail 'Razorpay configuration must be omitted or configured completely'
if [ "$payment_count" -eq 5 ]; then
  require_https RAZORPAY_BASE_URL
  require_https RAZORPAY_CHECKOUT_URL
fi

echo 'Billmesh environment validation passed.'
