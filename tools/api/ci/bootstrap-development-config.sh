#!/bin/sh
set -eu

fail() { echo "Development configuration bootstrap failed: $*" >&2; exit 1; }
usage() { echo "usage: $0 [ENV_FILE [TFVARS_FILE [KUBECONFIG_FILE]]]" >&2; exit 2; }

[ "$#" -le 3 ] || usage

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/../../.." && pwd)
terraform_dir=$repo_root/tools/api/infrastructure/aws-development
env_template=$script_dir/development.env.example
tfvars_template=$terraform_dir/terraform.tfvars.example
local_env_file=$repo_root/.env
env_file=${1:-$repo_root/tmp/development.env}
tfvars_file=${2:-$terraform_dir/terraform.tfvars}
kubeconfig_file=${3:-${HOME:?HOME is required}/.kube/restricted-billmesh-kubeconfig.yaml}
kubernetes_access_hostname=${KUBERNETES_ACCESS_HOSTNAME:-k8s-tociva-dev.tociva.com}

for command in awk base64 cp gh grep jq mkdir mktemp mv sort terraform tr; do
  command -v "$command" >/dev/null 2>&1 || fail "missing command: $command"
done

printf '%s' "$kubernetes_access_hostname" | grep -Eq '^[A-Za-z0-9.-]+$' ||
  fail 'KUBERNETES_ACCESS_HOSTNAME is invalid'

gh auth status -h github.com >/dev/null 2>&1 ||
  fail 'GitHub CLI is not authenticated; run: gh auth login -h github.com'

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/billmesh-development-config.XXXXXX")
cleanup() { rm -rf "$work_dir"; }
trap cleanup EXIT HUP INT TERM

[ -f "$env_template" ] || fail "missing template: $env_template"
[ -f "$tfvars_template" ] || fail "missing template: $tfvars_template"

mkdir -p "$(dirname "$env_file")" "$(dirname "$tfvars_file")"

if [ ! -e "$env_file" ]; then
  cp "$env_template" "$env_file"
fi
if [ ! -e "$tfvars_file" ]; then
  cp "$tfvars_template" "$tfvars_file"
fi

[ -f "$env_file" ] && [ ! -L "$env_file" ] || fail "$env_file must be a regular file"
[ -f "$tfvars_file" ] && [ ! -L "$tfvars_file" ] || fail "$tfvars_file must be a regular file"
chmod 600 "$env_file" "$tfvars_file"

if [ -e "$local_env_file" ]; then
  [ -f "$local_env_file" ] && [ ! -L "$local_env_file" ] ||
    fail "$local_env_file must be a regular file"
  local_import_result=$work_dir/local-import-result
  local_imported_keys=$work_dir/local-imported-keys
  awk -v imported_keys="$local_imported_keys" '
    FNR == NR {
      if ($0 ~ /^[A-Za-z_][A-Za-z0-9_]*=/) {
        key=$0; sub(/=.*/, "", key)
        value=$0; sub(/^[^=]*=/, "", value)
        source_line[key]=$0
        source_value[key]=value
      }
      next
    }
    $0 ~ /^[A-Za-z_][A-Za-z0-9_]*=/ {
      key=$0; sub(/=.*/, "", key)
      value=$0; sub(/^[^=]*=/, "", value)
      if ((value == "" || value ~ /<[^>]+>/) &&
          key in source_line && source_value[key] != "" &&
          source_value[key] !~ /<[^>]+>/) {
        print source_line[key]
        print key > imported_keys
        next
      }
    }
    { print }
  ' "$local_env_file" "$env_file" >"$local_import_result"
  chmod 600 "$local_import_result"
  mv "$local_import_result" "$env_file"
  if [ -s "$local_imported_keys" ]; then
    echo 'Imported populated values from .env for:'
    sort -u "$local_imported_keys"
  fi
fi

set_hcl_string() {
  hcl_key=$1
  hcl_value=$2
  hcl_result=$work_dir/hcl-result
  if ! awk -v wanted="$hcl_key" -v replacement="$hcl_value" '
    $1 == wanted && $2 == "=" {
      printf "%s = \"%s\"\n", wanted, replacement
      found=1
      next
    }
    { print }
    END { if (!found) exit 1 }
  ' "$tfvars_file" >"$hcl_result"; then
    fail "$hcl_key is missing from $tfvars_file"
  fi
  chmod 600 "$hcl_result"
  mv "$hcl_result" "$tfvars_file"
}

set_env_value() {
  env_key=$1
  env_value=$2
  env_single_line=$(printf '%s' "$env_value" | tr -d '\015\012')
  [ "$env_single_line" = "$env_value" ] || fail "$env_key must be a single-line value"
  env_result=$work_dir/env-result
  if ! awk -v wanted="$env_key" -v replacement="$env_value" '
    index($0,wanted "=") == 1 {
      print wanted "=" replacement
      found=1
      next
    }
    { print }
    END { if (!found) exit 1 }
  ' "$env_file" >"$env_result"; then
    fail "$env_key is missing from $env_file"
  fi
  chmod 600 "$env_result"
  mv "$env_result" "$env_file"
}

github_metadata=$(gh api repos/tociva/billmesh) || fail 'could not read tociva/billmesh metadata with GitHub CLI'
github_full_name=$(printf '%s\n' "$github_metadata" | jq -er '.full_name') || fail 'GitHub response has no repository name'
[ "$github_full_name" = tociva/billmesh ] || fail "unexpected GitHub repository: $github_full_name"
github_owner_id=$(printf '%s\n' "$github_metadata" | jq -er '.owner.id | tostring | select(test("^[1-9][0-9]*$"))') || fail 'invalid GitHub owner ID'
github_repository_id=$(printf '%s\n' "$github_metadata" | jq -er '.id | tostring | select(test("^[1-9][0-9]*$"))') || fail 'invalid GitHub repository ID'

