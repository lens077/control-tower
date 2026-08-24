# BFF + 服务端 session 实施手顺

决策与权衡见 [ADR-0002](adr-0002-bff-session.md)。本文是可执行手顺。

**这是改线上鉴权**：两个服务已切流、上游旧网关目录已删（无「切回旧网关」退路）。因此全程按**三轨并存 + 按阶段可回滚**设计，任一阶段异常都能退回上一阶段且不需要客户端配合。

## 设计要点

### 会话

存储：Dragonfly（`dragonfly.dragonfly.svc:6379`）。键 `sess:{id}`，值含：

| 字段 | 用途 |
|---|---|
| `access_token` / `refresh_token` / `access_exp` | 服务端续期用，绝不出网关 |
| `sub` / `owner` / `name` | 注入 `x-md-global-*` |
| `roles` | **登录时取一次**，消除每请求回源 Casdoor |
| `created_at` / `last_seen` / `ua_hash` | 会话清单展示与异常排查 |

- session id：32 字节随机 base64url，不可猜。
- TTL：绝对上限（建议 7 天）+ 空闲上限（建议 12 小时），用 Dragonfly 原生 TTL。
- 二级索引 `user:{sub} → set(session id)`：会话清单与「踢掉某用户全部会话」靠它。删会话时同步清索引。

### Cookie 属性

```
__Secure-ct_session=<opaque id>; HttpOnly; Secure; SameSite=Lax; Domain=.apikv.com; Path=/
```

- 不用 `__Host-` 前缀：它禁止 `Domain` 属性，而我们需要跨 `shop`/`gateway` 两个子域。
- `SameSite=Lax` 足够：两个子域是 same-site，XHR 照常携带；跨站 POST 被浏览器拦掉。
- ⚠️ 待实测：dev 走 `http://localhost` 时 `Secure` cookie 的行为（浏览器把 localhost 视为可信源，预期可用）。不通就按环境降级 `Secure`。

### 端点

| 端点 | 行为 |
|---|---|
| `GET /auth/login?redirect=` | 生成 state（存 Dragonfly，短 TTL）→ 302 到 Casdoor |
| `GET /auth/callback` | 校验 state → 用 client secret 换 token → 取角色 → 建会话 → `Set-Cookie` → 302 回 redirect |
| `POST /auth/logout` | 删会话 + 清索引 + 清 cookie；可选联动 Casdoor RP-initiated logout |
| `GET /auth/me` | 返回 `{authenticated, name, roles, expiresAt}` 供前端渲染，**不含任何 token** |

`/auth/*` 与 `/healthz`、`/readyz` 同列为本地路由，先于包路由注册，永不被代理。

### 请求链路变化

```
cookie/session-header → Dragonfly 查会话 → [access token 近过期则服务端续期并回写]
→ 身份与角色入 context → Casbin → 注入 x-md-global-* → 反代
```

- 前端**完全不参与续期**——今晚观察到的「callback 期间 401 触发全局退登」竞态随之消失。
- **不做会话查询的本地缓存**：即时撤权是本方案的核心卖点，缓存会把它打回「缓存 TTL 级」。Dragonfly 在集群内亚毫秒，先不优化；确有延迟问题再议，届时须在 ADR 里记明撤权时效的让步。

### CSRF

不引入 CSRF token，用两道：

1. **Origin/`Sec-Fetch-Site` 校验**：状态变更请求的 Origin 必须在允许列表内。
2. **Connect 协议头天然屏障**：跨站表单 POST 无法设置 `Connect-Protocol-Version`，也用不了 `application/json`，必然触发预检并被 CORS 拦下。

### readyz 与故障模式

- `readyz` 增加「会话存储可达」；不可达即摘流量（fail-closed，符合本方案的取舍）。
- 观测：会话数、创建/删除速率、查询 p99、续期成功率、存储错误率。存储错误率非零即告警——它现在是鉴权的单点。

## 阶段与回滚

| 阶段 | 内容 | 客户端影响 | 回滚 |
|---|---|---|---|
| **P0 前置** | Casdoor 机密客户端 + secret；**重建 Dragonfly 凭据 Secret**（旧 `redis-auth`/`redis-tls-ca` 随旧网关删除已不存在）；定 cookie domain | 无 | 无需 |
| **P1 网关加能力** ✅ 代码完成 | session 存储 + BFF 端点 + 三接受（cookie ∥ session header ∥ legacy bearer） | **零**（现有 bearer 客户端照常） | 回滚镜像 |
| **P2 前端切换** | 删 PKCE/tokenStore，改 `credentials:'include'` + `/auth/me`；dev 加 vite proxy 走同源 | 浏览器端切到 cookie | 前端回滚（网关三接受不变，旧前端立刻可用） |
| **P3 桌面端** | Tauri 改存 session id（OS keychain）并以 header 携带 | 桌面端切换 | 桌面端回滚到 bearer（网关仍接受） |
| **P4 拆除** | 移除 legacy bearer 轨、撤销名单机制、`auth/revocations.yaml` 键、每请求回源路径 | 无 | 需重新部署才能回退，故必须在 P2/P3 稳定后再做 |

