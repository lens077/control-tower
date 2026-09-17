# GitHub Actions 线上部署身份

`github-actions-rbac.yaml` 为当前公网 dev 部署准备最小权限身份：

- `ecommerce`：`control-tower-gateway` Deployment 的 `get`、`patch`，以及 rollout 所需的 ReplicaSet/Pod 读取权限。
- `config-center`：`config-center` 与 `config-center-web` Deployment 的 `get`、`patch`，以及 rollout 所需的 ReplicaSet/Pod 读取权限。

使用已有的集群管理员 kubeconfig 执行一次：

```bash
kubectl apply -f deploy/ci/github-actions-rbac.yaml
```

然后生成给 GitHub Actions 使用的 kubeconfig。以下命令只在本地终端生成，不要提交仓库：

```bash
NS=config-center
SA=control-tower-deployer
CTX=$(kubectl config current-context)
CLUSTER=$(kubectl config view -o jsonpath="{.contexts[?(@.name==\"$CTX\")].context.cluster}")
SERVER=$(kubectl config view --raw -o jsonpath="{.clusters[?(@.name==\"$CLUSTER\")].cluster.server}")
CA=$(kubectl config view --raw -o jsonpath="{.clusters[?(@.name==\"$CLUSTER\")].cluster.certificate-authority-data}")
TOKEN=$(kubectl create token "$SA" -n "$NS" --duration=8760h)
cat > /tmp/control-tower-ci-kubeconfig <<EOF
apiVersion: v1
kind: Config
clusters:
- name: public-cluster
  cluster:
    server: $SERVER
    certificate-authority-data: $CA
users:
- name: control-tower-deployer
  user:
    token: $TOKEN
contexts:
- name: control-tower-public-dev
  context:
    cluster: public-cluster
    user: control-tower-deployer
current-context: control-tower-public-dev
EOF
base64 < /tmp/control-tower-ci-kubeconfig | tr -d '\n'
```

把最后一行输出作为 GitHub 仓库 Secret。推荐直接通过 `gh` 写入当前仓库，命令不会把 Secret 内容显示在终端历史中：

```bash
# 在仓库根目录执行；要求 gh auth login 已登录且当前账号有该仓库的 Actions secrets 写权限。
gh secret set KUBECONFIG_B64 \
  --repo "$(gh repo view --json nameWithOwner --jq .nameWithOwner)" \
  < <(base64 < /tmp/control-tower-ci-kubeconfig | tr -d '\n')
```

也可以显式指定仓库，适合不在仓库根目录执行：

```bash
gh secret set KUBECONFIG_B64 \
  --repo OWNER/REPOSITORY \
  < <(base64 < /tmp/control-tower-ci-kubeconfig | tr -d '\n')
```

确认 Secret 已存在（只显示名称，不显示内容）：

```bash
gh secret list --repo OWNER/REPOSITORY | grep -E '^KUBECONFIG_B64\\b'
```

校验权限：

```bash
KUBECONFIG=/tmp/control-tower-ci-kubeconfig kubectl auth can-i get deployments -n ecommerce
KUBECONFIG=/tmp/control-tower-ci-kubeconfig kubectl auth can-i patch deployments -n ecommerce
KUBECONFIG=/tmp/control-tower-ci-kubeconfig kubectl auth can-i get deployments -n config-center
KUBECONFIG=/tmp/control-tower-ci-kubeconfig kubectl auth can-i patch deployments -n config-center
KUBECONFIG=/tmp/control-tower-ci-kubeconfig kubectl auth can-i get pods -n config-center
```

该 kubeconfig 是敏感凭据，只写入 GitHub Secret，不写入 `.env`、日志或仓库。
