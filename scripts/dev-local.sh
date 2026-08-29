#!/usr/bin/env bash
# 在 Mac 上跑 control-tower，数据面走 node3（Pigsty）的 Pangolin 隧道口。
#
#   scripts/dev-local.sh config     # 起 config 服务
#   scripts/dev-local.sh gateway    # 起网关（file 模式；见下方「为什么不用 discovery」）
#   scripts/dev-local.sh print      # 只渲染配置并打印路径，不启动
#
# 2026-08-29 重写。旧版把 PostgreSQL 当成集群内的 CNPG、凭据从 config-center 的
# Secret 取，这两条现在都不成立：
#   - `postgresql` 与 `config-center` 两个 ns 在当前集群都已不存在；
#   - PostgreSQL 与 Redis 都在集群外的 node3，唯一通路是 node1 Pangolin 的 raw 端口
#     pg.apikv.com:30001 / redis.apikv.com:30002 —— 本机与集群用的是同一个地址，
#     所以本地能跑就等于进集群也能跑，不再需要端口转发。
#
# 凭据优先取本地 services/config/configs/dev.yaml（已 gitignore，0600）；该文件缺失时
# 从 node3 的 .credentials-extra 渲染到 mktemp 文件（0600），退出即删——不进仓库、不进日志。
# 兼容 macOS Bash 3.2。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SSH_HOST="${SSH_HOST:-node3}"
DEV_CONFIG="$ROOT/services/config/configs/dev.yaml"
PG_HOST="${PG_HOST:-pg.apikv.com}"
PG_PORT="${PG_PORT:-30001}"
REDIS_HOST="${REDIS_HOST:-redis.apikv.com}"
REDIS_PORT="${REDIS_PORT:-30002}"
CFG=""

cleanup() { [ -n "$CFG" ] && rm -f "$CFG" || true; }
trap cleanup EXIT

need() { command -v "$1" >/dev/null || { echo "缺少 $1"; exit 1; }; }
need ssh
need go

# 在 node3 上执行 SQL。SQL 走 stdin，避免 ssh → su → psql 三层引号转义。
pgq() { ssh -o BatchMode=yes "$SSH_HOST" "su - postgres -c 'psql -d ecommerce -At'"; }

# 从本地 dev.yaml 派生一份「本地跑」的配置：
# 只改一处——关掉 observability。它的三个 OTLP 端点是集群内 DNS（*.svc），
# 在 Mac 上解析不了，开着会每 30s 打一条 "failed to upload metrics: no such host"。
# 其余（含 PG/Redis 的公网地址与 TLS）逐字沿用，保证「本地跑的就是要部署的」。
derive_from_dev_config() {
  CFG="$(mktemp -t ct-config)"; chmod 600 "$CFG"
  sed 's/^  enable: true$/  enable: false/' "$DEV_CONFIG" > "$CFG"
}

