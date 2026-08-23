# control-tower gateway 镜像。构建上下文 = 仓库根（需要根级 go.mod/go.sum）。
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

# compile 阶段钉在 $BUILDPLATFORM（本机原生跑编译器，不吃 QEMU），
# 变量名必须是 GOOS/GOARCH——Go 工具链只认这两个名字。
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=$CGO_ENABLED \
    go build -ldflags="-s -w -X main.Version=$VERSION" \
    -o /app/service ./services/gateway/cmd/server && \
    chmod +x /app/service

FROM alpine:3.22 AS final

RUN apk add --no-cache libc6-compat
RUN addgroup -g 1000 appuser && adduser -u 1000 -G appuser -D appuser
WORKDIR /app

COPY --from=compile --chown=appuser:appuser /app/service /app/

USER appuser

# 运行时配置经 Secret/Config 卷注入（selector 文件），不打进镜像。
ENV CONFIG_SOURCE_FILE=/etc/control-tower/config-source/gateway.yaml

EXPOSE 8080

ENTRYPOINT ["/app/service"]
