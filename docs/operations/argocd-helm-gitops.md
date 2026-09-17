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

当前仓库暂未自动生成 Helm chart 或 Argo CD Application，因为这会涉及线上资源接管、字段归属和 GitOps 仓库位置，不能在没有确认这些边界时直接接管生产资源。
