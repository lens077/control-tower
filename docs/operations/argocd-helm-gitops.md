# Argo CD + Helm 发布链路

2026-09-27 起生效。GitHub Actions 只做质量门禁、推镜像与推进 GitOps 提交；集群侧的权限只在 Argo CD。
本文是操作手册：正常发布、回滚、接管新组件、排障。设计取舍见文末。

## 链路

```
git tag X.Y.Z → push
  │
  ├─ CI（.github/workflows/ci.yml）
  │    test（含 make check-deploy）/ web / codegen
  │    image ×3 → TCR + GHCR，tag = X.Y.Z
  │    release job（只在 semver tag 上跑，needs image）：
  │      scripts/promote-release.sh X.Y.Z
  │        values-pre.yaml 三个镜像 tag = X.Y.Z → make deploy-render → go test ./deploy/ → check
  │      commit "release: X.Y.Z → pre" → push GitHub main
  │      fast-forward push → gitlab.com/sumery/control-tower（Argo CD 的 source）
  │
  ├─ GitLab push webhook → https://argocd.apikv.com/api/webhook（实测 8 秒；轮询兜底 3 分钟）
  │
  └─ Argo CD
       Application control-tower-config   → ns config-center（config + config-web）  自动同步 + selfHeal
       Application control-tower-gateway  → ns ecommerce（gateway）                  见「网关」
```

首次全链路：`0.2.18`，2026-09-27，tag 推出后约 12 分钟 `config-api.apikv.com/healthz` 报 `build: 0.2.18`。

## 各处对象

| 位置 | 内容 | 谁改 |
|---|---|---|
| `deploy/chart/control-tower/` | Helm chart（唯一真相源） | 人，走 PR |
| `deploy/chart/control-tower/values-pre.yaml` | 三个镜像 tag + pre 的主机名/回调 | tag 由 release job 回写；其余人改 |
| `deploy/{dev,pre}/` | `make deploy-render` 的渲染产物 | 不手改；改 chart 后重新渲染并一起提交 |
| `deploy/argocd/` | AppProject `control-tower`、Application ×2、repo Secret | 人，`kubectl apply -f` |
| GitLab `sumery/control-tower` | GitHub main 的镜像，只在发布时更新 | release job（也可手动 `git push gitlab main`，但要 fast-forward） |
| GitLab webhook `90127735` | push main → Argo；token 是 `argocd-secret` 里现有的 `webhook.gitlab.secret`（与 ecommerce 共用） | 人 |
| GitHub Secret `GITLAB_PUSH_TOKEN` | GitLab 项目访问令牌 `github-actions-release-20260927`，Developer + `write_repository`，**2027-09-27 到期** | 到期前用 `glab api ... access_tokens` 重签，`gh secret set` 写回 |

Argo CD 本身：`argocd` ns，v3.5.3，公网 `https://argocd.apikv.com`。本机用 CLI 不必登录：
把 context 的 namespace 切到 `argocd` 后加 `--core`（直接用 kubeconfig）。

## 正常发布

```bash
git tag -a X.Y.Z -m "…" && git push origin X.Y.Z
gh run watch            # 等 release job 绿
kubectl -n argocd get app control-tower-config   # Synced / Healthy
curl -s https://config-api.apikv.com/healthz     # build 字段 = X.Y.Z
```

前提：tag 打在 main 历史上（release job 校验）；提交说明里不要出现跳过 CI 的字面标记。
本地预演 release job 会改什么：`scripts/promote-release.sh X.Y.Z`，看完 `git checkout -- deploy`。

## 回滚

Git 回滚，不在 Argo UI 上点：

```bash
scripts/promote-release.sh <上一个版本>
git commit -am "release: 回滚到 <版本>：<原因>"
git push origin main && git push gitlab main
```

Argo 同步后 Deployment 滚回旧镜像。旧镜像 tag 永远在 TCR（发布 tag 不覆写）。
紧急情况可以 `kubectl -n config-center set image ...` 先止血，但 selfHeal 会在下一次刷新时改回 Git 的值，
所以止血后必须立刻补上面的 Git 回滚。

## 网关

Application `control-tower-gateway` 已 apply，`automated.enabled: false`。前置对象（Secret、policies.csv、
consul 引用 optional）2026-09-27 已就位，7 个对象服务端 dry-run 通过，ns 里没有同名旧对象。启用：

