#!/bin/sh
set -eu

fail() { echo "Development environment upload failed: $*" >&2; exit 1; }
usage() { echo "usage: $0 [--check|--generate|--replace] [ENV_FILE] [OUTPUT_DIR]" >&2; exit 2; }

mode=upload
case "${1:-}" in
  --check) mode=check; shift ;;
  --generate) mode=generate; shift ;;
  --replace) mode=replace; shift ;;
esac
[ "$#" -le 2 ] || usage

env_file=${1:-tmp/development.env}
output_dir=${2:-tmp/github-environments}
[ -f "$env_file" ] && [ ! -L "$env_file" ] || fail "$env_file must be a regular file"
script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

for command in awk grep mktemp openssl; do command -v "$command" >/dev/null 2>&1 || fail "missing command: $command"; done

ecr_variables='AWS_ACCOUNT_ID AWS_REGION AWS_BUILD_ROLE_ARN ECR_REPOSITORY'
deployment_variables='AWS_ACCOUNT_ID AWS_REGION AWS_DEPLOY_ROLE_ARN ECR_REPOSITORY KUBE_NAMESPACE API_HOSTNAME KUBERNETES_ACCESS_HOSTNAME POSTGRES_CA_SECRET_NAME POSTGRES_CA_SECRET_KEY OUTBOUND_CA_SECRET_NAME OUTBOUND_CA_SECRET_KEY'
deployment_secrets='KUBECONFIG_BASE64 CLOUDFLARE_ACCESS_CLIENT_ID CLOUDFLARE_ACCESS_CLIENT_SECRET'
runtime_variables='BILLMESH_ENVIRONMENT DELEGATION_ISSUER DELEGATION_DISCOVERY_URL DELEGATION_AUDIENCE DELEGATION_PROFILE_REFRESH_INTERVAL DELEGATION_PROFILE_MAX_STALENESS WEBHOOK_TIMEOUT WORKER_INTERVAL WEBHOOK_ALLOWED_PRIVATE_HOSTS AUTH_FAILURES_PER_MINUTE MUTATION_RATE_PER_SECOND MUTATION_BURST CONSOLE_APP_ORIGIN ADMIN_APP_ORIGIN BFF_CONSOLE_ISSUER BFF_CONSOLE_CLIENT_ID BFF_CONSOLE_AUDIENCE BFF_CONSOLE_SCOPE BFF_CONSOLE_REDIRECT_URI BFF_CONSOLE_POST_LOGOUT_REDIRECT_URI BFF_CONSOLE_STANDALONE_LOGOUT_URI BFF_ADMIN_ISSUER BFF_ADMIN_CLIENT_ID BFF_ADMIN_AUDIENCE BFF_ADMIN_SCOPE BFF_ADMIN_REDIRECT_URI BFF_ADMIN_POST_LOGOUT_REDIRECT_URI BFF_ADMIN_STANDALONE_LOGOUT_URI BFF_SESSION_IDLE_TTL BFF_SESSION_ABSOLUTE_TTL BFF_LOGIN_TTL BFF_LOGOUT_TTL BFF_REFRESH_SKEW BFF_LOGIN_ATTEMPTS_PER_MINUTE RAZORPAY_BASE_URL RAZORPAY_CHECKOUT_URL RAZORPAY_ACCOUNT_ID RAZORPAY_PREVIOUS_WEBHOOK_SECRET_EXPIRES_AT SSL_CERT_FILE'
runtime_secrets='DATABASE_URL BFF_CONSOLE_CLIENT_SECRET BFF_ADMIN_CLIENT_SECRET BFF_SESSION_ENCRYPTION_KEYS RAZORPAY_KEY_ID RAZORPAY_KEY_SECRET RAZORPAY_WEBHOOK_SECRET RAZORPAY_PREVIOUS_WEBHOOK_SECRET'
allowed_keys="$ecr_variables $deployment_variables $deployment_secrets $runtime_variables $runtime_secrets"
required_keys='AWS_ACCOUNT_ID AWS_REGION AWS_BUILD_ROLE_ARN AWS_DEPLOY_ROLE_ARN ECR_REPOSITORY KUBE_NAMESPACE API_HOSTNAME KUBERNETES_ACCESS_HOSTNAME KUBECONFIG_BASE64 CLOUDFLARE_ACCESS_CLIENT_ID CLOUDFLARE_ACCESS_CLIENT_SECRET BILLMESH_ENVIRONMENT DATABASE_URL DELEGATION_ISSUER DELEGATION_DISCOVERY_URL DELEGATION_AUDIENCE CONSOLE_APP_ORIGIN ADMIN_APP_ORIGIN BFF_CONSOLE_ISSUER BFF_CONSOLE_CLIENT_ID BFF_CONSOLE_CLIENT_SECRET BFF_CONSOLE_AUDIENCE BFF_CONSOLE_REDIRECT_URI BFF_CONSOLE_POST_LOGOUT_REDIRECT_URI BFF_CONSOLE_STANDALONE_LOGOUT_URI BFF_ADMIN_ISSUER BFF_ADMIN_CLIENT_ID BFF_ADMIN_CLIENT_SECRET BFF_ADMIN_AUDIENCE BFF_ADMIN_REDIRECT_URI BFF_ADMIN_POST_LOGOUT_REDIRECT_URI BFF_ADMIN_STANDALONE_LOGOUT_URI BFF_SESSION_ENCRYPTION_KEYS'

temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/billmesh-github-env.XXXXXX")
cleanup() { rm -rf "$temporary_dir"; }
trap cleanup EXIT HUP INT TERM

ecr_file=$temporary_dir/ecr-build.vars.env
deployment_variables_file=$temporary_dir/billmesh-development-api.deployment.vars.env
deployment_secrets_file=$temporary_dir/billmesh-development-api.deployment.secrets.env
runtime_variables_file=$temporary_dir/billmesh-development-api.runtime.vars.env
runtime_secrets_file=$temporary_dir/billmesh-development-api.runtime.secrets.env
app_env_file=$temporary_dir/application.env
kubeconfig_file=$temporary_dir/kubeconfig.yaml

for generated_file in "$ecr_file" "$deployment_variables_file" "$deployment_secrets_file" "$runtime_variables_file" "$runtime_secrets_file"; do
  echo '# Generated from tmp/development.env; do not edit.' >"$generated_file"
  chmod 600 "$generated_file"
done
: >"$kubeconfig_file"
chmod 600 "$kubeconfig_file"

contains_key() { case " $1 " in *" $2 "*) return 0 ;; *) return 1 ;; esac; }
key_present() { case "$seen_keys" in *" $1 "*) return 0 ;; *) return 1 ;; esac; }
value_for() { awk -v wanted="$1" 'index($0,"=")>0 && substr($0,1,index($0,"=")-1)==wanted {print substr($0,index($0,"=")+1); exit}' "$env_file"; }

seen_keys=' '
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in ''|'#'*) continue ;; esac
  case "$line" in *=*) ;; *) fail "invalid line in $env_file" ;; esac
  key=${line%%=*}
  value=${line#*=}
  printf '%s' "$key" | grep -Eq '^[A-Z][A-Z0-9_]*$' || fail "invalid key: $key"
  contains_key "$allowed_keys" "$key" || fail "unexpected key: $key"
  key_present "$key" && fail "duplicate key: $key"
  seen_keys="$seen_keys$key "
  [ -n "$value" ] || fail "$key is empty"
  case "$value" in *'< '*|*'<'*'>'*|*replace-with*|*change-me*|*TODO*) fail "$key still contains a placeholder" ;; esac

  contains_key "$ecr_variables" "$key" && printf '%s=%s\n' "$key" "$value" >>"$ecr_file"
  contains_key "$deployment_variables" "$key" && printf '%s=%s\n' "$key" "$value" >>"$deployment_variables_file"
  contains_key "$deployment_secrets" "$key" && printf '%s=%s\n' "$key" "$value" >>"$deployment_secrets_file"
  contains_key "$runtime_variables" "$key" && printf '%s=%s\n' "$key" "$value" >>"$runtime_variables_file"
  contains_key "$runtime_secrets" "$key" && printf '%s=%s\n' "$key" "$value" >>"$runtime_secrets_file"
done <"$env_file"

for key in $required_keys; do key_present "$key" || fail "$key is missing"; done

