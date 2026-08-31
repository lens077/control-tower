# 数据面鉴权：per-service machine token

## 背景与目标

初始状态是一把全局共享 token（`CONFIG_CENTER_SERVICE_TOKEN` 环境变量，恒时比较），任何持有者都能读取全部 namespace×environment 配置。2026-08-31 已将 10 个业务服务和 gateway 的 dev/pre selector 迁移到 per-service token；服务端暂时保留 legacy 回退，用于 7 天零命中烘烤和应急回滚。

目标：

- 凭据维度收窄到 **service × environment**；
- 读取范围收窄到 **namespace 白名单**（默认仅自身 namespace；gateway 白名单含 `gateway`）；
- 支持**两代重叠轮换**（同一 service×environment 可同时存在多枚有效 token）；
- **吊销生效于流**：已建立的 `WatchKeys` 长流在心跳周期内复验 token，吊销后断开；
- 签发/吊销可审计；token 明文仅在签发响应出现一次，服务端只存哈希。

## 数据模型

新表 `config.machine_token`（goose 迁移 0002）：

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | uuid PK | 主键 |
| `service_name` | text NOT NULL | 消费方服务名（如 `order`、`gateway`） |
| `environment` | text NOT NULL | `dev`/`pre` 等；请求的 environment 必须与之相等 |
| `token_hash` | bytea NOT NULL UNIQUE | SHA-256(token 明文) |
| `allowed_namespaces` | text[] NOT NULL | 可读 namespace 白名单 |
| `note` | text NOT NULL DEFAULT '' | 用途备注（轮换审计用） |
| `disabled` | boolean NOT NULL DEFAULT false | 吊销标记（保留行即保留审计） |
| `created_at` | timestamptz NOT NULL | 签发时刻 |
| `revoked_at` | timestamptz | 吊销时刻 |
| `last_used_at` | timestamptz | 最近认证成功时刻（观测用，低频更新） |

索引：`token_hash` 唯一；`(service_name, environment)` 普通索引。

token 明文格式：`ct_` + 32 字节随机数的 base64url（≥43 字符）。服务端不落明文、不入日志。

## 管理面 RPC（新增，wire 冻结允许的加法）

全部仅限管理员 JWT（复用现有 IAM 中间件）；machine token 不能调用。

- `ListMachineTokens`：按 service/environment 过滤，返回元数据（不含哈希）。
- `IssueMachineToken`：签发新 token，明文只在本响应返回一次。
- `RevokeMachineToken`：按 id 吊销（置 `disabled` + `revoked_at`）。

轮换手顺（文档化，不设单独 RPC）：Issue 新 token → 更新目标服务的 selector Secret → 滚动重启 → Revoke 旧 token。两代重叠窗口内新旧同时有效。

## 数据面校验（双栈过渡）

`x-config-center-service-token` 头的校验顺序：

1. **legacy 共享 token**：与 `CONFIG_CENTER_SERVICE_TOKEN` 恒时比较命中 → 按旧语义放行（任意 namespace 只读），记 WARN 日志与 `machine_token_legacy_hits` 可观测量表。量表从进程启动起累计，并始终上报 `0`，避免把「零命中」与「指标未接线」混为一谈；
2. **per-service token**：SHA-256 查表命中且未吊销 → 主体=(service, environment, namespaces)；强制校验：请求的 `environment` 与 token 相等、`namespace` ∈ 白名单；
3. 双双未命中 → 401。

作用域不变：machine token 只允许 `GetKey`/`WatchKeys`。

**吊销断流**：`WatchKeys` 服务端在每次心跳 tick（沿现有心跳周期）复验 token 状态；吊销后主动结束流。SDK 现有重连逻辑会带着新 Secret 重建流（若已轮换）或收到 401 快速失败。

**共享 token 关闭死线**：10 个业务服务与 gateway 全部换发 per-service token 后，`machine_token_legacy_hits` 必须连续 7 天为零。满足该条件后，移除环境变量、K8s Secret 字段和 legacy 分支。烘烤期内出现任意非零值时，从最后一次命中重新计算 7 天窗口。

**当前烘烤窗口**：VictoriaMetrics 于 `2026-08-31T16:05:03Z` 首次确认当前值与
`max_over_time(machine_token_legacy_hits[7d])` 均为 `0`；最早删除时间为
`2026-09-07T16:05:03Z`。每 6 小时 e2e 同时断言「时序存在」「当前值为零」「7 天窗口为零」；
空结果或任意非零值都会让巡检失败并重置退役窗口。

## 兼容性说明

- SDK 与请求头名不变——旧 SDK v0.1.0 与新 SDK 都按原样工作（wire 冻结）。
- 新增 RPC/字段全部为 additive，`buf breaking --against` 旧仓必须保持通过。