# dev.yaml 不在时的兜底：从 node3 现取凭据与 CA，渲染一份最小配置。
render_from_node3() {
  local creds pgpw pguser pgdb rpw ca
  creds="$(ssh -o BatchMode=yes "$SSH_HOST" 'cat /root/pigsty-deploy/.credentials-extra')" || {
    echo "取不到 node3 的 .credentials-extra"; exit 1; }
  pguser="$(printf '%s\n' "$creds" | sed -n 's/^pg_app_user=//p' | head -1)"
  pgpw="$(printf '%s\n' "$creds"  | sed -n 's/^pg_app_password=//p' | head -1)"
  pgdb="$(printf '%s\n' "$creds"  | sed -n 's/^pg_database=//p' | head -1)"
  rpw="$(printf '%s\n' "$creds"   | sed -n 's/^redis_password=//p' | head -1)"
  ca="$(ssh -o BatchMode=yes "$SSH_HOST" 'cat /etc/pki/ca.crt')"
  [ -z "$pgpw" ] && { echo "取不到 PG 口令"; exit 1; }

  CFG="$(mktemp -t ct-config)"; chmod 600 "$CFG"
  {
    echo "# 本地开发渲染件（临时文件，退出即删）。集群内形态见 services/config/configs/pre.yaml。"
    cat <<EOF
server:
  addr: 0.0.0.0:30010
  http: { read_timeout: 5s, idle_timeout: 60s }
  cors:
    allowed_origins:
      - http://localhost:3005
data:
  database:
    postgres:
      host: ${PG_HOST}
      port: ${PG_PORT}
      user: "${pguser:-app}"
      password: "${pgpw}"
      db_name: "${pgdb:-ecommerce}"
      timezone: "Asia/Shanghai"
      tls:
        enable: true
        # 证书 SAN 已含 pg.apikv.com（2026-08-29 补签），可以严格校验。
        # 见 pigsty-deploy 仓 cert-san-resign.md。
        ssl_mode: "verify-full"
        ca_pem: |
$(printf '%s\n' "$ca" | sed 's/^/          /')
      pool: { max_conns: 10, min_conns: 2, max_conn_lifetime: 1h, max_conn_idle_time: 5m, ping_timeout: 5s }
  cache:
    redis:
      presence: { enabled: true, key_prefix: "control-tower:presence-local", ttl: 90s }
      host: ${REDIS_HOST}
      port: ${REDIS_PORT}
      username: ""
      password: "${rpw}"
      db: 0
      dial_timeout: 5s
      read_timeout: 3s
      write_timeout: 3s
      pool_size: 5
      min_idle_conns: 1
      tls:
        # 同上：SAN 已含 redis.apikv.com，不需要 insecure_skip_verify。
        enable: true
        insecure_skip_verify: false
        ca_pem: |
$(printf '%s\n' "$ca" | sed 's/^/          /')
log:
  framework: { format: console, log_level: info, error_level: error }
  application: { format: console, level: debug }
observability:
  enable: false
EOF
  } > "$CFG"
}

prepare_config() {
  if [ -f "$DEV_CONFIG" ]; then
    derive_from_dev_config
    echo "→ 配置来源：${DEV_CONFIG}（本地副本已关掉 observability）"
  else
    render_from_node3
    echo "→ 配置来源：node3 现取凭据渲染（$DEV_CONFIG 不存在）"
  fi
}

case "${1:-config}" in
  print)
    prepare_config
    echo "渲染完成：${CFG}（本命令退出后会删除，仅供查看结构）"
    sed 's/\(password:\).*/\1 ***/' "$CFG"
    ;;

  config)
    prepare_config
    echo "→ PG ${PG_HOST}:${PG_PORT} TLS｜Redis ${REDIS_HOST}:${REDIS_PORT} TLS｜均在 node3，经 node1 Pangolin"
    echo "→ 服务将监听 http://127.0.0.1:30010（web 控制台 pnpm dev 在 3005，已在 CORS 白名单）"
    cd "$ROOT"
    # CONSUL_ENABLED=false 是硬要求：本机实例若注册进集群 Consul，
    # 集群内客户端可能把流量解析到你的 Mac（Pod 网段回不来，表现为诡异超时）。
    CONFIG_FILE="$CFG" \
    CONSUL_ENABLED=false \
    go run ./services/config/cmd/server
    ;;

  gateway)
    # 为什么不用 discovery：Consul 里注册的是 Pod IP（10.244.x.x），Mac 路由不到。
    # 因此本地跑网关用 file 模式 + direct:// 目标，后端自己按需 kubectl port-forward。
    DIR="${GATEWAY_CONFIG_DIR:-/tmp/ct-gateway-local}"
    mkdir -p "$DIR"
    # 网关配置存在 node3 的 config.entry 表里（PG 已从集群搬到 node3）。
    pgq > "$DIR/public.pem" <<'SQL'
SELECT value FROM config.entry WHERE namespace='gateway' AND environment='dev' AND key='secrets/public.pem';
SQL
    pgq > "$DIR/policies.csv" <<'SQL'
SELECT value FROM config.entry WHERE namespace='gateway' AND environment='dev' AND key='policies/policies.csv';
SQL
    pgq > "$DIR/model.conf" <<'SQL'
SELECT value FROM config.entry WHERE namespace='gateway' AND environment='dev' AND key='policies/model.conf';
SQL
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
