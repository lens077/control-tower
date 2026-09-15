# 数据面鉴权：per-service machine token

## 背景与目标

初始状态是一把全局共享 token（`CONFIG_CENTER_SERVICE_TOKEN` 环境变量，恒时比较），任何持有者都能读取全部 namespace×environment 配置。2026-08-31 已将 10 个业务服务和 gateway 的 dev/pre selector 迁移到 per-service token；服务端曾保留 legacy 回退用于 7 天零命中烘烤，**2026-09 关闭死线后已移除**（见文末「共享 token 退役记录」）。

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
| `role` | text NOT NULL DEFAULT 'service' | `service`（数据面只读）或 `operator`（管理面服务账号，迁移 0003） |

索引：`token_hash` 唯一；`(service_name, environment)` 普通索引。

token 明文格式：`ct_` + 32 字节随机数的 base64url（≥43 字符）。服务端不落明文、不入日志。

## 管理面 RPC（新增，wire 冻结允许的加法）

全部仅限管理员 JWT（复用现有 IAM 中间件）；machine token 不能调用。

- `ListMachineTokens`：按 service/environment 过滤，返回元数据（不含哈希）。
- `IssueMachineToken`：签发新 token，明文只在本响应返回一次。
- `RevokeMachineToken`：按 id 吊销（置 `disabled` + `revoked_at`）。

轮换手顺（文档化，不设单独 RPC）：Issue 新 token → 更新目标服务的 selector Secret → 滚动重启 → Revoke 旧 token。两代重叠窗口内新旧同时有效。

## 当前生命周期限制与后续改进

per-service machine token 是高熵随机 API Key，不是 JWT。当前数据模型没有 `expires_at`，校验过程也不处理有效期；token 签发后会一直有效，直到管理员调用 `RevokeMachineToken` 主动吊销。`created_at` 和 `last_used_at` 仅用于审计与观测，不会触发自动失效。

当前系统支持两代重叠轮换，但轮换完全依赖人工执行。仓库内没有自动轮换控制器、CronJob、固定轮换周期、到期告警或 Secret 自动刷新机制。因此，「支持轮换」不等于「已经定期轮换」。长期不轮换会使泄漏凭据的有效窗口持续到人工发现并吊销。

后续需要完成以下改进，具体周期和实施阶段在方案评审时确定，不在本文预设：

- 为 token 增加明确的生命周期策略，并评估是否在数据模型中加入 `expires_at`；
- 增加临近到期、长期未轮换和异常使用的指标与告警；
- 自动化「签发新 token → 更新目标 Secret → 滚动验证 → 吊销旧 token」流程，同时保留两代重叠和失败回滚；
- 使用 `last_used_at` 识别长期未使用的凭据，并建立审计与清理流程；
- 在自动化能力落地前，将定期轮换纳入人工运维清单；发生 Secret 泄漏、工作负载失陷或人员权限变更时立即轮换并吊销旧 token。

在引入到期机制时，必须同时处理 `WatchKeys` 长流：服务端应在心跳复验中检查到期状态，并确保新 Secret 已生效后再吊销或淘汰旧 token，避免配置读取中断。

## 数据面校验

`x-config-center-service-token` 头只有一条路径：

1. **per-service token**：SHA-256 查表命中且未吊销 → 主体=(service, environment, namespaces)；强制校验：请求的 `environment` 与 token 相等、`namespace` ∈ 白名单；
2. 未命中 → 401。

共享 token 分支（`CONFIG_CENTER_SERVICE_TOKEN` 恒时比较、`machine_token_legacy_hits` 量表）已在关闭死线后删除。

作用域：`role=service` 的 machine token 只允许 `GetKey`/`WatchKeys`；`role=operator` 的白名单见下节。

**吊销断流**：`WatchKeys` 服务端在每次心跳 tick（沿现有心跳周期）复验 token 状态；吊销后主动结束流。SDK 现有重连逻辑会带着新 Secret 重建流（若已轮换）或收到 401 快速失败。

