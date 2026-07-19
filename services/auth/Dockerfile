# syntax=docker/dockerfile:1.14

# Builder stage: compile the auth service binary and the healthcheck binary.
#
# The builder runs on the build host's native platform (--platform=$BUILDPLATFORM)
# so Go cross-compiles to the requested target via GOOS/GOARCH. This keeps
# builds fast (no QEMU) while still producing images for any target arch.
FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS builder

WORKDIR /workspace

# Cache module layer. go.mod/go.sum are copied before the rest of the source
# so dependency downloads are cached across source changes.
COPY services/auth/go.mod services/auth/go.sum ./services/auth/
COPY go.work ./

# Download dependencies. We allow the network for the first build; the host
# Docker build cache persists the module layer across builds.
RUN cd services/auth && go mod download

# Copy the rest of the auth service source. Build context is the repo root so
# migrations and source paths remain stable.
COPY services/auth ./services/auth

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
RUN cd services/auth && \
    export GOOS="${TARGETOS}" GOARCH="${TARGETARCH:-$(go env GOHOSTARCH)}" && \
    go build -trimpath -ldflags="-s -w" -o /out/auth ./cmd/auth && \
    go build -trimpath -ldflags="-s -w" -o /out/healthcheck ./cmd/healthcheck

# Final runtime stage: minimal non-root image with no toolchain.
# alpine:3.22 is pinned (not fabricated); digests are intentionally not
# hard-coded here and are left to the deploy pipeline to pin/verify.
FROM alpine:3.22 AS runtime

# ca-certificates is required for any TLS the auth service may use to reach
# other services; tini provides PID 1 signal forwarding for graceful shutdown.
RUN apk add --no-cache ca-certificates tini && \
    addgroup -S -g 10001 tokenmp && \
    adduser -S -D -H -u 10001 -G tokenmp tokenmp

WORKDIR /app

# Copy only the two compiled binaries. The migrate CLI is intentionally NOT
# included in the runtime image; migrations are applied out-of-band by CI/ops,
# never by the container at startup.
COPY --from=builder /out/auth /usr/local/bin/auth
COPY --from=builder /out/healthcheck /usr/local/bin/healthcheck

USER 10001:10001

# The healthcheck hits /healthz on the bound address. AUTH_HEALTHCHECK_ADDR
# can be overridden by the deployment.
ENV AUTH_HEALTHCHECK_ADDR=http://127.0.0.1:8080/healthz
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/healthcheck"]

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/auth"]
