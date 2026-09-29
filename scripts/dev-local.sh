#!/usr/bin/env bash
# 在 Mac 上跑 control-tower。
#
#   scripts/dev-local.sh config     # 起 config 服务
#   scripts/dev-local.sh dev        # 同时起 config 服务与控制台（见 run_dev 上方说明）
#   scripts/dev-local.sh gateway    # 起网关（file 模式，见 run_gateway 上方说明）
#   scripts/dev-local.sh print      # 只渲染配置并打印路径，不启动
#
# config 服务的依赖契约来自 sibling kubernetes 仓：
#   - PostgreSQL: CNPG pg-main，Pangolin remote-dev 入口 pg-dev.apikv.com:30001
#   - Dragonfly: 集群内 dragonfly，Pangolin remote-dev 入口 redis-dev.apikv.com:30005
#   - 账密和 CA 从契约声明的 Kubernetes Secret 读取，不把值写入仓库。
#   - Casdoor 公钥证书取自 Secret config-center/config-center-bootstrap 的 casdoor.pem，
#     与集群 config 服务同源；也可用 CASDOOR_CERTIFICATE_FILE 指定本地文件。
#
# 网关模式的 file 工件从 Config Center 拉取（configctl + operator token），
# 业务后端经 kubectl port-forward 到本机，BFF 会话走 redis-dev.apikv.com。
#
# KUBERNETES_REPO 可覆盖默认路径 ../kubernetes；KUBECONFIG 沿用 kubectl 的默认解析。
# 临时配置文件与目录为 0600/0700，进程退出后删除；port-forward 随脚本退出一起结束。
# 兼容 macOS Bash 3.2。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
KUBERNETES_REPO="${KUBERNETES_REPO:-$ROOT/../kubernetes}"
KUBECTL="${KUBECTL:-kubectl}"
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
# 2026-09-29 起 metrics.apikv.com 经 vmauth 只读，不带 token 返回 401。
# https 查询端的只读 token 默认从 kubernetes 仓 vmauth 组件的 Secret 现取（只写进 0600 临时配置），
# 也可用 METRIC_QUERY_BEARER_TOKEN 直接给。http 端点（集群内 / port-forward 直连 VM）不带 token。
METRIC_QUERY_TOKEN_SECRET="${METRIC_QUERY_TOKEN_SECRET:-victoriametrics/vmauth-credentials}"
METRIC_QUERY_TOKEN_KEY="${METRIC_QUERY_TOKEN_KEY:-read-token}"
CFG=""
CASDOOR_PEM=""
GW_DIR=""
PF_PIDS=""
# dev 模式下后端与控制台各自的进程组号（= 组长 pid）。停要停整组：
# go run 与 pnpm 都会再派生真正的服务进程，只杀父进程会留下孤儿继续占着端口。
DEV_PGIDS=""

stop_dev_groups() {
  local pgid i
  [ -n "$DEV_PGIDS" ] || return 0
  for pgid in $DEV_PGIDS; do kill -TERM -- "-$pgid" 2>/dev/null || true; done
  # 给 3 秒优雅退出，还在的直接 KILL
  for i in 1 2 3 4 5 6; do
    for pgid in $DEV_PGIDS; do kill -0 -- "-$pgid" 2>/dev/null && break; pgid=""; done
    [ -z "$pgid" ] && break
    sleep 0.5
  done
  for pgid in $DEV_PGIDS; do kill -KILL -- "-$pgid" 2>/dev/null || true; done
  DEV_PGIDS=""
}

