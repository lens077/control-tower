---
status: accepted
date: 2026-08-24
supersedes: ADR-0001
---

# ADR-0002：改用 BFF + 服务端 session，取代 bearer JWT

网关演进为 BFF：它自己完成 Casdoor code 交换、把 token 保管在服务端（Dragonfly），浏览器只拿一枚 httpOnly 的不透明 session id。撤权 = 删会话（即时）。本 ADR **取代 ADR-0001**。

## 为什么推翻 ADR-0001

ADR-0001 列的三条触发条件（同源化／桌面端退场／合规硬要求）**一条都没满足**。推翻的真实理由是另外两条，且都比原来那三条更硬：

1. **ADR-0001 有一处事实错误**。它写「三个源不同 → 需 `SameSite=None; Secure` → 撞三方 cookie 淘汰」——**在 prod 是错的**：`shop.apikv.com`、`gateway.apikv.com`、`casdoor.apikv.com` 同属 `apikv.com`，属 **same-site**。cookie 用 `Domain=.apikv.com; SameSite=Lax` 即可，不受三方 cookie 淘汰影响。当初对 cookie 方案的成本评估被这条错误抬高了。
2. **依赖成本已被接受**。会话存储（Dragonfly）集群里现成在跑，用户明确接受这份依赖——而「重新引入热路径状态」正是 ADR-0001 拒绝 A 方案的主要理由。

另外三项 A 独有、B 系方案给不了的能力：**会话清单**（可枚举某用户的活跃会话并逐个下线）、**即时撤权**（无推送延迟）、**封禁不再依赖跨系统两步**（今天必须先去 Casdoor 撤 refresh，漏做就等于没封）。

## 决定

- 网关新增 BFF 面：`/auth/{login,callback,logout,me}`；Casdoor 由公共客户端改为**机密客户端**（client secret）。
- 会话存于 **Dragonfly**（`dragonfly.dragonfly.svc:6379`），存 access/refresh token、身份与**角色**；浏览器只见不透明 session id。
- 传输：浏览器走 httpOnly cookie；桌面端（Tauri，拿不到浏览器 cookie）走**同一套 session、以 header 携带 session id**——不再用 bearer JWT，安全模型统一。
- **撤权 = 删会话**，即时生效；access token 续期由网关服务端完成，前端完全不参与。

## 后果

**得到**：撤权即时且无需维护名单；会话清单／设备管理能力；前端净删约 400 行（PKCE + tokenStore 全删）；角色在登录时取一次存进会话，**消除每请求回源 Casdoor**（`CasdoorRoleSource` 降级为登录时调用）；merchant/admin 白捡登录能力。

**付出（诚实记账）**：

1. **网关重获热路径状态依赖**——本次迁移刚删掉的东西又加回来了。`decisions.md` 里「Redis 角色缓存｜删除」那条据此更新。
2. **故障模式变化**：会话存储不可用 = 浏览器侧鉴权全线不可用（fail-closed by construction）。对比 B 系方案是「推送通道断 → 名单停更但业务继续」。因此 `readyz` 必须纳入会话存储可达性，且该存储要有 HA 预案。
3. **新增 CSRF 面**：靠「Origin 允许列表校验 + Connect 协议头天然屏障」应对（跨站表单 POST 无法设置 `Connect-Protocol-Version`，也无法用 `application/json`，因此必然触发预检并被 CORS 拦下），不引入 CSRF token。
4. **迁移期三轨并存**：cookie session ∥ session header ∥ legacy bearer JWT，直到桌面端跟上才拆。

**随之退役**（第 4 阶段完成后）：撤销名单机制、`auth/revocations.yaml` 键、`CasdoorRoleSource` 的每请求回源路径、前端 PKCE 实现。在此之前它们服务于 legacy bearer 轨，不能提前删。

## 未变

后端 10 个服务零改动——它们只认网关注入的 `x-md-global-*`，与凭据载体无关。这一点在 ADR-0001 与本 ADR 下同样成立。

实施手顺见 [bff-migration.md](bff-migration.md)。
