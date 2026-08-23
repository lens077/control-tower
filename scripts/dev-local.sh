#!/usr/bin/env bash
# 在 Mac 上跑 control-tower，依赖直连内网集群。
#
#   scripts/dev-local.sh config     # 起 config 服务（PG 端口转发 + Dragonfly/Consul LAN 直连）
#   scripts/dev-local.sh gateway    # 起网关（file 模式；见下方「为什么不用 discovery」）
#   scripts/dev-local.sh print      # 只渲染配置并打印路径，不启动
#
# 凭据全部运行时从集群 Secret 取，渲染进 mktemp 文件（0600），退出即删——不进仓库、不进日志。
# 兼容 macOS Bash 3.2。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PG_LOCAL_PORT="${PG_LOCAL_PORT:-15432}"
CONSUL_LAN="${CONSUL_LAN:-192.168.3.120:8500}"      # consul-expose-servers LoadBalancer
DRAGONFLY_LAN="${DRAGONFLY_LAN:-192.168.3.122}"      # cilium-gateway-dragonfly-gateway（TLS 6380）
DRAGONFLY_PORT="${DRAGONFLY_PORT:-6380}"
CFG=""; PF_PID=""

cleanup() {
  [ -n "$PF_PID" ] && kill "$PF_PID" 2>/dev/null || true
  [ -n "$CFG" ] && rm -f "$CFG" || true
}
trap cleanup EXIT

need() { command -v "$1" >/dev/null || { echo "缺少 $1"; exit 1; }; }
need kubectl

secret() { kubectl -n "$1" get secret "$2" -o jsonpath="{.data.$3}" 2>/dev/null | base64 -d; }

start_pg_forward() {
  kubectl -n postgresql port-forward svc/pg-main-rw "$PG_LOCAL_PORT:5432" >/tmp/ct-pf-pg.log 2>&1 &
  PF_PID=$!
  for i in $(seq 1 20); do nc -z 127.0.0.1 "$PG_LOCAL_PORT" 2>/dev/null && return 0; sleep 0.5; done
  echo "PG 端口转发未就绪，见 /tmp/ct-pf-pg.log"; exit 1
}

render_config() {
  # PG 口令取自集群内 bootstrap（与线上同一份），失败则退回 CNPG app 用户 Secret。
  local pgpw rpw
  pgpw="$(kubectl -n config-center get secret config-center-bootstrap -o jsonpath='{.data.config\.yaml}' 2>/dev/null \
          | base64 -d | sed -n '/postgres:/,/timezone/p' | sed -n 's/^ *password: *//p' | head -1 | tr -d '"')"
  [ -z "$pgpw" ] && pgpw="$(secret postgresql pg-main-app password)"
  rpw="$(secret dragonfly dragonfly-password-secret password)"
  [ -z "$pgpw" ] && { echo "取不到 PG 口令"; exit 1; }

  CFG="$(mktemp -t ct-config)"; chmod 600 "$CFG"
  cat > "$CFG" <<EOF
# 本地开发渲染件（临时文件，退出即删）。集群内形态见 services/config/configs/config.yaml。
server:
  addr: 0.0.0.0:30010
  http: { read_timeout: 5s, idle_timeout: 60s }
  cors:
    allowed_origins:
      - http://localhost:3005
  tls: { enable: false, cert_pem: "", key_pem: "", client_ca_pem: "", require_client_cert: false }
data:
  database:
    postgres:
      # ClusterIP-only，必须端口转发（本脚本已起）。
      host: 127.0.0.1
      port: ${PG_LOCAL_PORT}
      user: app
      password: "${pgpw}"
      db_name: ecommerce
      tls: { enable: false, ssl_mode: "disable", ca_pem: "" }
      timezone: "Asia/Shanghai"
      pool: { max_conns: 10, min_conns: 2, max_conn_lifetime: 1h, max_conn_idle_time: 5m, ping_timeout: 5s }
  cache:
    redis:
      # LAN 直连 Cilium Gateway 的 TLS 端口（证书按 IP 访问不匹配，故 skip verify）。
      presence: { enabled: true, key_prefix: "control-tower:presence-local", ttl: 90s }
      host: ${DRAGONFLY_LAN}
      port: ${DRAGONFLY_PORT}
      tls: { enable: true, insecure_skip_verify: true, ca_pem: "" }
      username: ""
      password: "${rpw}"
      db: 0
      dial_timeout: 5s
      read_timeout: 3s
      write_timeout: 3s
      pool_size: 5
      min_idle_conns: 1
log:
  framework: { format: console, log_level: info, error_level: error }
  application: { format: console, level: debug }
observability:
  enable: false
discovery:
  consul:
    # 只为「能查询目录」；注册由 CONSUL_ENABLED=false 关掉（见下）。
    addr: ${CONSUL_LAN}
    health_check: false
    scheme: http
    check: { ttl: { duration: "30s", ping_interval: 10s } }
    tls: { enable: false, insecure_skip_verify: true, ca_pem: "" }
EOF
}

