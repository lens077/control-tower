# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

个人或小团队的运维/后端工程师，自己既是配置的作者也是消费者。两个真实场景（用户 2026-09 确认）：

1. **日常改配置**：找到某个 key（网关路由、服务参数），改值、保存，确认它已经下发。这是高频动作。
2. **发布前核对与回滚**：看版本历史、对比 diff、必要时回滚到某一版。

排障（看 SDK 客户端连接、config 服务自身指标）与签发 Machine Token 是低频的辅助任务，仍在产品内，但不是主路径。

## Product Purpose

control-tower 的配置中心控制台（`web/`）：管理网关与微服务在各命名空间 × 环境下的配置项，
每个 key 有格式（YAML/TOML/JSON/PlainText）、版本历史、密钥标记；保存即下发给在跑的服务
（WatchKeys 长流）。成功的标准是：工程师能在几秒内定位到一个 key、看清它属于哪个环境、
放心地改并知道改动已经生效；发布前能清楚看到「这一版改了什么」并一键回滚。

## Positioning

网关（gateway）与配置中心（config）合一的平台仓，控制台直接面对同一份真相源：
保存即通过 WatchKeys 推给消费方，历史不可删、回滚以「写成新版本」实现。
邻近产品（通用 KV 后台）没有「路由模板 + 网关消费 + 客户端在线订阅可见」这条链路。

## Operating Context

- 后端：`services/config`（Connect RPC，`web/src/gen` 为生成的 TS 客户端）。控制台部署在
  `config.apikv.com`，接口在 `config-api.apikv.com`。
- 登录：Casdoor OIDC + PKCE，静默续期走隐藏 iframe；未登录时页面提示「请先登录以管理配置」。
- 数据模型：namespace → environment（dev/pre/prod/uat 等）→ key（层级路径，如 `gateway/routes.yaml`）
  → 版本（v1, v2…）。namespace 列表带每个 ns 的环境集合与 key 数。
- 编辑器：Monaco（同源自托管 `/vs`），编辑页与历史 diff 都依赖它；格式校验本地先跑一遍。
- 密钥（isSecret）的值在读取与历史里都脱敏为 `******`。
- 环境常量：`dev`、`pre`、`prod`、`uat`；线上公网网关当前消费的是 dev 键（见 AGENTS.md）。
- 巡检：`e2e/` Playwright 用例每 6 小时打真实环境，锚点是按钮文案（登出/保存/删除/历史/
  新建 Key/创建并编辑/回滚到 vN/确认回滚/签发 Token/签发/吊销/确认吊销/关闭/确认关闭）、
  表单 label（命名空间/环境/服务名/允许的命名空间/备注/Key…）、以及 MUI 类名
  （`.MuiChip-label` 的版本号 `vN|新建`、`.MuiCard-root` 包裹 token/连接条目、
  `.MuiAlert-standardError`）、`role=listitem` 的历史版本项、`.monaco-editor`/`.view-line`。

## Capabilities and Constraints

- 页面：Key 浏览（/）、编辑（/edit?ns&env&key）、历史与回滚（/history?ns&env&key）、
  客户端连接（/connections）、Machine Token（/tokens）、系统信息（/system）、OAuth 回调（/callback）。
- 路由 URL 与 search 参数是 e2e 与深链的契约，保持不变；浏览页的 ns/env 存在 valtio store
  并持久化到 localStorage。
- 保存前置条件：内容通过所选格式的解析；服务端 InvalidArgument 标注到编辑器。
- 删除 key 当前没有二次确认（e2e 依赖此行为）。
- 中英双语（i18next，`web/src/locales/{zh-CN,en}/config.json`），MUI 语言包随之切换。
- 组件库：MUI v9 + Emotion；图标 lucide-react；图表 @mui/x-charts。视觉可以整套换，
  但组件库保留以维持 e2e 的类名锚点。
- CSP：`script-src 'self'`，不能引外链字体/脚本；字体走 `@fontsource` 自托管。
- 用户 2026-09 确认的现状痛点（本次重做要解决的）：
  找 key 要反复切 ns/env、列表平铺；编辑页元信息/按钮/编辑器挤在一起、编辑器矮；
  编辑↔历史↔列表各自独立跳转丢上下文；看不出当前环境/线上/未保存状态；
  玻璃态大圆角卡片信息密度低。

## Brand Commitments

名称「配置中心 · Config Center」。无既定品牌色或 logo；用户明确表示视觉可整套重做。
中文文案遵循 tech-doc-style-chinese（直角引号「」）。

## Evidence on Hand

- 真实数据来自线上 config 服务（需要 Casdoor 账号）；仓内没有截图或设计稿。
- 路由模板 `routes/{dev,pre}.yaml` 是真实 key 内容的样例。
- 没有用户访谈以外的可用性数据；不要编造使用统计。

## Product Principles

1. 定位优先：ns × env × key 的三级结构必须一眼可见、一步可达，不靠记忆和反复切换。
2. 环境是一等状态：在哪个环境、这一版是不是线上、有没有未保存改动，随时可读。
3. 编辑器是主角：编辑页把面积让给内容，元信息与操作退到边缘但不消失。
4. 上下文不丢：列表、编辑、历史在同一个壳里切换，URL 契约不变。
5. 密度服务扫读：工具型界面，信息密度高于展示型，但每一行都有清晰的层级。

## Accessibility & Inclusion

键盘可操作（表单、列表、对话框沿用 MUI 的可访问性）；颜色不作为唯一状态标识（环境/密钥/错误
都配文字或图标）。未确立更具体的标准。
