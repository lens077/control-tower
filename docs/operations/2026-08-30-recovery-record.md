# 2026-08-29/30 恢复实录：从「编辑器一直 Loading」到两个服务全绿

一次排查牵出一串问题，最后把两个服务重新拉起、把数据面指向 node3、并补上端到端护栏。
本文按「问题 → 判别 → 根因 → 修复 → 验证」记录，供下一次踩到同类症状时对照。

结论先行：**六个独立故障，五个的共同特征是「网络面板一片正常，只有控制台或日志里
一条容易被忽略的记录」**。它们全都逃过了 `make verify` 与单元测试。

## 时间线与产出

| 提交 | 内容 |
|---|---|
| `d28c21c` | Monaco 自托管，修编辑器永远 Loading |
| `f146a7c` | 数据面切 node3，重写 `scripts/dev-local.sh` |
| `1259047` `f46016b` | config 重新部署（镜像走 TCR、Consul 端口修正） |
| `4e398a9` | config 服务不再注册进 Consul |
| `464a7c1` `df9136f` | CSP `frame-src` + `X-Frame-Options: SAMEORIGIN` |
| `0f0b131` `c53e15e` | gateway 重新部署（`0.2.1`，2 副本） |
| `4ac2d5a` `c6fc37a` `21b1866` | e2e 接入 CI，失败进 ntfy |

发布：`0.2.1`（config）、`0.2.2` → `0.2.3`（config-web）。

## 一、编辑器永远停在 Loading

**症状**：`/edit?ns=…&key=…` 一直显示 `Loading...`，网络面板没有任何失败条目。

**判别**：`Loading...` 是 `@monaco-editor/react` 的**兜底占位符**，不是页面自己的转圈。
看到它就说明 monaco 的 AMD loader 没加载成。

**根因**：`@monaco-editor/react` 默认从 `cdn.jsdelivr.net` 注入 loader，而镜像的 CSP 是
`script-src 'self'`。**CSP 拦截发生在发请求之前**，所以网络面板看不到失败；而组件只把
init 失败 `console.error`，不改渲染状态。

**修复**：把 `monaco-editor/min/vs` 自托管到同源 `/vs`（`web/vite.config.ts` 的
`monaco-self-host` 插件 + `src/monaco.ts` 的 `loader.config`），Caddy 单独 `handle /vs/*`
避免落进 SPA fallback。主 bundle 不涨，按需加载不变。

**验证**：用真 Caddy 配置 + 真 CSP 起容器，无头浏览器实测 `state: "rendered"`、
`CONSOLE: []`。

## 二、按钮上显示 `action.save` 这样的原始 key

**根因**：代码引用了从不存在的 `common:` namespace（i18n 只注册了 `config` 一个）。
i18next 找不到 namespace 时会把前缀剥掉、把 key 原样吐回来。共 7 处 5 个 key。

**修复**：把 `action` 段补进 `config` namespace，去掉伪造的前缀。

## 三、`x509: certificate is valid for …, not redis.apikv.com`

**判别**（两者处置完全不同）：

| 错误 | 含义 | 处置 |
|---|---|---|
| `valid for A, B, not C` | 链已验过，卡主机名 | 补签 SAN |
| `signed by unknown authority` | CA 配错 | 换 CA，别重签 |

**根因**：证书按机器上的名字签（`redis.pigsty` / `node3`），而客户端经 Pangolin 隧道用的是
`redis.apikv.com`。

**两个容易误判的地方**：`verify-ca` 只验链不验名，会把问题一直掩盖到有人改成 `verify-full`；
`insecure_skip_verify: true` 不是修复，Go 的这个开关会跳过**全部**校验。

**修复**：用现有私钥重签、SAN 只加不减（少一个名字就会打断原先用它连接的组件）。
PostgreSQL 走 `pg_reload_conf()` 热加载，不重启。完整步骤见工作区
`pigsty-deploy/cert-san-resign.md`。

## 四、指标链路三连

系统页的曲线要经过「服务 → collector → VictoriaMetrics → 查询」四段，三段各坏一处：

1. **域名改名后 collector 没同步**：`node3-metrics` 改成 `metrics` 后，collector 仍推旧域名，
   日志里 `Exporting failed. Dropping data. dropped_items: 1061` —— **整条指标链路静默断了**。
