#!/usr/bin/env bash
# Static Compose environment contract. Keep this allowlist synchronized with
# services/*/internal/config when the feature branches are integrated. It is
# deliberately self-contained: CI checkouts do not contain sibling worktrees.
set -euo pipefail

compose=${1:-compose.yaml}
[ -f "$compose" ] || { echo "missing Compose file: $compose" >&2; exit 1; }

# Feature-branch config allowlist (exact accepted names, not aliases):
# shared-rate-limit Auth/API; config-publish-hardening API _FILE follow-up;
# billing-settlement Billing _FILE follow-up.
required=(
  AUTH_RATE_LIMIT_ENABLED AUTH_RATE_LIMIT_REDIS_ADDR AUTH_RATE_LIMIT_REDIS_DB
  AUTH_RATE_LIMIT_HMAC_SECRET_FILE AUTH_RATE_LIMIT_TRUSTED_PROXIES
  AUTH_RATE_LIMIT_LOGIN_IP_CAPACITY AUTH_RATE_LIMIT_LOGIN_IP_REFILL
  AUTH_RATE_LIMIT_LOGIN_ACCOUNT_CAPACITY AUTH_RATE_LIMIT_LOGIN_ACCOUNT_REFILL
  AUTH_RATE_LIMIT_REGISTER_IP_CAPACITY AUTH_RATE_LIMIT_REGISTER_IP_REFILL
  AUTH_RATE_LIMIT_REGISTER_ACCOUNT_CAPACITY AUTH_RATE_LIMIT_REGISTER_ACCOUNT_REFILL
  AUTH_RATE_LIMIT_REFRESH_IP_CAPACITY AUTH_RATE_LIMIT_REFRESH_IP_REFILL
  AUTH_RATE_LIMIT_REFRESH_ACCOUNT_CAPACITY AUTH_RATE_LIMIT_REFRESH_ACCOUNT_REFILL
  AUTH_RATE_LIMIT_BUCKET_TTL
  API_RATE_LIMIT_ENABLED API_RATE_LIMIT_REDIS_ADDR API_RATE_LIMIT_REDIS_DB
  API_RATE_LIMIT_HMAC_SECRET_FILE API_RATE_LIMIT_TRUSTED_PROXIES
  API_RATE_LIMIT_IP_CAPACITY API_RATE_LIMIT_IP_REFILL
  API_RATE_LIMIT_SUBJECT_CAPACITY API_RATE_LIMIT_SUBJECT_REFILL API_RATE_LIMIT_BUCKET_TTL
  CONFIG_ADMIN_TOKEN_FILE API_CONFIG_SERVICE_TOKEN_FILE
  BILLING_LOGGING_URL BILLING_LOGGING_SERVICE_TOKEN_FILE BILLING_LOGGING_TIMEOUT
  BILLING_SWEEPER_ENABLED BILLING_SWEEPER_INTERVAL BILLING_SWEEPER_PENDING_GRACE
  BILLING_SWEEPER_EXPIRY_BATCH BILLING_SWEEPER_PENDING_BATCH
  BILLING_SWEEPER_RETENTION_DEADLINE BILLING_SWEEPER_UNKNOWN_POLICY
)

for name in "${required[@]}"; do
  if ! grep -Eq "^[[:space:]]+${name}:" "$compose"; then
    echo "compose env contract missing: ${name}" >&2
    exit 1
  fi
done

# These were speculative aliases and are never accepted by the feature configs.
for name in AUTH_REDIS_URL API_REDIS_URL AUTH_HMAC_SECRET_FILE API_HMAC_SECRET_FILE API_CONFIG_TOKEN_FILE; do
  if grep -Eq "^[[:space:]]+${name}:" "$compose"; then
    echo "compose env contract contains forbidden alias: ${name}" >&2
    exit 1
  fi
done

# Tokens must be paths to Docker secrets. Compose must never interpolate their
# contents into an environment value.
for name in CONFIG_ADMIN_TOKEN_FILE API_CONFIG_SERVICE_TOKEN_FILE BILLING_LOGGING_SERVICE_TOKEN_FILE AUTH_RATE_LIMIT_HMAC_SECRET_FILE API_RATE_LIMIT_HMAC_SECRET_FILE; do
  if ! grep -Eq "^[[:space:]]+${name}: /run/secrets/" "$compose"; then
    echo "${name} must point at a /run/secrets file" >&2
    exit 1
  fi
done
