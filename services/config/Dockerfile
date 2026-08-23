# control-tower config 镜像。构建上下文 = 仓库根（需要根级 go.mod/go.sum）。
ARG GO_IMAGE=golang:1.26.5-alpine3.24
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS compile

ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
ARG GOPROXY=https://goproxy.cn,direct
ARG CGO_ENABLED=0

WORKDIR /build

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download -x

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=$CGO_ENABLED \
    go build -ldflags="-s -w -X main.Version=$VERSION" \
    -o /app/service ./services/config/cmd/server && \
    chmod +x /app/service

FROM alpine:3.22 AS final

RUN apk add --no-cache libc6-compat
RUN addgroup -g 1000 appuser && adduser -u 1000 -G appuser -D appuser
WORKDIR /app

COPY --from=compile --chown=appuser:appuser /app/service /app/

USER appuser

# config 服务从本地文件/Secret 自举（禁止依赖自己），路径经卷挂载注入。
ENV CONFIG_FILE=/app/config/config.yaml

EXPOSE 30010

ENTRYPOINT ["/app/service"]
