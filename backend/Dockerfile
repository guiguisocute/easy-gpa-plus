# 多阶段构建：同一镜像承载 API、迁移与全部 Worker。
# 编译阶段固定在构建机架构上交叉编译，多架构镜像不必在模拟器里跑 go build。
FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine AS build
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY backend/ .
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/zongce ./cmd/zongce

FROM alpine:3.22 AS runtime-base
RUN apk add --no-cache ca-certificates poppler-utils antiword postgresql-client tzdata \
    && adduser -D -u 10001 app \
    && mkdir -p /backups/local /backups/offsite \
    && chown -R app:app /backups
USER app
WORKDIR /app
COPY asset/mailtemplate /app/mailtemplate
ENV MAIL_TEMPLATE_DIR=/app/mailtemplate
EXPOSE 8080
ENTRYPOINT ["zongce"]
CMD ["api"]

# GitHub Actions compiles with setup-go so its persistent Go build cache works
# across ephemeral runners, then asks BuildKit for this minimal packaging stage.
FROM runtime-base AS prebuilt
COPY .release/zongce /usr/local/bin/zongce

# Default target kept for local/manual builds: no prebuilt artifact is required.
FROM runtime-base AS runtime
COPY --from=build /out/zongce /usr/local/bin/zongce