**共享 token 退役记录**（原「关闭死线」条款）：规则是 `machine_token_legacy_hits` 连续 7 天为零后移除环境变量、Secret 字段与 legacy 分支；烘烤期内任意非零值从最后一次命中重算 7 天。2026-08-31T16:05Z 起窗口清零；2026-09-12 02:35–03:55Z 出现 12 次命中，全部是 `client=cart-service` 从开发机 `make dev` 发出——本地 gitignored selector 里塞的是共享 token（dev 环境当时没有 per-service selector，8-31 签的 dev token 明文已失）。处理：为 dev 建 `ecommerce-config-source-dev` 并签 10 枚 per-service token，开发机切换，8-31 的孤儿 token 吊销；重启 config 服务归零量表（量表从进程启动累计，不重启永不归零）。集群内所有 Secret 核对后无任何消费者再持有共享 token，按规则最后一次命中 + 7 天 = **2026-09-19T03:55Z** 可移除，本次改动即该移除（分支 `chore/retire-legacy-shared-token`，到期后合并发版）。移除后 e2e 的零命中巡检用例同时删除——指标不存在了，「时序存在」断言没有意义。

## operator 角色（管理面服务账号，2026-09-11）

**目的**：自动化（例如把各服务 `bootstrap.yaml` 写回配置中心、为其签发 service token 的 harvest 脚本）不该依赖人类的 Casdoor 浏览器会话拿管理员 JWT。`role=operator` 的 machine token 是一个非交互、最小权限的管理面主体，走同一个 `x-config-center-service-token` 头。

**作用域**：

- 与 service token 一样按 **environment** 收窄：请求的 `environment` 必须与 token 相等，跨环境一律 403/PermissionDenied。
- `allowed_namespaces` 支持通配 `*`（任意 namespace）；签发 operator 时留空默认为 `["*"]`，显式给白名单则按白名单。通配只放宽 namespace，不放宽 environment。
- procedure 白名单（iam `operatorProcedure`）：`GetKey`、`WatchKeys`、`ListKeys`、`ListNamespaces`、`PutKey`、`ListRevisions`、`GetRevision`、`ListMachineTokens`、`IssueMachineToken`、`RevokeMachineToken`。
- `PutKey` 在 service 层再过一次 `machineScopeGuard`：写范围与读范围同一条规则。
- 主体名为 `operator:<service_name>`，会落到 `updated_by`/revision `author` 与审计日志。

**不能做的事**：

- 不能签发 `role=operator` 的 token（`operator token cannot issue operator tokens`），不能吊销 operator token（含自身）——防止自我复制与横向清场；这两条都只有管理员 JWT 能做。
- 签发与吊销 service token 也只限**自身 environment**（`operator token can only issue tokens for its own environment` / `... revoke tokens in its own environment`）：一枚 pre 的 operator 碰不到 dev 的凭据。
- `DeleteKey`、`Rollback`、`ListClientConnections` 不在白名单，仍仅限管理员 JWT。
- 不能读取任何 token 哈希/明文；`ListMachineTokens` 只回元数据（含 `role`）。

**签发**：管理员 JWT 调 `IssueMachineToken`，`role = MACHINE_TOKEN_ROLE_OPERATOR`（proto 新增枚举，`UNSPECIFIED` 等价于 `SERVICE`）；Web 控制台的 `/tokens` 页面走的是同一个 RPC。明文同样只在响应出现一次。

**存放**：只进 K8s Secret（自动化的 Job/CronJob 以 env 或挂载读取）与本地环境变量；不进文件、不进仓库、不进日志。轮换与吊销手顺与 service token 相同（两代重叠：签新 → 换 Secret → 吊旧）。

**滚动前置**：goose 迁移 `00003_machine_token_role.sql`（`ALTER TABLE config.machine_token ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'service'`）随进程启动自动应用；存量 token 自动落为 `service`，行为不变。旧镜像不读该列，可安全回退。

## 兼容性说明

- SDK 与请求头名不变——旧 SDK v0.1.0 与新 SDK 都按原样工作（wire 冻结）。
- 新增 RPC/字段全部为 additive，`buf breaking --against` 旧仓必须保持通过。
- `MachineTokenRole` 枚举、`IssueMachineTokenRequest.role = 5`、`MachineTokenMeta.role = 10` 同为 additive；旧客户端不填 `role` 即签发 service token。
