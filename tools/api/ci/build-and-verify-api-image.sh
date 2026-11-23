#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo 'usage: build-and-verify-api-image.sh [IMAGE_REFERENCE]' >&2
  exit 2
}

if [[ $# -gt 1 || "${1:-}" =~ [[:space:]] ]]; then
  usage
fi

image_ref=${1:-billmesh-api:local-verify}
[[ -n "$image_ref" ]] || usage

script_dir="$(CDPATH= cd "$(dirname "$0")" && pwd)"
repo_root="$(CDPATH= cd "${script_dir}/../../.." && pwd)"
dockerfile="${repo_root}/deploy/Dockerfile"
platform=linux/arm64

command -v docker >/dev/null 2>&1 || {
  echo 'Local image verification failed: docker is required' >&2
  exit 1
}
docker info >/dev/null 2>&1 || {
  echo 'Local image verification failed: Docker is not running' >&2
  exit 1
}

echo "Building ${image_ref} for ${platform}..."
docker build \
  --platform "$platform" \
  --file "$dockerfile" \
  --target runtime \
  --tag "$image_ref" \
  "$repo_root"

image_platform=$(docker image inspect "$image_ref" --format '{{.Os}}/{{.Architecture}}')
[[ "$image_platform" == "$platform" ]] || {
  echo "Local image verification failed: expected ${platform}, found ${image_platform}" >&2
  exit 1
}

image_user=$(docker image inspect "$image_ref" --format '{{.Config.User}}')
[[ "$image_user" == '10001:10001' ]] || {
  echo "Local image verification failed: expected image user 10001:10001, found ${image_user:-unset}" >&2
  exit 1
}

docker run --rm --platform "$platform" --entrypoint apk "$image_ref" \
  info --installed 'zlib>=1.4-r0' >/dev/null || {
    echo 'Local image verification failed: zlib 1.4-r0 or newer is required' >&2
    exit 1
  }

API_IMAGE_SMOKE_PLATFORM="$platform" \
  "$script_dir/smoke-test-api-image.sh" "$image_ref"

echo "Local ARM64 image verification passed: $image_ref"
