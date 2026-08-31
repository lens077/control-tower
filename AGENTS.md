# AGENTS.md — control-tower

网关与配置中心合一的平台仓：单 module、两服务（`services/gateway`、`services/config`）。由 ecommerce 旧网关（go-kratos/gateway fork）与 config-center 合并重写而来。

## 部署现状（2026-08-31 逐资源核对 + e2e 实测）

集群 2026-08-21 前后重建过，`postgresql` ns 已不存在。`config-center` ns 于 2026-08-29
按 `deploy/pre/config/` 重建，gateway 于 2026-08-30 重新拉起，**两个服务都在跑**。
2026-08-31 发布裸 tag `0.2.6` 后，config 与 config web 已从 public GHCR 的 `0.2.5`
滚动到 `0.2.6`；gateway 仍为 `0.2.5`。dev/pre 的四份 config manifest 已同步为 `0.2.6`，
gateway manifest 保持 `0.2.5`。三个 `0.2.6` 包均已通过无凭据 OCI 请求验证，config 与 config web
还完成了集群滚动拉取。不要只用本机 `docker manifest inspect` 判断可见性（Keychain 会偷偷带凭据）。

**公网入口已恢复**：三条 HTTPRoute 均为 `Accepted=True`、`ResolvedRefs=True`。
`config.apikv.com/` 返回 200，`config-api.apikv.com/healthz`、
`gateway.apikv.com/{healthz,readyz}` 均返回 200。网关根路径 `/` 没有业务路由，按契约返回应用层
`404 ROUTE_NOT_FOUND`；入口探活必须使用 `/healthz`，不能用根路径。

| 服务 | 集群状态 | 备注 |
|---|---|---|
| config | `config-center/config-center` **运行中**（`0.2.6`） | `config-center.config-center.svc:30010`，镜像走 GHCR |
| config web | `config-center/config-center-web` **运行中**（`0.2.6`） | `config-center-web.config-center.svc:80`，镜像走 GHCR |
| gateway | `ecommerce/control-tower-gateway` **运行中**（`0.2.5`，2 副本） | `ecommerce-gateway-service.ecommerce.svc:8080`，镜像走 GHCR；`/healthz`、`/readyz` 均 200 |

重新收敛公网入口：

```bash
kubectl apply -f deploy/pre/config/httproute.yaml -f deploy/pre/gateway/httproute.yaml
```

2026-08-31 已对 `deploy/dev` 的 18 个资源执行 API Server dry-run，并把补齐后的
`deploy/pre/gateway` 实际滚动到集群；随后 live e2e 通过。dev/pre manifest 仍指向同一组 namespace
与对象名，不是两个隔离部署：公网 gateway 必须使用 pre config-source，不能再混用 dev deployment。
pre 当前未配置 BFF 会话轨，按设计退回 legacy bearer；不得把 dev 的 insecure/localhost BFF 参数带到公网。

### Consul 的实际作用范围

- **网关需要 Consul**：`routes/{dev,pre}.yaml` 的 11 个后端全是 `discovery:///<service>`，
  靠 Consul 解析成 Pod IP。这 10 个业务服务都已注册在案（token 是 `ecommerce/consul-ecommerce-token`，
  策略 `ecommerce-services` = `service_prefix "" { policy = "write" }`）。
- **找 config 服务不需要 Consul**：网关的配置源写死的是
  `http://config-center.config-center.svc:30010`，走 K8s Service DNS。
- 所以 config 服务的 Consul 注册**没有任何消费方**，默认显式关闭：
  `deploy/{dev,pre}/config/deployment.yaml` 均为 `CONSUL_ENABLED=false`。
- 2026-08-31 已做一次完整烟测：用临时最小权限策略
  `service "config-service" { policy = "write" }` 注册成功，catalog/health 显示 1 个 passing 实例；
  随后切回 false，确认当前 Pod 日志为 `Consul disabled or not configured`，并删除临时实例、
  ACL token/policy 与 K8s Secret。**能力已验证，默认仍保持关。**
- 要长期重新开启：按烟测的最小策略建持久令牌，存成 Secret 后补
  `CONSUL_HTTP_TOKEN`，再把开关改回 `true`。
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

PG 与 Redis 的证书由 node3 的 Pigsty 自签 CA 签发，SAN 已补上两个公网域名（2026-08-29）
与入口 IP `114.132.233.129`（2026-08-30），域名/IP 两条路径都可 `verify-full`。补签步骤见工作区 `pigsty-deploy/cert-san-resign.md`。

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
| `docs/operations/2026-08-30-recovery-record.md` | 2026-08-29/30 恢复实录：六个故障的判别法、根因与回滚点——**排查「网络面板正常但功能不对」这类症状前先翻它** |

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

# 实机浏览器端到端（打真实环境，覆盖两个微服务）。凭据只从环境变量给。
cd e2e && pnpm install && pnpm run install-browser
E2E_USERNAME=<账号> E2E_PASSWORD=<口令> pnpm test

# 管理面变更验收：真实 WatchKeys 长流 + /connections + token 吊销。
E2E_USERNAME=<账号> E2E_PASSWORD=<口令> E2E_ADMIN_MUTATIONS=true \
  pnpm exec playwright test tests/config-watch-admin.spec.ts
```

管理面变更测试会签发并吊销一枚临时 Machine Token。吊销行按审计设计保留，因此默认关闭，
不随 6 小时巡检运行；发布或鉴权变更后通过 `workflow_dispatch` 的 `admin_mutations=true` 显式打开。

CI 里由 `.github/workflows/e2e.yml` 承接。当前工作树已恢复 6 小时 schedule，合入 `main` 后生效；
`workflow_dispatch` 继续保留，当前公网环境已用它完成手动验收。
失败发 ntfy（正文带失败用例名）；B1 恢复通知已实测：前一次 failure、下一次 success 时只发一条
「✅ 已恢复」，连续 success 不重复发。

`e2e/` 的每条用例都对应一个真实发生过的故障（见其 README 的对照表）。它抓到过
`make verify` 与单测都测不到的三类问题：CSP/安全响应头把自家资源拦掉、
指标链路名字对不上、网关路由形态被误解。**改 `web/Dockerfile` 的响应头、改鉴权流程、
改 `promql/catalog.go` 之后必须跑它。**

CI 由裸 semver tag（`X.Y.Z`）触发发布；PR 只跑质量门禁；push main 不构建。
