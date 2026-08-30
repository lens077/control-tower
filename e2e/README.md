# e2e —— 两个微服务的实机浏览器测试

用真浏览器打**真实环境**，覆盖 config 服务 + Web 控制台与 gateway 两条链路。

## 为什么打真环境

这套用例的价值全在于覆盖只有真环境才会暴露的东西：CSP 响应头、Pangolin 隧道、
Casdoor 单点登录、node3 上的 PostgreSQL / Redis / VictoriaMetrics。本地起 mock 一个都测不到。
代价是它依赖外部可用性，因此失败要先看是不是环境抖动，再看是不是代码回归。

## 跑起来

```bash
cd e2e
pnpm install
pnpm run install-browser          # 首次:装 chromium
E2E_USERNAME=<账号> E2E_PASSWORD=<口令> pnpm test
```

凭据只从环境变量来，不写进仓库（AGENTS.md 硬约束 4）。缺变量时 `global-setup` 直接报错退出。

可覆盖的地址变量：

| 变量 | 默认值 |
|---|---|
| `E2E_CONFIG_URL` | `https://config.apikv.com` |
| `E2E_GATEWAY_URL` | `https://gateway.apikv.com` |

失败后看报告：`pnpm run report`（失败用例带 trace 与录像）。

## 登录态怎么处理

`global-setup.ts` 登录一次，存下的是 **Casdoor 那边的会话 Cookie**，不是控制台的登录态——
控制台把 access token 只放在内存里，整页加载后靠隐藏 iframe 向 Casdoor 做 `prompt=none`
静默续期换回来。所以「存 Casdoor Cookie + 每条用例整页打开」这套组合，顺带把静默续期
这条链路也持续回归了。

## 用例与它们守住的故障

每条用例都对应一个真实发生过的故障，注释里写明是哪一个。

| 用例 | 守住的故障 |
|---|---|
| 整页加载后保持登录 | CSP 少了 `frame-src`，静默续期的 iframe 被拦 → 每次刷新退回「请先登录」，深链永远打不开 |
| 深链打开 `/edit` 渲染编辑器 | Monaco 从 `cdn.jsdelivr.net` 取 loader 被 `script-src 'self'` 拦掉 → 永远停在 `Loading...` |
| 历史页 diff 编辑器 | 同上，`DiffEditor` 是另一条入口 |
| 系统页指标有数据 | 指标链路三连：域名改名后 collector 还推旧域名、VM 没开 `usePrometheusNaming`、主机指标缺 DaemonSet |
| 无未翻译的原始 key | 代码引用了不存在的 `common:` 命名空间，按钮上直接显示 `action.save` |
| 无 CSP 违规与未捕获异常 | 兜底：新加的跨源资源忘了在 CSP 里放行 |
| gateway `/healthz` `/readyz` | 网关没部署，或 HTTPRoute 的 backendRef 指向不存在的 Service |
| 受保护路由 fail-close | 鉴权被绕过（2xx）或网关自身出错（5xx） |
| `/config.v1.*` 不经网关暴露 | 路由边界被破坏 |

## 已知的判断陷阱

- **只断言「编辑器容器存在」不够**：加载失败时兜底占位符在同一个位置，必须断言真实内容行
  （`.view-line`）。
- **只探 `/healthz` 不够**：网关起来了但路由表/公钥没加载成也会返回 200，要看 `/readyz`。
- **图表空 ≠ 后端不可用**：后端不可用时前端会显式提示；查询成功但零序列则是指标名或标签对不上，
  两者要分开断言。
