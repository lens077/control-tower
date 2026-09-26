# 部署前置操作

本文只覆盖「apply 之前」的一次性准备。对外环境一律用 `deploy/pre/`；`deploy/dev/gateway/` 带
localhost BFF 参数，只服务本地开发，不要 apply 进集群（原因见 `AGENTS.md`「恢复网关前要先补的对象」）。

## 1. Config Center 里的网关键

在 config 管理台（`https://config.apikv.com`）`gateway` namespace 对应 environment 下确认以下键存在，
缺了就创建（写键优先用 `configctl`，见 `docs/operations/configctl.md`）：

| key | 内容来源 | format | is_secret |
|---|---|---|---|
| `routes.yaml` | 仓库 `routes/{env}.yaml` 原文 | yaml | false |
| `auth/revocations.yaml` | `revocations: []`（空表起步） | yaml | false |

另外三键 `config.yaml`、`secrets/public.pem`、`policies/*` 也由网关读取，不要删。

## 2. 给网关签发 machine token

管理台 `/tokens` 页：service=`gateway`，environment 按环境，namespaces 留空（默认 gateway），
note 写用途。明文直接进入下一步的 Secret，不落任何文件。

## 3. 创建网关 selector Secret

以 `examples/config-source/gateway.yaml` 为模板填入真 token 后：

```bash
kubectl -n ecommerce create secret generic control-tower-config-source-<env> \
  --from-file=gateway.yaml=/dev/stdin < <(填好的 selector 内容)
```

或先落临时文件再 `--from-file=gateway.yaml=<path>`，用完即删。Deployment 以 `defaultMode: 0400` 挂载（清单已写死）。

## 4. 核对非机密 ConfigMap

`deploy/<env>/gateway/deployment.yaml` 内的 `control-tower-gateway-config`：
`JWT_AUDIENCES` 已填共享 client id；`OTEL_EXPORTER_OTLP_ENDPOINT` 按集群 collector 实址核对。

## 5. 镜像引用

集群统一从 TCR 拉镜像：`ccr.ccs.tencentyun.com/sumery/control-tower-{gateway,config,config-web}:<tag>`。
CI 把同一构建双推 GHCR（`ghcr.io/lens077/...`，只作追溯）与 TCR（需要仓库 Secrets
`TCR_USERNAME`/`TCR_PASSWORD`；缺失时 CI 只推 GHCR 并给 warning，此时要手工 `docker pull` → `tag` → `push` 补到 TCR）。
apply 前把 Deployment 的 image 换成目标**不可变 tag**，不要用 `dev`/`latest`。

TCR 仓库是 private：每份 Deployment 的 pod spec 必须带 `imagePullSecrets: [tcr-pull]`，
`config-center` 与 `ecommerce` 两个 ns 都已有该 Secret。漏掉时新 Pod 卡在
`ErrImagePull ... failed to fetch anonymous token ... 401 Unauthorized`，`deploy/manifest_test.go` 会先在本地拦下。
本机手工构建时加 `--platform linux/amd64`（节点全是 amd64）。

## 6. apply 与验收

```bash
kubectl apply -f deploy/pre/gateway/
kubectl -n ecommerce rollout status deploy/control-tower-gateway
curl -sf https://gateway.apikv.com/readyz      # 路由、公钥、Casbin、resolver 快照都就绪才返回 200
```

每一步集群操作先征询。
