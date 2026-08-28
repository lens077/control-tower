# Config Center 内容 Schema 校验

## 目标

Config Center 在 `PutKey` 持久化前校验已登记配置的业务结构，而不是只检查 YAML、JSON 或 TOML 语法。这样未知键、缺少必需段和跨服务字段会在写入口被拒绝，不再等服务启动或热更新时才暴露。

触发事故是 ecommerce 的 7 个非 search 服务长期携带 `search.elastic_search`：域名和 Elasticsearch 均不存在，配置还包含历史明文口令；共享 proto 却保留该字段，因此语法校验、运行时解码和旧 JSON Schema 都没有报错。

## Interface 与 seam

`biz.ContentValidator` 是单方法 interface。`ConfigUseCase.PutKey` 先执行格式校验，再调用该 interface，最后才进入 `ConfigRepo.PutEntry`。JSON Schema 的编译、来源、模式和错误脱敏全部封装在 `internal/schema.Registry` 中，service 和 data 层不认识 JSON Schema。

`Rollback` 也通过 `PutKey`，避免历史 revision 绕过相同规则。紧急情况下不要用 Rollback 穿透校验，改用下述 observe 模式。

## 登记范围

当前内嵌 ecommerce 10 个服务的 `bootstrap.yaml` Schema：

- 已登记 namespace + `bootstrap.yaml`：执行 Schema 校验；空值、纯注释和 plaintext 同样拒绝，因为消费方无法把它们解码成 Bootstrap；
- 未登记 namespace：放行，避免新增服务被旧 control-tower 阻断；
- 其他 key：放行，继续由原格式校验负责；
- namespace 和 key 按 Config Center 的精确、区分大小写语义匹配，不把 `Cart` 或 `bootstrap.yml` 偷偷归一成另一项配置；
- 校验错误：只返回 JSON 实例位置，不返回 Schema 消息或配置值。

Schema 在进程启动时一次性编译。任何内嵌文件缺失或无法编译都会让 config 服务启动失败，不把损坏推迟到第一次写入。

## 模式与应急旁路

环境变量 `CONFIG_SCHEMA_MODE`：

| 值 | 行为 |
|---|---|
| 空或 `enforce` | 默认；Schema 违规返回 `InvalidArgument`，不写 entry/revision，不发变更通知 |
| `observe` | 记录 namespace、environment、key、实例位置和 Schema 来源 revision，但允许写入；日志不含配置值 |

`observe` 只用于 Schema 漂移阻断发布时的临时旁路。解除事故后必须恢复 `enforce` 并滚动重启。

## Schema 来源与跨仓发布顺序

内嵌文件来自 ecommerce 的 `backend/services/<service>/configs/bootstrap.schema.json`，来源 commit 记录在 `services/config/internal/schema/schemas/ecommerce-source-revision.txt`。同步命令：

```bash
make sync-ecommerce-schemas
```

ecommerce 增加或收紧 Bootstrap 字段时，必须按以下顺序发布：

1. 在 ecommerce 生成并提交新 Schema；
2. 在 control-tower 同步快照、运行 `make verify`，先发布并部署 config 服务；
3. 写入新配置；
4. 再发布和部署消费该配置的 ecommerce 服务。

旧 control-tower Schema 使用 `additionalProperties: false`，若跳过第 2 步，会拒绝 ecommerce 新增的合法字段。未知 namespace 的放行策略不能解决已登记服务的版本漂移。
