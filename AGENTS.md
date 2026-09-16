# AGENTS.md — control-tower

网关与配置中心合一的平台仓：单 module、两服务（`services/gateway`、`services/config`）。由 ecommerce 旧网关（go-kratos/gateway fork）与 config-center 合并重写而来。

## 部署现状（2026-09-15 逐资源核对 + 公网探活）

集群 2026-08-21 前后重建过，`postgresql` ns 已不存在。`config-center` ns 于 2026-08-29
按 `deploy/pre/config/` 重建，gateway 于 2026-08-30 重新拉起，**两个服务都在跑**。

**镜像源已于 2026-09-15 从 GHCR 切到 TCR**：三份 Deployment（dev/pre 各一套）的 image 统一为
`ccr.ccs.tencentyun.com/sumery/control-tower-{config,config-web,gateway}:<tag>`，并且每份都带
`imagePullSecrets: [tcr-pull]`（TCR 仓库是 private，`config-center` 与 `ecommerce` 两个 ns 都已有该 Secret；
漏掉的症状是 `ErrImagePull ... 401 Unauthorized`）。`deploy/manifest_test.go` 守这两条。
CI 发布 tag 时仍双推 GHCR + TCR（`.github/workflows/ci.yml`），GHCR 只作追溯，集群不再从它拉。
本机手工构建 web 镜像时必须 `--platform linux/amd64`：三个节点都是 amd64，Apple Silicon 默认出 arm64
会 `exec format error`。不要只用本机 `docker manifest inspect` 判断可见性（Keychain 会偷偷带凭据）。

12 个 consumer selector 已换成 scoped Machine Token；legacy 回退仍在 7 天烘烤期。
machine token 的 `role` 列（`service`|`operator`，见 `docs/design/machine-token.md`「operator 角色」）
已随 config `0.2.11` 滚动落地：2026-09-15 启动日志 `goose: no migrations to run. current version: 3`。
pre 环境的 operator token 存于 Secret `config-center/config-center-operator`（主体 `operator:harvest`，
scope 只到 pre；读 dev 会 `permission_denied`），走 `x-config-center-service-token` 头，
2026-09-15 用它完成 `gateway/pre/routes.yaml` 的 PutKey（v2）实测放行。
`machine_token_legacy_hits` 的零命中窗口从 `2026-08-31T16:05:03Z` 起算，最早于
`2026-09-07T16:05:03Z` 删除回退；任何非零命中都会重置窗口。

**公网入口已恢复**：三条 HTTPRoute 均为 `Accepted=True`、`ResolvedRefs=True`。
`config.apikv.com/` 返回 200，`config-api.apikv.com/healthz`、
`gateway.apikv.com/{healthz,readyz}` 均返回 200。网关根路径 `/` 没有业务路由，按契约返回应用层
`404 ROUTE_NOT_FOUND`；入口探活必须使用 `/healthz`，不能用根路径。

| 服务 | 集群状态 | 备注 |
|---|---|---|
| config | `config-center/config-center` **运行中**（`0.2.11`，TCR） | `config-center.config-center.svc:30010`；来自 `deploy/pre/config/deployment.yaml` |
| config web | `config-center/config-center-web` **运行中**（`0.2.13`，TCR） | `config-center-web.config-center.svc:80`；来自 `deploy/pre/config/web-deployment.yaml` |
| gateway | `ecommerce/control-tower-gateway` **运行中**（`0.2.10`，TCR，2 副本） | `ecommerce-gateway-service.ecommerce.svc:8080`；**来自 `deploy/dev/gateway/deployment.yaml`**（见下）；`/healthz`、`/readyz` 均 200 |

重新收敛公网入口：

```bash
kubectl apply -f deploy/pre/config/httproute.yaml -f deploy/pre/gateway/httproute.yaml
```

dev/pre manifest 仍指向同一组 namespace 与对象名，不是两个隔离部署。**公网 gateway 当前跑的是 dev
manifest**（`DEPLOYMENT_MODE=dev`、`control-tower-config-source-dev`、带 dragonfly session 的 BFF 会话轨），
2026-09-15 用 `kubectl diff -f deploy/dev/gateway/deployment.yaml` 核对，除镜像源外与线上零差异。
这与 2026-08-31 的「公网必须用 pre」结论相反，原因有两条，都在同日实测：

1. `gateway/pre/routes.yaml` 的 target 直到当天还是 `discovery:///`，而业务服务 2026-09-03 起已关闭
   Consul 注册（见下节），用 pre manifest 滚动后新 Pod 的 `/readyz` 持续 503、日志刷
   `consul returned empty instance list`，已 `rollout undo`。模板 `routes/pre.yaml` 与 Config Center 的
   pre 键（v2）当天已改成 `direct://`，这一条**已修**。
2. pre manifest 没有 BFF 会话轨，而公网网关每天有 `/auth/me` 真实流量（consumer-next 走 BFF 登录）。
   切到 pre 会让登录态消失。这一条**未修**：要先给 pre 补一套非 insecure、非 localhost 的 BFF 参数
   （`BFF_PUBLIC_BASE_URL`/`BFF_ALLOWED_REDIRECTS` 指向 `https://shop.apikv.com`，session Redis 走
   `redis.apikv.com:30002`），再切。切之前不要 apply `deploy/pre/gateway/deployment.yaml` 到集群。

`deploy/manifest_test.go` 仍禁止 pre 出现 dev 的 insecure/localhost BFF 参数，这条不变。

### Consul 的实际作用范围

- **网关当前不依赖 Consul**：`routes/{dev,pre}.yaml` 的 11 个后端自 2026-09-03（dev，e0c59ee）/
  2026-09-15（pre）起全是 `direct://<k8s Service>.ecommerce.svc:<port>`，走 K8s Service DNS。
  业务服务已关闭 Consul 注册（各 deployment `CONSUL_ENABLED=false`），带 token 查 catalog 只剩
  `consul` 自身；`discovery:///` 会因空列表让 `/readyz` 503。gateway manifest 里的
  `CONSUL_HTTP_ADDR`/`CONSUL_HTTP_TOKEN`（`ecommerce/consul-ecommerce-token`）保留给将来重开注册时用，
  两种 target 写法网关都支持（`proxy.go` 的 `discoveryPrefix`/`directPrefix`）。
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
make api           # proto 变更后重新生成：Go/Connect（buf.gen.yaml）+ 控制台 TS（buf.gen.ts.yaml → web/src/gen）
make check-gen     # 生成物门禁：三份产物必须与 proto 一致（CI 的 codegen job 跑的就是它；先 make tools）
                   # make tools 同时钉插件库版本与编插件用的 Go（GOTOOLCHAIN=go.mod 的版本）。
                   # 少钉后者：gofmt 对注释代码块的缩进规则随 Go 版本变，validate.pb.go 会出 1500 行假 diff。
cd web && pnpm test && pnpm build   # 控制台单测（jsdom + src/test-setup.ts 显式提供 Web Storage）+ tsc + 生产构建

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

CI 由裸 semver tag（`X.Y.Z`）触发发布；PR 只跑质量门禁（`test` Go 门禁 + `web` 控制台门禁 +
`codegen` 生成物门禁，三者都过才构建镜像）；push main 不构建。