cleanup() {
  local pid
  stop_dev_groups
  for pid in $PF_PIDS; do kill "$pid" 2>/dev/null || true; done
  [ -n "$CFG" ] && rm -f "$CFG" || true
  [ -n "$CASDOOR_PEM" ] && rm -f "$CASDOOR_PEM" || true
  [ -n "$GW_DIR" ] && rm -rf "$GW_DIR" || true
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
      local metric_tls=false metric_token=""
      case "$METRIC_QUERY_ENDPOINT" in
        https://*)
          metric_tls=true
          metric_token="${METRIC_QUERY_BEARER_TOKEN:-$(secret_value "$METRIC_QUERY_TOKEN_SECRET" "$METRIC_QUERY_TOKEN_KEY")}"
          ;;
      esac
      cat <<EOF
  metric_query:
    endpoint: $(yaml_quote "$METRIC_QUERY_ENDPOINT")
    timeout: 5s
    bearer_token: $(yaml_quote "$metric_token")
    tls:
      enable: $metric_tls
      insecure_skip_verify: false
      ca_pem: ''
EOF
    fi
  } >"$CFG"
}

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
  local token_src=""
  case "$METRIC_QUERY_ENDPOINT" in
    https://*) token_src="｜只读 token 来自 ${METRIC_QUERY_BEARER_TOKEN:+环境变量 METRIC_QUERY_BEARER_TOKEN}"
               [ -n "${METRIC_QUERY_BEARER_TOKEN:-}" ] || token_src="${token_src}Secret $METRIC_QUERY_TOKEN_SECRET" ;;
  esac
  echo "→ 指标查询端：${METRIC_QUERY_ENDPOINT:-（未配置，System 页面不显示历史曲线）}$token_src"
  prepare_casdoor_certificate
}

# ─── 网关本地调试 ───────────────────────────────────────────────────────────
#
# 网关用 CONFIG_SOURCE=file 从一个临时目录读五份工件，来源如下：
#   - public.pem / policies.csv / model.conf / revocations.yaml：
#       Config Center 的 gateway/<GATEWAY_ENV> 键，与集群网关读的是同一份。
#       凭据是 Secret config-center/config-center-operator-<env> 的 operator token
#       （已设 CONFIG_CENTER_SERVICE_TOKEN 时直接用它）；端点默认 https://config-api.apikv.com，
#       CONFIG_CENTER_ENDPOINT 可改成本机 http://localhost:30010。
#   - policies.csv 可用 GATEWAY_POLICIES_FILE 换成本地文件，便于先验证再写回 Config Center。
#     网关只认 act 列字面 POST 的 p 行（authz.validatePolicyRow），有一行不合法就整表拒载、启动失败。
#   - routes.yaml：GATEWAY_ROUTES_SOURCE=template（默认，仓库 routes/<env>.yaml，与本地代码同版本）
#       | config-center（线上键）；或用 GATEWAY_ROUTES_FILE 直接指定文件。
#
# 路由 target 是 direct://<svc>.<ns>.svc:<port>，Mac 解析不了集群 DNS。脚本把它们改写成
# direct://127.0.0.1:<port>，并对每个后端起一条 kubectl port-forward（GATEWAY_PORT_FORWARD=false 关闭）。
# 本机端口已被占用时视为你自己转发过，跳过该后端。
#
# BFF 会话轨（GATEWAY_BFF=false 关闭）：会话存 Dragonfly 的 remote-dev 入口（组件契约的
# REMOTE_HOST:REMOTE_PORT），账密与 CA 取 Secret ecommerce/dragonfly-session，Casdoor client
# 取 ecommerce/casdoor-bff。回调与跳回都是 http://localhost:3000（前端 vite proxy 把 /auth 代到本网关），
# Casdoor 应用的 Redirect URLs 需含 http://localhost:3000/auth/callback。

GATEWAY_ENV="${GATEWAY_ENV:-dev}"
GATEWAY_ROUTES_SOURCE="${GATEWAY_ROUTES_SOURCE:-template}"
GATEWAY_PORT_FORWARD="${GATEWAY_PORT_FORWARD:-true}"
GATEWAY_BFF="${GATEWAY_BFF:-true}"
GATEWAY_HTTP_PORT="${GATEWAY_HTTP_PORT:-8080}"
GATEWAY_FRONTEND_ORIGIN="${GATEWAY_FRONTEND_ORIGIN:-http://localhost:3000}"
# 与 deploy/dev/gateway/deployment.yaml 的 control-tower-gateway-config 保持一致。
GATEWAY_JWT_AUDIENCES="${GATEWAY_JWT_AUDIENCES:-baxf6718e392099b7915}"

