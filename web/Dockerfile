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

    # Monaco 编辑器的自托管资源(构建时由 vite 插件从 node_modules 拷进 dist/vs)。
    # 单独 handle 是为了不落进下面的 SPA fallback —— 少文件时应该诚实地 404,
    # 而不是把 index.html 当 JS 喂给 AMD loader,那种错误极难看出来。
    # 文件名不带指纹(loader.js / editor.main.js),所以只缓存一周,不用 immutable。
    handle /vs/* {
        header Cache-Control "public, max-age=604800"
        file_server
    }

    handle {
        try_files {path} /index.html
        file_server
    }

    header {
        # 不能用 DENY:DENY 连**同源自己**都不许嵌,而静默续期正是把自己的
        # /callback 加载进本站的隐藏 iframe(见 src/auth/pkce.ts 的 silentRenew)。
        # DENY 的表现是 net::ERR_BLOCKED_BY_RESPONSE + 控制台一句
        # "Refused to display ... because it set 'X-Frame-Options' to 'deny'",
        # 续期永远失败 → 每次整页加载都退回「请先登录」。
        # SAMEORIGIN 仍然挡住第三方站点嵌套(防点击劫持),下面的 CSP
        # frame-ancestors 'self' 是现代浏览器里等价且优先的那条。
        X-Frame-Options SAMEORIGIN
        X-Content-Type-Options nosniff
        Referrer-Policy strict-origin-when-cross-origin
        # connect-src 要放开 API 与 Casdoor 两个来源 —— 它们与本站不同源,
        # 具体地址随环境变(见 config.json),所以这里用通配的 https:;
        # 收紧成具体域名需要构建期就知道环境,与运行时注入的设计冲突。
        #
        # frame-src 同理,而且**必须显式写出**:静默续期靠往隐藏 iframe 里加载
        # Casdoor 的 authorize?prompt=none 完成(src/auth/pkce.ts 的 silentRenew)。
        # 不写 frame-src 就会落到 default-src 'self' 把 iframe 拦掉 → 续期永远失败 →
        # **每次整页加载(刷新、直接打开深链)都退回「请先登录」**,而网络面板一片正常、
        # 只有控制台一条 CSP 报告。2026-08-30 由 e2e/ 的浏览器实测发现。
        Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self' https: http:; frame-src https:; frame-ancestors 'self'; worker-src 'self' blob:"
    }
}
EOF

COPY --from=build /usr/src/app/dist /usr/share/caddy

EXPOSE 80
CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--watch"]