2. **VM 没开 `-opentelemetry.usePrometheusNaming`**：指标名保持 OTLP 点号形态
   （`pgxpool.acquired_connections`），与 `internal/pkg/promql/catalog.go` 按 Prometheus 规范
   写的查询对不上，表现为**查询成功但一条序列都没有**。
3. **主机指标缺 DaemonSet**：单副本 Deployment 上开 `hostMetrics` 只能拿到一个 Pod 的视角。
   另外 `system.cpu/memory.utilization` 是**默认关闭的可选指标**，不显式打开的话
   CPU/内存两张图永远空，而磁盘/网络正常——很容易误判成「采集器坏了一半」。

**修复 owner 在 kubernetes 仓** `components/opentelemetry{,-node}/`。验收用仓库自带的
live 测试，它会逐组报「取不到任何序列」：

```bash
CONFIG_CENTER_VM_ENDPOINT=http://metrics.apikv.com \
  go test ./services/config/internal/pkg/promql -run Live -v
```

⚠️ 该测试用 24h 窗口 + 5m 步长。数据刚接通时栅格点落不到新数据上，会假红；
换 1h 窗口或等数据积累即可区分。

## 五、刷新页面就掉登录（e2e 抓到，此前一直坏着）

**症状**：登录后一切正常，但**刷新或直接打开深链就退回「请先登录」**。

**根因是两层，都在同一个 Caddy header 块里**：

1. CSP 没写 `frame-src` → 落到 `default-src 'self'`，静默续期的隐藏 iframe
   （Casdoor `authorize?prompt=none`）被拦；
2. 补上 `frame-src` 后仍失败，日志显示授权本身成功、回调页加载不出来：
   `net::ERR_BLOCKED_BY_RESPONSE` + `Refused to display … 'X-Frame-Options' to 'deny'`。
   **`DENY` 连同源自己都不许嵌**，而静默续期正是把本站的 `/callback` 加载进本站的 iframe。

**修复**：`frame-src https:` + `X-Frame-Options: SAMEORIGIN` + `frame-ancestors 'self'`。
第三方嵌套照样挡得住。

顺带清掉一条**永久性假警报**：Zod 4 会试 `Function("")` 探测能否 JIT，在 `script-src 'self'`
下必被拦。它自己 try/catch 了、功能无碍，但控制台永远躺着一条红色 CSP 违规，把真错误淹掉
（排查时确实先被它带偏）。`z.config({ jitless: true })` 消除。

## 六、集群侧的现状漂移

排查途中发现文档与实际严重不符，已逐条核对并写进 `AGENTS.md`：

- `postgresql` 与 `config-center` 两个 ns 都不存在了（集群 2026-08-21 前后重建）；
- PG/Redis 已搬到 node3，唯一通路是 Pangolin 的 raw 端口；
- gateway 被手工 `delete -f`，只剩孤儿 HTTPRoute 让 `gateway.apikv.com` 返 500；
- **GHCR 上 `control-tower-config` 是 private**（匿名拉取 401，`ImagePullBackOff`），
  而 `control-tower-config-web` 是 public，两者可见性不一致；
- `CONSUL_ADDR` 原来写 8501/https，而 consul Service 根本没暴露 8501。

### 一个被我自己传播过的错误

`deploy/*/config/deployment.yaml` 里有条注释说「SERVICE_NAME 必须与网关的
`discovery:///config-service` 一致」。**与实际配置不符**：网关的配置源写死的是
`http://config-center.config-center.svc:30010`，走 K8s Service DNS；`routes/{dev,pre}.yaml`
里的 `discovery:///` 只指向 11 个业务后端，不含 config 服务。

我照抄了这条注释，得出「gateway 回来后会解析不到 config 服务」的错误结论，查证后更正。
教训：**注释不是真相源，配置才是**。

由此 config 服务的 Consul 注册没有任何消费方，已置 `CONSUL_ENABLED=false`（见
`docs/design/decisions.md` 的防回退条目）。

⚠️ 另一个坑：**匿名读 Consul catalog 返回空对象而不是 403**，第一次查看到「空」，
差点得出「一个服务都没注册」的错误结论。查真实状态必须带 token。

## 现在的护栏

