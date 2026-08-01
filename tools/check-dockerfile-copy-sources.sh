#!/usr/bin/env bash
# Verify that repo-root-context Dockerfile COPY sources exist before Docker
# evaluates them. --from copies are stage-local and deliberately skipped.
set -euo pipefail

readonly services=(api billing config logging)
status=0

for service in "${services[@]}"; do
  dockerfile="services/${service}/Dockerfile"
  [[ -f "$dockerfile" ]] || { printf 'missing Dockerfile: %s\n' "$dockerfile" >&2; exit 1; }

  # These images are always built with the repository root as build context.
  while IFS= read -r source; do
    case "$source" in
      services/"$service"|services/"$service"/*|packages/go/httpresp|packages/go/httpresp/*) ;;
      *)
        printf '%s: unexpected repo-root COPY source: %s\n' "$dockerfile" "$source" >&2
        status=1
        continue
        ;;
    esac
    if [[ ! -e "$source" ]]; then
      printf '%s: COPY source does not exist in repo-root context: %s\n' "$dockerfile" "$source" >&2
      status=1
    fi
  done < <(
    awk '
      /^[[:space:]]*COPY[[:space:]]/ {
        line=$0
        sub(/^[[:space:]]*COPY[[:space:]]+/, "", line)
        if (line ~ /^--from=/) next
        n=split(line, fields, /[[:space:]]+/)
        # All sources precede the final destination.
        for (i=1; i<n; i++) print fields[i]
      }
    ' "$dockerfile"
  )
done

exit "$status"
