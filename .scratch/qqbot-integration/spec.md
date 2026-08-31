# control-tower 接入 QQ 机器人开放平台

Status: needs-triage

> 勘察与撰写：2026-09-01，AgentTeams `qqbot-integration` / project-scout。
> 平台事实来自队长核实的 <https://bot.q.qq.com/wiki/> 结论，本文不重复联网核对。
>
> **范围声明**：本文只谈 control-tower 作为「网关 + 配置中心」**这个产品自身**能从 QQ 机器人
> 得到什么。ecommerce 侧的 qqbot 服务如何经本网关接线，由队友 wiring-architect 负责，本文不涉及。
>
> 本仓此前没有 `.scratch/` 目录，也没有 `docs/agents/issue-tracker.md`。本文沿用同级仓
> ecommerce 的约定（`.scratch/<feature-slug>/spec.md` + 顶部 `Status:` 行）。
> 本仓 `.gitignore` 未忽略 `.scratch/`，因此本文件会入库。

## 1. 这个项目是什么

网关与配置中心合一的平台仓：单 Go module，两个独立部署的服务。

| 结论 | 依据文件 |
|---|---|
| `services/gateway`：Connect 原生反代、JWT/Casbin 鉴权、混合撤权 | `README.md` 服务表 |
| `services/config`：键值 + 版本历史 + WatchKeys 热推送 + machine token + 管理台 | 同上 |
| 由 ecommerce 旧网关（go-kratos/gateway fork）与 config-center 合并重写而来 | `README.md`；`AGENTS.md` 末段 |
| 两个服务均已切流上线，公网入口 `config.apikv.com` / `gateway.apikv.com` 已恢复 | `AGENTS.md`「部署现状（2026-08-31 逐资源核对 + e2e 实测）」 |
| 数据面已搬离集群：PG/Redis 在 node3，指标查询走 node3 VictoriaMetrics | `AGENTS.md` 数据面表格 |
| 变更 RPC 面：`PutKey` / `DeleteKey` / `Rollback` / `IssueMachineToken` / `RevokeMachineToken` | `api/config/v1/config.proto:27,29,35,44,45` |
| 配置改动经 `WatchKeys` 流式**热推送**给各服务，无灰度、无审批 | `api/config/v1/config.proto:38`；`README.md` |
| 现有告警出口是 **ntfy**，且与 Gatus/Healthchecks/Alertmanager **共用同一个私有 topic** | `.github/workflows/e2e.yml:148` 注释 |
| e2e 只在「由红转绿」时发一次恢复通知，理由是「告警的价值 = 新信息量」 | `.github/workflows/e2e.yml:116-117` |

## 2. QQ 机器人对它有没有价值

**结论：有限价值，且当前不建议在本仓实现。** 拆成三问：

### 2.1 需要「把告警发到手机」吗？需要，但这件事已经有人做了

`e2e.yml:148` 的注释写得很清楚：失败通知进 ntfy，**与 Gatus / Healthchecks / Alertmanager
同一个私有 topic 与 bearer token**。也就是说 control-tower 并不拥有自己的告警链路，
它只是一条共用运维通知总线上的若干生产者之一。

由此得到本文最重要的判断：

> 想让告警落到 QQ，正确的做法是在**那条共用总线上加一个 QQ 消费者**（ntfy 订阅端转发，
> 或 Alertmanager 加一个 webhook receiver），**而不是**在 control-tower 仓里写一份 QQ 客户端。

前者改一处、所有生产者受益；后者会开一个坏头——每个仓各自接一遍 QQ，凭据散落多处，
频控计数各算各的，最后必然超配额且无人能说清是谁发的。

### 2.2 有没有「非通知不可、且只有 control-tower 知道」的事件？有，两个

这两个是真正属于本仓、别处拿不到的信号：

1. **`machine_token_legacy_hits` 非零命中。**
   `AGENTS.md` 记着零命中窗口从 `2026-08-31T16:05:03Z` 起算、最早 `2026-09-07T16:05:03Z`
   删除 legacy 回退，且**任何非零命中都会重置窗口**。这是个典型的「稀有但必须立刻知道」
   事件：一次命中就让一周的烘烤白等。人不会去盯 7 天。
2. **配置热推送后的异常。** `PutKey` / `DeleteKey` 经 `WatchKeys` 立即生效，
   而 `docs/design/decisions.md` 明确把「审批流/配置灰度/审计/静态加密」列为**不搭车**、
   由 config-center 自己的路线图单独排期。也就是说改错一个键，没有任何门能拦，
   只有 `Rollback`（`config.proto:35`）能救。**告警 + 一键回滚**在这里是有真实价值的。

### 2.3 那 QQ 相对 ntfy 强在哪？强在「回执」，但被本仓的现状卡住

QQ 的 `keyboard` 内嵌按钮 + `INTERACTION` intent 能做到「在手机上点一下就回滚」，
ntfy 也能做（http action），但 QQ 的按钮数量和排版不受 ntfy 那 3 个按钮的限制。

问题在于：**让运维动作可以从手机一键触发，本质上是在给生产配置面开一条新的写入通路。**
本仓对写入通路一向保守——`decisions.md` 里 `Casdoor webhook 自动写撤销名单` 都还是「延后，
先验证人工流程」。在审批流尚未落地（同表「不搭车」）的前提下，先加一条手机侧的一键写入，
顺序是反的。

## 3. 若要做：场景、intents 与消息类型

按价值排序，**只有场景 A 建议现在考虑**：

