# syntax=docker/dockerfile:1.14

# Builder stage: compile the executor service binary and the healthcheck binary.
#
# The builder runs on the build host's native platform (--platform=$BUILDPLATFORM)
# so Go cross-compiles to the requested target via GOOS/GOARCH. This keeps
# builds fast (no QEMU) while still producing images for any target arch.
FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS builder

WORKDIR /workspace

# Cache module layer. go.mod/go.sum are copied before the rest of the source
# so dependency downloads are cached across source changes.
COPY services/executor/go.mod services/executor/go.sum ./services/executor/

# Build the independently deployable Executor module outside the repository
# workspace. Copying go.work would require copying every workspace module into
# this image and would violate the per-service image boundary.
# We allow the network for the first build; the host Docker build cache persists
# the module layer across builds.
RUN cd services/executor && GOWORK=off go mod download

# Copy the rest of the executor service source. Build context is the repo root
# so migrations and source paths remain stable.
COPY services/executor ./services/executor

# TARGETOS/TARGETARCH are injected automatically by BuildKit for multi-arch
# builds (e.g. `docker buildx build --platform linux/amd64,linux/arm64`).
# They are intentionally NOT given an amd64 default — defaulting to amd64
# would silently produce the wrong arch for non-amd64 targets when the value
# is unset. When a plain `docker build` does not inject them, the shell falls
# back to Go's native host arch (go env GOHOSTARCH) for a reliable local
# build, and TARGETOS defaults to linux. CGO is disabled so the resulting
# static binary runs on any Alpine variant.
ARG TARGETOS=linux
ARG TARGETARCH
ENV CGO_ENABLED=0
RUN cd services/executor && \
    export GOWORK=off GOOS="${TARGETOS}" GOARCH="${TARGETARCH:-$(go env GOHOSTARCH)}" && \
    go build -trimpath -ldflags="-s -w" -o /out/executor ./cmd/executor && \
    go build -trimpath -ldflags="-s -w" -o /out/healthcheck ./cmd/healthcheck

# Final runtime stage: minimal non-root image with no toolchain.
# alpine:3.22 is pinned (not fabricated); digests are intentionally not
# hard-coded here and are left to the deploy pipeline to pin/verify.
FROM alpine:3.22 AS runtime

# ca-certificates is required for any TLS the executor service may use to reach
# upstream providers; tini provides PID 1 signal forwarding for graceful shutdown.
RUN apk add --no-cache ca-certificates tini && \
    addgroup -S -g 10001 tokenmp && \
    adduser -S -D -H -u 10001 -G tokenmp tokenmp

WORKDIR /app

# Copy only the two compiled binaries. No config files, secrets, credentials,
# or credential mappings are baked into the image — all runtime configuration
# is provided via environment variables and mounted files.
COPY --from=builder /out/executor /usr/local/bin/executor
COPY --from=builder /out/healthcheck /usr/local/bin/healthcheck

USER 10001:10001

EXPOSE 8081

# The healthcheck hits /healthz on the bound address. EXECUTOR_HEALTHCHECK_ADDR
# can be overridden by the deployment.
ENV EXECUTOR_HEALTHCHECK_ADDR=http://127.0.0.1:8081/healthz
HEALTHCHECK --interval=15s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/healthcheck"]

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/executor"]
