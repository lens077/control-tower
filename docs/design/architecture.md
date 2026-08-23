# control-tower 架构

单仓两服务：`services/gateway`（Connect 反代 + 鉴权）与 `services/config`（配置中心）。两个部署单元独立伸缩；gateway 部署在 `ecommerce` namespace（与旧网关同 Service 便于切流/回滚），config 保持在 `config-center` namespace。

## 网关请求链路

```text
Client（Connect/JSON 或 gRPC-Web）
→ h2c server（HTTP/1.1 + 明文 HTTP/2；TLS 由 Cilium Gateway 终止）
→ recover → otelhttp → access log
→ CORS（OPTIONS 短路）
→ 身份头剥离（无条件删除入站 x-md-global-*）
→ 路由匹配（一级 proto 包名，如 /user.v1.UserService/SignIn 取 user）→ 匿名清单判定
→ [非匿名] JWT 验签（iss/aud/tokenType/sub/iat/exp + 60s leeway）
   → 撤销名单查表（内存，Watch 秒级更新）
   → [online_check 路由] Casdoor 实时校验（fail-close）
   → Casbin（角色数组 × procedure）
→ 可信身份头注入（在 ReverseProxy.Rewrite 内执行，含 SetXForwarded）
→ 路由级总超时（context）
→ Resolver 选点（Consul Watch + 健康过滤 + P2C，预留 K8s Service DNS 实现）
→ h2c Transport（http2.Transport{AllowHTTP} + 明文 DialTLSContext + ReadIdleTimeout/PingTimeout）
→ 后端 Connect 服务
```

不变式：

- 端到端 Connect 直通，无协议转码；网关对 HTTP method 透明。
- 404/405/无节点/超时/鉴权失败统一输出 Connect 规范错误 JSON（details 非空）+ `X-Error-Reason`。
- 重试默认关闭；无请求体缓存。
- `RawPath != Path`（含转义）直接 404；路径长度设上限；大小写敏感；`/healthz`、`/readyz` 先于包路由注册，永不代理。
- `/readyz` 就绪条件 = 路由表 + JWT 公钥 + Casbin 模型/策略全部加载成功。

## 配置与自举

- 网关经 selector + 仓内 SDK 从 config 服务拉取并 Watch 五个键（`gateway/<env>/` 下）：`routes.yaml`、`secrets/public.pem`、`policies/policies.csv`、`policies/model.conf`、`auth/revocations.yaml`。原子替换 + last-known-good + 1→30 秒指数退避。
- 旧网关的 `config.yaml` 键冻结不动，直至旧网关退役（回滚保证，终裁 P0-A）。
- config 服务自身从本地 `CONFIG_FILE`/Secret 自举，禁止依赖自己（启动死锁）。
- 路由模板的机器可读载体是根目录 `routes/` 包（go:embed），供 ecommerce structcheck import 双向核对；拓扑真相源仍是 ecommerce 的 `.service-matrix.yaml`。

## config 服务

架构沿 config-center：fx 装配、标准库 ServeMux + h2c、pgx/sqlc、Postgres LISTEN/NOTIFY 驱动 WatchKeys、Redis presence、Casdoor PKCE 管理面 + machine token 数据面。本仓演进点：

- 数据面鉴权升级为 per-service × per-environment machine token（哈希入库、namespace 白名单、两代重叠轮换、吊销即断流）。
- goose 管理 migrations（0001 为存量 schema 快照，存量库按接管手顺插基线记录）。
- 对外 proto 处于 wire 冻结期：存量包名/Service/RPC 名/字段号/类型不动，新增自由（见 decisions.md）。

## 发布与部署

- CI 由裸 semver tag（`X.Y.Z`）触发发布；PR 只跑质量门禁。镜像三枚：`control-tower-gateway`、`control-tower-config`、`control-tower-config-web`（P2 起），双推 GHCR 与 TCR。
- 网关部署基线：双副本、RollingUpdate、PDB、探针（/healthz、/readyz）、资源 requests/limits；`/metrics` 与 pprof 挂独立内部端口，pprof 默认关闭。