port_in_use() { lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1; }

# 从 Config Center 取一个 gateway 键写到文件。键不存在时返回非零。
fetch_gateway_key() {
  local key=$1 out=$2
  "$GW_DIR/configctl" get -namespace gateway -environment "$GATEWAY_ENV" -key "$key" >"$out"
}

prepare_gateway_artifacts() {
  local routes_src
  GW_DIR="$(mktemp -d -t ct-gateway)"
  chmod 700 "$GW_DIR"

  if [ -z "${CONFIG_CENTER_SERVICE_TOKEN:-}" ]; then
    CONFIG_CENTER_SERVICE_TOKEN="$(secret_value "config-center/config-center-operator-$GATEWAY_ENV" token)"
  fi
  export CONFIG_CENTER_SERVICE_TOKEN

  (cd "$ROOT" && go build -o "$GW_DIR/configctl" ./services/config/cmd/configctl) \
    || die "构建 configctl 失败"

  fetch_gateway_key secrets/public.pem "$GW_DIR/public.pem" || die "取 gateway/$GATEWAY_ENV secrets/public.pem 失败"
  if [ -n "${GATEWAY_POLICIES_FILE:-}" ]; then
    [ -r "$GATEWAY_POLICIES_FILE" ] || die "GATEWAY_POLICIES_FILE 不可读: $GATEWAY_POLICIES_FILE"
    cp "$GATEWAY_POLICIES_FILE" "$GW_DIR/policies.csv"
    echo "→ 策略：$GATEWAY_POLICIES_FILE（GATEWAY_POLICIES_FILE 指定，不读 Config Center）"
  else
    fetch_gateway_key policies/policies.csv "$GW_DIR/policies.csv" || die "取 gateway/$GATEWAY_ENV policies/policies.csv 失败"
  fi
  fetch_gateway_key policies/model.conf "$GW_DIR/model.conf" || die "取 gateway/$GATEWAY_ENV policies/model.conf 失败"
  # 撤销名单可缺省：键不存在时网关以空表启动。
  fetch_gateway_key auth/revocations.yaml "$GW_DIR/revocations.yaml" 2>/dev/null \
    || rm -f "$GW_DIR/revocations.yaml"

  if [ -n "${GATEWAY_ROUTES_FILE:-}" ]; then
    [ -r "$GATEWAY_ROUTES_FILE" ] || die "GATEWAY_ROUTES_FILE 不可读: $GATEWAY_ROUTES_FILE"
    cp "$GATEWAY_ROUTES_FILE" "$GW_DIR/routes.src.yaml"
    routes_src="$GATEWAY_ROUTES_FILE"
  else
    case "$GATEWAY_ROUTES_SOURCE" in
      template)
        [ -f "$ROOT/routes/$GATEWAY_ENV.yaml" ] || die "没有路由模板 routes/$GATEWAY_ENV.yaml"
        cp "$ROOT/routes/$GATEWAY_ENV.yaml" "$GW_DIR/routes.src.yaml"
        routes_src="routes/$GATEWAY_ENV.yaml"
        ;;
      config-center)
        fetch_gateway_key routes.yaml "$GW_DIR/routes.src.yaml" || die "取 gateway/$GATEWAY_ENV routes.yaml 失败"
        routes_src="Config Center gateway/$GATEWAY_ENV routes.yaml"
        ;;
      *) die "GATEWAY_ROUTES_SOURCE 只能是 template 或 config-center: $GATEWAY_ROUTES_SOURCE" ;;
    esac
  fi

  # 集群 DNS 目标改写成本机回环，端口不变。
  sed -E 's#direct://[a-z0-9-]+\.[a-z0-9-]+\.svc(\.cluster\.local)?:([0-9]+)#direct://127.0.0.1:\2#g' \
    "$GW_DIR/routes.src.yaml" >"$GW_DIR/routes.yaml"
  grep -Eq '^[[:space:]]*target:[[:space:]]*discovery:///' "$GW_DIR/routes.yaml" \
    && echo "⚠️ 路由里仍有 discovery:/// 目标：本机连不到 Consul 注册的 Pod IP，这些路由会失败" >&2
  echo "→ 网关工件：Config Center gateway/$GATEWAY_ENV（${CONFIG_CENTER_ENDPOINT:-https://config-api.apikv.com}）｜路由来自 $routes_src"
}