set_hcl_string github_repository_owner_id "$github_owner_id"
set_hcl_string github_repository_id "$github_repository_id"
terraform fmt "$tfvars_file" >/dev/null

set_env_value KUBE_NAMESPACE billmesh-development
set_env_value API_HOSTNAME api-dev.billme.sh
set_env_value KUBERNETES_ACCESS_HOSTNAME "$kubernetes_access_hostname"
set_env_value ECR_REPOSITORY billmesh/api
set_env_value DELEGATION_ISSUER https://auth-dev.idnest.cloud/auth/v1/delegation
set_env_value DELEGATION_DISCOVERY_URL https://auth-dev.idnest.cloud/.well-known/idnest-delegation-configuration
set_env_value DELEGATION_AUDIENCE https://api-dev.billme.sh
set_env_value DELEGATION_PROFILE_REFRESH_INTERVAL 10s
set_env_value DELEGATION_PROFILE_MAX_STALENESS 1m
set_env_value WORKER_INTERVAL 1s
set_env_value CONSOLE_APP_ORIGIN https://console-dev.billme.sh
set_env_value ADMIN_APP_ORIGIN https://admin-dev.billme.sh
set_env_value BFF_CONSOLE_ISSUER https://hydra-dev.idnest.cloud/
set_env_value BFF_CONSOLE_AUDIENCE billmesh
set_env_value BFF_CONSOLE_SCOPE 'openid profile email offline_access'
set_env_value BFF_CONSOLE_REDIRECT_URI https://api-dev.billme.sh/api/v1/auth/console/callback
set_env_value BFF_CONSOLE_POST_LOGOUT_REDIRECT_URI https://api-dev.billme.sh/api/v1/auth/console/logout/callback
set_env_value BFF_CONSOLE_STANDALONE_LOGOUT_URI https://auth-dev.idnest.cloud/logout
set_env_value BFF_ADMIN_ISSUER https://hydra-dev.idnest.cloud/
set_env_value BFF_ADMIN_AUDIENCE billmesh
set_env_value BFF_ADMIN_SCOPE 'openid profile email offline_access'
set_env_value BFF_ADMIN_REDIRECT_URI https://api-dev.billme.sh/api/v1/auth/admin/callback
set_env_value BFF_ADMIN_POST_LOGOUT_REDIRECT_URI https://api-dev.billme.sh/api/v1/auth/admin/logout/callback
set_env_value BFF_ADMIN_STANDALONE_LOGOUT_URI https://auth-dev.idnest.cloud/logout

terraform_values_loaded=false
if terraform_outputs=$(terraform -chdir="$terraform_dir" output -json 2>/dev/null); then
  if printf '%s\n' "$terraform_outputs" | jq -e '
    .aws_account_id.value and .aws_region.value and .aws_build_role_arn.value and
    .aws_deploy_role_arn.value and .ecr_repository.value
  ' >/dev/null 2>&1; then
    set_env_value AWS_ACCOUNT_ID "$(printf '%s\n' "$terraform_outputs" | jq -er '.aws_account_id.value | tostring')"
    set_env_value AWS_REGION "$(printf '%s\n' "$terraform_outputs" | jq -er '.aws_region.value | tostring')"
    set_env_value AWS_BUILD_ROLE_ARN "$(printf '%s\n' "$terraform_outputs" | jq -er '.aws_build_role_arn.value | tostring')"
    set_env_value AWS_DEPLOY_ROLE_ARN "$(printf '%s\n' "$terraform_outputs" | jq -er '.aws_deploy_role_arn.value | tostring')"
    set_env_value ECR_REPOSITORY "$(printf '%s\n' "$terraform_outputs" | jq -er '.ecr_repository.value | tostring')"
    terraform_values_loaded=true
  fi
fi

kubeconfig_loaded=false
if [ -f "$kubeconfig_file" ] && [ ! -L "$kubeconfig_file" ]; then
  grep -Fq 'server: https://127.0.0.1:6443' "$kubeconfig_file" || fail "$kubeconfig_file must use https://127.0.0.1:6443"
  kubeconfig_base64=$(base64 <"$kubeconfig_file" | tr -d '\n')
  [ -n "$kubeconfig_base64" ] || fail "could not encode $kubeconfig_file"
  set_env_value KUBECONFIG_BASE64 "$kubeconfig_base64"
  kubeconfig_loaded=true
fi

echo "Prepared Terraform variables: $tfvars_file"
echo "Prepared development environment: $env_file"
[ "$terraform_values_loaded" = true ] || echo 'AWS outputs remain pending; rerun after terraform apply.'
[ "$kubeconfig_loaded" = true ] || echo 'KUBECONFIG_BASE64 remains pending; rerun after creating the restricted kubeconfig.'

remaining_keys=$(awk '
  /^[A-Z][A-Z0-9_]*=/ {
    key=$0; sub(/=.*/,"",key)
    value=$0; sub(/^[^=]*=/,"",value)
    if (value ~ /<[^>]+>/) print key
  }
' "$env_file")
if [ -n "$remaining_keys" ]; then
  echo 'Manual values still required:'
  printf '%s\n' "$remaining_keys"
fi
