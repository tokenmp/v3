# syntax=docker/dockerfile:1.14

# Builder stage: compile the notice service binary and the healthcheck binary.
FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS builder

WORKDIR /workspace

COPY services/notice/go.mod services/notice/go.sum ./services/notice/
# Copy the shared httpresp package (replace directive points to this path).
COPY packages/go/httpresp ./packages/go/httpresp

RUN cd services/notice && GOWORK=off go mod download

# Ensure the shared httpresp package is present for the replace directive.
COPY packages/go/httpresp ./packages/go/httpresp
COPY services/notice ./services/notice

ARG TARGETOS=linux
ARG TARGETARCH
ENV CGO_ENABLED=0
RUN cd services/notice && \
    export GOWORK=off GOOS="${TARGETOS}" GOARCH="${TARGETARCH:-$(go env GOHOSTARCH)}" && \
    go build -trimpath -ldflags="-s -w" -o /out/notice ./cmd/notice && \
    go build -trimpath -ldflags="-s -w" -o /out/healthcheck ./cmd/healthcheck

FROM alpine:3.22 AS runtime

RUN apk add --no-cache ca-certificates tini && \
    addgroup -S -g 10001 tokenmp && \
    adduser -S -D -H -u 10001 -G tokenmp tokenmp

WORKDIR /app

COPY --from=builder /out/notice /usr/local/bin/notice
COPY --from=builder /out/healthcheck /usr/local/bin/healthcheck

USER 10001:10001

ENV NOTICE_HEALTHCHECK_ADDR=http://127.0.0.1:8081/healthz
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/healthcheck"]

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/notice"]
