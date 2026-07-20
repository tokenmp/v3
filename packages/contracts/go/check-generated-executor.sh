#!/usr/bin/env bash
# check-generated-executor.sh — Verify Executor v1 generated Go files are fresh.
#
# Regenerates models.gen.go and server.gen.go into a temporary directory and
# byte-compares each result with the committed output. Does not modify the
# working tree and works from any current working directory.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
COMMITTED_DIR="${REPO_ROOT}/services/executor/internal/contract/executorv1"
FILES=(models.gen.go server.gen.go)

for file in "${FILES[@]}"; do
  if [ ! -f "${COMMITTED_DIR}/${file}" ]; then
    echo "ERROR: committed generated file not found: ${COMMITTED_DIR}/${file}" >&2
    echo "Run packages/contracts/go/generate-executor.sh first." >&2
    exit 1
  fi
done

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

echo "Regenerating into a temporary directory for freshness check..."
"${SCRIPT_DIR}/generate-executor.sh" "$TMPDIR" 2>&1

status=0
for file in "${FILES[@]}"; do
  if cmp -s "${COMMITTED_DIR}/${file}" "${TMPDIR}/${file}"; then
    echo "OK: ${file} is fresh."
  else
    echo "STALE: ${file} does not match regeneration." >&2
    diff -u "${COMMITTED_DIR}/${file}" "${TMPDIR}/${file}" >&2 || true
    status=1
  fi
done

if [ "$status" -ne 0 ]; then
  echo "Run: packages/contracts/go/generate-executor.sh to regenerate." >&2
fi
exit "$status"
