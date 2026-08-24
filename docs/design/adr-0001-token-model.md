---
status: superseded by ADR-0002
date: 2026-08-24
---

# ADR-0001：用 bearer JWT + 撤销名单，而不是 cookie session

> ⚠️ **本 ADR 已于同日被 [ADR-0002](adr-0002-bff-session.md) 取代**（改用 BFF + 服务端 session）。
> 保留原文供追溯，其中「三个源不同 → 撞三方 cookie 淘汰」一句**是错的**（prod 三个域同属 `apikv.com`，属 same-site），
> 这处错误正是当初压低 cookie 方案评分的原因，也是翻案理由之一。下文其余分析仍然有效。

浏览器与桌面端持 Casdoor 签发的短 TTL access token（bearer 头，只存内存），网关本地验签后查一份经配置中心秒级推送的撤销名单；**不采用** httpOnly cookie + 服务端 session。原因是这套拓扑里 session 的两个前提都不成立——前端与网关不同源（三方 cookie）、客户端不止浏览器（Tauri 桌面端），而 session 唯一的决定性优势「即时撤权」已被撤销名单以另一种方式拿到。

> ADR 编号在本目录内顺序递增（`adr-NNNN-slug.md`）；其余设计文档仍平铺于 `docs/design/`。

## 背景：这个选择大部分是继承的

写下本 ADR 时，以下都是既成事实，不是本次迁移的选择空间：

- Casdoor 是 OIDC provider，签发的就是 JWT；对抗评审已裁决 IdP 保持 Casdoor（Zitadel 转 AGPL、Ory 双组件过重、Keycloak 与「去 JVM」预算方向冲突）。
- 前端已改为 PKCE 直连 Casdoor（更早一次重构删掉了 user 服务里 40 行的 code→token 代理）。
- token 只存前端内存（`packages/utils/src/tokenStore.ts`，注释写明防 XSS），刷新页面即丢、靠 refresh token 冷启动恢复。
- 客户端有四个：consumer / merchant / admin 三个 Web + 一个 Tauri 桌面端（loopback redirect）。
- 10 个后端 Connect 服务的身份契约是网关注入的 `x-md-global-*` 头，服务自身不验签。

本次迁移真正设计的是**撤权机制**（见 auth.md），不是 token 载体。

## 考虑过的方案

**A. cookie + 服务端 session（未采纳）**

- ✅ 即时撤权原生免费；httpOnly 使 JS 完全接触不到凭据（比内存存储的 XSS 姿态更强）；心智模型简单，无 TTL 调参。
- ❌ 前端、网关、Casdoor 三个源互不相同 → 需要 `SameSite=None; Secure`，正撞浏览器三方 cookie 淘汰；本地开发是明文 HTTP，`Secure` 直接不成立。
- ❌ 桌面端（Tauri）不是浏览器会话模型，要另做一套凭据通道。
- ❌ 每请求查会话存储 → Redis/DB 进网关热路径，网关可用性绑死它；而本次迁移刚**删掉**网关的 Redis 依赖（旧角色缓存）。
- ❌ 引入 CSRF 面，需要另配防护。

**B. opaque token + 每请求 introspection（未采纳）**

真·有状态 token。撤权归零延迟，但把 Casdoor 放进每个请求的热路径——Casdoor 收编进集群之前那还是一条公网 RTT。作为 Q8-3 的选项 b 明确否决。

**C. 短 TTL JWT + 撤销名单推送（采纳）**

把查询方向倒过来：不是每请求**拉**一次会话状态，而是把一份极小的黑名单**推**到网关内存（只存「已撤销且未过期」条目，自动过期剔除），热路径只查本地 map。

## 实测数据（dev 集群，2026-08-24）

| 项 | 实测值 |
|---|---|
| access token TTL | 900 秒 |
| claims 形态 | `iss`/`aud`/`tokenType`/`sub`/`jti` 齐备，**无 `roles`** |
| 撤销名单热达 | 写入后约 1 秒到达全部副本 |
| 撤销生效 | 名单到达后首个请求即 `401 TOKEN_REVOKED`（毫秒级判定） |
| 自愈 | 前端静默续期，新 token 签发时刻 = 撤销后 1 秒，请求恢复 200，用户无感 |

## 后果

**得到**：热路径零远程调用；跨源与多端天然可用；网关无状态、可随意横向扩；撤权时效从旧网关的 5 分钟缓存窗压到秒级。

**付出（诚实记账）**：

1. **比纯 JWT 复杂**。多一条推送通道、一份要运维的名单、三种撤权场景各自的操作顺序（封禁必须先动 Casdoor 再写名单，否则被 refresh 绕回）。session 模型不需要这些。
2. **「零远程调用」已被削弱**。实测本部署 Casdoor 不把 roles 嵌进 claims，网关只好回源 `get-user`（5 分钟缓存 + singleflight + 过期兜底）。当前形态比设计初衷更靠近 session 一步——只是缓存粒度粗，且撤权不依赖它。
3. **撤销名单通道是单点**。config 服务不可用时名单停止更新；暴露窗由 token TTL 硬性封顶（≤15 分钟），断连超时后仅对 admin/高危路由升级为在线校验，不做全局 fail-close（理由见 decisions.md）。

## 什么时候重新评估

以下三条**凑齐两条**即值得重新评估「BFF + httpOnly cookie」（token 只存服务端、浏览器只拿 cookie，是当前浏览器应用的主流推荐）：

1. **同源化**：前端与网关收拢到同一域（Cilium 边缘统一的既定方向）——三方 cookie 问题随之消失；
2. **桌面端退场**，或桌面端改走独立的凭据通道；
3. **合规要求撤权绝对即时**，连秒级推送窗口都不接受。

## 真要迁移，代价在哪

工作量集中在前端与网关，**后端 10 个服务完全不用动**（它们只认 `x-md-global-*` 头，与凭据载体无关）：

| 范围 | 具体工作 |
|---|---|
| 网关 | 新增 session 存储接线（Redis/DB）+ 会话中间件；CSRF 防护；把 Casdoor 回调换成「code→建会话→下发 cookie」的服务端流程 |
| 前端 ×3 | 拆掉 PKCE 与 tokenStore，改为 cookie 承载；请求全部改 `credentials: include`；登录/登出/续期链路重写 |
| 桌面端 | 需要独立凭据方案（cookie 不适用） |
| 运维 | 会话存储的容量/高可用/备份；网关可用性从此绑定该存储 |
| 一次性 | 全量用户重新登录 |

反向的好处是撤销名单、`revocations.yaml` 键、名单新鲜度指标与三场景手顺可以一并退役。

**结论：现在不动。** 本次迁移刚在 dev 集群完成端到端验证（登录闭环、claims 实测、撤销演练全过），推倒重来的风险与收益不对等。触发条件凑齐再议，届时以本 ADR 为起点，新开 ADR-0002 记录翻案理由。
