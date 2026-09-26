#!/usr/bin/env bash
# 在 Mac 上跑 control-tower。
#
#   scripts/dev-local.sh config     # 起 config 服务
#   scripts/dev-local.sh gateway    # 起网关（file 模式；见下方「为什么不用 discovery」）
#   scripts/dev-local.sh print      # 只渲染配置并打印路径，不启动
#
# config 服务的依赖契约来自 sibling kubernetes 仓：
#   - PostgreSQL: CNPG pg-main，Pangolin remote-dev 入口 pg-dev.apikv.com:30001
#   - Dragonfly: 集群内 dragonfly，Pangolin remote-dev 入口 redis-dev.apikv.com:30005
#   - 账密和 CA 从契约声明的 Kubernetes Secret 读取，不把值写入仓库。
#   - Casdoor 公钥证书取自 Secret config-center/config-center-bootstrap 的 casdoor.pem，
#     与集群 config 服务同源；也可用 CASDOOR_CERTIFICATE_FILE 指定本地文件。
#
# KUBERNETES_REPO 可覆盖默认路径 ../kubernetes；KUBECONFIG 沿用 kubectl 的默认解析。
# 临时配置文件为 0600，进程退出后删除。
# 兼容 macOS Bash 3.2。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
KUBERNETES_REPO="${KUBERNETES_REPO:-$ROOT/../kubernetes}"
KUBECTL="${KUBECTL:-kubectl}"
SSH_HOST="${SSH_HOST:-node3}"
# 控制台登录用的 Casdoor 公钥证书。集群 config 服务挂的是同一个 Secret 键，
# 内容与 https://casdoor.apikv.com/.well-known/jwks 中 kid=lens 的 x5c 一致。
CASDOOR_CERT_SECRET="${CASDOOR_CERT_SECRET:-config-center/config-center-bootstrap}"
CASDOOR_CERT_KEY="${CASDOOR_CERT_KEY:-casdoor.pem}"
# 与 deploy/{dev,pre}/config/deployment.yaml、web/public/config.json 保持一致。
CASDOOR_ISSUER="${CASDOOR_ISSUER:-https://casdoor.apikv.com}"
CASDOOR_AUDIENCE="${CASDOOR_AUDIENCE:-baxf6718e392099b7915}"
# System 页面历史曲线的指标查询端（VictoriaMetrics PromQL HTTP API），只给主机名、不带路径。
# 2026-09-23 实测：https://metrics.apikv.com 返回 200（公共证书，无需 ca_pem），
# http 入口改为 302 跳转 https。显式设为空串则不渲染 metric_query，页面只显示即时值。
METRIC_QUERY_ENDPOINT="${METRIC_QUERY_ENDPOINT-https://metrics.apikv.com}"
CFG=""
CASDOOR_PEM=""

cleanup() {
  [ -n "$CFG" ] && rm -f "$CFG" || true
  [ -n "$CASDOOR_PEM" ] && rm -f "$CASDOOR_PEM" || true
}
trap cleanup EXIT

