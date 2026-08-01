# syntax=docker/dockerfile:1.14

# Build from the repository root so the service-local replace directive for the
# shared http response package resolves without copying the full monorepo.
FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS builder

# Keep the public Go module proxy as the portable default. Deployments may
# provide GOPROXY at build time without baking an environment-specific proxy
# into this Dockerfile. ENV also covers downloads triggered by go build.
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /workspace

COPY services/logging/go.mod services/logging/go.sum ./services/logging/
COPY packages/go/httpresp ./packages/go/httpresp
RUN cd services/logging && GOWORK=off go mod download

COPY services/logging ./services/logging

ARG TARGETOS=linux
ARG TARGETARCH
ENV CGO_ENABLED=0
RUN cd services/logging && \
    export GOWORK=off GOOS="${TARGETOS}" GOARCH="${TARGETARCH:-$(go env GOHOSTARCH)}" && \
    go build -trimpath -ldflags="-s -w" -o /out/logging ./cmd/logging

FROM alpine:3.22 AS runtime

RUN apk add --no-cache ca-certificates tini && \
    addgroup -S -g 10001 tokenmp && \
    adduser -S -D -H -u 10001 -G tokenmp tokenmp

WORKDIR /app
COPY --from=builder /out/logging /usr/local/bin/logging

USER 10001:10001

EXPOSE 8083

# /readyz confirms database readiness.
ENV LOGGING_HEALTHCHECK_ADDR=http://127.0.0.1:8083/readyz
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null "$LOGGING_HEALTHCHECK_ADDR"

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/logging"]