| # | 场景 | 消息形态 | 主动/被动 | intent |
|---|---|---|---|---|
| **A** | legacy token 命中 / e2e 失败 / 公网入口探活失败 —— **只读告警** | msg_type 0 文本 | **主动** | 不需要上行 intent |
| B | 配置变更播报（谁改了哪个键、rev N→N+1） | msg_type 2 Markdown（需申请模板权限） | **主动** | 同上 |
| C | 告警带「回滚到 rev N」按钮 → 调 `Rollback` | keyboard + msg_type 0 | 主动下行 + 被动回执 | `INTERACTION`（1<<26） |
| D | 手机查状态（`/status` → `GetSystemStatus`） | 文本 | **被动回复** | `GROUP_AND_C2C_EVENT`（1<<25） |

场景 C、D 把生产写入/读取通路接到 IM 上，在审批流落地前不建议做（见 §2.3）。

**频控评估**：这些事件天然低频（e2e 每 6 小时一次、配置变更每天个位数），
主动消息的 20/qpm、每日 1000 条上限完全不构成约束。唯一要防的是**告警风暴**——
后端 10 服务同时异常时逐条推送会瞬间打满 20/qpm，必须在发送侧做聚合。
这一点与 `e2e.yml:116` 已经确立的「只发由红转绿那一次」是同一种克制，方向一致。

## 4. 接入代价评估

| 维度 | 结论 |
|---|---|
| **技术栈匹配** | ✅ **四个项目里最好的**。官方 SDK `tencent-connect/botgo` 是 Go，本仓是单 Go module（`go.mod`）。 |
| **公网出口** | ⚠️ 平台对新机器人默认启用 IP 白名单，只有白名单公网 IP 能连 WS / 调 OpenAPI（仅正式环境）。<br>**未确认**：集群的**出口** IP。`AGENTS.md` 只给了**入口** IP `114.132.233.129`（PG/Redis 证书 SAN 补签记录），入口 ≠ 出口，不能拿来当白名单值填。需人工核实 SNAT 出口。 |
| **Webhook 备选** | 本仓已有成熟公网入口（三条 HTTPRoute，`Accepted=True`），443 在允许的 80/443/8080/8443 内，接 op=13 验证与 ed25519 验签不难。但**发消息仍需调 OpenAPI**，出口 IP 问题绕不开。 |
| **备案域名** | 仅当消息带链接（如指向 `config.apikv.com` 的 diff 页）时需要报备 + ICP 备案，上限 20 条。<br>**未确认**：`apikv.com` 备案状态——四个仓 grep `备案\|ICP` 零命中，仓库内无证据。 |
| **凭据** | AppID/AppSecret 属凭据，按 `AGENTS.md` 硬约束 4「凭据不进仓库」，只能进 Config Center / K8s Secret。这条本身没有障碍——本仓已有成熟的 Secret 惯例。 |
| **代码现状** | ⚠️ `services/config/internal/{biz,service}` 目前**没有任何出站 HTTP**（grep `net/http\|http.Client\|http.Post` 零命中）。加 QQ 推送等于给一个纯粹的读写服务新开一条外网出站依赖，架构上是新增了一类故障源。 |

最后一行是本文对「在本仓实现」持保留意见的技术理由：config 服务当前的外部依赖面很干净，
不该为了发通知而把它接上公网 IM。

## 5. 优先级建议与最小验证路径

**优先级：P3（低）。建议先做的不是接入，而是一次定位决策。**

### 建议的处置顺序

1. **先在共用告警总线上加 QQ 消费者**（不在本仓，改动量最小、收益覆盖所有服务）。
   `e2e.yml:148` 指明该总线由 Gatus/Healthchecks/Alertmanager 共用，
   接一个 ntfy→QQ 的转发器即可让 control-tower 的告警落到 QQ，**本仓零改动**。
2. **把 `machine_token_legacy_hits` 非零命中做成一条告警规则**（本仓有价值的部分）。
   这条与 QQ 无关——它现在缺的是「有没有人在盯」，不是「用哪个 IM」。
   指标链路已经修通（`AGENTS.md` 记 2026-08-29 修过三处，验收用
   `CONFIG_CENTER_VM_ENDPOINT=http://metrics.apikv.com go test ./services/config/internal/pkg/promql -run Live -v`）。
3. **等审批流落地后**，再评估场景 C 的「手机一键回滚」。
   `decisions.md` 已把审批流排给 config-center 自己的路线图，届时 QQ 按钮是天然的审批载体。

### 若仍决定在本仓做，最小验证路径

1. 人工核实两条 **未确认** 事实：集群 SNAT 出口 IP、`apikv.com` ICP 备案状态。
   **这两条不落实，后面都是空谈**——出口 IP 拿不到就连不上正式环境 OpenAPI。
2. 沙箱环境（不受 IP 白名单约束，群/频道 ≤20 人）用 `botgo` 发一条文本消息，
   验证 Go 侧接入成本。
3. 只做场景 A（只读告警，无 keyboard、无上行 intent），跑满一个 e2e 巡检周期（6 小时 × N），
   确认告警聚合逻辑不会在后端批量异常时打满 20/qpm。
4. 复核一遍 `decisions.md`——按 `AGENTS.md` 必读表，「改动前先查这里，别把砍掉的东西加回来」。
   本文没有与该文件冲突的提议，但新增出站依赖应当在那张表里补一行「重新引入触发条件」。

### 需要提醒的一条纪律

`AGENTS.md` 硬约束 3：改 `routes/{dev,pre}.yaml` 必须同 PR 升级 ecommerce 仓对本 module
的依赖版本。本文的任何方案**都不涉及路由模板变更**（告警是出站行为，不占路由），
因此不触发这条。若队友的 ecommerce 侧 qqbot 服务需要经网关暴露，那属于路由变更，
纪律适用——但那是 wiring-architect 的范围，不在本文内。
