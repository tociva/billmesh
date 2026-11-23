#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || -z "$1" || "$1" =~ [[:space:]] ]]; then
  echo 'usage: smoke-test-api-image.sh IMAGE_REFERENCE' >&2
  exit 2
fi

image_ref=$1
script_dir="$(CDPATH= cd "$(dirname "$0")" && pwd)"
repo_root="$(CDPATH= cd "${script_dir}/../../.." && pwd)"
compose_file="${repo_root}/deploy/compose.test.yml"
project="billmesh-smoke-${GITHUB_RUN_ID:-$$}"
container="billmesh-api-smoke-${GITHUB_RUN_ID:-$$}"
platform=${API_IMAGE_SMOKE_PLATFORM:-}
platform_args=()
[[ -z "$platform" ]] || platform_args=(--platform "$platform")

compose() { docker compose --project-name "$project" --file "$compose_file" "$@"; }
cleanup() {
  docker rm --force "$container" >/dev/null 2>&1 || true
  compose down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup
compose up -d postgres

deadline=$((SECONDS + 90))
until [[ "$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$(compose ps -q postgres)" 2>/dev/null || true)" == healthy ]]; do
  ((SECONDS < deadline)) || { compose logs postgres >&2; exit 1; }
  sleep 2
done

owner_url='postgres://billmesh:testpassword@postgres:5432/billmesh_e2e?sslmode=disable&search_path=billmesh'
runtime_env=(
  --env "DATABASE_URL=${owner_url}"
  --env DELEGATION_ISSUER=https://identity.invalid/delegation
  --env DELEGATION_DISCOVERY_URL=https://identity.invalid/.well-known/idnest-delegation-configuration
  --env DELEGATION_AUDIENCE=https://api.invalid
  --env CONSOLE_APP_ORIGIN=https://console.invalid
  --env ADMIN_APP_ORIGIN=https://admin.invalid
  --env BFF_CONSOLE_ISSUER=https://identity.invalid
  --env BFF_CONSOLE_CLIENT_ID=billmesh-console-smoke
  --env BFF_CONSOLE_CLIENT_SECRET=smoke-secret
  --env BFF_CONSOLE_AUDIENCE=billmesh
  --env BFF_CONSOLE_REDIRECT_URI=https://api.invalid/api/v1/auth/console/callback
  --env BFF_CONSOLE_POST_LOGOUT_REDIRECT_URI=https://api.invalid/api/v1/auth/console/logout/callback
  --env BFF_CONSOLE_STANDALONE_LOGOUT_URI=https://identity.invalid/logout
  --env BFF_ADMIN_ISSUER=https://identity.invalid
  --env BFF_ADMIN_CLIENT_ID=billmesh-admin-smoke
  --env BFF_ADMIN_CLIENT_SECRET=smoke-secret
  --env BFF_ADMIN_AUDIENCE=billmesh
  --env BFF_ADMIN_REDIRECT_URI=https://api.invalid/api/v1/auth/admin/callback
  --env BFF_ADMIN_POST_LOGOUT_REDIRECT_URI=https://api.invalid/api/v1/auth/admin/logout/callback
  --env BFF_ADMIN_STANDALONE_LOGOUT_URI=https://identity.invalid/logout
  --env BFF_SESSION_ENCRYPTION_KEYS=v1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
)

docker run --rm "${platform_args[@]}" --network "${project}_default" --env "DATABASE_URL=${owner_url}" "$image_ref" migrate up
docker run -d "${platform_args[@]}" --name "$container" --network "${project}_default" --publish 127.0.0.1::8080 --read-only --tmpfs /tmp:rw,noexec,nosuid,size=64m --cap-drop ALL --security-opt no-new-privileges:true "${runtime_env[@]}" "$image_ref" api >/dev/null

port=$(docker port "$container" 8080/tcp | awk -F: 'END {print $NF}')
[[ "$port" =~ ^[0-9]+$ ]] || { docker logs "$container" >&2; exit 1; }
deadline=$((SECONDS + 120))
until curl --fail --silent --noproxy '*' "http://127.0.0.1:${port}/readyz" >/dev/null; do
  [[ "$(docker inspect --format '{{.State.Running}}' "$container" 2>/dev/null || true)" == true ]] || { docker logs "$container" >&2; exit 1; }
  ((SECONDS < deadline)) || { docker logs "$container" >&2; exit 1; }
  sleep 2
done
curl --fail --silent --noproxy '*' "http://127.0.0.1:${port}/healthz" >/dev/null
echo "Image smoke test passed: $image_ref"
