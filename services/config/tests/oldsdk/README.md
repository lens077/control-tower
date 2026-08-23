# 跨版本探针（旧 SDK v0.1.0 → 新 config 服务）

本目录是独立 Go module，依赖**真旧 SDK** `github.com/lens077/config-center v0.1.0`，
用来证明 wire 冻结成立：滚动窗口与回滚场景内的旧客户端打新服务必须继续工作。

## 运行

```bash
# 仓库根执行；需要本机 docker（throwaway Postgres/Redis，跑完即删）
make test-crossversion
```

编排脚本：`scripts/crossversion.sh`。覆盖四个场景：

| 场景 | 断言 |
|---|---|
| a. legacy 共享 token | 旧 SDK Load/Watch 成功；服务端出现 legacy WARN（双栈告警数据源） |
| b. per-service token | 白名单内 namespace 可读；越权 namespace 被拒（PermissionDenied） |
| c. 吊销断流 | Watch 建流后吊销 token，流在一个心跳周期内被服务端断开（实测 ≈27s，合格线 40s） |
| d. 两代重叠 | 同 service×environment 两枚 token 并存可用；吊销其一不影响另一枚 |

## 注意

- 探针的 `-mode=watch` 正常在超时后以退出码 2 结束（Watch 返回即流结束），脚本已按场景处理。
- token 明文只存在于测试材料，与任何真实环境无关。
- 私仓解析：`GOPRIVATE=github.com/lens077` + 本机 git ssh。