`e2e/`（`@playwright/test`，打真实环境）——每条用例对应上面某个具体故障，
对照表见 `e2e/README.md`。CI 由 `.github/workflows/e2e.yml` 承接：
`workflow_dispatch`（每次 `kubectl apply` 后手动跑）＋ 每 6 小时巡检，失败进 ntfy 且
正文带失败用例名。**没挂在 PR 上**——它测的是已部署的东西。

单测侧新增 `web/src/monaco.test.ts`（删掉 `loader.config` 就红）。

## 回滚点

| 变更 | 回滚 |
|---|---|
| node3 Redis 证书 | 初次补域名：`redis-tls.pem.bak-20260829-220329`；再补入口 IP：`redis-tls.pem.bak-20260830-181443` |
| node3 PG 证书 | 初次补域名：`server.crt.bak-20260829-220415`；再补入口 IP：`server.crt.bak-20260830-181443` + `pg_reload_conf()` |
| node3 VM 参数 | `/etc/default/vmetrics.bak-20260829-234102` |
| HTTPRoute | 文件在 `deploy/pre/{config,gateway}/`；2026-08-31 已恢复集群对象。不要再依赖 `/tmp/gw-orphans/`（已随重启消失） |
| 镜像 | 当前三个服务统一为 public GHCR 的 `0.2.5`；config 的 TCR 绕路已撤销 |

## 2026-08-31 后续收口

- **写路径 e2e 已补**：新建 → 保存 v1 → 列表可见 → 保存 v2 → 回滚为 v3 → 删除，
  专用 `e2e` namespace，跑完零残留；CI 由 11 条变 12 条。
- **恢复通知已补并实测**：上一次 failure、这一次 success 时 ntfy 只发一条「✅ 已恢复」；
  连续 success 不重复发。实现直接查 GitHub Actions API，不用不可变 key 的 cache。
- **Consul 注册能力已烟测**：临时最小权限 `service "config-service" {write}` 注册成功，
  catalog/health 有 1 个 passing 实例；随后关闭并删除实例、ACL token/policy 与 K8s Secret。
  dev/pre 默认仍显式 `CONSUL_ENABLED=false`。
- **HTTPRoute 已恢复**：三条对象均为 `Accepted=True`、`ResolvedRefs=True`；config Web 根路径、
  config API `/healthz`、gateway `/healthz` 与 `/readyz` 均返回 200。gateway 根路径按应用契约
  返回 `404 ROUTE_NOT_FOUND`，不能用作入口探针。工作树已恢复 e2e 的 6 小时 schedule，合入
  `main` 后生效。
- **overlay 已收敛**：`deploy/dev` 的 18 个资源通过 API Server dry-run；pre gateway 补齐 Service、
  config-source Secret 与 Config Center 的 5 个启动键后，已实际滚动到 `DEPLOYMENT_MODE=pre`。
  公网不再运行 dev 的 insecure/localhost BFF 参数；pre 暂时关闭会话轨，仅保留 legacy bearer。
  切换后的 `workflow_dispatch` run `33364139018` 全部通过。dev/pre 仍指向同一组 namespace 与对象名，
  不能当作隔离环境。
- **镜像统一**：dev/pre 六份 manifest 使用 public GHCR 的同一 `0.2.5` tag。config 包改 public 后，
  无凭据 OCI manifest 请求返回 200；live config 已从 TCR 滚动到 GHCR，镜像 digest 保持
  `sha256:aff79b5339364588d6b6e61c4dfec199ebc8dbfec188a0b351d30613d6630774`。

## 遗留

- **dev 与 pre 共用同一个库**：node3 是单实例，`pre.yaml` 里已标 TODO，拆开前别把 pre 当隔离环境用。
- **API_\* 三组曲线**：`rpc.server.duration` 只在 RPC **完成时**记样本，而 config 服务当前流量
  几乎全是长连的 `WatchKeys`，所以那三张图要等有短请求才有数据（不是故障）。
- **`metric-query.apikv.com` 的半成品资源已删除**：Pangolin resource 44 当时仍启用，但唯一 target
  已禁用，所以表现为与不存在子域一致的 404。2026-08-31 经 Pangolin API 删除并核对数据库零残留；
  删除前备份为 node1 的 `db.sqlite.bak-metric-query-removal-20260831T055059Z`。实际查询入口只保留
  `http://metrics.apikv.com`（resource 21，target 已启用）。
