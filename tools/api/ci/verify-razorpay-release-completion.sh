#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../../.." && pwd)
environment_validator="$repository_root/tools/api/kubernetes/validate-app-env.sh"

fail() { echo "Razorpay release completion failed: $*" >&2; exit 1; }
[ "$#" -eq 3 ] || fail 'usage: verify-razorpay-release-completion.sh PLAN EVIDENCE_ENV PRODUCTION_ENV'
plan=$1
evidence=$2
production_env=$3

for command in awk cmp grep iconv sed sort tr wc; do
  command -v "$command" >/dev/null 2>&1 || fail "missing required command: $command"
done
for file in "$plan" "$evidence" "$production_env" "$environment_validator"; do
  [ -f "$file" ] && [ ! -L "$file" ] && [ -s "$file" ] || fail "input must be a non-empty regular file: $file"
done

temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/billmesh-razorpay-completion.XXXXXX")
cleanup() { rm -rf -- "$temporary_dir"; }
trap cleanup EXIT HUP INT TERM
pre_completion_plan="$temporary_dir/pre-completion-plan.md"
awk '/^## 17\. Completion criteria/{found=1; exit} {print} END{if(!found) exit 2}' "$plan" >"$pre_completion_plan" ||
  fail 'plan does not contain the Phase 17 completion section'

unresolved="$temporary_dir/unresolved.txt"
awk '
  /^[[:space:]]*- \[ \]/ && $0 !~ /<!-- future-scope: *[A-Z][A-Z0-9_]* *-->/ {print NR ":" $0; failed=1}
  END {exit failed}
' "$pre_completion_plan" >"$unresolved" || {
  cat "$unresolved" >&2
  fail 'applicable pre-completion checklist items remain open'
}

[ "$(wc -c <"$evidence" | tr -d ' ')" -le 65536 ] || fail 'evidence file exceeds 64 KiB'
LC_ALL=C tr -d '\000\015' <"$evidence" | cmp -s - "$evidence" || fail 'evidence contains NUL or carriage-return bytes'
iconv -f UTF-8 -t UTF-8 "$evidence" >/dev/null 2>&1 || fail 'evidence is not valid UTF-8'

awk '
  /^[[:space:]]*($|#)/ {next}
  {
    separator=index($0,"=")
    if(separator==0){printf "invalid evidence line %d\n",NR > "/dev/stderr"; failed=1; next}
    key=substr($0,1,separator-1); value=substr($0,separator+1)
    if(key !~ /^[A-Z][A-Z0-9_]*$/){printf "invalid evidence key on line %d\n",NR > "/dev/stderr"; failed=1}
    if(seen[key]++){printf "duplicate evidence key %s\n",key > "/dev/stderr"; failed=1}
    lowered=tolower(value)
    if(length(value)==0 || lowered ~ /(^|[^a-z])(pending|replace|todo|tbd)([^a-z]|$)/){printf "incomplete evidence value for %s\n",key > "/dev/stderr"; failed=1}
    if(key ~ /(SECRET|PASSWORD|TOKEN|SIGNATURE|RAW_PAYLOAD)/){printf "forbidden sensitive evidence key %s\n",key > "/dev/stderr"; failed=1}
  }
  END {exit failed}
' "$evidence" || fail 'evidence contract failed'

value_for() {
  awk -v wanted="$1" 'index($0,"=")>0 && substr($0,1,index($0,"=")-1)==wanted {print substr($0,index($0,"=")+1); exit}' "$evidence"
}
require_evidence() {
  key=$1
  [ -n "$(value_for "$key")" ] || fail "missing evidence key: $key"
}

for key in \
  AUTOMATED_TEST_EVIDENCE_REFERENCE STAGING_ACCEPTANCE_EVIDENCE_REFERENCE \
  OPENAPI_CONSUMER_PARITY_REFERENCE PRODUCTION_CONFIGURATION_EVIDENCE_REFERENCE \
  GO_LIVE_EVIDENCE_REFERENCE RELEASE_VERSION RELEASE_COMMIT COMPLETED_AT \
  P0_P1_OPEN_COUNT ENGINEERING_SIGNOFF_REFERENCE SECURITY_SIGNOFF_REFERENCE \
  FINANCE_SIGNOFF_REFERENCE PRODUCT_SIGNOFF_REFERENCE OPERATIONS_SIGNOFF_REFERENCE \
  SUPPORT_SIGNOFF_REFERENCE; do
  require_evidence "$key"
done
[ "$(value_for P0_P1_OPEN_COUNT)" = 0 ] || fail 'P0_P1_OPEN_COUNT must be zero'

future_scope_ids="$temporary_dir/future-scope-ids.txt"
sed -n 's/.*<!-- future-scope: *\([A-Z][A-Z0-9_]*\) *-->.*/\1/p' "$pre_completion_plan" | sort -u >"$future_scope_ids"
while IFS= read -r scope_id; do
  [ -z "$scope_id" ] || require_evidence "FUTURE_SCOPE_${scope_id}_REFERENCE"
done <"$future_scope_ids"

"$environment_validator" "$production_env" >/dev/null || fail 'production environment validation failed'
grep -Eq '^BILLMESH_ENVIRONMENT=production$' "$production_env" || fail 'production environment must declare BILLMESH_ENVIRONMENT=production'
grep -Eq '^RAZORPAY_BASE_URL=https://api\.razorpay\.com$' "$production_env" || fail 'production environment must use the official Razorpay API endpoint'
grep -Eq '^RAZORPAY_KEY_ID=rzp_live_.+' "$production_env" || fail 'production environment must use an rzp_live_ key'
grep -Eq '^RAZORPAY_ACCOUNT_ID=acc_.+' "$production_env" || fail 'production environment must bind the approved merchant account'

echo 'Razorpay release completion verification passed.'