case "${1:-config}" in
  print)
    start_pg_forward; render_config
    echo "渲染完成：${CFG}（本命令退出后会删除，仅供查看结构）"
    sed 's/\(password:\).*/\1 ***/' "$CFG"
    ;;

  config)
    start_pg_forward; render_config
    echo "→ PG 经 127.0.0.1:${PG_LOCAL_PORT}（转发）｜Redis ${DRAGONFLY_LAN}:${DRAGONFLY_PORT} TLS｜Consul ${CONSUL_LAN}"
    echo "→ 服务将监听 http://127.0.0.1:30010（web 控制台 pnpm dev 在 3005，已在 CORS 白名单）"
    # CONSUL_ENABLED=false 是硬要求：本机实例若注册进集群 Consul，
    # 集群内客户端可能把流量解析到你的 Mac（pod 网段回不来，表现为诡异超时）。
    cd "$ROOT"
    CONFIG_FILE="$CFG" \
    CONSUL_ENABLED=false \
    CONSUL_HTTP_ADDR="$CONSUL_LAN" \
    CONSUL_HTTP_TOKEN="$(secret config-center consul-config-center-token CONSUL_HTTP_TOKEN)" \
    CONFIG_CENTER_SERVICE_TOKEN="$(secret config-center config-center-iam service-token)" \
    go run ./services/config/cmd/server
    ;;

  gateway)
    # 为什么不用 discovery：Consul 里注册的是 Pod IP（10.244.x.x），Mac 路由不到。
    # 因此本地跑网关用 file 模式 + direct:// 目标，后端自己按需 kubectl port-forward。
    DIR="${GATEWAY_CONFIG_DIR:-/tmp/ct-gateway-local}"
    mkdir -p "$DIR"
    kubectl -n postgresql exec pg-main-1 -c postgres -- psql -U postgres -d ecommerce -Atc \
      "SELECT value FROM config.entry WHERE namespace='gateway' AND environment='dev' AND key='secrets/public.pem'" > "$DIR/public.pem"
    kubectl -n postgresql exec pg-main-1 -c postgres -- psql -U postgres -d ecommerce -Atc \
      "SELECT value FROM config.entry WHERE namespace='gateway' AND environment='dev' AND key='policies/policies.csv'" > "$DIR/policies.csv"
    kubectl -n postgresql exec pg-main-1 -c postgres -- psql -U postgres -d ecommerce -Atc \
      "SELECT value FROM config.entry WHERE namespace='gateway' AND environment='dev' AND key='policies/model.conf'" > "$DIR/model.conf"
    [ -f "$DIR/routes.yaml" ] || {
      cp "$ROOT/routes/dev.yaml" "$DIR/routes.yaml"
      echo "已复制 routes 模板到 $DIR/routes.yaml —— 把要打的后端 target 改成 direct://127.0.0.1:<你转发的端口>"
      echo "例如： kubectl -n ecommerce port-forward svc/ecommerce-user-service 30001:30001"
    }
    cd "$ROOT"
    CONFIG_SOURCE=file CONFIG_DIR="$DIR" \
    JWT_ISSUER=https://casdoor.apikv.com \
    JWT_AUDIENCES=a36e6718e392099b7915 \
    CASDOOR_URL=https://casdoor.apikv.com \
    HTTP_PORT=:8080 LOG_LEVEL=debug \
    go run ./services/gateway/cmd/server
    ;;

  *) echo "用法: $0 [config|gateway|print]"; exit 64 ;;
esac
