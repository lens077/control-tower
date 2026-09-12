# 管理员服务健康快照

## 目的与边界

为 ecommerce 的 `/monitor` 提供真实探测结果。端点是网关本地的只读诊断接口，不创建新业务服务，不改变 Connect RPC 的路由或 POST 授权纪律，也不替代 `/healthz`、`/readyz`。

`GET /admin/health/services` 复用网关的身份认证（BFF 会话优先，保留既有迁移期 bearer 支持），仅允许角色精确包含 `admin` 的已认证主体。角色来自已验证身份，不读取客户端 `x-md-global-*`。固定路径单独注册，不进入匿名/访客清单，不允许用户指定目标、路径或查询参数。POST 等方法返回 405；无身份返回 401；非 admin 返回 403；响应均带 `Cache-Control: no-store`。

鉴权复用 `AuthDeps.authenticate`，不为此复制 session/token 处理；授权是这个本地诊断端点的固定 admin 策略，不能通过业务 Casbin 的 POST-only 策略误放行 GET。网关其余路径保持原链路。

## 探测来源

每次从已加载的不可变路由表取快照。目标只来自运营配置：`direct://host:port` 复用 Service 地址，`discovery:///name` 复用 Resolver 选点。`telemetry` 与 `behavior` 共用目标时不重复展示。新配置生效后下次请求使用新表，不缓存旧目标。

本接口报告的是「从当前网关沿现有路由选择的一次健康探测」，不是所有副本巡检，也不能证明下单、支付等业务流程正常。不能将单实例成功写成整个集群健康。

对选中的目标发送无用户凭据的 h2c `GET /healthz`，复用生产网关 transport。不跟随重定向；不复制请求 cookie、Authorization 或身份头；响应最多读取 64 KiB。只解析 `healthy` 布尔值，丢弃依赖错误原文、内部地址、build 和其他字段。

## HTTP 契约

成功获取快照返回 200，即使其中部分服务异常；顶层 503 表示路由表未就绪、无可探测配置或快照生成失败。服务按名称排序。

| 字段 | 约束与含义 |
|---|---|
| `checked_at` | RFC3339 UTC，本轮快照完成时间；缓存命中不刷新时间 |
| `services` | 服务数组，最多 64 项；不存在时为空数组，不返回 null |
| `services[].name` | 路由一级包名，不含地址 |
| `services[].status` | `healthy / degraded / unavailable / unknown` |
| `services[].latency_ms` | 非负整数，包含本次探测与读取耗时；未发出探测时为 null |
| `services[].checked_at` | 该项观察完成时间，RFC3339 UTC |
| `services[].reason` | 可选固定原因码，不是原始错误字符串 |

状态口径：

- `healthy`：HTTP 200，JSON 明确 `healthy: true`。
- `degraded`：HTTP 200 或 503，JSON 明确 `healthy: false`，原因 `dependency_unhealthy`。
- `unavailable`：无实例、请求超时、连接失败或意外 HTTP 状态，原因 `no_instance / timeout / probe_failed`。
- `unknown`：缺失或错误的 JSON、非法目标配置、未开始的探测，原因 `invalid_response / not_configured`。

健康探测沿用代理的传输反馈语义：只有连接层失败可影响 Resolver 的被动健康计数；`healthy: false` 不是传输失败，不能据此冷却节点。调用方取消不归咎后端。Resolver 的 done 始终只调用一次。

## 资源边界

最多 4 个并行探测，每项 2 秒，整轮 5 秒。调用方断开或超时取消本轮剩余工作。单网关进程最多一轮在途探测；其他调用等待该轮结束，可独立取消等待。完整快照缓存 5 秒（仅可丢内存，按路由表指针失效），减少多个管理页轮询对后端的压力；过期或取消的快照不冒充新结果。没有后台 Ticker。

## 前端与部署

浏览器只访问网关的固定端点，10 秒轮询并允许手动刷新。请求失败区分网络/权限/网关尚未升级；保留旧快照时必须显示旧的检查时间与过期提示。401/403 清除旧数据，禁止继续展示已失权的快照。

此变更不修改 `routes/{dev,pre}.yaml`、Config Center 键或业务端口，不要求 ecommerce 升级 Go 模块依赖。仓内核对 ecommerce 的 `helm/files/zero-trust.yaml`：gateway egress 和业务 ingress 已在对应服务端口提供 L4 放行，没有限制 GET /healthz 的 L7 规则，因此本次不新增网络权限。发布前仍须核查部署环境是否使用相同策略；若另有 L7 策略须明确放行 GET /healthz。前端的同源 `/api` 代理须透传 `/admin/health/services`，不能把它当 SPA 静态路径。升级网关镜像和前端之后功能才会在部署环境可用；本地提交不代表已部署。

## 验收边界

经用户确认，只在两个公开边界验证：完整网关 HTTP 请求链和 Firefox 中的 monitor 页面。网关使用本地 HTTP/h2c 服务验证已认证 admin、非 admin、伪造身份头、部分失败、错误 JSON、超时/取消、禁止重定向及配置更新；真实 Resolver 通过本地 Consul HTTP 目录获取实例。并发测试验证多个读者复用同一快照、探测不超过并发上限以及总截止后不缓存不完整结果。

浏览器固定响应验证动态卡片、权限与过期态、刷新恢复和助手跳转。另提供跨仓完整链路验收（需 ecommerce 已安装依赖和 Playwright Firefox）：

```bash
E2E_ECOMMERCE_DIR=../ecommerce go test ./services/gateway/tests -run '^TestServiceHealthFirefoxContract$' -count=1 -v
```

该测试启动真实本地网关/BFF 会话和 h2c 后端，通过 sibling `frontend/e2e/monitor.gateway.mjs` 拉起独立端口的 admin Vite；Firefox 经同源代理取得真实健康响应，未用 page.route 替换健康数据。普通 `make verify` 不设置该变量，明确跳过跨仓 Node/浏览器测试。本地夹具和真实协议联调均不声称等价线上验收。
