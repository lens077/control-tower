# Argo CD + Helm 部署建议

可以用 Argo CD + Helm 替代 GitHub Actions 直接执行 `kubectl`。这是当前线上集群更合适的方向：GitHub Actions 只负责质量门禁和推送镜像，Argo CD 在集群内 watch Git 仓库并负责同步资源。

## 当前变更

- GitHub Actions 已删除 `deploy-public` job。
- GitHub Actions 不再引用 `KUBECONFIG_B64`。
- `KUBECONFIG_B64` Secret 已从 `lens077/control-tower` 删除。
- 仓库不再维护给 GitHub Actions 使用的 deployer ServiceAccount、Role 和 RoleBinding。

## 推荐目标结构

```text
push main
  -> GitHub Actions: test / web / codegen / build + push TCR
  -> 更新 GitOps 仓库中的镜像 tag 或 digest
  -> Argo CD 在集群内发现 Git 变更
  -> Argo CD 使用 Helm 渲染并同步 ecommerce/config-center 资源
```

## Helm 拆分建议

将当前 `deploy/dev` 清单逐步迁移为一个 Helm chart 或三个 release：

- `control-tower-gateway`：namespace `ecommerce`
- `control-tower-config`：namespace `config-center`
- `control-tower-config-web`：namespace `config-center`

镜像建议使用不可变的 `sha-<7位提交号>` 或镜像 digest，不要让 Argo CD 追踪可变的 `dev` tag。

## Argo CD 应用边界

Argo CD 应持有集群内权限，而不是 GitHub Actions。建议为每个线上应用建立独立 Argo CD Application，并限制到对应 namespace；不要给 Argo CD 使用集群管理员权限。

## 后续实施顺序

1. 新建 Helm chart，把 `deploy/dev` 的 Deployment、Service、ConfigMap、HTTPRoute 和相关引用迁入模板。
2. 在集群内安装 Argo CD，并为 GitOps 仓库配置只读 Git 凭据。
3. 创建三个 namespace-scoped Application，先使用 `syncPolicy.automated.prune: false` 做观察性同步。
4. 对比 Argo CD rendered manifest 与现有线上资源，确认无差异后再开启自动同步。
5. 将镜像 tag 更新作为 GitOps 提交，或使用 Argo CD Image Updater；推荐提交 digest 以保留审计记录。
6. 稳定运行后删除旧的手工部署入口和临时权限。

## 当前实机状态（2026-09-27）

集群 2026-09-21 前后重建后，Argo CD 仍在（`argocd.apikv.com` 返回 200），但本仓的 Application
`argocd/control-tower-public-dev` **已不存在**，当前 `argocd` ns 只有 `ecommerce-kyverno`。
config 与 config web 是按 `deploy/pre/config/` 手工 `kubectl apply` 的，网关尚未部署。
重新接入时从上面「后续实施顺序」第 3 步开始；GitOps 仓库 `https://gitlab.com/sumery/control-tower-gitops`
是否仍可用需先确认。

HTTPRoute 在 Argo CD 里显示 OutOfSync 时，通常不是缺 Gateway API，而是 API Server 补了默认的
`group`、`kind`、`weight` 字段，用 `ignoreDifferences` 忽略即可。

## Mac 本地访问 Argo CD

`argocd.dev.test` 是集群 Gateway 的开发主机名，不是公网 DNS。当前 Gateway 地址为 `10.10.31.240`；如果 Mac 不在该网段，直接访问会超时，即使 HTTPRoute 本身是 Accepted。

优先使用公网域名：

```bash
curl -kI https://argocd.apikv.com
```

本地临时访问可以使用端口转发：

```bash
kubectl -n argocd port-forward svc/argocd-server 8080:80
open http://127.0.0.1:8080
```

如果 Mac 已经能路由到 `10.10.31.240`，再把开发域名解析到 Gateway 地址后访问：

```text
10.10.31.240 argocd.dev.test
```

不要把 `argocd.dev.test` 指向 `argocd-server` 的 ClusterIP；Gateway API 的 HTTPRoute 必须经由共享的 `default/cilium-gateway`。
