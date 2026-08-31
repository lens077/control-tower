# 服务互联寻址手册

本文只回答一个问题：**control-tower 的服务要连某个依赖，该写哪个地址。**
所有结论都在 2026-09-01 从「Mac」与「集群内 Pod」两侧实测过，表里的状态码是实测值。

## 一条铁律

> **程序之间互联走 `.dev.test`（集群网关），不要走 `.apikv.com`（Pangolin）。**

`.apikv.com` 是给人用浏览器登的，Pangolin 默认给每个资源挂 SSO 登录墙；
程序拿不到会话，只会收到 `401 Unauthorized`。这不是 Consul/后端的问题，是入口层拦的。

实测三方对照（同一个 Consul API 端点 `/v1/status/leader`）：

| 从哪儿进 | 结果 | 说明 |
|---|---|---|
| 集群内 `consul-server.consul.svc:8500` | `200` | 集群内 DNS，无鉴权 |
| 网关 IP `192.168.3.121` + Host 头 | `200` | 直连 Cilium Gateway，**绕过 Pangolin** |
| `https://consul.dev.test`（经 /etc/hosts） | `200` | 同上，正规写法 |
| `https://consul.apikv.com` | **`401`** | 经 Pangolin，被 badger 中间件拦下 |

判别法：401 的响应体是 `Unauthorized`、`content-type: text/plain`——这是 Traefik/badger 的口径；
Consul 自己的 ACL 拒绝会说 `ACL not found` 或 `Permission denied`。别把入口鉴权错认成后端故障。

## 三层入口的分工

| 入口 | 地址形态 | 谁在用 | 鉴权 |
|---|---|---|---|
| **集群 DNS** | `<svc>.<ns>.svc:<port>` | 集群内 Pod（pre） | 无 |
| **Cilium Gateway** | `<name>.dev.test`（→ `192.168.3.121`） | 本机开发（dev）、内网互联 | 无 |
| **Pangolin** | `<name>.apikv.com` / raw `30001-30003` | 公网访问、人用浏览器 | HTTP 资源默认 SSO |

Gateway 与 Pangolin 是**串联**关系：公网 → Pangolin → Cilium Gateway → Service。
走 `.dev.test` 等于从中间插进去，少一层鉴权，这正是程序需要的。

## 当前寻址表

`services/config/configs/{dev,pre}.yaml` 实际使用的地址：

| 依赖 | dev（本机） | pre（集群内） | 为什么不同 |
|---|---|---|---|
| PostgreSQL | `pg.apikv.com:30001` | 同左 | node3 上，**唯一通路**是 Pangolin raw 口，两边同址 |
| Redis | `redis.apikv.com:30002` | 同左 | 同上 |
| 指标查询（VM） | `http://metrics.apikv.com` | 同左 | node3 上，经 Pangolin；**只有 http**，https 返回 404 |
| OTLP 推送 | `...opentelemetry.svc:4318` | 同左 | 集群内 ClusterIP；**本机注定不通**，见下 |
| Consul | `consul.dev.test:443`（https） | `consul-server.consul.svc:8500` | dev 走网关，pre 走集群 DNS |

注意 PG/Redis/VM 三项两边同址：它们在集群**外**（node3），集群内外都只能经 Pangolin 的
raw 端口进，所以「本地能跑 = 进集群也能跑」。这几项是 TCP 或已有域名，不适用 HTTPRoute。

### 两个例外

**OTLP 在本机无解。** 集群内 collector 只有 ClusterIP，没有 NodePort/LoadBalancer/HTTPRoute；
node3 虽跑着 `otelcol-contrib`（4317/4318），但公网端口被防火墙挡住。本机 `go run` 会每 30s
打一条 `no such host`，**不阻断启动、不影响功能**（PG/Redis/schema/HTTP 全正常，healthz 200）。

嫌吵可以置 `observability.enable: false`。**2026-09-01 起这么做是安全的**：该开关已只管
「推送与埋点」，System 页面的历史曲线由 `metric_query.endpoint` 自治（见提交
`8b95251`）。在此之前两者共用一个开关，关噪音会连带把明明可达的 VictoriaMetrics 查询
一起静默关掉，页面上只剩一排空图——而空图与「一切正常但流量为零」长得一模一样。

**`.dev.test` 需要私有 CA。** 网关证书由 `my-global-root-ca` 签发（SAN: `dev.test`, `*.dev.test`），
Mac 系统信任库里没有，必须显式给 `ca_pem`，否则 curl 返回 `000`（证书校验失败）：

```bash
kubectl get secret global-root-ca-secret -n cert-manager -o jsonpath='{.data.ca\.crt}' | base64 -d
```

`dev.yaml` 的 `discovery.consul.tls.ca_pem` 已内嵌这份 CA。

### 这条链路做过真实注册验证

2026-09-01 用真实 Consul 注册跑通了全生命周期，证明 `.dev.test` 这条路不只是 curl 能通，
Go 的 Consul 客户端也能正常工作（配置 → HTTPRoute → HTTPS + 私有 CA → Consul API）：

