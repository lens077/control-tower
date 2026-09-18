---
version: 1
slug: "web-src-routes-root-tsx"
primary_target: "web/src/routes/__root.tsx"
related_targets: ["web/src/routes/index.tsx","web/src/routes/edit.tsx","web/src/routes/history.tsx","web/src/routes/connections.tsx","web/src/routes/tokens.tsx","web/src/routes/system.tsx"]
---

# 配置中心控制台（web/）— 重做

Scope: 整个控制台壳与六个页面（/ /edit /history /connections /tokens /system）。Visitor mode: Operate。

Audience: 个人/小团队运维工程师。Job: 几秒内定位 ns×env×key，改值保存并确认下发；发布前看 diff 与回滚。
Constraints: 功能与接口不变；MUI 保留（e2e 类名锚点 .MuiChip-label / .MuiCard-root / .MuiAlert-standardError）；按钮与表单文案不变；URL 契约不变；CSP script-src 'self'（字体自托管）；中英双语。

Chosen direction: 用户在决策页选择了「虹彩云边」（seed 12c96b9f 的 challenger clouds-storms-auroras-iridescent-cloud-edge）。
Memorable moment: 顶栏下沿与当前环境的发丝虹彩色带；正文完全无彩，颜色只出现在边缘。

Unresolved: 「线上」环境无法在产品层判定（公网网关目前消费 dev 键），界面只标环境名与颜色，不声称哪个是线上。

## Direction contract

THESIS: 配置中心是一张云白的工作台，颜色只住在边缘——环境、选中、错误都以发丝色带表达；它拒绝品类默认的「侧边导航 + 白底表格 + 蓝色主按钮」。
OWN-WORLD: 云白 #FFFFFF / 雾 #F2F4F7 地面，板岩 #5A6470 与 #1F2630 墨；薄荷 / 玫瑰 / 紫罗兰 / 琥珀 / 天蓝只作 1–2px 色带与虹彩细线；细人文无衬线 Source Sans 3（300–500）+ PingFang，JetBrains Mono 承担 key 与值；控件是白底发丝框、紫罗兰为焦点/选中带。
STORY: 打开就看见 ns → env → key 的三级位置与自己在哪个环境；改动未保存时紫点常亮，保存后变绿并回到静默；历史在同一壳内看 diff 并回滚。
FIRST VIEWPORT: 44px 顶栏（品牌、四个文字导航、语言、登出），下沿一条虹彩发丝线；左 288px 资源栏（命名空间 / 环境组合框 + 搜索 + 按路径分组的 28px 行 key 列表，选中行左沿紫罗兰带）；中央编辑器占满剩余高度，上方一行 40px 路径条（ns › env › key、版本 chip、脏点、历史/删除/保存），右 248px 属性栏（格式、密钥、说明、变更备注）。主操作「保存」在路径条右端。
FORM: 虹彩云边（用户从 challenger 栏「Adopt anyway」采纳），我的 grounded 列表之外；seed key 12c96b9f。
FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance.
