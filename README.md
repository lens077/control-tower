# control-tower

网关与配置中心合一的平台仓：单 Go module、两个独立部署的服务。

| 服务 | 目录 | 职责 |
|---|---|---|
| gateway | `services/gateway` | Connect 原生反代（一级 proto 包名路由）、JWT/Casbin 鉴权、混合撤权、统一 Connect 错误、可观测 |
| config | `services/config` | 配置中心：键值 + 版本历史 + WatchKeys 热推送、per-service machine token、管理台（`web/`） |

由 ecommerce 旧网关（go-kratos/gateway fork）与 config-center 合并重写而来。前端与后端 10 服务经网关的调用路径、错误契约保持不变；迁移决策与对抗评审档案见工作区 `.migration-scratch/`。

## 文档

- `docs/design/architecture.md` — 架构、请求链路、不变式
- `docs/design/auth.md` — JWT 信任域、混合撤权三场景操作手册
- `docs/design/machine-token.md` — 数据面凭据设计
- `docs/design/decisions.md` — 砍掉/不做清单及原因
- `docs/design/cutover.md` — 切流与回滚手顺
- `AGENTS.md` — 协作基线与硬约束

## 常用命令

```bash
make verify            # build + buf lint + go vet + test -race（提交前必跑）
make api               # proto 变更后重新生成
make breaking-legacy   # wire 冻结门禁：对旧 config-center 仓 WIRE_JSON 检查
make test-crossversion # 旧 SDK v0.1.0 → 新服务的跨版本实测（需本机 docker）
```

本地起服务（示例）：

```bash
# config（需本地 Postgres/Redis，参考 scripts/crossversion.sh 的 throwaway 方式）
CONFIG_FILE=services/config/tests/oldsdk/harness-config.yaml go run ./services/config/cmd/server

# gateway（file 模式，本地/测试专用）
CONFIG_SOURCE=file CONFIG_DIR=<五工件目录> JWT_ISSUER=https://casdoor.apikv.com \
  JWT_AUDIENCES=<client-id> go run ./services/gateway/cmd/server
```

## 发布

CI 由裸 semver tag（`X.Y.Z`）触发：质量门禁 + 三镜像（gateway/config/config-web）推 GHCR（配置 TCR Secrets 后双推）。PR 只跑质量门禁；push main 不构建。部署清单在 `deploy/{dev,pre}`，切流手顺见 `docs/design/cutover.md`。