```bash
kubectl config set-context --current --namespace=argocd
argocd app sync control-tower-gateway --core --timeout 300
kubectl -n ecommerce rollout status deploy/control-tower-gateway
curl -sf https://gateway.apikv.com/readyz
# 通过后：deploy/argocd/app-gateway.yaml 的 automated.enabled 改 true，kubectl apply，提交
```

`/readyz` 503 时看 Pod 日志：`p row act column` 是策略表问题（改 Config Center 键，不改镜像），
`config-source` 相关是 Secret `control-tower-config-source-pre` 或 token 被吊销。

## 接管一个已在跑的组件

config 接管时的做法，以后加组件照此：

1. values 钉住**线上正在跑的**镜像 tag；Application 以 `automated.enabled: false` apply；
2. `argocd app diff <app> --core`：只允许出现 Argo 跟踪注解、`last-applied` 与本就没 apply 过的对象；
   出现其它差异说明 chart 与线上不一致，先修 chart；
3. 记下 Pod UID，手动 `argocd app sync`，再比对 UID/重启计数——必须完全不变；
4. 改 `automated.enabled: true` 重新 apply；`prune` 稳定一个发布周期后再开。

Application 不带 `resources-finalizer`：删除 Application 只是让 Argo 放手，线上资源原地不动，接管随时可退。

## 排障

| 症状 | 看哪里 |
|---|---|
| tag 推了但没有任何 CI run | head 提交说明含跳过标记；或 tag 不在 main 上 |
| release job 在「Push main to GitLab」失败 | GitLab main 领先于 GitHub（有人直接推了 GitLab）：`git fetch gitlab && git log origin/main..gitlab/main`，手动对齐；或 `GITLAB_PUSH_TOKEN` 过期 |
| Argo 一直 OutOfSync 但 diff 只有 HTTPRoute 的 `group/kind/weight` | API Server 补的默认值，`ignoreDifferences` 已列，检查 `RespectIgnoreDifferences=true` 还在 |
| Argo 没跟上推送 | `kubectl -n argocd logs deploy/argocd-server \| grep webhook`；GitLab 项目 Settings → Webhooks 看投递记录；轮询 3 分钟内总会追上 |
| VPA 同步失败 `updateMode InPlace` | admission-controller 没开 InPlace gate；values 里当前是 `Off`，不要为绕过改成 Recreate/Auto |
| `check-deploy` 在 CI 红、本地绿 | helm 版本不同（CI 钉 v4.3.0）；本地 `helm version` 对齐 |

## 取舍

- **chart 与源码同仓，GitOps 仓是镜像**而不是独立仓：旧 `control-tower-gitops` 是人手抄 `deploy/` 出来的 chart，
  抄的那一刻就已不一致，2026-09-20 事故的载体就是它。现在 chart 只有一份，Argo 读的是同一仓的 GitLab 镜像。
- **渲染产物保留在仓库里**：11 条守门测试零改动继续读文本；`kubectl apply -f deploy/pre/` 仍是 Argo 故障时的兜底；
  PR 里能直接 review 渲染后的 diff。代价是 `make check-deploy` 门禁，与 `check-gen` 同一纪律。
- **只有 semver tag 上线**：pre 是对外环境，未经审视的东西不该到线上；main 只出 `sha-` 镜像。
- **两个 Application 按 namespace 拆**：出问题互不影响，且 AppProject 的 destination 与资源白名单能收得更紧。
- **不用 Argo CD Image Updater**：追踪镜像仓库绕过 Git 审计，与「版本进 Git 提交」的目标相反。
- **Secret、Namespace 不归 Argo**：AppProject 白名单不含 Secret，`clusterResourceWhitelist` 为空。
- **Argo Rollouts**：chart 预留 `configWeb.rollouts.enabled`（Blue-Green，preview Service，VPA 自动关），
  AppProject 已放行 `Rollout/AnalysisTemplate/AnalysisRun`，`ignoreDifferences` 已列 `rollouts-pod-template-hash`。
  默认关；启用是单独一步：values-pre 开 → Argo 同步 → `kubectl argo rollouts promote`。

## Mac 本地访问 Argo CD

`argocd.dev.test` 是集群 Gateway 的开发主机名，不是公网 DNS；公网用 `https://argocd.apikv.com`。
不登录也能操作：`kubectl config set-context --current --namespace=argocd` 后所有 `argocd` 命令加 `--core`。