die() { echo "错误: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null || die "缺少 $1"; }

need go

# macOS 的 base64 用 -D，Linux/GNU coreutils 用 -d。
b64decode() {
  if base64 -D </dev/null >/dev/null 2>&1; then
    base64 -D
  else
    base64 -d
  fi
}

# 用 YAML 单引号包住凭据，并按 YAML 规则转义单引号。
yaml_quote() {
  printf "'%s'" "$(printf '%s' "$1" | sed "s/'/''/g")"
}

# 从 Kubernetes Secret 读取一个 base64 字段。只返回解码后的值，不打印值。
secret_value() {
  local ref=$1 key=$2 ns name jsonpath_key encoded
  ns=${ref%%/*}
  name=${ref#*/}
  jsonpath_key=${key//./\\.}
  encoded=$("$KUBECTL" -n "$ns" get secret "$name" -o "jsonpath={.data.$jsonpath_key}") \
    || die "读取 Secret $ref 的 $key 字段失败"
  [ -n "$encoded" ] || die "Secret $ref 缺少 $key 字段"
  printf '%s' "$encoded" | b64decode \
    || die "解码 Secret $ref 的 $key 字段失败"
}

# 解析 component.env 中的 CA_REF，例如 secret:postgresql/pg-main-ca:ca.crt。
ca_value() {
  local ref=$1 kind rest secret_ref key
  kind=${ref%%:*}
  rest=${ref#*:}
  [ "$kind" = secret ] || die "暂不支持 CA_REF=$ref"
  secret_ref=${rest%%:*}
  key=${rest#*:}
  secret_value "$secret_ref" "$key"
}

load_component_contract() {
  local file=$1
  [ -f "$file" ] || die "缺少组件契约文件: $file"
  unset ID NAMESPACE CRED_SECRET CRED_KEYS CRED_USER CA_REF REMOTE_HOST REMOTE_PORT REMOTE_SCHEME REMOTE_CA
  # shellcheck disable=SC1090
  source "$file"
}

render_from_kubernetes() {
  local pg_meta="$KUBERNETES_REPO/components/postgres/component.env"
  local redis_meta="$KUBERNETES_REPO/components/dragonfly/component.env"
  local pg_host pg_port pg_scheme pg_cred_secret pg_cred_keys pg_user_key pg_password_key pg_ca_ref
  local redis_host redis_port redis_scheme redis_cred_secret redis_cred_keys redis_user_key redis_password_key redis_ca_ref
  local pg_user pg_password pg_ca_pem redis_user redis_password redis_ca_pem

  [ -d "$KUBERNETES_REPO" ] || die "找不到 Kubernetes 仓库: $KUBERNETES_REPO（可设置 KUBERNETES_REPO 覆盖）"
  need "$KUBECTL"

  load_component_contract "$pg_meta"
  pg_host="$REMOTE_HOST"
  pg_port="$REMOTE_PORT"
  pg_scheme="$REMOTE_SCHEME"
  pg_cred_secret="$CRED_SECRET"
  pg_cred_keys="$CRED_KEYS"
  pg_ca_ref="$CA_REF"

  load_component_contract "$redis_meta"
  redis_host="$REMOTE_HOST"
  redis_port="$REMOTE_PORT"
  redis_scheme="$REMOTE_SCHEME"
  redis_cred_secret="$CRED_SECRET"
  redis_cred_keys="$CRED_KEYS"
  redis_user_key="$CRED_USER"
  redis_ca_ref="$CA_REF"

  pg_user_key=${pg_cred_keys%% *}
  pg_password_key=${pg_cred_keys#* }
  [ -n "$pg_user_key" ] && [ -n "$pg_password_key" ] \
    || die "PostgreSQL 契约的 CRED_KEYS 不完整: $pg_cred_keys"
  [ -n "$redis_user_key" ] || redis_user_key=default
  redis_password_key=${redis_cred_keys%% *}

  pg_user="$(secret_value "$pg_cred_secret" "$pg_user_key")"
  pg_password="$(secret_value "$pg_cred_secret" "$pg_password_key")"
  pg_ca_pem="$(ca_value "$pg_ca_ref")"
  redis_password="$(secret_value "$redis_cred_secret" "$redis_password_key")"
  redis_ca_pem="$(ca_value "$redis_ca_ref")"
  redis_user="$redis_user_key"

  [ "$pg_scheme" = postgresql ] || die "PostgreSQL 当前协议不是 postgresql: $pg_scheme"
  [ "$redis_scheme" = rediss ] || die "Dragonfly 当前协议不是 rediss: $redis_scheme"
  [ -n "$pg_host" ] && [ -n "$pg_port" ] && [ -n "$redis_host" ] && [ -n "$redis_port" ] \
    || die "Kubernetes 组件契约缺少远程地址"

  CFG="$(mktemp -t ct-config)"
  chmod 600 "$CFG"
  {
    echo "# 本地开发渲染件（临时文件，退出即删）。"
    cat <<EOF
server:
  addr: 0.0.0.0:30010
  http:
    read_timeout: 5s
    idle_timeout: 60s
  cors:
    allowed_origins:
      - http://localhost:3005

data:
  database:
    postgres:
      host: $(yaml_quote "$pg_host")
      port: $pg_port
      user: $(yaml_quote "$pg_user")
      password: $(yaml_quote "$pg_password")
      db_name: ecommerce
      timezone: Asia/Shanghai
      tls:
        enable: true
        # CNPG 对外证书 SAN 不含 Pangolin 域名，只能 verify-ca。
        ssl_mode: verify-ca
        ca_pem: |
$(printf '%s\n' "$pg_ca_pem" | sed 's/^/          /')
      pool:
        max_conns: 10
        min_conns: 2
        max_conn_lifetime: 1h
        max_conn_idle_time: 5m
        ping_timeout: 5s
  cache:
    redis:
      presence:
        enabled: true
        key_prefix: control-tower:presence-local
        ttl: 90s
      host: $(yaml_quote "$redis_host")
      port: $redis_port
      username: $(yaml_quote "$redis_user")
      password: $(yaml_quote "$redis_password")
      db: 0
      dial_timeout: 5s
      read_timeout: 3s
      write_timeout: 3s
      pool_size: 5
      min_idle_conns: 1
      tls:
        enable: true
        insecure_skip_verify: false
        ca_pem: |
$(printf '%s\n' "$redis_ca_pem" | sed 's/^/          /')

observability:
  enable: false
EOF
    # metric_query 与 enable 相互独立：enable=false 只关 OTLP 推送，不影响历史曲线。
    if [ -n "$METRIC_QUERY_ENDPOINT" ]; then
      local metric_tls=false
      case "$METRIC_QUERY_ENDPOINT" in https://*) metric_tls=true ;; esac
      cat <<EOF
  metric_query:
    endpoint: $(yaml_quote "$METRIC_QUERY_ENDPOINT")
    timeout: 5s
    tls:
      enable: $metric_tls
      insecure_skip_verify: false
      ca_pem: ''
EOF
    fi
  } >"$CFG"
}

# 网关模式仍从 node3 的 PostgreSQL 查询配置；config 模式不依赖 SSH。
pgq() { ssh -o BatchMode=yes "$SSH_HOST" "su - postgres -c 'psql -d ecommerce -At'"; }

# 准备 Casdoor 公钥证书文件。已显式设置 CASDOOR_CERTIFICATE_FILE 时直接沿用。
prepare_casdoor_certificate() {
  if [ -n "${CASDOOR_CERTIFICATE_FILE:-}" ]; then
    [ -r "$CASDOOR_CERTIFICATE_FILE" ] || die "CASDOOR_CERTIFICATE_FILE 不可读: $CASDOOR_CERTIFICATE_FILE"
    echo "→ Casdoor 证书：$CASDOOR_CERTIFICATE_FILE（CASDOOR_CERTIFICATE_FILE 指定）"
    return
  fi
  CASDOOR_PEM="$(mktemp -t ct-casdoor)"
  chmod 600 "$CASDOOR_PEM"
  secret_value "$CASDOOR_CERT_SECRET" "$CASDOOR_CERT_KEY" >"$CASDOOR_PEM"
  grep -q 'BEGIN CERTIFICATE' "$CASDOOR_PEM" \
    || die "Secret $CASDOOR_CERT_SECRET 的 $CASDOOR_CERT_KEY 不是 PEM 证书"
  CASDOOR_CERTIFICATE_FILE="$CASDOOR_PEM"
  echo "→ Casdoor 证书：Secret $CASDOOR_CERT_SECRET 的 $CASDOOR_CERT_KEY｜issuer $CASDOOR_ISSUER｜audience $CASDOOR_AUDIENCE"
}

prepare_config() {
  render_from_kubernetes
  echo "→ 配置来源：$KUBERNETES_REPO 的 postgres/dragonfly 契约 + Kubernetes Secret"
  echo "→ PG pg-dev.apikv.com:30001 TLS｜Dragonfly redis-dev.apikv.com:30005 TLS"
  echo "→ 指标查询端：${METRIC_QUERY_ENDPOINT:-（未配置，System 页面不显示历史曲线）}"
  prepare_casdoor_certificate
}

case "${1:-config}" in
  print)
    prepare_config
    echo "渲染完成：${CFG}（本命令退出后会删除，仅供查看结构）"
    sed -E 's/(password:).*/\1 ***/' "$CFG"
    ;;

  config)
    prepare_config
    echo "→ 服务将监听 http://127.0.0.1:30010（web 控制台 pnpm dev 在 3005，已在 CORS 白名单）"
    cd "$ROOT"
    # 本机实例不能注册进集群 Consul，否则集群内客户端可能解析到 Mac。
    CONFIG_FILE="$CFG" \
    CASDOOR_CERTIFICATE_FILE="$CASDOOR_CERTIFICATE_FILE" \
    CASDOOR_ISSUER="$CASDOOR_ISSUER" \
    CASDOOR_AUDIENCE="$CASDOOR_AUDIENCE" \
    CONSUL_ENABLED=false \
    go run ./services/config/cmd/server
    ;;

  gateway)
    need ssh
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
    JWT_AUDIENCES=baxf6718e392099b7915 \
    CASDOOR_URL=https://casdoor.apikv.com \
    HTTP_PORT=:8080 LOG_LEVEL=debug \
    go run ./services/gateway/cmd/server
    ;;

  *) echo "用法: $0 [config|gateway|print]"; exit 64 ;;
esac
