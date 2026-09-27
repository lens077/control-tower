# deploy/：部署清单与 GitOps 对象

```
deploy/
  chart/control-tower/      Helm chart，部署清单的唯一真相源
    values.yaml             环境无关结构（副本、探针、资源、Casdoor 信任域）
    values-pre.yaml         对外环境；三个镜像 tag 由 CI release job 回写
    values-dev.yaml         本地开发；带 localhost 回调，不要 apply 进集群
    components/{config,gateway}.yaml   Argo CD 两个 Application 各自只渲染自己的 namespace
  dev/  pre/                `make deploy-render` 的渲染产物：守门测试读它、Argo 故障时 kubectl 兜底用它
  argocd/                   AppProject、两个 Application、repo 声明，由人 kubectl apply
  examples/config-source/   网关 selector Secret 的模板
```

**改清单只改 chart**，然后 `make deploy-render` 并把产物一起提交；CI 的 `make check-deploy` 比对两边，
不一致就红（与 proto → `make check-gen` 同一纪律）。发布链路与回滚见
`docs/operations/argocd-helm-gitops.md`。

## 正常发布

不需要碰 `kubectl`：

```bash
git tag X.Y.Z && git push origin X.Y.Z
```

CI 构建三个镜像，release job 把 `values-pre.yaml` 的 tag 改成 `X.Y.Z`、重新渲染、提交到 main，
Argo CD 直接读本仓，经 GitHub webhook 同步。要本地预演：`scripts/promote-release.sh X.Y.Z`（只改工作区）。

## Argo CD 对象

```bash
kubectl apply -f deploy/argocd/project.yaml -f deploy/argocd/repo.yaml
kubectl apply -f deploy/argocd/app-config.yaml -f deploy/argocd/app-gateway.yaml
```

Application 不带 resources-finalizer：删除它只是让 Argo 放手，线上资源原地不动。
Secret 与 Namespace 不归 Argo 管，AppProject 也不放行它们。

## 手工兜底

Argo CD 不可用时才用，并且只用 `pre`：

```bash
kubectl apply -f deploy/pre/config/    # 或 deploy/pre/gateway/
```

`deploy/dev/gateway/` 带 `http://localhost:3000` 回调与 `SESSION_COOKIE_INSECURE=true`，namespace 与
HTTPRoute 主机名和 pre 完全相同，apply 它 = 把 localhost 值推上公网（2026-09-20 事故）。

## 网关的一次性准备

以下对象 chart 只引用名字，由人建好。2026-09-27 已全部就位，换环境或轮换时按此重做。

### 1. Config Center 里的网关键

`gateway` namespace 对应 environment 下须有 `routes.yaml`、`auth/revocations.yaml`、`config.yaml`、
`secrets/public.pem`、`policies/{model.conf,policies.csv}`。写键用 `configctl`（`docs/operations/configctl.md`）。

`policies.csv` 的每个 `p` 行 act 列必须是字面 `POST`（网关 `authz.validatePolicyRow`），
`.*`、`GET|POST` 之类整表拒载、启动失败。

### 2. 签发 machine token 并建 selector Secret

用对应环境的 operator token（Secret `config-center/config-center-operator-<env>`）签发 `role=service`、
`service_name=gateway` 的 token，明文直接进 Secret，不落文件：

```bash
OP="$(kubectl -n config-center get secret config-center-operator-pre -o jsonpath='{.data.token}' | base64 -D)"
curl -sf -X POST https://config-api.apikv.com/config.v1.ConfigService/IssueMachineToken \
  -H "content-type: application/json" -H "x-config-center-service-token: $OP" \
  -d '{"serviceName":"gateway","environment":"pre","note":"control-tower-gateway selector","role":"MACHINE_TOKEN_ROLE_SERVICE"}' \
| python3 -c 'import sys,json; t=json.load(sys.stdin)["token"]; print(open("deploy/examples/config-source/gateway.yaml").read().replace("REPLACE_WITH_ISSUED_TOKEN", t).replace("environment: dev", "environment: pre"))' \
| kubectl -n ecommerce create secret generic control-tower-config-source-pre --from-file=gateway.yaml=/dev/stdin
```

Deployment 以 `defaultMode: 0400` 挂载。Secret 建失败时先去 `/tokens` 页吊销刚签的 token 再重来，
不要留下没有落地的明文。

### 3. 其它 Secret

`ecommerce` ns：`tcr-pull`（拉镜像）、`dragonfly-session`（会话 Redis 账密 + CA）、`casdoor-bff`（BFF client）。
可选：`otel-auth`（OTLP 鉴权头，缺了匿名发送）、`consul-ecommerce-token`（已 optional，网关不经 Consul）。

### 4. 镜像

集群统一从 TCR 拉 `ccr.ccs.tencentyun.com/sumery/control-tower-{gateway,config,config-web}:<tag>`，
private 仓库，每份 pod spec 必须带 `imagePullSecrets: [tcr-pull]`（`deploy/manifest_test.go` 守）。
chart 的 `ct.image` helper 在 tag 为空时直接 fail，不会静默落到 latest。
本机手工构建加 `--platform linux/amd64`。

### 5. 验收

```bash
kubectl -n ecommerce rollout status deploy/control-tower-gateway
curl -sf https://gateway.apikv.com/readyz      # 路由、公钥、Casbin、resolver 快照都就绪才 200
```

根路径 `/` 按契约返回应用层 `404 ROUTE_NOT_FOUND`，不是故障。