每阶段验收：`make verify` 全绿 + 该阶段的实测项（P1：三轨各自能通；P2：登录/续期/登出/撤权四项浏览器实测；P3：桌面端同上；P4：全链路回归 + 确认无 legacy 流量）。

### P1 落地记录（2026-08-24）

代码已完成，`make verify` 25 包全绿（新增 `bff`、`session` 两包）。**启用方式是配置驱动**：
`SESSION_REDIS_ADDR`、`CASDOOR_CLIENT_ID/SECRET`、`BFF_PUBLIC_BASE_URL` 四项齐备才启用会话轨，
缺任一项即退化为纯 legacy bearer（`BFF_ENABLED=false` 可强制关闭）。因此**镜像上线本身不改变任何行为**。

单测覆盖的行为契约：

| 契约 | 测试 |
|---|---|
| cookie 轨认证通过，角色取自会话（热路径不回源 Casdoor） | `TestSessionCookieAuthenticates`（回退源被设为「一调用就失败」） |
| header 轨（桌面端）同一套会话 | `TestSessionHeaderAuthenticates` |
| cookie 轨状态变更请求必须带可信 Origin | `TestCookieTrackRejectsBadOrigin`（坏 Origin 与缺失 Origin 都拒） |
| header 轨不受 Origin 影响（非环境凭据） | `TestHeaderTrackIgnoresOrigin` |
| 删会话即时生效 | `TestDeletedSessionRejected` |
| 临近过期服务端续期，前端无感 | `TestServerSideRefresh` |
| 续期被 IdP 拒 → 删会话 + 401 | `TestRefreshRejectionRevokesSession` |
| **legacy bearer 与会话轨并存不受影响** | `TestLegacyBearerStillWorksAlongsideSessions` |
| 空闲/绝对双 TTL、会话清单、整户踢出 | `session` 包五个测试 |
| state 校验、开放重定向防护、`/auth/me` 不泄露令牌 | `bff` 包五个测试 |

### P1 部署与端到端实测（2026-08-24，dev 集群）

镜像 `sha-c8cb6a8`，先以「会话轨关闭」形态上线验证零影响，再补 `casdoor-bff` Secret 点亮。

**前置落地**：`ecommerce/dragonfly-session`（密码/用户名/CA，值取自集群内 `dragonfly-password-secret` 与 `dragonfly-tls`）；
`ecommerce/casdoor-bff`（client id/secret，取自 Config Center `user/dev/bootstrap.yaml` 的 casdoor 段，
与网关 `JWT_AUDIENCES` 同属应用 `lens/ecommerce`）。

**坑**：Dragonfly 以 `--tls` 启动，明文连不上；其证书 SAN 含 `dragonfly.dragonfly.svc`，故走正常 CA 校验即可。

| 验证项 | 实测结果 |
|---|---|
| 会话轨关闭态（缺 Casdoor Secret） | 日志 `has_casdoor_client:false`，Pod 正常启动；healthz/readyz 200、匿名 200、无 token 401、`/auth/me` 404（端点未注册） |
| 会话轨启用 | 日志 `BFF 会话轨已启用`；**启动 ping 通过**即证明 Dragonfly TLS+AUTH 可用 |
| 登录闭环 | `/auth/login` → Casdoor（机密客户端换 code）→ `/auth/callback` → 建会话 → 下发 cookie → 跳回前端，浏览器实测一次通过 |
| 会话落库 | `EXISTS sess:*`=1、TTL≈43200s、`SMEMBERS user:<sub>` 命中 → 二级索引（会话清单底座）生效 |
| 身份与角色 | `/auth/me` 返回 name/owner/roles，**roles 在登录时取一次存入会话**，热路径不回源 |
| cookie 轨 + 可信 Origin | 受保护 RPC 200，真后端返回 `{"isCartEmpty":true}` |
| CSRF | 同一 cookie + 恶意 Origin → **403 CSRF_ORIGIN_REJECTED** |
| 桌面端 header 轨 | 无 Origin → 200 |
| **即时撤权** | 删会话后**下一个请求**即 `SESSION_INVALID`，间隔 40ms 且全为建连开销——**零传播延迟**（旧模型为写名单+1s 推送） |
| 登出 | 204 + `Max-Age=0` 清 cookie + 会话删除 + 后续请求 401 |

