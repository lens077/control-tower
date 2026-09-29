# control-tower

网关与配置中心合一的平台仓：单 Go module、两个独立部署的服务。

| 服务 | 目录 | 职责 |
|---|---|---|
| gateway | `services/gateway` | Connect 原生反代（一级 proto 包名路由）、JWT/Casbin 鉴权、混合撤权、统一 Connect 错误、可观测 |
| config | `services/config` | 配置中心：键值 + 版本历史 + WatchKeys 热推送、per-service machine token、管理台（`web/`） |

由 ecommerce 旧网关（go-kratos/gateway fork）与 config-center 合并重写而来。前端与后端 10 服务经网关的调用路径、错误契约保持不变；迁移决策与对抗评审档案见工作区 `.migration-scratch/`。

部署现状（哪些在跑、地址、前置对象）以 `AGENTS.md`「部署现状」为准，本文不重复。

> ⚠️ `config-center` 这个命名空间与 Deployment 名只是**没改的遗留标签**，里面跑的镜像是本仓的 `control-tower-config` / `control-tower-config-web`。看到这个名字不要以为旧 config-center 仓还在跑——它已退役。

## 文档

- `docs/design/architecture.md` — 架构、请求链路、不变式
- `docs/design/service-health.md` — admin 专属的服务健康快照、h2c 探测口径、超时与缓存边界
- `docs/design/auth.md` — JWT 信任域、混合撤权三场景操作手册
- `docs/design/machine-token.md` — 数据面凭据设计
- `docs/design/decisions.md` — 砍掉/不做清单及原因
- `docs/operations/configctl.md` — 命令行写 Config Center
- `AGENTS.md` — 协作基线与硬约束

## 常用命令

```bash
make verify            # build + buf lint + go vet + test -race（提交前必跑）
make api               # proto 变更后重新生成
make breaking-legacy   # wire 冻结门禁：对旧 config-center 仓 WIRE_JSON 检查
make test-crossversion # 旧 SDK v0.1.0 → 新服务的跨版本实测（需本机 docker）
```

## 本地开发

```bash
make dev                        # config 服务（:30010）+ 控制台（:3005）一起起，Ctrl-C 一起停
make config                     # 只起 config 服务，等价于 scripts/dev-local.sh config
make gateway                    # 等价于 scripts/dev-local.sh gateway，监听 :8080
scripts/dev-local.sh print      # 只渲染配置看结构（口令脱敏）
```

`dev-local.sh config` 读 sibling kubernetes 仓 `components/{postgres,dragonfly}/component.env` 的依赖契约，
从 K8s Secret 取账密与 CA，渲染 0600 临时配置、退出即删，凭据不进仓库也不进日志。
PostgreSQL 走 `pg-dev.apikv.com:30001`，Redis（Dragonfly）走 `redis-dev.apikv.com:30005`，均为 TLS 直通；
System 页面的历史曲线查 `https://metrics.apikv.com`（`METRIC_QUERY_ENDPOINT` 可覆盖）；该入口经 vmauth 只读，
脚本自动从 Secret `victoriametrics/vmauth-credentials` 取只读 token（`METRIC_QUERY_BEARER_TOKEN` 可覆盖）。

`dev-local.sh gateway` 以 file 模式起网关：公钥、Casbin 策略、撤销名单取自 Config Center `gateway/dev`
（operator token 从 Secret `config-center/config-center-operator-dev` 读），路由用仓库 `routes/dev.yaml`，
其中 `direct://<svc>.ecommerce.svc:<port>` 改写成 `127.0.0.1:<port>` 并自动 `kubectl port-forward`。
BFF 会话走 `redis-dev.apikv.com:30005`，回调为 `http://localhost:3000/auth/callback`。
可调开关（`GATEWAY_ENV`、`GATEWAY_ROUTES_SOURCE`、`GATEWAY_POLICIES_FILE`、`GATEWAY_BFF` 等）见脚本内 `run_gateway` 上方说明。

本地跑 config 服务时 `CONSUL_ENABLED=false` 是硬要求（脚本已内置）：本机实例注册进集群目录后，集群内客户端可能把流量解析到你的 Mac。

完全离线（不碰集群）：`make test-crossversion` 那套 throwaway Postgres/Redis，或手写 `CONFIG_FILE` 指向 `services/config/tests/oldsdk/harness-config.yaml`。

web 控制台：`cd web && pnpm install && pnpm dev`（端口 3005，已在上面渲染配置的 CORS 白名单里）。

## 发布

```bash
git tag X.Y.Z && git push origin X.Y.Z
```

PR 只跑质量门禁；push main 构建三镜像（gateway/config/config-web）并双推 GHCR + TCR 但不部署；
裸 semver tag（`X.Y.Z`）额外跑 `release` job：把 `deploy/chart/control-tower/values-pre.yaml` 的镜像 tag
推进到该版本、重新渲染 `deploy/pre/`、提交并推到 GitLab 镜像仓，Argo CD 经 webhook 同步上线。
部署清单的真相源是 `deploy/chart/`，`deploy/{dev,pre}/` 是渲染产物（`make deploy-render`）。
链路、回滚与排障见 `docs/operations/argocd-helm-gitops.md`，目录说明与网关前置见 `deploy/README.md`。

## 许可

本项目采用 **[CC BY-NC-SA 4.0](https://creativecommons.org/licenses/by-nc-sa/4.0/)**
（署名—非商业性使用—相同方式共享）授权，详见 [`LICENSE`](LICENSE)：

- 可用于个人学习、技术交流与非营利研究；衍生作品须以相同或更严格的协议开源，并注明出处。
- **任何商业使用**（直接售卖、SaaS 集成、含付费内容或广告的平台等）须事先获得书面授权。
- 商业授权或闭源例外请联系版权方：<https://github.com/lens077>
