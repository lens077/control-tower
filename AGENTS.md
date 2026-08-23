# AGENTS.md — control-tower

网关与配置中心合一的平台仓：单 module、两服务（`services/gateway`、`services/config`）。由 ecommerce 旧网关（go-kratos/gateway fork）与 config-center 合并重写而来。

## 必读

| 文档 | 内容 |
|---|---|
| `docs/design/architecture.md` | 两服务架构、网关请求链路、不变式 |
| `docs/design/auth.md` | JWT 信任域、混合撤权三场景操作手册 |
| `docs/design/decisions.md` | 砍掉/不做清单及原因——**改动前先查这里，别把砍掉的东西加回来** |

## 硬约束

1. **wire 冻结**：`api/` 下存量 proto 的包名、Service/RPC 名、字段号与类型不可动（改名仅限 `go_package`）；新增字段/RPC 自由。解除条件见 `docs/design/decisions.md`。
2. **`http.Server` 不设 `WriteTimeout`**：会掐断 `WatchKeys` 长流（历史事故）。超时走路由级 context。
3. **路由模板变更纪律**：改 `routes/{dev,pre}.yaml` 必须同 PR 升级 ecommerce 仓对本 module 的依赖版本，否则其 structcheck 门禁变红。
4. 凭据不进仓库：token/私钥只存 Config Center、K8s Secret 与本地环境。
5. 中文文档与注释遵循 tech-doc-style-chinese 规范：直角引号「」，允许第二人称「你」。

## 验证锚点

```bash
make verify        # build + buf lint + go vet + test（提交前必跑）
make api           # proto 变更后重新生成（buf generate + lint）
```

CI 由裸 semver tag（`X.Y.Z`）触发发布；PR 只跑质量门禁；push main 不构建。

## 迁移背景

迁移期决策与对抗评审档案在工作区 `.migration-scratch/`（不入本仓）：06 决策日志、11 终裁书、12 实施方案 v2。迁移完成后此节移除。