require_complete_pair() {
  first=$1
  second=$2
  if key_present "$first" || key_present "$second"; then
    key_present "$first" && key_present "$second" || fail "$first and $second must be configured together"
  fi
}
require_complete_pair POSTGRES_CA_SECRET_NAME POSTGRES_CA_SECRET_KEY
require_complete_pair OUTBOUND_CA_SECRET_NAME OUTBOUND_CA_SECRET_KEY

printf '%s' "$(value_for AWS_ACCOUNT_ID)" | grep -Eq '^[0-9]{12}$' || fail 'AWS_ACCOUNT_ID must contain 12 digits'
printf '%s' "$(value_for AWS_REGION)" | grep -Eq '^[a-z0-9-]+$' || fail 'AWS_REGION is invalid'
printf '%s' "$(value_for ECR_REPOSITORY)" | grep -Eq '^[a-z0-9][a-z0-9._/-]*$' || fail 'ECR_REPOSITORY is invalid'
printf '%s' "$(value_for KUBE_NAMESPACE)" | grep -Eq '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$' || fail 'KUBE_NAMESPACE is invalid'
printf '%s' "$(value_for API_HOSTNAME)" | grep -Eq '^[A-Za-z0-9.-]+$' || fail 'API_HOSTNAME is invalid'
printf '%s' "$(value_for KUBERNETES_ACCESS_HOSTNAME)" | grep -Eq '^[A-Za-z0-9.-]+$' || fail 'KUBERNETES_ACCESS_HOSTNAME is invalid'

printf '%s' "$(value_for KUBECONFIG_BASE64)" | openssl base64 -d -A >"$kubeconfig_file" 2>/dev/null || fail 'KUBECONFIG_BASE64 is not valid base64'
grep -Fq 'apiVersion: v1' "$kubeconfig_file" || fail 'KUBECONFIG_BASE64 does not decode to a kubeconfig'
grep -Fq 'server: https://127.0.0.1:6443' "$kubeconfig_file" || fail 'restricted kubeconfig must use https://127.0.0.1:6443'
printf '%s' "$(value_for CLOUDFLARE_ACCESS_CLIENT_ID)" | grep -Eq '^[A-Za-z0-9_-]+\.access$' || fail 'CLOUDFLARE_ACCESS_CLIENT_ID has an unexpected format'

grep -v '^#' "$runtime_variables_file" >"$app_env_file"
grep -v '^#' "$runtime_secrets_file" >>"$app_env_file"
"$script_dir/../kubernetes/validate-app-env.sh" "$app_env_file" >/dev/null

if [ -L "$output_dir" ]; then fail "$output_dir must not be a symbolic link"; fi
mkdir -p "$output_dir"
for output_name in ecr-build.vars.env billmesh-development-api.deployment.vars.env billmesh-development-api.deployment.secrets.env billmesh-development-api.runtime.vars.env billmesh-development-api.runtime.secrets.env; do
  [ ! -L "$output_dir/$output_name" ] || fail "$output_dir/$output_name must not be a symbolic link"
  source_file=$temporary_dir/$output_name
  mv "$source_file" "$output_dir/$output_name"
done

echo "Generated ecr-build and billmesh-development-api files in $output_dir."
if [ "$mode" = check ] || [ "$mode" = generate ]; then
  echo "Development environment validation passed: $env_file"
  exit 0
fi

command -v gh >/dev/null 2>&1 || fail 'GitHub CLI is not installed'
gh auth status >/dev/null

if [ "$mode" = replace ]; then
  for environment in ecr-build billmesh-development-api; do
    for name in $(gh variable list --env "$environment" --json name --jq '.[].name'); do gh variable delete "$name" --env "$environment"; done
    for name in $(gh secret list --env "$environment" --json name --jq '.[].name'); do gh secret delete "$name" --env "$environment"; done
  done
fi

gh variable set --env ecr-build --env-file "$output_dir/ecr-build.vars.env"
gh variable set --env billmesh-development-api --env-file "$output_dir/billmesh-development-api.deployment.vars.env"
gh variable set --env billmesh-development-api --env-file "$output_dir/billmesh-development-api.runtime.vars.env"
gh secret set --env billmesh-development-api --env-file "$output_dir/billmesh-development-api.deployment.secrets.env"
gh secret set --env billmesh-development-api --env-file "$output_dir/billmesh-development-api.runtime.secrets.env"
echo 'Uploaded ecr-build variables and billmesh-development-api variables/secrets.'