```
Service registered with Consul using TTL check  {"ttl": "30s"}
starting ttl pinger                             {"interval": "25s"}
→ Consul 侧: passing 实例 1 个, config-service @ 192.168.3.220:30010, tags [v1 fx ttl]
→ TTL 检查: passing ("ttl check passing")
SIGTERM → deregistering service from consul / ttl pinger stopped gracefully
→ Consul 侧: catalog 返回 [], 干净注销
```

验证用的是最小权限临时令牌（`service "config-service" { policy = "write" }`），
测完连同策略一并删除，catalog 回到 11 个服务。

⚠️ 过程中踩到一个**假故障**值得记一笔：第一次测时 TTL 检查显示 `critical`，
看着像心跳有问题。真因是上一轮的进程还占着 30010，新进程注册成功后立刻
`bind: address already in use` 退出，心跳自然停了。**注册成功不等于进程活着**——
排查 TTL critical 时先确认进程还在，别一头扎进 Consul 配置里。

⚠️ 本机跑仍应带 `CONSUL_ENABLED=false`（`dev-local.sh` 已内置）。上面是专门的验证，
不是日常开发姿势：把 Mac 注册进集群 Consul，集群内客户端可能解析到 `192.168.3.220`，
而 Pod 网段回不来，表现为诡异超时。当前 `config-service` 恰好**没有消费方**
（`routes/{dev,pre}.yaml` 的 `discovery:///` 只指 10 个业务后端，网关找 config 用的是
K8s Service DNS），所以这次验证的爆炸半径是零——换个有消费方的服务就不能这么试。

## 本机 hosts 映射

`/etc/hosts` 已有（**无需改动**，网关 IP 是 `192.168.3.121`）：

```
192.168.3.121  argocd.dev.test consul.dev.test gateway.dev.test search.dev.test shop.dev.test
192.168.3.132  pg.dev.test
192.168.3.122  dragonfly.dev.test
```

⚠️ `nslookup`/`dig` 查 `.dev.test` 会显示「不可解析」——它们查 DNS 服务器，**绕过 hosts 文件**。
验证要用 `dscacheutil -q host -a name consul.dev.test` 或直接 curl。别被误导。

新增服务需要 `.dev.test` 名字时，两步：① 给它的 HTTPRoute 的 `hostnames` 补一条 `<name>.dev.test`；
② 在上面这行追加该名字（同一个网关 IP，不用新起一行）。改 hosts 需要 sudo。

## 上线互联：在 Pangolin 上开一个资源

Pangolin 部署在 `docker-deploy/pangolin`（Traefik + badger）。资源在面板建，也可走 API。

### HTTP 服务

面板 → Resources → Add Resource：填子域名 + site + target。泛解析与泛域名证书已就绪，
**新增子域名零 DNS 操作**。

```bash
# API 建资源
PUT /org/<org>/resource  {"name":"foo","subdomain":"foo","domainId":"domain1","mode":"http"}
```

**⚠️ 给程序调用的资源必须显式关掉 SSO**，否则就是本文开头那个 401：

```bash
POST /resource/:rid  {"sso": false}
```

关掉 SSO 等于把该资源完全公开，所以**优先考虑不上 Pangolin**——内网互联走 `.dev.test` 即可。
只有真需要公网访问时才开资源，并自带鉴权（如本仓的 Machine Token）。

### 非 HTTP 服务（数据库、gRPC 裸 TCP 等）

选 raw 模式，绑一个预留端口。当前已占用：

| 入口 | 用途 |
|---|---|
| `tcp-30001` | PostgreSQL（`pg.apikv.com:30001`） |
| `tcp-30002` | Redis（`redis.apikv.com:30002`） |
| `udp-30003` | 空闲 |

**扩新 raw 端口要同时改三处，少一处不通**：

1. `docker-compose.yml` 的 `ports:` 加 `30004:30004/tcp`；
2. `config/traefik/traefik_config.yml` 的 `entryPoints:` 加 `tcp-30004`（**命名必须是
   `<protocol>-<port>` 格式**）；
3. 云防火墙/安全组放行 `30004/tcp`。

### 探针注意

badger v1.7.0 起按 `User-Agent` 分流：`curl` 看到的状态码与浏览器不同（`401` vs `302`）。
写健康探针时以实际 UA 的返回为准，别拿浏览器行为推断。

## 排查顺序

遇到「网络看着通但功能不对」，按这个顺序排：

1. **走的是 `.apikv.com` 还是 `.dev.test`？** 前者会被 Pangolin SSO 拦 → 401；
2. **是不是私有 CA 没给？** 表现为 curl `000`、Go 侧证书校验失败；
3. **是不是拿 nslookup 验的 `.dev.test`？** 它绕过 hosts，必然显示不可解析；
4. **集群内直连同一后端是什么结果？** 若集群内 200 而外部非 200，问题一定在入口层，不在后端。
