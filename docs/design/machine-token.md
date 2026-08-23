# 数据面鉴权：per-service machine token

## 背景与目标

现状是一把全局共享 token（`CONFIG_CENTER_SERVICE_TOKEN` 环境变量，恒时比较），任何持有者可读全部 namespace×environment 的配置。盘点已标记为短板；终裁 §三-2 采纳升级方案。

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

1. **legacy 共享 token**：与 `CONFIG_CENTER_SERVICE_TOKEN` 恒时比较命中 → 按旧语义放行（任意 namespace 只读），记 WARN 日志与 `machine_token_legacy_hits` 指标——「双栈仍开」告警数据源；
2. **per-service token**：SHA-256 查表命中且未吊销 → 主体=(service, environment, namespaces)；强制校验：请求的 `environment` 与 token 相等、`namespace` ∈ 白名单；
3. 双双未命中 → 401。

作用域不变：machine token 只允许 `GetKey`/`WatchKeys`。

**吊销断流**：`WatchKeys` 服务端在每次心跳 tick（沿现有心跳周期）复验 token 状态；吊销后主动结束流。SDK 现有重连逻辑会带着新 Secret 重建流（若已轮换）或收到 401 快速失败。

**共享 token 关闭死线**：10 服务 + gateway + config-seed 全部换发 per-service token 并稳定运行后（P6），移除环境变量并删除 legacy 分支；期间 `machine_token_legacy_hits` 持续非零即为未完成信号。

## 兼容性说明

- SDK 与请求头名不变——旧 SDK v0.1.0 与新 SDK 都按原样工作（wire 冻结）。
- 新增 RPC/字段全部为 additive，`buf breaking --against` 旧仓必须保持通过。
