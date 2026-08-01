#!/usr/bin/env bash
# Verify that repo-root-context Dockerfile COPY sources exist and cover every
# local package replace declared by each deployable Go service. --from copies
# are stage-local and deliberately skipped.
set -euo pipefail

readonly services=(api auth billing config executor logging notice)
status=0

for service in "${services[@]}"; do
  dockerfile="services/${service}/Dockerfile"
  module="services/${service}"
  [[ -f "$dockerfile" ]] || { printf 'missing Dockerfile: %s\n' "$dockerfile" >&2; exit 1; }
  [[ -f "$module/go.mod" ]] || { printf 'missing go.mod: %s\n' "$module/go.mod" >&2; exit 1; }

  sources="$(
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
  )"

  while IFS= read -r source; do
    [[ -n "$source" ]] || continue
    case "$source" in
      "$module"|"$module"/*|packages/go/httpresp|packages/go/httpresp/*|packages/go/ratelimit|packages/go/ratelimit/*) ;;
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
  done <<< "$sources"

  # A Docker image builds each service with GOWORK=off. Every module-local
  # replace must therefore be copied at the same repo-root path expected by
  # the replace target, both before `go mod download` and before `go build`.
  while IFS= read -r replace_target; do
    [[ -n "$replace_target" ]] || continue
    if [[ ! -d "$module/$replace_target" || ! -f "$module/$replace_target/go.mod" ]]; then
      printf '%s: local replace target is not a Go module: %s\n' "$module/go.mod" "$replace_target" >&2
      status=1
      continue
    fi
    replacement="$(cd "$module/$replace_target" && pwd)"
    replacement="${replacement#"$PWD/"}"
    if ! printf '%s\n' "$sources" | grep -Fx -- "$replacement" >/dev/null; then
      printf '%s: missing COPY source for local replace target: %s\n' "$dockerfile" "$replacement" >&2
      status=1
    fi
  done < <(awk '$1 == "replace" && $3 == "=>" && $4 ~ /^\.\.\// { print $4 }' "$module/go.mod")
done

exit "$status"
