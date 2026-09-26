# AGENTS.md — control-tower

网关与配置中心合一的平台仓：单 module、两服务（`services/gateway`、`services/config`）。由 ecommerce 旧网关（go-kratos/gateway fork）与 config-center 合并重写而来。

## 部署现状（2026-09-27 逐资源核对 + 公网探活）

集群在 2026-09-21 前后**又重建了一次**（节点 k1/k2/k3 与大部分 ns 的 AGE 约 5 天），
node3（Pigsty）已于 2026-09-03 退役，数据面全部回到集群内。重建后只恢复了 config，**网关没有部署**。

| 服务 | 集群状态 | 备注 |
|---|---|---|
| config | `config-center/config-center` **运行中**（`sha-68386e8`，TCR，1 副本） | `config-center.config-center.svc:30010`；按 `deploy/pre/config/deployment.yaml` 手工 `kubectl apply`（`DEPLOYMENT_MODE=pre`），2026-09-27 由 `0.2.11` 升级 |
| config web | `config-center/config-center-web` **运行中**（`sha-68386e8`，TCR） | `config-center-web.config-center.svc:80`，即 `config.apikv.com`；来自 `deploy/pre/config/web-deployment.yaml`，2026-09-27 由 `0.2.15` 升级 |
| gateway | **未部署**：`ecommerce` ns 里没有 `control-tower-gateway`，也没有 `ecommerce-gateway-service` | 仓库 manifest 已是 `0.2.17`；缺的前置对象见下 |

公网探活（2026-09-27）：`config.apikv.com/` 200、`config-api.apikv.com/healthz` 200；
`gateway.apikv.com/{healthz,readyz}` 均 **404**（没有网关 HTTPRoute，也没有后端）。
网关恢复后根路径 `/` 仍按契约返回应用层 `404 ROUTE_NOT_FOUND`，入口探活只能用 `/healthz`。

config 的两条 HTTPRoute 主机名是 `config(-api).dev.test` + `config(-api).apikv.com`，
Pangolin 资源的 tlsServerName/Host 指向 `*.dev.test`（2026-09-23，5cbf2d5）。重新收敛：

```bash
kubectl apply -f deploy/pre/config/httproute.yaml
```

### 恢复网关前要先补的对象

2026-09-27 核对 `deploy/pre/gateway/deployment.yaml` 的引用，新集群 `ecommerce` ns 里**已有**：
`dragonfly-session`（会话 Redis 凭据 + CA）、`tcr-pull`、`casdoor-bff`（BFF 的 Casdoor client）。

**缺失且会阻断启动**（非 optional，Pod 卡在 `ContainerCreating` 或 `CreateContainerConfigError`）：

- Secret `control-tower-config-source-pre`：挂成 `config-source` 卷（selector + machine token，
  手顺见 `deploy/README.md` §2–3）。ns 里现有的 `ecommerce-config-source-{dev,pre}` 是业务服务用的，不是这份；
- Secret `consul-ecommerce-token`：`CONSUL_HTTP_TOKEN` 的 `secretKeyRef` 没有 `optional`。网关虽然不走
  Consul，缺了这份 Secret 容器照样起不来。要么补 Secret，要么先把该引用改成 `optional: true`。

**缺失但不阻断**：

- ConfigMap `control-tower-gateway-config`（`JWT_AUDIENCES` 等）：就在同一份 manifest 里，apply 时一起建；
- Secret `otel-auth`（`optional: true`）：缺了网关会匿名推 OTLP。token 真相源是
  `opentelemetry/otlp-public-auth`，需要时从它派生，键名是 `OTEL_EXPORTER_OTLP_HEADERS_GATEWAY`。

**对外环境一律 apply `deploy/pre/`，不要 apply `deploy/dev/gateway/`**。dev overlay 带
`http://localhost:3000` 的 BFF 回调与 `SESSION_COOKIE_INSECURE=true`，两份 overlay 的 namespace 与
HTTPRoute 主机名相同，kubectl 不会提示。2026-09-20 事故就是线上长期跑着 dev 那份，
`shop.apikv.com` 登录被送去 `localhost:3000/auth/callback`。此后 pre 已补齐 BFF 会话轨
（`BFF_PUBLIC_BASE_URL=https://gateway.apikv.com`、`BFF_ALLOWED_REDIRECTS=https://shop.apikv.com`、
会话 Redis 走 `dragonfly.dragonfly.svc:6379` TLS），守门测试是
`deploy/manifest_test.go` 的 `TestPreGatewayEnablesBFFWithPublicOrigins`。

### 发布方式

- **镜像**：集群统一从 TCR 拉 `ccr.ccs.tencentyun.com/sumery/control-tower-{config,config-web,gateway}:<tag>`，
  每份 pod spec 必须带 `imagePullSecrets: [tcr-pull]`（private 仓库，漏掉是 `ErrImagePull ... 401`），
  `deploy/manifest_test.go` 守这两条。CI 双推 GHCR + TCR，GHCR 只作追溯。
