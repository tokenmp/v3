#!/usr/bin/env bash
# Seed one minimal Config Service provider/model/credential/route and publish it.
# This script never accepts or transmits an upstream API key: credentials are
# represented only by a vault:// reference in the Config database.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: CONFIG_ADMIN_TOKEN_FILE=/path/to/config-admin-token \
  tools/seed-config.sh <config-admin-base-url> [tools/seed-config.example.json]

The base URL must reach Config Service directly (for example,
http://127.0.0.1:18082) and must not be publicly exposed. The JSON input is a
safe template, not provider data. Replace every "replace-with-..." value,
choose an actual HTTPS provider base URL and upstream model, then pass it to
this script. The script creates the model, provider, credential, and route,
and invokes /v1/config/admin/compile to create and publish a snapshot.

The input credential_ref must be vault://... . Configure the matching
EXECUTOR_CREDENTIAL_REF_MAP_JSON key and its named secret environment variable
before starting Executor. No upstream API key belongs in this JSON or Config DB.
EOF
}

if [[ ${1:-} == "-h" || ${1:-} == "--help" || $# -lt 1 || $# -gt 2 ]]; then
  usage >&2
  exit 2
fi

base_url=${1%/}
seed_file=${2:-tools/seed-config.example.json}
token_file=${CONFIG_ADMIN_TOKEN_FILE:-}

[[ -n $token_file && -f $token_file ]] || { echo "CONFIG_ADMIN_TOKEN_FILE must name a readable regular file" >&2; exit 1; }
[[ -f $seed_file ]] || { echo "seed file not found: $seed_file" >&2; exit 1; }
command -v curl >/dev/null || { echo "curl is required" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

jq -e '
  (.model.id | strings | startswith("replace-with-") | not) and
  (.provider.id | strings | startswith("replace-with-") | not) and
  (.provider.base_url | strings | startswith("https://")) and
  (.credential.id | strings | startswith("replace-with-") | not) and
  (.credential.credential_ref | strings | startswith("vault://")) and
  (.route.id | strings | startswith("replace-with-") | not) and
  (.route.upstream_model | strings | startswith("replace-with-") | not)
' "$seed_file" >/dev/null || {
  echo "seed input still has placeholders, lacks an HTTPS base_url, or lacks a vault:// credential_ref" >&2
  exit 1
}

admin_token=$(<"$token_file")
[[ -n $admin_token ]] || { echo "CONFIG_ADMIN_TOKEN_FILE is empty" >&2; exit 1; }
trap 'unset admin_token' EXIT

request() {
  local method=$1 path=$2 body=${3:-}
  local args=(--fail-with-body --silent --show-error --connect-timeout 5 --max-time 20
    -X "$method" -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json')
  if [[ -n $body ]]; then args+=(--data-binary "@$body"); fi
  curl "${args[@]}" "$base_url$path"
}

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"; unset admin_token' EXIT
jq '.model' "$seed_file" > "$tmpdir/model.json"
jq '.provider' "$seed_file" > "$tmpdir/provider.json"
jq --arg provider_id "$(jq -r '.provider.id' "$seed_file")" '.credential + {provider_id: $provider_id}' "$seed_file" > "$tmpdir/credential.json"
jq --arg model_id "$(jq -r '.model.id' "$seed_file")" --arg provider_id "$(jq -r '.provider.id' "$seed_file")" '.route + {model_id: $model_id, provider_id: $provider_id}' "$seed_file" > "$tmpdir/route.json"

request POST /v1/config/admin/models "$tmpdir/model.json" >/dev/null
request POST /v1/config/admin/providers "$tmpdir/provider.json" >/dev/null
request POST "/v1/config/admin/providers/$(jq -r '.provider.id' "$seed_file")/credentials" "$tmpdir/credential.json" >/dev/null
request POST /v1/config/admin/routes "$tmpdir/route.json" >/dev/null
request POST /v1/config/admin/compile >/dev/null

printf '%s\n' 'Minimal Config snapshot published. Verify GET /v1/config/snapshots/latest and start/reload Executor only after its credential mapping is installed.'
