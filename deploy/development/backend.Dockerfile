# syntax=docker/dockerfile:1.7

FROM golang:1.26.6-alpine

ARG AIR_VERSION=v1.67.4

RUN apk add --no-cache \
      antiword \
      ca-certificates \
      gcc \
      git \
      musl-dev \
      poppler-utils \
      postgresql-client \
      socat \
      tzdata

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go install github.com/air-verse/air@${AIR_VERSION} && \
    go install github.com/go-delve/delve/cmd/dlv@v1.26.0

ENV CGO_ENABLED=0 \
    GOCACHE=/go/cache/build \
    GOMODCACHE=/go/pkg/mod

WORKDIR /workspace/backend