# 为路由里每个 direct://<svc>.<ns>.svc:<port> 起一条 port-forward。
start_port_forwards() {
  local svc ns port pid i
  [ "$GATEWAY_PORT_FORWARD" = true ] || { echo "→ 已关闭自动 port-forward（GATEWAY_PORT_FORWARD=false）"; return; }
  while read -r svc ns port; do
    [ -n "$svc" ] || continue
    if port_in_use "$port"; then
      echo "→ :$port 已被占用，视为已转发，跳过 $ns/$svc"
      continue
    fi
    "$KUBECTL" -n "$ns" port-forward "svc/$svc" "$port:$port" >"$GW_DIR/pf-$svc.log" 2>&1 &
    pid=$!
    PF_PIDS="$PF_PIDS $pid"
    i=0
    until port_in_use "$port"; do
      kill -0 "$pid" 2>/dev/null || die "port-forward $ns/$svc 退出：$(tail -1 "$GW_DIR/pf-$svc.log")"
      i=$((i + 1))
      [ "$i" -lt 50 ] || die "port-forward $ns/$svc 10 秒内未就绪"
      sleep 0.2
    done
    echo "→ 转发 $ns/$svc → 127.0.0.1:$port"
  done <<EOF
$(sed -nE 's#.*direct://([a-z0-9-]+)\.([a-z0-9-]+)\.svc(\.cluster\.local)?:([0-9]+).*#\1 \2 \4#p' "$GW_DIR/routes.src.yaml" | sort -u)
EOF
}

# 输出 BFF 会话轨需要的环境变量（KEY=VALUE 每行一条），供 env 使用。
bff_env() {
  local redis_host redis_port redis_user redis_password client_id client_secret
  load_component_contract "$KUBERNETES_REPO/components/dragonfly/component.env"
  redis_host="$REMOTE_HOST"
  redis_port="$REMOTE_PORT"
  [ -n "$redis_host" ] && [ -n "$redis_port" ] || die "Dragonfly 契约缺少 REMOTE_HOST/REMOTE_PORT"
  # 先逐个赋值：写在 printf 参数里的命令替换失败时 set -e 不会中止。
  redis_user="$(secret_value ecommerce/dragonfly-session username)"
  redis_password="$(secret_value ecommerce/dragonfly-session password)"
  client_id="$(secret_value ecommerce/casdoor-bff client-id)"
  client_secret="$(secret_value ecommerce/casdoor-bff client-secret)"
  secret_value ecommerce/dragonfly-session ca.crt >"$GW_DIR/session-ca.crt"
  printf '%s\n' \
    "SESSION_REDIS_ADDR=$redis_host:$redis_port" \
    "SESSION_REDIS_USERNAME=$redis_user" \
    "SESSION_REDIS_PASSWORD=$redis_password" \
    "SESSION_REDIS_TLS=true" \
    "SESSION_REDIS_CA_FILE=$GW_DIR/session-ca.crt" \
    "SESSION_COOKIE_INSECURE=true" \
    "BFF_PUBLIC_BASE_URL=$GATEWAY_FRONTEND_ORIGIN" \
    "BFF_ALLOWED_REDIRECTS=$GATEWAY_FRONTEND_ORIGIN" \
    "CASDOOR_CLIENT_ID=$client_id" \
    "CASDOOR_CLIENT_SECRET=$client_secret"
}