- 本机手工构建镜像必须 `--platform linux/amd64`（节点全是 amd64）；不要只用本机
  `docker manifest inspect` 判断可见性（Keychain 会偷偷带凭据）。
- **CI 不持有集群凭据**（2026-09-18，6c301b4），也不执行 `kubectl`。`docs/operations/argocd-helm-gitops.md`
  记录的 Argo CD Application `argocd/control-tower-public-dev` 在重建后**已不存在**（当前 `argocd` ns 只有
  `ecommerce-kyverno`），config 是手工 apply 的。恢复 GitOps 前，发布就是手工 `kubectl apply`。

### 数据面与可观测

| 依赖 | 集群内（pre 用） | 本机开发（`scripts/dev-local.sh` 用） |
|---|---|---|
| PostgreSQL | CNPG `pg-main`：`pg-main-rw.postgresql.svc:5432`，`verify-full` | `pg-dev.apikv.com:30001`，TLS 直通，`verify-ca`（证书 SAN 不含该域名） |
| Redis | Dragonfly：`dragonfly.dragonfly.svc:6379`，TLS | `redis-dev.apikv.com:30005`，TLS 直通 |
| 指标查询端 | `http://vm-single-victoria-metrics-single-server.victoriametrics.svc:8428` | `https://metrics.apikv.com`（公共证书；`http://` 现在 302 跳 https） |
| OTLP 采集 | `otel-opentelemetry-collector.opentelemetry.svc:4318` | 无（本机置 `observability.enable: false`） |

- 这些地址与凭据位置的真相源是 sibling kubernetes 仓的 `components/{postgres,dragonfly}/component.env`
  （`SVC`/`PORT`/`REMOTE_*`/`CRED_SECRET`/`CA_REF`）。`dev-local.sh config` 直接读这两份契约并从
  K8s Secret 取账密与 CA，渲染 0600 临时配置，退出即删。指标查询端不在契约里，由脚本变量
  `METRIC_QUERY_ENDPOINT` 决定（默认 `https://metrics.apikv.com`，设成空串则不渲染 `metric_query`）。
- 线上 config 的配置文件是 Secret `config-center/config-center-bootstrap` 的 `config.yaml` 键，本地对应
  `services/config/configs/pre.yaml`（gitignore）。改完要重灌 Secret 再滚动 Pod，否则两边静默漂移。
  Secret 里那份文件头部注释仍写着 node3 地址，以正文字段为准。
- `services/config/configs/dev.yaml`（gitignore）的 PG/Redis 仍指向已退役的 `pg.apikv.com`/`redis.apikv.com`
  （只有 `metric_query` 已改成 `https://metrics.apikv.com`）；`dev-local.sh` 已不读它，要单独 `go run`
  时先按上表改地址。
- **主机指标**（控制台 System 页「所在节点」四张图）来自 `opentelemetry-node` DaemonSet
  （kubernetes 仓 `components/opentelemetry-node/`，helm release `opentelemetry/otel-node`），每节点一份，
  只采 hostmetrics，经 `https://metrics.apikv.com/opentelemetry/v1/metrics` 写入 VM。公网入口与集群内
  `vm-single` 是同一个实例（2026-09-27 两边序列总数一致）。集群重建时它曾漏装，四张图显示「所选时间窗内无数据」，
  2026-09-27 重跑 `bash components/opentelemetry-node/install.sh` 修复，`otel-node-opentelemetry-collector-agent`
  3/3。再遇到空图，先查 `count({__name__=~"system_.*"})` 是否为 0。
- VM 必须开 `-opentelemetry.usePrometheusNaming=true`（集群内 `vm-single` 已开），否则指标名保持 OTLP
  点号形态，与 `internal/pkg/promql/catalog.go` 的查询对不上，表现为**查询成功但一条序列都没有**。
- 验收用仓库自带的 live 测试（2026-09-27 全部 13 组通过）。刚装好采集端时要等过一个 5 分钟整点再跑：
  测试按 5 分钟步长对齐取点，数据不满一个对齐点时会误报「取不到任何序列」：
  `CONFIG_CENTER_VM_ENDPOINT=https://metrics.apikv.com go test ./services/config/internal/pkg/promql -run Live -v`。

### Machine Token

- consumer selector 已换成 scoped Machine Token；legacy 共享 token 回退**仍在代码里**，等零命中窗口满 7 天再删。
- `role` 列（`service`|`operator`，见 `docs/design/machine-token.md`「operator 角色」）已随 config
  `0.2.11` 落地，启动日志 `goose: no migrations to run. current version: 3`。
- operator token 按环境分两份：Secret `config-center/config-center-operator-dev` 与
  `config-center/config-center-operator-pre`。operator 只能访问与自身相同的 environment，跨环境一律
  `permission_denied`；走 `x-config-center-service-token` 头。命令行写键用 `configctl`（见必读）。
