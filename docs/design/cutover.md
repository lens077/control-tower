# 切流与回滚手顺（P5）

本文是 control-tower 上线替换旧网关与 config-center 的操作手顺。原则：**并行部署、原子切流、旧栈待命、演练过的回滚**。

## 0. 前置条件（全部满足才进入切流）

- [ ] P4 完成：ecommerce 10 服务已换新 SDK import 并通过 `scripts/verify-quick.sh`；三前端 + 桌面端会话续期实测通过。
- [ ] 发布 tag 的 CI 已产出三镜像（gateway/config/config-web），部署引用不可变 `sha-*` tag 或 digest，禁 `latest`/`dev`。
- [ ] Secrets 就位：
  - `ecommerce/control-tower-config-source-{env}`：网关 selector（key=`routes.yaml`，含网关自己的 per-service token）；
  - `control-tower-gateway-config` ConfigMap 的 `JWT_AUDIENCES` 已填四端真实 client id；
  - 10 服务的 `ecommerce-config-source-{env}` 已换 per-service token（双栈期内旧共享 token 仍有效，保回滚）。
- [ ] Config Center `gateway` namespace 五键就绪：`routes.yaml`（新）、`auth/revocations.yaml`（新，可为空表）、旧三键不动；**旧 `config.yaml` 键冻结**。
- [ ] Casdoor access token TTL 已调至 15 分钟并在 dev 验证四端续期。

## 1. config 服务切流（先于网关）

服务名/域名/namespace 全部不变，切流 = 既有 Deployment 滚动换镜像：

```bash
kubectl apply -f deploy/<env>/config/          # image 已指向 control-tower-config@sha-*
kubectl -n config-center rollout status deploy/config-center
```

验收：web 控制台 CRUD/历史/回滚正常；`make test-crossversion` 等价的线上抽查（旧 SDK 服务重启一台，确认 GetKey/Watch 正常）；`machine_token_legacy_hits` 开始计数（说明双栈生效）。

回滚：`kubectl apply` 旧 config-center 仓的对应清单（镜像换回），数据层无迁移不兼容（00002 是纯增表）。

## 2. 网关并行部署

```bash
kubectl apply -f deploy/<env>/gateway/
kubectl -n ecommerce rollout status deploy/control-tower-gateway
kubectl -n ecommerce port-forward deploy/control-tower-gateway 18080:8080 &
curl -sf http://127.0.0.1:18080/readyz        # 必须 ok（路由+公钥+Casbin+resolver 快照）
```

预检（不切流量）：port-forward 直打新网关跑全路由 smoke（登录、商品、下单、支付回调匿名路径、telemetry、`/config.v1.*` 应 404）。

## 3. 网关切流（原子）

dev/prod（LoadBalancer Service）与 pre（同 Service，HTTPRoute 指向该 Service，无需动 HTTPRoute）统一为 selector 原子切换：

```bash
kubectl -n ecommerce patch service ecommerce-gateway-service \
  -p '{"spec":{"selector":{"app":"control-tower-gateway"}}}'
```

**旧网关 Deployment 保持运行（缩容为 1 可以，不删除）**，进入烘烤期。

## 4. 烘烤期（≥48h）观察清单

- 网关：`X-Error-Reason` 分布（TOKEN_* 突增=鉴权回归）、5xx 率、p95、`consul empty instance` 告警、撤销名单新鲜度。
- 前端：三端登录/续期/支付回调全链路回归；`TOKEN_EXPIRED` 401 率。
- config：`machine_token_legacy_hits` 趋势（应随 10 服务滚动趋零）；WatchKeys 断连率。
- 演练（烘烤期内必须做一次）：
  - **撤销演练**：config UI 写撤销条目 → 秒级 401 → 前端静默续期恢复；
  - **冷回滚演练**（终裁 P0-A/B 的最终证明）：selector 切回 `app: ecommerce-gateway` → 流量正常 → **强制重启旧网关 Pod** → 旧网关从冻结的 `config.yaml` 键正常起表 → 再切回新网关。

## 5. 回滚

任一环节异常：

```bash
kubectl -n ecommerce patch service ecommerce-gateway-service \
  -p '{"spec":{"selector":{"app":"ecommerce-gateway"}}}'
```

- 旧网关消费的旧四键全程冻结未动；旧镜像/Deployment/构建源（`gateway-backup-*` 与 ecommerce 仓）在 P6 前全部保留。
- 10 服务回滚：双栈期内旧共享 token 一直有效，服务侧无需任何操作。

## 6. 退役条件（进入 P6 的门）

- 烘烤期满且无回滚触发；
- `machine_token_legacy_hits` 连续 7 天为零 → 移除 `CONFIG_CENTER_SERVICE_TOKEN`（关闭共享 token 死线）；
- 冷回滚演练通过记录在案；
- 之后才执行：删除 ecommerce/gateway 目录、matrix 改 external、structcheck 切换、旧 `config.yaml` 键归档删除。