run_gateway() {
  local env_file line
  need "$KUBECTL"
  need lsof
  [ -d "$KUBERNETES_REPO" ] || die "找不到 Kubernetes 仓库: $KUBERNETES_REPO（可设置 KUBERNETES_REPO 覆盖）"
  port_in_use "$GATEWAY_HTTP_PORT" && die ":$GATEWAY_HTTP_PORT 已被占用（可设置 GATEWAY_HTTP_PORT）"

  prepare_gateway_artifacts
  start_port_forwards

  # 运行参数写进 0600 文件再交给 env，避免口令出现在命令行参数里。
  env_file="$GW_DIR/gateway.env"
  : >"$env_file"
  chmod 600 "$env_file"
  if [ "$GATEWAY_BFF" = true ]; then
    bff_env >>"$env_file"
    echo "→ BFF 会话轨：Dragonfly $(sed -n 's/^SESSION_REDIS_ADDR=//p' "$env_file") TLS｜回调 $GATEWAY_FRONTEND_ORIGIN/auth/callback"
  else
    echo "BFF_ENABLED=false" >>"$env_file"
    echo "→ BFF 会话轨已关闭（GATEWAY_BFF=false），只接受 bearer JWT"
  fi
  echo "→ 网关将监听 http://127.0.0.1:$GATEWAY_HTTP_PORT（/healthz、/readyz）"

  cd "$ROOT"
  # 读 env 文件逐行 export；值里可能有空格或 =，不能用 source。
  while IFS= read -r line; do
    [ -n "$line" ] && export "${line?}"
  done <"$env_file"
  CONFIG_SOURCE=file CONFIG_DIR="$GW_DIR" \
  DEPLOYMENT_MODE=dev \
  JWT_ISSUER="$CASDOOR_ISSUER" \
  JWT_AUDIENCES="$GATEWAY_JWT_AUDIENCES" \
  CASDOOR_URL="$CASDOOR_ISSUER" \
  HTTP_PORT=":$GATEWAY_HTTP_PORT" \
  LOG_LEVEL="${LOG_LEVEL:-debug}" \
    go run ./services/gateway/cmd/server
}

# 起 config 服务（前台执行，调用方决定是否放后台）。需要先 prepare_config。
run_config_server() {
  cd "$ROOT"
  # 本机实例不能注册进集群 Consul，否则集群内客户端可能解析到 Mac。
  CONFIG_FILE="$CFG" \
  CASDOOR_CERTIFICATE_FILE="$CASDOOR_CERTIFICATE_FILE" \
  CASDOOR_ISSUER="$CASDOOR_ISSUER" \
  CASDOOR_AUDIENCE="$CASDOOR_AUDIENCE" \
  CONSUL_ENABLED=false \
  go run ./services/config/cmd/server
}

# ─── 后端 + 控制台一起起 ───────────────────────────────────────────────────
#
# 两个进程共用一个终端，输出按 [config] / [web] 加前缀。Ctrl-C 两个一起停；
# 任何一个自己退出，另一个也会被停掉，不会留下「前端还开着、后端早没了」的半套环境。
#
# 端口必须是 30010 与 3005：web/public/config.json 的 apiUrl 写死 30010，
# config 服务的 CORS 白名单与 Casdoor 回调只登记了 localhost:3005。
# 控制台自己的配置是端口被占就顺延（strictPort: false），这里显式加 --strictPort，
# 并在启动前检查，被占就直接报错，不让它悄悄换到 3006 然后登录失败。

CONFIG_HTTP_PORT=30010
WEB_PORT=3005

