#!/usr/bin/env bash
# 跨版本实测编排（P2 验收）：旧 SDK v0.1.0 → 新 config 服务。
# 场景：a legacy 双栈 / b per-service 范围 / c 吊销断流时延 / d 两代重叠。
# 兼容 macOS Bash 3.2；throwaway 容器用完即删。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PROBE_DIR="$ROOT/services/config/tests/oldsdk"
PG_NAME=ct-xver-pg
REDIS_NAME=ct-xver-redis
SERVER_LOG="$(mktemp -t ct-xver-server)"
LEGACY_TOKEN="legacy-test-token"
TOKEN_A="ct_crossversion_token_a_0000000000000000000000"
TOKEN_B="ct_crossversion_token_b_0000000000000000000000"
SERVER_PID=""

cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
  docker rm -f "$PG_NAME" "$REDIS_NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

psqlc() { docker exec -i "$PG_NAME" psql -U postgres -d postgres -tA -c "$1"; }

sha256hex() { printf '%s' "$1" | shasum -a 256 | awk '{print $1}'; }

step() { printf '\n== %s\n' "$1"; }

step "起 throwaway Postgres/Redis"
docker rm -f "$PG_NAME" "$REDIS_NAME" >/dev/null 2>&1 || true
docker run -d --name "$PG_NAME" -p 15432:5432 -e POSTGRES_PASSWORD=postgres postgres:18rc1-alpine3.22 >/dev/null
docker run -d --name "$REDIS_NAME" -p 16379:6379 redis:7-alpine >/dev/null
for i in $(seq 1 30); do
  docker exec "$PG_NAME" pg_isready -U postgres >/dev/null 2>&1 && break
  sleep 1
done

step "起新 config 服务（goose 迁移自动执行）"
(
  cd "$ROOT"
  CONFIG_FILE="$PROBE_DIR/harness-config.yaml" \
  CONFIG_CENTER_SERVICE_TOKEN="$LEGACY_TOKEN" \
  go run ./services/config/cmd/server >"$SERVER_LOG" 2>&1
) &
SERVER_PID=$!
for i in $(seq 1 60); do
  curl -sf http://127.0.0.1:18095/healthz >/dev/null 2>&1 && break
  sleep 1
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "server exited early:"; tail -30 "$SERVER_LOG"; exit 1
  fi
done
curl -sf http://127.0.0.1:18095/healthz >/dev/null || { echo "server not healthy"; tail -30 "$SERVER_LOG"; exit 1; }
echo "server healthy"

step "灌测试数据（配置条目 + 两枚 machine token）"
psqlc "INSERT INTO config.entry (namespace, environment, key, format, value) VALUES ('order','dev','bootstrap.yaml','yaml','answer: 42') ON CONFLICT DO NOTHING;" >/dev/null
psqlc "INSERT INTO config.entry (namespace, environment, key, format, value) VALUES ('payment','dev','bootstrap.yaml','yaml','answer: 43') ON CONFLICT DO NOTHING;" >/dev/null
HASH_A="$(sha256hex "$TOKEN_A")"
HASH_B="$(sha256hex "$TOKEN_B")"
psqlc "INSERT INTO config.machine_token (id, service_name, environment, token_hash, allowed_namespaces) VALUES (gen_random_uuid(),'order','dev','\\x$HASH_A','{order}') ON CONFLICT DO NOTHING;" >/dev/null
psqlc "INSERT INTO config.machine_token (id, service_name, environment, token_hash, allowed_namespaces) VALUES (gen_random_uuid(),'order','dev','\\x$HASH_B','{order}') ON CONFLICT DO NOTHING;" >/dev/null
echo "tokens seeded: $(psqlc 'SELECT count(*) FROM config.machine_token;')"

cd "$PROBE_DIR"
export GOPRIVATE=github.com/lens077
export GOFLAGS=-mod=mod
PROBE="go run ."

step "a) legacy 共享 token：Load + Watch 快照"
$PROBE -mode=load -token="$LEGACY_TOKEN" | tee /tmp/ct-xver-a1 | grep -q LOAD_OK
$PROBE -mode=watch -token="$LEGACY_TOKEN" -timeout=8s | tee /tmp/ct-xver-a2 | grep -q EVENT_VALUE || true
grep -q "EVENT_VALUE" /tmp/ct-xver-a2
grep -q "legacy shared service token" "$SERVER_LOG" && echo "legacy WARN 已出现 ✅"

step "b) per-service token：范围内成功、范围外被拒"
$PROBE -mode=load -token="$TOKEN_A" | grep -q LOAD_OK && echo "order/dev ✅"
if $PROBE -mode=load -namespace=payment -token="$TOKEN_A" >/tmp/ct-xver-b2 2>&1; then
  echo "❌ payment 不应可读"; exit 1
fi
grep -qi "permission" /tmp/ct-xver-b2 && echo "payment/dev 越权被拒 ✅"

step "c) 吊销断流：Watch 建流 → 吊销 → 测断流时延"
$PROBE -mode=watch -token="$TOKEN_A" -timeout=90s >/tmp/ct-xver-c &
WATCH_PID=$!
sleep 3
REVOKE_TS=$(date +%s)
psqlc "UPDATE config.machine_token SET disabled=TRUE, revoked_at=now() WHERE token_hash='\\x$HASH_A';" >/dev/null
wait "$WATCH_PID" || true
END_TS=$(date +%s)
LATENCY=$((END_TS - REVOKE_TS))
cat /tmp/ct-xver-c
grep -q "STREAM_END" /tmp/ct-xver-c
grep -qi "permission" /tmp/ct-xver-c && echo "断流原因=permission_denied ✅"
echo "吊销→断流时延 ${LATENCY}s（心跳 30s 内为合格）"
[ "$LATENCY" -le 40 ] || { echo "❌ 断流超时"; exit 1; }

step "d) 两代重叠：B 不受 A 吊销影响"
$PROBE -mode=load -token="$TOKEN_B" | grep -q LOAD_OK && echo "token B 仍可用 ✅"
if $PROBE -mode=load -token="$TOKEN_A" >/dev/null 2>&1; then
  echo "❌ 已吊销的 A 不应可用"; exit 1
fi
echo "已吊销的 A 被拒 ✅"

step "全部场景通过 🎉"
