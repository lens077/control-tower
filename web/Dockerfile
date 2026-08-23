# syntax=docker/dockerfile:1
#
# 配置中心 Web 控制台。构建产物由 Caddy 伺服 —— 与 ecommerce 的 consumer
# 同一种做法(这套生态里没用过 nginx),不塞进 Go 二进制 embed:
# 前端改一行就要重建重发整个后端镜像,代价不成比例。

ARG NODE_VERSION=24
ARG PNPM_VERSION=11.6.0

# 钉在 $BUILDPLATFORM:构建产物是静态文件,与目标架构无关。不钉的话
# 多架构构建会把 pnpm install + vp build + tsc 放进 QEMU 里再跑一遍
# (arm64 上慢一个量级),白等而产出完全相同。deps/build 阶段都继承这里,
# 只有最后的 caddy 阶段按目标架构出。
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS base
WORKDIR /usr/src/app
RUN --mount=type=cache,target=/root/.npm \
    npm install -g pnpm@${PNPM_VERSION}

# 依赖层单独切出来:只有 package.json / lock 变了才重装,源码改动走缓存
FROM base AS deps
# pnpm-workspace.yaml 必须一起进来:onlyBuiltDependencies 写在它里面,
# 少了它 pnpm 会以 ERR_PNPM_IGNORED_BUILDS 失败(pnpm 10+ 不再读 package.json 的 pnpm 字段)
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
# --config.strictDepBuilds=false:pnpm 11 会在安装末尾对「被忽略的构建脚本」
# 直接报 ERR_PNPM_IGNORED_BUILDS 并以非零退出。这里唯一被忽略的是 @bufbuild/buf
# —— 它只用于 proto 代码生成(make api),前端构建(vp build + tsc)根本不需要它,
# 让它跑 postinstall 反而白白拖慢镜像构建。
# 注意 pnpm-workspace.yaml 里的 onlyBuiltDependencies 在这条路径上不足以放行,
# 实测仍会触发该检查,所以显式关掉。
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile --config.strictDepBuilds=false

FROM deps AS build
COPY . .
# vp build + tsc(见 package.json 的 build 脚本)
RUN pnpm build

FROM caddy:alpine AS final

# Caddyfile 在这里生成而不是单独一个文件:它只有十几行,且与镜像强绑定,
# 拆成外部文件反而多一处会漂移的东西。
RUN cat > /etc/caddy/Caddyfile <<'EOF'
:80 {
    root * /usr/share/caddy
    encode gzip

    # config.json 是运行时注入的(k8s 里由 ConfigMap 覆盖挂载),绝不能被缓存 ——
    # 缓存住的话改了网关地址要等浏览器缓存过期才生效,而且没人会想到是这里。
    # 前端侧已经用 fetch(cache: "no-store") 防了一层,这里是服务端的第二层。
    @runtime-config path /config.json
    header @runtime-config {
        Cache-Control "no-store, must-revalidate"
        Pragma "no-cache"
    }

    # 带指纹的静态资源可以长缓存
    @assets path /assets/*
    header @assets Cache-Control "public, max-age=31536000, immutable"

    # 容器编排的存活/就绪探针打这里,不要落到 SPA fallback 上
    handle /healthz {
        respond "ok" 200
    }

    handle {
        try_files {path} /index.html
        file_server
    }

    header {
        X-Frame-Options DENY
        X-Content-Type-Options nosniff
        Referrer-Policy strict-origin-when-cross-origin
        # connect-src 要放开 API 与 Casdoor 两个来源 —— 它们与本站不同源,
        # 具体地址随环境变(见 config.json),所以这里用通配的 https:;
        # 收紧成具体域名需要构建期就知道环境,与运行时注入的设计冲突。
        Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self' https: http:; worker-src 'self' blob:"
    }
}
EOF

COPY --from=build /usr/src/app/dist /usr/share/caddy

EXPOSE 80
CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--watch"]
