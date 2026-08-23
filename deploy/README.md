# 部署前置操作（P5 序曲）

切流总手顺见 `../docs/design/cutover.md`；本文只覆盖「apply 之前」的一次性准备。按环境（dev/pre）各做一遍。

## 1. Config Center 播种两个新键

在 config 管理台（config.app.com）`gateway` namespace 对应 environment 下创建：

| key | 内容来源 | format | is_secret |
|---|---|---|---|
| `routes.yaml` | 仓库 `routes/{env}.yaml` 原文复制 | yaml | false |
| `auth/revocations.yaml` | `revocations: []`（空表起步） | yaml | false |

旧键 `config.yaml`、`secrets/public.pem`、`policies/*` **不动**（旧网关冻结依赖 + 新网关共用后三键）。

> 前提：config 服务已按 cutover.md §1 完成滚动换镜像（新键的 `/tokens` 签发能力随之可用）。

## 2. 给网关签发 machine token

管理台 `/tokens` 页：service=`gateway`，environment 按环境，namespaces 留空（默认 gateway），
note 写用途。**明文只显示一次**，直接进入下一步的 Secret，不落任何文件。

## 3. 创建网关 selector Secret

以 `../configs/selector-examples/gateway.yaml` 为模板填入真 token 后：

```bash
kubectl -n ecommerce create secret generic control-tower-config-source-<env> \
  --from-file=gateway.yaml=/dev/stdin < <(填好的 selector 内容)
```

或先落临时文件再 `--from-file=gateway.yaml=<path>`，用完即删。Deployment 以 `defaultMode: 0400` 挂载（清单已写死）。

## 4. 核对非机密 ConfigMap

`deploy/<env>/gateway/deployment.yaml` 内的 `control-tower-gateway-config`：
`JWT_AUDIENCES` 已填共享 client id；`OTEL_EXPORTER_OTLP_ENDPOINT` 按集群 collector 实址核对。

## 5. 镜像引用

发布 tag 的 CI 产物：`ghcr.io/lens077/control-tower-{gateway,config,config-web}:sha-<7位>`。
apply 前把三份 Deployment 的 image 换成目标 **不可变 sha tag**（禁 dev/latest 上生产路径）；
配置 TCR Secrets 后同名镜像亦在 `ccr.ccs.tencentyun.com/sumery/` 双推。

完成以上五步 → 回到 `cutover.md` 从 §1 开始逐步执行（每一步集群操作先征询）。
