# configctl：命令行写 Config Center

控制台适合人点，但「把仓库里的一份 YAML 放进某个 namespace/environment 的某个 key」
需要在终端和 CI 里可复现。`configctl` 就是这条路径：读文件、写 key、写完读回校验。

代码在 `services/config/cmd/configctl/`，可复用的客户端在 `sdk/configadmin/`
（与只读数据面 `sdk/configsource` 分开：后者只认 service token，不能写）。

```bash
go build -o bin/configctl ./services/config/cmd/configctl
```

## 凭据

只从环境变量或 `-token-file` 读，不进仓库（AGENTS.md 硬约束 4）。两种二选一：

| 变量 | 身份 | 能做什么 |
|---|---|---|
| `CONFIG_CENTER_SERVICE_TOKEN` | machine token | `role=operator` 才能写，且**只能写自身 environment**；`role=service` 只能 `GetKey`/`WatchKeys` |
| `CONFIG_CENTER_BEARER_TOKEN` | 管理员 Casdoor JWT | 全量，含 `DeleteKey`/`Rollback` 这类 operator 也做不了的 |

端点默认 `https://config-api.apikv.com`，用 `CONFIG_CENTER_ENDPOINT` 或 `-endpoint` 覆盖。

operator token 的 scope 在签发时钉死（environment + allowed namespaces），跨环境写会
`permission_denied`——这不是 bug，换一枚对应环境的 token，或改用管理员 JWT。
签发方式见 `docs/design/machine-token.md`。

## 写一个 key

```bash
configctl put \
  -namespace observability -environment prod \
  -key grafana/datasources/jaeger \
  -file examples/grafana-datasources/jaeger.yaml \
  -comment "接入 Jaeger 数据源"
```

行为：

1. 先 `GetKey` 判断是新建还是更新，输出 `created` / `updated` 与新版本号。
2. `PutKey` 写入；`format` 留空时按文件后缀推断（`.yaml`/`.yml`/`.toml`/`.json`），
   推不出来就报错而不是默默按 plaintext 写——那会绕过服务端的语法校验。
3. 写完再 `GetKey` 读回比对，值对不上直接非零退出。只看 PutKey 的回执不算验证。

其他开关：`-dry-run`（只比对不写，会说明是新建、无变化还是会改多少字节）、
`-secret`、`-description`、`-file -`（读 stdin，此时必须显式 `-format`）。

## 读与列举

```bash
configctl get -namespace observability -environment prod -key grafana/datasources/jaeger
configctl get -meta ...                      # 只看 format/version/secret/updated_by
configctl ls  -namespace observability -environment prod -prefix grafana/
configctl namespaces
```

`get` 默认把 value 原样打到 stdout，可以直接重定向成文件或喂给 `diff`。
