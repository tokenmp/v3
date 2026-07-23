#!/usr/bin/env bash
# generate-api.sh — Generate API v1 Go models and Chi/strict server code.
#
# Reads:   packages/contracts/openapi/api/v1.yaml
# Configs: packages/contracts/go/api-v1-{models,server}.yaml
# Outputs: services/api/internal/contract/apiv1/{models,server}.gen.go
#
# Usage: ./generate-api.sh [OUTPUT_DIR]
# OUTPUT_DIR defaults to services/api/internal/contract/apiv1

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
CONTRACT="${REPO_ROOT}/packages/contracts/openapi/api/v1.yaml"
MODELS_CONFIG="${REPO_ROOT}/packages/contracts/go/api-v1-models.yaml"
SERVER_CONFIG="${REPO_ROOT}/packages/contracts/go/api-v1-server.yaml"

OUTPUT_DIR="${1:-${REPO_ROOT}/services/api/internal/contract/apiv1}"

if ! command -v oapi-codegen >/dev/null 2>&1; then
  echo "Installing oapi-codegen v2.8.0..." >&2
  GOBIN="${SCRIPT_DIR}/bin" go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0
  export PATH="${SCRIPT_DIR}/bin:${PATH}"
fi

mkdir -p "${OUTPUT_DIR}"

echo "Generating API v1 Go code with oapi-codegen v2.8.0..." >&2
oapi-codegen -config="${MODELS_CONFIG}" -o="${OUTPUT_DIR}/models.gen.go" "${CONTRACT}"
echo "Generated: ${OUTPUT_DIR}/models.gen.go" >&2
oapi-codegen -config="${SERVER_CONFIG}" -o="${OUTPUT_DIR}/server.gen.go" "${CONTRACT}"
echo "Generated: ${OUTPUT_DIR}/server.gen.go" >&2
