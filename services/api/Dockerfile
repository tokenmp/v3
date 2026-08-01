# syntax=docker/dockerfile:1.14

# Build from the repository root so the service-local replace directive for the
# shared http response package resolves without copying the full monorepo.
FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS builder

WORKDIR /workspace

COPY services/api/go.mod services/api/go.sum ./services/api/
COPY packages/go/httpresp ./packages/go/httpresp
COPY packages/go/ratelimit ./packages/go/ratelimit
RUN cd services/api && GOWORK=off go mod download

COPY services/api ./services/api

ARG TARGETOS=linux
ARG TARGETARCH
ENV CGO_ENABLED=0
RUN cd services/api && \
    export GOWORK=off GOOS="${TARGETOS}" GOARCH="${TARGETARCH:-$(go env GOHOSTARCH)}" && \
    go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM alpine:3.22 AS runtime

RUN apk add --no-cache ca-certificates tini && \
    addgroup -S -g 10001 tokenmp && \
    adduser -S -D -H -u 10001 -G tokenmp tokenmp

WORKDIR /app
COPY --from=builder /out/api /usr/local/bin/api

USER 10001:10001

EXPOSE 3002

# /healthz confirms liveness.
ENV API_HEALTHCHECK_ADDR=http://127.0.0.1:3002/healthz
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null "$API_HEALTHCHECK_ADDR"

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/api"]