# 给子进程输出加前缀；awk 逐行 fflush，避免两路输出攒着一起出。
prefix() { awk -v p="$1" '{ print p $0; fflush() }'; }

wait_until() {
  local what=$1 seconds=$2 check=$3 i=0
  until eval "$check"; do
    for pgid in $DEV_PGIDS; do
      kill -0 -- "-$pgid" 2>/dev/null || die "$what 未就绪进程就退出了，看上面的输出"
    done
    i=$((i + 1))
    [ "$i" -lt $((seconds * 2)) ] || die "$what ${seconds} 秒内未就绪"
    sleep 0.5
  done
}

run_dev() {
  local config_pid web_pid pgid
  need pnpm
  need lsof
  need curl
  [ -d "$ROOT/web/node_modules" ] || die "控制台依赖未安装：先 cd web && pnpm install"
  port_in_use "$CONFIG_HTTP_PORT" && die ":$CONFIG_HTTP_PORT 已被占用（是不是已经开着 make config？）：$(lsof -nP -iTCP:"$CONFIG_HTTP_PORT" -sTCP:LISTEN | awk 'NR==2 {print $1" pid "$2}')"
  port_in_use "$WEB_PORT" && die ":$WEB_PORT 已被占用（是不是已经开着 pnpm dev？）：$(lsof -nP -iTCP:"$WEB_PORT" -sTCP:LISTEN | awk 'NR==2 {print $1" pid "$2}')"

  prepare_config

  # 打开作业控制：每个后台作业自成一个进程组，便于整组停止。
  # 代价是终端的 Ctrl-C 只发给脚本自己，所以下面自己接 INT/TERM 再统一收尾。
  set -m
  trap 'echo; echo "→ 正在停止后端与控制台…"; exit 130' INT
  trap 'exit 143' TERM

  run_config_server > >(prefix "[config] ") 2>&1 &
  config_pid=$!
  DEV_PGIDS="$config_pid"

  (cd "$ROOT/web" && exec pnpm dev --strictPort) > >(prefix "[web]    ") 2>&1 &
  web_pid=$!
  DEV_PGIDS="$DEV_PGIDS $web_pid"

  echo "→ 等待后端 http://localhost:$CONFIG_HTTP_PORT/healthz（首次 go run 需要编译）…"
  wait_until "后端" 180 "curl -sf -o /dev/null http://localhost:$CONFIG_HTTP_PORT/healthz"
  wait_until "控制台" 60 "port_in_use $WEB_PORT"
  echo
  echo "✓ 后端 http://localhost:$CONFIG_HTTP_PORT   控制台 http://localhost:$WEB_PORT"
  echo "  打开 http://localhost:$WEB_PORT ；Ctrl-C 同时停止两者"
  echo

  # Bash 3.2 没有 wait -n，轮询两个进程组，任一退出就收尾
  while :; do
    for pgid in $DEV_PGIDS; do
      if ! kill -0 -- "-$pgid" 2>/dev/null; then
        [ "$pgid" = "$config_pid" ] && echo "→ 后端已退出，停止控制台" || echo "→ 控制台已退出，停止后端"
        exit 1
      fi
    done
    sleep 1
  done
}

case "${1:-config}" in
  print)
    prepare_config
    echo "渲染完成：${CFG}（本命令退出后会删除，仅供查看结构）"
    sed -E 's/(password:|bearer_token:).*/\1 ***/' "$CFG"
    ;;

  config)
    prepare_config
    echo "→ 服务将监听 http://127.0.0.1:$CONFIG_HTTP_PORT（只启动后端）"
    echo "→ 控制台不随本命令启动：另开终端 cd web && pnpm dev，或改用 make dev 一起启动"
    run_config_server
    ;;

  dev)
    run_dev
    ;;

  gateway)
    run_gateway
    ;;

  *) echo "用法: $0 [config|dev|gateway|print]"; exit 64 ;;
esac
