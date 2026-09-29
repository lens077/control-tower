# System 页共享主机指标

## 来源与兼容边界

System 页「全部主机」覆盖 k1–k3 与 node0–node4。`services/config/internal/pkg/promql/catalog.go`
只消费共享 recording rules；规则真相源在 kubernetes 仓的 `components/vmalert/rules/host-recording.yml`。
先部署并验证共享规则，再发布 control-tower。新规则不自动回填旧历史。

`host` 是全局唯一主机名，`host_kind` 为 `cloud` 或 `kubernetes`。所有主机查询按这两个标签关联，
不套用配置中心的 `service_name` / `service_namespace` 过滤条件。

| 指标组 | 共享记录 | API 单位与图例 |
|---|---|---|
| CPU | `host:cpu_busy_ratio`、`host:cpu_iowait_ratio` | 比率各乘一次100，`PERCENT`；`host`、`host / iowait` |
| Memory | `host:memory_used_ratio` | 比率乘100，`PERCENT`；`host` |
| Disk | `host:filesystem_used_ratio{mountpoint="/"}` | 根分区比率乘100，`PERCENT`；`host` |
| Network | `host:network_io_bytes_per_second`，按 `direction="receive"` / `direction="transmit"` 分两条查询 | 已完成5分钟 rate并排除虚拟网卡，保持 `BYTES_PER_SECOND`；`host / receive`、`host / transmit` |

CPU busy 不包含 iowait。网络不再次 rate，不跨主机汇总，也不换算为 bit/s。
前端按现有 `MetricUnit` 格式化，bytes/s 使用1024进制的 B/KiB/MiB…/s。
`host:cpu_count` 与 `host:load1` 不属于本页现有四组曲线，本次不新增枚举。

QueryMetrics 的 proto、生成物和 wire 字段保持不变。`MetricLine` 仍为 `label/unit/points`，
不新增 host 过滤参数或元数据字段。请求的最多8组限制不是最多8台主机；CPU和网络各可返回16条线。
接口仍要求管理员 JWT，VM只读 token 与 machine token 都不能替代管理员登录。

## 新鲜度与无数据

查询用 PromQL `time()` 按每个历史评估时点判断180秒新鲜度，允许30秒采集、30秒评估和30秒 evalDelay。
它同时要求：

- 利用率/速率记录自身的样本 timestamp 新鲜；
- `host:signal_present` 按对应 `signal` 等值选择，覆盖值为1且该覆盖记录自身新鲜；
- `host:last_seen_timestamp_seconds` 的值新鲜；它取 CPU、memory、filesystem、network 四类原始样本的最大采集时间，不依赖某一类单独可用；
- `host:rules_evaluation_timestamp_seconds` 的规则评估时间新鲜。

以上条件使用存在性过滤，不与利用率相乘。缺少规则、覆盖为0、记录过旧均返回缺口；
不补零、不回退原始 exporter 公式，也不以当前时间删除过去合法的数据点。

每次含主机组的 API 请求额外查询一次 `host:expected_info`。该清单不依赖主机曾经上报。
预期主机整窗无数据时，服务端保留 `points=[]` 的具名占位；清单的常数1不会成为数据点。
同名清单和曲线合并去重，不求和。前端单列这些主机的无数据提示，不绘制零值曲线。
主机曲线按请求步长补齐时间轴，所有主机同时断采时也用 null 断线。

已有历史但最后一点距离窗口末端超过「180秒＋查询步长」时，保留历史并显示过旧提示。
查询步长的余量用于避免24小时视图的5分钟对齐点误报。此提示不是独立的实时健康探针。
整个请求失败时显示错误且不继续显示缓存的成功结果；部分查询失败时保留其它曲线并同时显示错误。
清单查询失败或为空时明确提示覆盖范围未知，不声称八台齐全。

## 验证

仓库根目录运行离线验证：

```bash
go test -short -race ./services/config/internal/pkg/promql ./services/config/internal/service
pnpm -C web test
pnpm -C web build
```

共享规则落地后，由已获授权的操作者安全注入 `CONFIG_CENTER_VM_BEARER_TOKEN`，不要将值写入命令历史或日志：

```bash
CONFIG_CENTER_VM_ENDPOINT=https://metrics.apikv.com \
  go test ./services/config/internal/pkg/promql -run Live -v
```

live 测试对主机组使用最近5分钟、30秒步长，核对预期八台、每条 busy/iowait/收发查询、百分比范围与新鲜点。
其它应用组仍使用24小时窗口，避免把低流量误判成故障。无凭据时不执行公网验证。

发布后以既有安全环境提供 E2E 登录凭据，运行 `pnpm -C e2e exec playwright test tests/config-console.spec.ts`。
浏览器还需核验四组主机图、16条图例在小屏幕上的可读性和缺失提示。原有系统页 e2e只检查至少一组有数据，
不能替代上述逐主机 live 验证。

本页不发布告警；75/90的颜色阈值仍只用于进程即时读数。vmalert与watchdog告警的所有权由基础设施规则管理。
