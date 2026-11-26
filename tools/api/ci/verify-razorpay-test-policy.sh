#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../../.." && pwd)
consumer_root=${BILLMESH_WEB_ROOT:-"$repository_root/../billmesh-web"}
manifest="$repository_root/tests/testdata/test-cases.md"

fail() { echo "Razorpay test policy failed: $*" >&2; exit 1; }

for path in \
  "$repository_root/internal/app" \
  "$repository_root/tests/fakeserver" \
  "$repository_root/tests/testkit" \
  "$repository_root/tests/e2e/payments" \
  "$repository_root/tests/e2e/subscriptions"; do
  matches=$(find "$path" -type f -name '*.go' -exec grep -HnE '(^|[^A-Za-z])(t|b)\.Skip(f|Now)?\(' {} + 2>/dev/null || true)
  if [ -n "$matches" ]; then
    printf '%s\n' "$matches" >&2
    fail 'payment and Razorpay tests must not be skipped'
  fi
done

if [ -d "$consumer_root/apps/console/src/app/billing" ]; then
  matches=$(find "$consumer_root/apps/console/src/app/billing" -type f -name '*.spec.ts' -exec grep -HnE '\.(skip|only)\(' {} + 2>/dev/null || true)
  if [ -n "$matches" ]; then
    printf '%s\n' "$matches" >&2
    fail 'consumer checkout tests must not be skipped, focused, or quarantined'
  fi
fi

if [ "$#" -eq 0 ]; then
  echo 'Razorpay skipped/flaky test policy passed.'
  exit 0
fi
[ "$#" -eq 2 ] || fail 'usage: verify-razorpay-test-policy.sh [BASE_REF PR_BODY_FILE]'
base_ref=$1
pr_body=$2
[ -f "$pr_body" ] || fail 'PR body file is missing'

changed=$(git -C "$repository_root" diff --name-only "$base_ref...HEAD")
if ! printf '%s\n' "$changed" | grep -Eq '^(api/|internal/(app|config|database)/|tests/fakeserver/)'; then
  echo 'No production payment surface changed; PR scenario evidence is not required.'
  exit 0
fi
printf '%s\n' "$changed" | grep -Eq '(_test\.go$|^tests/|\.spec\.ts$)' || fail 'production changes require a changed automated test'

scenario_ids=$(grep -Eo '[A-Z]+-[0-9]{3}' "$pr_body" | sort -u || true)
[ -n "$scenario_ids" ] || fail 'PR body must list named Scenario IDs'
for scenario_id in $scenario_ids; do
  grep -Fq "$scenario_id" "$manifest" || fail "unknown Scenario ID in PR body: $scenario_id"
done
grep -Eiq 'test command' "$pr_body" || fail 'PR body must retain the Test command evidence section'
grep -Eiq '(go test|gotestsum|make test-|run-razorpay-quality-gates|pnpm (run|exec))' "$pr_body" || fail 'PR body must contain an exact executable test command'

echo 'Razorpay PR scenario and test-evidence policy passed.'
