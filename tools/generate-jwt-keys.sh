#!/usr/bin/env bash
# Generate the Ed25519 signing key pair shared by Auth and JWT-verifying services.
# The output directory is deployment-owned and must not be committed.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: tools/generate-jwt-keys.sh <output-directory>

Creates auth-jwt-private.pem (0600) and auth-jwt-public.pem (0644). The private
key is for Auth only. Mount the public key read-only into API, Executor, and
Notice. The script refuses to overwrite either existing output file.
EOF
}

if [[ $# -ne 1 || $1 == "-h" || $1 == "--help" ]]; then
  usage >&2
  exit 2
fi

output_dir=$1
private_key="$output_dir/auth-jwt-private.pem"
public_key="$output_dir/auth-jwt-public.pem"

if [[ -e $private_key || -e $public_key ]]; then
  echo "refusing to overwrite existing JWT key file" >&2
  exit 1
fi

umask 077
mkdir -p -- "$output_dir"

cleanup() {
  rm -f -- "$private_key" "$public_key"
}
trap cleanup ERR

openssl genpkey -algorithm ED25519 -out "$private_key"
openssl pkey -in "$private_key" -pubout -out "$public_key"
chmod 0600 "$private_key"
chmod 0644 "$public_key"

# Prove both output files are parseable and the private key is Ed25519 before
# reporting paths; do not print PEM contents.
openssl pkey -in "$private_key" -noout -text | grep -q 'ED25519 Private-Key'
openssl pkey -pubin -in "$public_key" -noout -text | grep -q 'ED25519 Public-Key'
trap - ERR

printf '%s\n' "Generated Ed25519 JWT keys:"
printf '%s\n' "  private (Auth only, mode 0600): $private_key"
printf '%s\n' "  public  (API/Executor/Notice, mode 0644): $public_key"
printf '%s\n' "Mount these deployment-owned files via AUTH_JWT_*_KEY_FILE, API_JWT_PUBLIC_KEY_FILE, EXECUTOR_JWT_PUBLIC_KEY_FILE, and NOTICE_JWT_PUBLIC_KEY_FILE."
