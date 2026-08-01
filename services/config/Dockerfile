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

COPY services/config/go.mod services/config/go.sum ./services/config/
COPY packages/go/httpresp ./packages/go/httpresp
RUN cd services/config && GOWORK=off go mod download

COPY services/config ./services/config

ARG TARGETOS=linux
ARG TARGETARCH
ENV CGO_ENABLED=0
RUN cd services/config && \
    export GOWORK=off GOOS="${TARGETOS}" GOARCH="${TARGETARCH:-$(go env GOHOSTARCH)}" && \
    go build -trimpath -ldflags="-s -w" -o /out/config ./cmd/config

FROM alpine:3.22 AS runtime

RUN apk add --no-cache ca-certificates tini && \
    addgroup -S -g 10001 tokenmp && \
    adduser -S -D -H -u 10001 -G tokenmp tokenmp

WORKDIR /app
COPY --from=builder /out/config /usr/local/bin/config

USER 10001:10001

EXPOSE 8082

# /readyz confirms database readiness.
ENV CONFIG_HEALTHCHECK_ADDR=http://127.0.0.1:8082/readyz
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null "$CONFIG_HEALTHCHECK_ADDR"

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/config"]