- **烘烤窗口**：pre 实例 `c5252de3` 在 `2026-09-24T14:06:21Z` 记到 1 次 legacy 命中
  （日志 `legacy shared service token used`，`/config.v1.ConfigService/GetKey`，`client` 为空）。
  窗口从这次命中起算，最早 `2026-10-01T14:06:21Z` 可删，前提是先找出并换掉这个调用方，且此后零增量。
  e2e `config-legacy-metric.spec.ts` 当前应为红。
- 窗口用 `increase(machine_token_legacy_hits[7d])`，不要用 `max_over_time`：后者对累计计数器问的是
  「历史上有没有命中过」，Pod 重启后旧实例的 stale 时序会让门禁一直红。细节见
  `docs/design/machine-token.md`「当前烘烤窗口」。

### Consul 的实际作用范围

- 集群里仍有 `consul` ns，但**网关与 config 都不依赖它**。`routes/{dev,pre}.yaml` 的 11 个后端全是
  `direct://<k8s Service>.ecommerce.svc:<port>`；业务服务已关闭 Consul 注册，`discovery:///` 会因空列表
  让 `/readyz` 503。两种 target 写法网关都支持（`proxy.go` 的 `discoveryPrefix`/`directPrefix`）。
- 网关找 config 服务走 K8s Service DNS `http://config-center.config-center.svc:30010`，不经 Consul。
- config 服务的 Consul 注册没有消费方，`deploy/{dev,pre}/config/deployment.yaml` 均为
  `CONSUL_ENABLED=false`。2026-08-31 用最小策略 `service "config-service" { policy = "write" }` 烟测过注册，
  能力已验证，默认保持关；长期开启需建持久令牌存 Secret，补 `CONSUL_HTTP_TOKEN` 再改回 `true`。
- 本机跑 config 必须 `CONSUL_ENABLED=false`，否则集群内客户端可能解析到你的 Mac。
- ⚠️ 匿名读 Consul catalog 返回**空对象**而不是 403，容易误判成「一个服务都没注册」。查真实状态要带 token。

### 历史

- `config-center` 这个 ns 名是 2026-08-24 切流时没改的遗留标签，里面跑的就是本仓的 config 服务。
- 上游 ecommerce 仓的旧 `gateway/` 目录已于 2026-08-24 删除（历史在其 tag
  `backup/pre-control-tower-20260823`），旧 config-center 仓同样退役。本仓是这两块的唯一真相源。

## 必读

| 文档 | 内容 |
|---|---|
| `docs/design/architecture.md` | 两服务架构、网关请求链路、不变式 |
| `docs/design/auth.md` | JWT 信任域、混合撤权三场景操作手册 |
| `docs/design/decisions.md` | 砍掉/不做清单及原因——**改动前先查这里，别把砍掉的东西加回来** |
| `docs/design/adr-0002-bff-session.md` | **现行鉴权决策**：BFF + 服务端 session（取代 ADR-0001） |
| `docs/design/bff-migration.md` | BFF 化实施手顺：三轨并存、四阶段、按阶段回滚 |
| `docs/operations/2026-08-30-recovery-record.md` | 2026-08-29/30 恢复实录：六类故障的判别法与教训——**排查「网络面板正常但功能不对」这类症状前先翻它** |
| `docs/operations/argocd-helm-gitops.md` | GitOps 发布方案与 CI 去集群凭据的原因 |
| `deploy/README.md` | 网关 apply 前的一次性准备：Config Center 键、machine token、selector Secret |
| `docs/operations/configctl.md` | 命令行写 Config Center：`configctl put/get/ls`、凭据与 scope 限制——**要把文件灌进某个 key 就用它，别手搓 curl** |

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

CI 里由 `.github/workflows/e2e.yml` 承接：每 6 小时 schedule 巡检，另留 `workflow_dispatch`。
网关未部署期间，打 `gateway.apikv.com` 的用例与 legacy 指标用例会红，这是环境问题，不是回归。
失败发 ntfy（正文带失败用例名）；B1 恢复通知已实测：前一次 failure、下一次 success 时只发一条
「✅ 已恢复」，连续 success 不重复发。

`e2e/` 的每条用例都对应一个真实发生过的故障（见其 README 的对照表）。它抓到过
`make verify` 与单测都测不到的三类问题：CSP/安全响应头把自家资源拦掉、
指标链路名字对不上、网关路由形态被误解。**改 `web/Dockerfile` 的响应头、改鉴权流程、
改 `promql/catalog.go` 之后必须跑它。**

CI（`.github/workflows/ci.yml`）：PR 与 `workflow_dispatch` 只跑质量门禁（`test` Go 门禁 + `web` 控制台门禁 +
`codegen` 生成物门禁）；push main 与裸 semver tag（`X.Y.Z`）在三者都通过后构建并双推镜像。
main 推 `dev` 与 `sha-<7位>` tag，发布 tag 额外推同名版本 tag。`dev` 会被覆盖，不要用于部署。