**dev 的已知限制**：前端 `localhost:3000` 与网关 `192.168.3.131:8080` 跨站，`SameSite=Lax` 会拦掉 cookie，
因此上述验证在网关同源下完成。P2 的 vite proxy 正是为解决这一点（prod 同属 `apikv.com` 无此问题）。

## 工作项清单

**网关（control-tower）**
- 新增 `internal/session`（Dragonfly 客户端、CRUD、二级索引、TTL）——可参照 `services/config/internal/data` 的 Redis 接线范式（含 TLS/用户名密码）
- 新增 `internal/bff`（四端点、state 管理、code 交换、服务端续期）
- 改 `internal/httpmw/auth.go`：三接受识别 + 会话路径
- 新增 Origin/CSRF 中间件；`readyz` 纳入存储可达；观测指标
- `CasdoorRoleSource` 降级为登录时调用（保留代码，服务于 legacy 轨）

**前端（ecommerce）**
- 删 `packages/configs/src/auth/pkce.ts`(243)、`packages/utils/src/tokenStore.ts`(77)、`session.ts` 大半(92)
- `packages/api` 的 transport/interceptor：`credentials:'include'`，停止附 Authorization
- consumer `AuthProvider` 重写为「读 `/auth/me`」；merchant/admin 顺势接入
- `vite.config` 加 dev proxy（顺带根除跨源 CORS 与 secure-context 问题）
- 涉及认证原语的 **12 个文件**需过一遍

**桌面端**：session id 存 OS keychain，请求带 header（P3）

**部署**：网关加 `CASDOOR_CLIENT_SECRET`、Dragonfly 地址与凭据 Secret；cookie domain 配置项

**后端 10 服务**：零改动

**文档**：ADR-0001 标记 superseded；`auth.md` 重写为会话模型；`decisions.md` 更新 Redis 依赖与撤销名单两条

### P3 真机验证记录（2026-08-24，Tauri 桌面端）

**结果**：登录闭环通过，日志链路 `GET /auth/login 302 → GET /auth/callback 302 → GET /auth/me 200
→ POST /user.v1.UserService/UserProfile 200`——最后一条是真业务 RPC 带权成功，证明会话头贯穿全链。

**真机才暴露的四个问题**（Web 端全绿、类型检查全绿都发现不了）：

| # | 问题 | 根因 | 修法 |
|---|---|---|---|
| 1 | `missing oauth state` | Tauri 登录子窗口是**独立 WebView**，回写的 state cookie 在回调时取不到 | native 流程 **state 存服务端**（GETDEL 单次使用 + 10min TTL）。安全性由「state 不可猜 + 单次使用 + 回调必须回环」保证 |
| 2 | `/auth/me` 恒返回未登录 | 该端点与 `/auth/logout` **只读 cookie**，不认会话头 | 两端点同时识别 cookie 与会话头 |
| 3 | 请求打不到网关 | `bff.ts` 读**构建期** env（dev 是 `/api`），在 `tauri://localhost` 下解析成 `tauri://localhost/api/...` | 改用运行时 `getGatewayBaseUrl()` |
| 4 | 预检被挡 | `bff.ts` 用全局 fetch，而 `tauri://localhost` 不在 CORS 允许列表 | 改用 `getAppFetch()`（桌面端是 Rust 侧 http 插件，绕开 CORS） |

**同时补上 P1 的两个缺口**：`/auth/*` 原先直接挂 mux、**绕过整条中间件链**——
既不进访问日志（问题 1 发生时网关侧查不到任何记录，排障只能靠推测），
生产上跨源调 `/auth/me` 也拿不到 CORS 头（`shop.apikv.com` → `gateway.apikv.com` 必被浏览器挡）。
现套 Recover/AccessLog/Cors，但**不套 Auth**（登录入口必须匿名可达）。

**dev 环境的硬约束**：整条 BFF 流程必须与 `BFF_PUBLIC_BASE_URL` **同源**，否则 state cookie 中途丢失。
dev 里靠 vite proxy 凑同源（桌面端网关设为 `http://localhost:3000/api`）；
生产天然满足（三域同属 `apikv.com`）。

**会话清单实证**：`user:<sub>` 索引 6 条、实际存活 4 条（2 条是撤权演练直删 `sess:` 留下的悬挂条目，
`ListByUser` 惰性清理，设计如此）。这是服务端会话方案独有的能力。
