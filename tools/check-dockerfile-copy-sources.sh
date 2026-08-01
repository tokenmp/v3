#!/usr/bin/env bash
# Verify that repo-root-context Dockerfile COPY sources exist and cover every
# local package replace declared by each deployable Go service. Also validate
# the clean-checkout Web production Dockerfile. --from copies are stage-local
# and deliberately skipped.
set -euo pipefail

readonly services=(api auth billing config executor logging notice)
readonly web_dockerfile="apps/web/Dockerfile.web"
status=0

# Docker parser directives must appear only at the start of a Dockerfile.
# A duplicate syntax directive fails BuildKit before COPY checks or any build.
while IFS= read -r dockerfile; do
  syntax_count="$(grep -Ec '^[[:space:]]*#[[:space:]]*syntax=docker/dockerfile:' "$dockerfile" || true)"
  if [[ "$syntax_count" -gt 1 || "$(head -n 1 "$dockerfile")" != '# syntax=docker/dockerfile:'* ]]; then
    printf '%s: require exactly one syntax directive on the first line\n' "$dockerfile" >&2
    status=1
  fi
done < <(find apps services -name 'Dockerfile*' -type f -print | sort)

for service in "${services[@]}"; do
  dockerfile="services/${service}/Dockerfile"
  module="services/${service}"
  [[ -f "$dockerfile" ]] || { printf 'missing Dockerfile: %s\n' "$dockerfile" >&2; exit 1; }
  [[ -f "$module/go.mod" ]] || { printf 'missing go.mod: %s\n' "$module/go.mod" >&2; exit 1; }

  # GOPROXY is a builder-only, environment-overridable build input. It must be
  # declared and exported before the first module download so it also governs
  # any dependency download Go performs later during `go build`.
  arg_line="$(grep -nFx 'ARG GOPROXY=https://proxy.golang.org,direct' "$dockerfile" | cut -d: -f1 || true)"
  env_line="$(grep -nFx 'ENV GOPROXY=${GOPROXY}' "$dockerfile" | cut -d: -f1 || true)"
  download_line="$(grep -nE 'go mod download([[:space:]]|$)' "$dockerfile" | head -n 1 | cut -d: -f1 || true)"
  if [[ "$(printf '%s\n' "$arg_line" | sed '/^$/d' | wc -l | tr -d ' ')" != 1 ||
        "$(printf '%s\n' "$env_line" | sed '/^$/d' | wc -l | tr -d ' ')" != 1 ||
        -z "$download_line" || "$arg_line" -ge "$download_line" || "$env_line" -ge "$download_line" ]]; then
    printf '%s: require one ARG GOPROXY and ENV GOPROXY before go mod download\n' "$dockerfile" >&2
    status=1
  fi

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

# Every deployable Go service must pass the same public default through Compose
# while allowing an environment-specific module proxy override. Web is not a
# Go build and must not receive this argument.
readonly compose="compose.yaml"
[[ -f "$compose" ]] || { printf 'missing Compose file: %s\n' "$compose" >&2; exit 1; }
for service in "${services[@]}"; do
  # Scope the check to this service's `build:` mapping, so a GOPROXY-like
  # runtime environment entry cannot satisfy the build-argument contract.
  if ! awk -v service="$service" '
    $0 == "  " service ":" { service_open=1; next }
    service_open && /^  [[:alnum:]_-]+:$/ { exit }
    service_open && /^    build:$/ { build_open=1; next }
    build_open && /^    [[:alnum:]_-]+:$/ { exit }
    build_open && $0 == "      dockerfile: services/" service "/Dockerfile" { dockerfile=1 }
    build_open && /^      args:$/ { args_open=1; next }
    args_open && /^      [[:alnum:]_-]+:$/ { args_open=0 }
    args_open && $0 == "        GOPROXY: ${TOKENMP_V3_GO_PROXY:-https://proxy.golang.org,direct}" { proxy=1 }
    END { exit !(dockerfile && proxy) }
  ' "$compose"; then
    printf '%s: require GOPROXY build arg for Go service %s\n' "$compose" "$service" >&2
    status=1
  fi
done
web_block="$(awk '
  $0 == "  web:" { inside=1; next }
  inside && /^  [[:alnum:]_-]+:$/ { exit }
  inside { print }
' "$compose")"
if printf '%s\n' "$web_block" | grep -F 'GOPROXY:' >/dev/null; then
  printf '%s: web must not receive a GOPROXY build arg\n' "$compose" >&2
  status=1
fi

# Compose builds Web with this Dockerfile. Every non-stage COPY source must be
# a tracked clean-context input, never an ignored prebuilt .next artifact or
# optional apps/web/public directory.
[[ -f "$web_dockerfile" ]] || { printf 'missing Dockerfile: %s\n' "$web_dockerfile" >&2; exit 1; }
web_sources="$(
  awk '
    /^[[:space:]]*COPY[[:space:]]/ {
      line=$0
      sub(/^[[:space:]]*COPY[[:space:]]+/, "", line)
      if (line ~ /^--from=/) next
      n=split(line, fields, /[[:space:]]+/)
      for (i=1; i<n; i++) print fields[i]
    }
  ' "$web_dockerfile"
)"
while IFS= read -r source; do
  [[ -n "$source" ]] || continue
  case "$source" in
    package.json|pnpm-lock.yaml|pnpm-workspace.yaml|apps/web|apps/web/*|packages/contracts|packages/contracts/*|packages/ui-tokens|packages/ui-tokens/*) ;;
    *)
      printf '%s: unexpected repo-root COPY source: %s\n' "$web_dockerfile" "$source" >&2
      status=1
      continue
      ;;
  esac
  if [[ ! -e "$source" ]]; then
    printf '%s: COPY source does not exist in repo-root context: %s\n' "$web_dockerfile" "$source" >&2
    status=1
  fi
done <<< "$web_sources"

if grep -Eq '^[[:space:]]*COPY([[:space:]]+--[^[:space:]]+)*[[:space:]]+apps/web/(public|\.next)([[:space:]/]|$)' "$web_dockerfile"; then
  printf '%s: must not COPY optional public or prebuilt .next from the build context\n' "$web_dockerfile" >&2
  status=1
fi

exit "$status"
