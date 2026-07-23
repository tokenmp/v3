#!/usr/bin/env bash
# check-generated-api.sh — Verify API v1 generated Go files are fresh.
#
# Regenerates models.gen.go and server.gen.go into a temporary directory and
# byte-compares each result with the committed output. Does not modify the
# working tree and works from any current working directory.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
COMMITTED_DIR="${REPO_ROOT}/services/api/internal/contract/apiv1"
FILES=(models.gen.go server.gen.go)

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

echo "Regenerating into a temporary directory for freshness check..." >&2
bash "${SCRIPT_DIR}/generate-api.sh" "${tmpdir}" >/dev/null 2>&1

exit_code=0
for f in "${FILES[@]}"; do
  if [ ! -f "${COMMITTED_DIR}/${f}" ]; then
    echo "MISSING: ${COMMITTED_DIR}/${f}" >&2
    exit_code=1
    continue
  fi
  if ! cmp -s "${tmpdir}/${f}" "${COMMITTED_DIR}/${f}"; then
    echo "STALE: ${COMMITTED_DIR}/${f}" >&2
    exit_code=1
  else
    echo "OK: ${f} is fresh." >&2
  fi
done

exit "${exit_code}"
