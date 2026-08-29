# AGENTS.md — control-tower

网关与配置中心合一的平台仓：单 module、两服务（`services/gateway`、`services/config`）。由 ecommerce 旧网关（go-kratos/gateway fork）与 config-center 合并重写而来。

## 部署现状（2026-08-29 逐资源核对）

集群 2026-08-21 前后重建过，`postgresql` ns 已不存在，`config-center` ns 于 2026-08-29
按 `deploy/pre/config/` 重建；gateway 仍是被手工 `delete -f` 掉的状态。

| 服务 | 集群状态 | 备注 |
|---|---|---|
| config | `config-center/config-center` **运行中**（`0.2.1`） | 镜像走 TCR —— GHCR 上 `control-tower-config` 是 private，匿名拉取 401 |
| config web | `config-center/config-center-web` **运行中**（`0.2.1`） | `https://config.apikv.com` |
| gateway | Deployment / Service / Pod **均不存在** | 孤儿 HTTPRoute 与 VPA 已于 2026-08-29 清掉，`https://gateway.apikv.com/` 从 500 变成 404（「没有路由」的诚实状态）。**保留** Secret `ecommerce/control-tower-config-source-dev`——它是网关的配置源、含机器令牌，重新部署时要用 |

### Consul 的实际作用范围（别照着 `deploy/*/config/deployment.yaml` 里那条注释理解）

- **网关需要 Consul**：`routes/{dev,pre}.yaml` 的 11 个后端全是 `discovery:///<service>`，
  靠 Consul 解析成 Pod IP。这 10 个业务服务都已注册在案（token 是 `ecommerce/consul-ecommerce-token`，
  策略 `ecommerce-services` = `service_prefix "" { policy = "write" }`）。
- **找 config 服务不需要 Consul**：网关的配置源写死的是
  `http://config-center.config-center.svc:30010`，走 K8s Service DNS。
- 所以 config 服务当前注册失败（`anonymous token lacks permission 'service:write'`；
  ACL 默认策略是 deny，而它的 token 随旧 `config-center` ns 一起没了）**没有任何消费方受影响**，
  代价只是每次启动刷一条 ERROR。要么补一个 token，要么直接 `CONSUL_ENABLED=false`。
- ⚠️ 匿名读 Consul catalog 会返回**空对象**而不是 403，很容易误判成「一个服务都没注册」。
  查真实状态要带 token：`curl -H "X-Consul-Token: $TOK" .../v1/catalog/services`。

数据面也已经搬离集群，这是配置里最容易踩空的一点：

| 依赖 | 现在在哪 | 地址 |
|---|---|---|
| PostgreSQL | node3 的 Patroni（`pg-meta`，单实例），**不再是集群内 CNPG** | `pg.apikv.com:30001`（node1 Pangolin raw 口） |
| Redis | node3 的 Redis 主从 + stunnel TLS 终止，**不再是集群内 dragonfly** | `redis.apikv.com:30002` |
| 指标查询端 | node3 的 VictoriaMetrics；集群 `victoriametrics` ns 已空 | `http://metrics.apikv.com`（**只有 http**，https 返回 404） |
| OTLP 采集 | 集群内 collector（Deployment）+ 每节点 agent（DaemonSet，只采主机指标） | `otel-opentelemetry-collector.opentelemetry.svc:4318`（仅集群内可解析） |

指标链路 2026-08-29 修过三处，改动 owner 在 kubernetes 仓 `components/opentelemetry{,-node}/`：
①域名由 `node3-metrics` 改名为 `metrics`，collector 没同步导致整条指标链路静默断了；
②node3 的 VictoriaMetrics 要开 `-opentelemetry.usePrometheusNaming=true`，否则指标名保持
OTLP 点号形态（`pgxpool.acquired_connections`），与 `internal/pkg/promql/catalog.go` 按
Prometheus 规范写的查询对不上，表现为**查询成功但一条序列都没有**；
③主机指标需要 DaemonSet + 显式打开 `system.cpu/memory.utilization`。
验收用仓库自带的 live 测试：
`CONFIG_CENTER_VM_ENDPOINT=http://metrics.apikv.com go test ./services/config/internal/pkg/promql -run Live -v`。

PG 与 Redis 的证书由 node3 的 Pigsty 自签 CA 签发，SAN 已补上两个公网域名（2026-08-29），
可以 `verify-full` / 严格校验。补签步骤见工作区 `pigsty-deploy/cert-san-resign.md`。

历史：2026-08-24 曾以 `ecommerce/control-tower-gateway`（`sha-143ef5f`）与
`config-center/config-center`（`sha-a27f90a`）切流上线；`config-center` 这个 ns 名是当时
没改的遗留标签，里面跑的就是本仓的 config 服务，不代表旧 config-center 仓还在跑。

上游 ecommerce 仓的旧 `gateway/` 目录已于 2026-08-24 删除（历史在其 tag `backup/pre-control-tower-20260823`），旧 config-center 仓同样退役。本仓是这两块的唯一真相源。

## 必读

| 文档 | 内容 |
|---|---|
| `docs/design/architecture.md` | 两服务架构、网关请求链路、不变式 |
| `docs/design/auth.md` | JWT 信任域、混合撤权三场景操作手册 |
| `docs/design/decisions.md` | 砍掉/不做清单及原因——**改动前先查这里，别把砍掉的东西加回来** |
| `docs/design/adr-0002-bff-session.md` | **现行鉴权决策**：BFF + 服务端 session（取代 ADR-0001） |
| `docs/design/bff-migration.md` | BFF 化实施手顺：三轨并存、四阶段、按阶段回滚 |
| `docs/design/adr-0001-token-model.md` | 已被 ADR-0002 取代，留作追溯（含一处已标注的事实错误） |

## 硬约束

1. **wire 冻结**：`api/` 下存量 proto 的包名、Service/RPC 名、字段号与类型不可动（改名仅限 `go_package`）；新增字段/RPC 自由。解除条件见 `docs/design/decisions.md`。
2. **`http.Server` 不设 `WriteTimeout`**：会掐断 `WatchKeys` 长流（历史事故）。超时走路由级 context。
3. **路由模板变更纪律**：改 `routes/{dev,pre}.yaml` 必须同 PR 升级 ecommerce 仓对本 module 的依赖版本，否则其 structcheck 门禁变红。
4. 凭据不进仓库：token/私钥只存 Config Center、K8s Secret 与本地环境。
5. 中文文档与注释遵循 tech-doc-style-chinese 规范：直角引号「」，允许第二人称「你」。

## 验证锚点

```bash
make verify        # build + buf lint + go vet + test（提交前必跑）
make api           # proto 变更后重新生成（buf generate + lint）
```

CI 由裸 semver tag（`X.Y.Z`）触发发布；PR 只跑质量门禁；push main 不构建。

## 迁移背景（迁移已完成，本节留作索引）

迁移期决策与对抗评审档案在工作区 `.migration-scratch/`（不入本仓）：06 决策日志、11 终裁书、12 实施方案 v2。

迁移本身已完成、上游旧目录已删（当前集群里 config 已重新拉起、gateway 未部署，见上方「部署现状」，与迁移无关）。**本节保留的唯一理由**是那批档案记着「哪些东西是被刻意砍掉的、为什么」——与 `docs/design/decisions.md` 配合使用，避免有人把砍掉的东西当成遗漏加回来。等 `decisions.md` 把这些理由全部吸收之后，本节连同档案一并归档。
