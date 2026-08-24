# 鉴权设计：JWT 信任域、混合撤权与操作手册

> 「为什么是 bearer JWT 而不是 cookie session」的权衡、翻案触发条件与迁移代价见 [ADR-0001](adr-0001-token-model.md)。

## 结论摘要

- IdP 保持 Casdoor；授权引擎保持 Casbin（网关进程内粗闸）。
- Access token 短 TTL（dev 实测 900 秒生效）。**角色不在 claims 里**——2026-08-24 真 token 实测本部署 Casdoor 不嵌 `roles`，已按下文回退分支启用 `CasdoorRoleSource`（get-user + 5 分钟进程内缓存）。
- 撤权走「配置中心撤销名单键 + Casdoor 侧操作」的混合通道，生效时间约等于 Watch 推送延迟（秒级）。
- 高危路由可标注 `online_check`，命中时实时调 Casdoor 校验，错误按 fail-close 处理（只收窄授权，不放大）。

## JWT 校验（P0-C 修复）

网关对每个非匿名请求本地验签，并强制绑定信任域。全部条件缺一不可：

| 校验项 | 说明 |
|---|---|
| 签名 | RS256，公钥来自 Config Center `gateway/<env>/secrets/public.pem`，热更新 |
| `iss` | 必须等于 Casdoor issuer |
| `aud` | 必须命中本系统 client id 白名单 |
| `tokenType` | 必须是 access token；**拒绝 refresh token**——Casdoor 的 refresh 与 access 同钥 RS256，裸验签会被长效 refresh token 冒充（config-center `iam.go` 的既有防线反证了这一风险） |
| `sub/iat/exp` | 必填；`nbf/iat` 容差 60 秒（Casdoor 与网关亚秒时差曾造成登录死循环） |

角色 claims 的形状以三前端 + 桌面端的真实 token 固化为 fixture 测试。多角色语义：Casbin 消费完整角色数组，任一角色允许即放行；旧网关只取 `Roles[0]` 的行为不再保留。

### 回退分支

若实测 Casdoor 无法把角色可靠嵌入 access token claims，回退为「网关调 Casdoor get-user + 本地缓存」，并叠加撤销名单。回退后撤权时效仍优于旧网关（名单秒级 + 缓存 TTL 封顶）。分支决策点在 P3 真实 token 验收，结论回写本文。

## 混合撤权（P0-D 修复）

撤销名单存放在 Config Center 键 `gateway/<env>/auth/revocations.yaml`，网关 Watch 后内存查表。条目的 `expires_at` 由 config 服务按「撤销时刻 + 最大 token TTL + 60 秒 leeway」派生，**禁止手工填写**——手填过短会让被撤 token 复活。

三种场景的操作顺序不同，不能混用：

### 场景一：调整角色（升权或降权）

1. 在 Casdoor 修改用户角色。
2. 写撤销条目 `{sub, issued_before=现在}`。
3. 效果：存量 access token 秒级失效 → 前端静默用 refresh token 换新 → 新 token 携带新角色。refresh 拿到新 claims 是设计机制，不是漏洞。

### 场景二：封禁 / 强制下线

1. **先在 Casdoor 撤销该用户的 session 与 refresh token，或直接禁用账户。**
2. 再写撤销条目杀存量 access token（可用无视 `iat` 的 sub 全拒条目兜底）。
3. 顺序不能颠倒：只写名单不动 Casdoor，前端会在数秒内用仍然有效的 refresh token 换出新 access token，绕过封禁。

### 场景三：单个 token 泄露

1. 写 `jti` 级撤销条目（`jti` 的存在性以真实 token 实测为准；不存在则退化为场景一的 `{sub, issued_before}`）。

## 撤销名单通道的可用性边界

- 名单新鲜度由 Watch 心跳驱动，暴露为指标；断连告警。
- 断连超过「TTL + leeway」后，仅 admin 与 `online_check` 路由升级为实时在线校验（fail-close）；普通流量继续使用 last-known-good 名单。不做全局 fail-close，原因见 decisions.md。

## 高危路由在线校验（`online_check`）

- 路由表 `auth.online_check_procedures` 列出的 procedure 在通过本地校验后，再实时调用 Casdoor 校验一次。
- 前置条件（P3 验证）：确认 Casdoor 可用的 introspection API 与 client 凭据模式；凭据经 Kubernetes Secret 注入。
- 约束：超时 ≤2 秒、禁止重试、熔断计数与告警；错误一律拒绝（fail-close）。
- 已知限制：Casdoor 收编进集群完成之前，此类路由的可用性受公网链路影响。

## 前端配套（P4）

- Casdoor access token TTL 调短前，merchant/admin 应用需接入与 consumer 相同的会话续期 Provider；桌面端是第四个客户端，一并实测。
- 验收指标：四端静默续期通过、`TOKEN_EXPIRED` 类 401 率无异常、撤权三场景演练秒级生效。
