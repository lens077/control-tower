# control-tower

网关与配置中心合一的平台仓：单 Go module、两个独立部署的服务。

| 服务 | 目录 | 职责 |
|---|---|---|
| gateway | `services/gateway` | Connect 原生反代（一级 proto 包名路由）、JWT/Casbin 鉴权、混合撤权、统一 Connect 错误、可观测 |
| config | `services/config` | 配置中心：键值 + 版本历史 + WatchKeys 热推送、per-service machine token、管理台（`web/`） |

由 ecommerce 旧网关（go-kratos/gateway fork）与 config-center 合并重写而来。前端与后端 10 服务经网关的调用路径、错误契约保持不变；迁移决策与对抗评审档案见工作区 `.migration-scratch/`。

## 文档

- `docs/design/architecture.md` — 架构、请求链路、不变式
- `docs/design/auth.md` — JWT 信任域、混合撤权三场景操作手册
- `docs/design/machine-token.md` — 数据面凭据设计
- `docs/design/decisions.md` — 砍掉/不做清单及原因
- `docs/design/cutover.md` — 切流与回滚手顺
- `AGENTS.md` — 协作基线与硬约束

## 常用命令

```bash
make verify            # build + buf lint + go vet + test -race（提交前必跑）
make api               # proto 变更后重新生成
make breaking-legacy   # wire 冻结门禁：对旧 config-center 仓 WIRE_JSON 检查
make test-crossversion # 旧 SDK v0.1.0 → 新服务的跨版本实测（需本机 docker）
```

## 本地开发（Mac 直连内网集群）

```bash
scripts/dev-local.sh config     # config 服务：PG 端口转发 + Dragonfly/Consul LAN 直连
scripts/dev-local.sh gateway    # 网关：file 模式（见下）
scripts/dev-local.sh print      # 只渲染配置看结构（口令脱敏）
```

凭据运行时从集群 Secret 取、渲染进临时文件（0600）、退出即删——不进仓库也不进日志。三条依赖通路各不相同：

| 依赖 | 通路 | 原因 |
|---|---|---|
| PostgreSQL | `kubectl port-forward svc/pg-main-rw`（脚本自动起） | 集群里只有 ClusterIP，LAN 不可达 |
| Dragonfly（Redis） | LAN 直连 `192.168.3.122:6380`（TLS，skip verify） | Cilium Gateway 已暴露；按 IP 访问证书不匹配 |
| Consul | LAN 直连 `192.168.3.120:8500` + ACL token | `consul-expose-servers` LoadBalancer；**无 token 会静默返回 `{}`** 而不是报错 |

本地跑 config 服务时 `CONSUL_ENABLED=false` 是硬要求：本机实例注册进集群目录后，集群内客户端可能把流量解析到你的 Mac。

**网关本地跑的限制**：Consul 里注册的是 Pod IP（`10.244.x.x`），Mac 路由不到，`discovery:///` 在本机无效。脚本的 `gateway` 子命令用 file 模式（自动从集群拉 public.pem/policies/model），把 `routes.yaml` 的 target 改成 `direct://127.0.0.1:<端口>` 并自行 `kubectl port-forward` 对应后端即可。

完全离线（不碰集群）：`make test-crossversion` 那套 throwaway Postgres/Redis，或手写 `CONFIG_FILE` 指向 `services/config/tests/oldsdk/harness-config.yaml`。

web 控制台：`cd web && pnpm install && pnpm dev`（端口 3005，已在上面渲染配置的 CORS 白名单里）。

## 发布

CI 由裸 semver tag（`X.Y.Z`）触发：质量门禁 + 三镜像（gateway/config/config-web）推 GHCR（配置 TCR Secrets 后双推）。PR 只跑质量门禁；push main 不构建。部署清单在 `deploy/{dev,pre}`，切流手顺见 `docs/design/cutover.md`。
