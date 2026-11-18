#!/bin/sh
set -eu

fail() { echo "ECR image vulnerability gate failed: $*" >&2; exit 1; }
[ "$#" -eq 2 ] || fail 'usage: gate-ecr-image-scan.sh ECR_REPOSITORY IMAGE_DIGEST'
repository=$1
requested_digest=$2
digest=$requested_digest
scan_platform=${ECR_SCAN_PLATFORM:-linux/arm64}
critical_limit=${ECR_CRITICAL_FINDING_LIMIT:-0}
high_limit=${ECR_HIGH_FINDING_LIMIT:-0}

printf '%s' "$repository" | grep -Eq '^[a-z0-9][a-z0-9._/-]*$' || fail 'invalid ECR repository'
printf '%s' "$digest" | grep -Eq '^sha256:[a-f0-9]{64}$' || fail 'invalid image digest'
printf '%s' "$scan_platform" | grep -Eq '^linux/(amd64|arm64)$' || fail 'invalid scan platform'

image_metadata=$(aws ecr batch-get-image --repository-name "$repository" --image-ids "imageDigest=$digest" --query 'images[0]' --output json) || fail 'could not read image manifest metadata'
resolved_digest=$(printf '%s\n' "$image_metadata" | IMAGE_DIGEST="$digest" ECR_SCAN_PLATFORM="$scan_platform" python3 -c '
import json, os, re, sys
metadata=json.load(sys.stdin)
requested=os.environ["IMAGE_DIGEST"]
target_os,target_arch=os.environ["ECR_SCAN_PLATFORM"].split("/",1)
manifest=json.loads(metadata["imageManifest"])
media=metadata.get("imageManifestMediaType") or manifest.get("mediaType")
if media not in {"application/vnd.oci.image.index.v1+json","application/vnd.docker.distribution.manifest.list.v2+json"}:
    print(requested); raise SystemExit(0)
for entry in manifest.get("manifests",[]):
    platform=entry.get("platform") or {}
    value=entry.get("digest","")
    if platform.get("os")==target_os and platform.get("architecture")==target_arch and re.fullmatch(r"sha256:[a-f0-9]{64}",value):
        print(value); raise SystemExit(0)
print("requested platform manifest was not found",file=sys.stderr); raise SystemExit(1)
') || fail 'could not resolve platform manifest'
printf '%s' "$resolved_digest" | grep -Eq '^sha256:[a-f0-9]{64}$' || fail 'resolved scan digest is invalid'
digest=$resolved_digest
image_id="imageDigest=$digest"

if ! wait_output=$(aws ecr wait image-scan-complete --repository-name "$repository" --image-id "$image_id" 2>&1); then
  case "$wait_output" in
    *ScanNotFoundException*)
      aws ecr start-image-scan --repository-name "$repository" --image-id "$image_id" >/dev/null || fail 'could not start image scan'
      aws ecr wait image-scan-complete --repository-name "$repository" --image-id "$image_id" || fail 'image scan did not complete'
      ;;
    *) printf '%s\n' "$wait_output" >&2; fail 'image scan did not complete' ;;
  esac
fi

counts=$(aws ecr describe-image-scan-findings --repository-name "$repository" --image-id "$image_id" --query '[imageScanFindings.findingSeverityCounts.CRITICAL || `0`, imageScanFindings.findingSeverityCounts.HIGH || `0`]' --output text) || fail 'could not read scan findings'
set -- $counts
[ "$#" -eq 2 ] || fail 'unexpected ECR severity response'
critical=$1
high=$2
printf '%s' "$critical" | grep -Eq '^[0-9]+$' || fail 'invalid CRITICAL count'
printf '%s' "$high" | grep -Eq '^[0-9]+$' || fail 'invalid HIGH count'
[ "$critical" -le "$critical_limit" ] && [ "$high" -le "$high_limit" ] || fail "digest $digest has CRITICAL=$critical and HIGH=$high findings"

echo "ECR image scan passed for $requested_digest via $scan_platform manifest $digest (CRITICAL=$critical, HIGH=$high)."
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    echo '### ECR vulnerability gate'
    echo
    echo "- Image digest: \`$requested_digest\`"
    echo "- Scanned manifest: \`$digest\` (\`$scan_platform\`)"
    echo "- Critical findings: \`$critical\`"
    echo "- High findings: \`$high\`"
  } >>"$GITHUB_STEP_SUMMARY"
fi
