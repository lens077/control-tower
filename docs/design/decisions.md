# 设计决策：砍掉与不做清单

本文记录 control-tower 重写时明确「不迁移」「不实现」或「延后」的能力。每项都写明原因与重新引入的触发条件——规则可以从代码读出来，理由不能。

背景：control-tower 由 ecommerce 旧网关（go-kratos/gateway fork）与 config-center 合并重写而来。迁移决策与对抗评审结论存档于工作区 `.migration-scratch/`（06 决策日志、11 终裁书、12 方案 v2）。

## 网关侧

| 能力 | 处置 | 原因 | 重新引入触发条件 |
|---|---|---|---|
| Connect→gRPC 手工转码层 | 砍除 | 后端 10 服务原生同时支持 Connect/gRPC/gRPC-Web，转码是历史包袱；新网关端到端直通 | 无 |
| BBR 自适应限流 | 不做 | 瘦身边界（决策 Q11-2a）；Cilium 侧限流需手写 CiliumEnvoyConfig，同样不免费 | k6 压测证明网关或后端过载；先做全局令牌桶再评估 |
| 熔断 | 砍除 | 旧网关 YAML 未配置 options，实际是 no-op；假开关比没有开关更危险 | 后端出现持续性雪崩案例 |
| HTTP/3/QUIC 与 Alt-Svc | 砍除 | 边缘在 Cilium Gateway，网关是明文内网跳；QUIC 复杂度与收益不成比 | 边缘架构变更且有实测收益 |
| 网关自持 TLS | 砍除 | Cilium Gateway 已终止 TLS；网关只听 HTTP/1.1 + h2c | 脱离 Cilium 的部署形态 |
| rewrite/stripPrefix 中间件 | 砍除 | 旧配置全部注释未启用；按一级 proto 包名路由后无路径改写需求 | 出现真实路径改写需求 |
| routerfilter/ctrl-loader/hosts/examples/benchmark | 砍除 | 死代码；routerfilter 存在包级可变状态的并发缺陷 | 无 |
| priority 本地配置覆盖目录 | 砍除 | 与「Config Center 单一配置来源」纪律冲突 | 无（环境差异用 Config Center 的 environment 维度表达） |
| WebSocket 代理 | 不支持 | 旧实现本就不可靠；Connect streaming 已覆盖流式需求 | 出现非 Connect 的 WS 场景 |
| 灰度发布 | 不做 | 旧网关未成型（仅 Consul weight 元数据）；瘦身边界 | 多副本金丝雀需求成立后单独设计 |
| 路由级重试 | 默认关闭 | Connect RPC 全部是 POST，旧网关两层重试相乘导致非幂等写重放（历史事故） | 幂等键（requestId）落地后按路由显式开启 |
| Redis 角色缓存 + Casdoor get-user 回源 | 删除 | 角色进 JWT claims + 撤销名单（见 auth.md），撤权时效从 5 分钟缓存窗降到秒级推送 | 若 Casdoor 无法可靠嵌入角色 claims，按 auth.md 回退分支恢复 get-user+缓存 |
| `/config*` 网关路由 | 删除 | config web/api 走独立域名直连（决策 Q14a）；经网关转发会产生双重鉴权，且 config 服务刻意不信任网关转发的身份头 | 出现统一入口需求并完成信任转发设计 |
| Kratos 框架 | 不迁 | 技术栈整体替换为 connect-go + fx | 无 |

## 鉴权侧

| 能力 | 处置 | 原因 | 重新引入触发条件 |
|---|---|---|---|
| Casdoor webhook 自动写撤销名单 | 延后 | 人工写名单的延迟已被短 TTL 封顶；先验证人工流程 | 撤权操作频率上升，或出现人为遗漏事故 |
| 撤销名单通道全局 fail-close | 拒绝 | 暴露窗被 access token TTL 硬性封顶；全局 fail-close 会把 config 服务可用性耦合进全站鉴权热路径，违背 last-known-good 哲学 | 无（断连超过 TTL+leeway 后仅 admin/高危路由定向升级在线校验） |
| OpenFGA 进网关 | 禁止 | 技术栈对抗裁决：OpenFGA 只做服务内资源关系授权，禁止进网关热路径 | 无 |

## config 侧

| 能力 | 处置 | 原因 | 重新引入触发条件 |
|---|---|---|---|
| config proto 大整形（v2 major） | 延后 | wire 冻结保回滚（终裁 P0-B）：滚动窗口与回滚场景内旧 SDK v0.1.0 仍在线上，存量包名/Service/RPC 名/字段号/类型不可动 | 全部消费方离开 SDK v0.1.0，且出现真实 schema 痛点 |
| 审批流/配置灰度/审计/静态加密 | 不搭车 | 属产品路线图能力，已在 config-center TODO 单独排期；不该搭迁移的车 | 按各自路线图推进 |

## 运行细节约定（防回退）

- 网关与 config 服务的 `http.Server` 不设 `WriteTimeout`：它会掐断 `WatchKeys` 长流（config-center 历史事故）。超时统一走路由级 context。
- P2C 负载均衡保留 Consul 实例 `weight` 元数据语义。
- 出现 streaming 路由时豁免路由级总超时（当前后端 API 实测零 streaming RPC）。
