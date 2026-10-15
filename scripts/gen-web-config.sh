#!/usr/bin/env bash
# 用本仓 chart 的 prod 值生成 EdgeOne 控制台运行时 config.json。
#
#   scripts/gen-web-config.sh web/dist
#
# web 的运行时配置是「运行时注入」的（见 web/src/runtime-config.ts）：同一个 dist 构建物，
# k8s 里由 ConfigMap 覆盖挂载，EdgeOne 上则由本脚本在部署前写进 dist/config.json。
# 这样 apiUrl / casdoor 随环境变，不必把 prod 地址写死在 web/public/config.json（那份是
# localhost，仅供本机 dev）。真相源是 deploy/chart/control-tower/values-pre.yaml 的
# configWeb.apiUrl 与 values.yaml 的 casdoor.*（与 k8s 控制台用的是同一组值）。
set -euo pipefail

OUT="${1:-web/dist}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
command -v yq >/dev/null || { echo "缺少 yq" >&2; exit 1; }

API_URL="$(yq '.configWeb.apiUrl' deploy/chart/control-tower/values-pre.yaml)"
ISSUER="$(yq '.casdoor.issuer' deploy/chart/control-tower/values.yaml)"
CLIENT_ID="$(yq '.casdoor.clientId' deploy/chart/control-tower/values.yaml)"
ORG="$(yq '.casdoor.organizationName' deploy/chart/control-tower/values.yaml)"
APP="$(yq '.casdoor.appName' deploy/chart/control-tower/values.yaml)"

mkdir -p "$OUT"
cat > "$OUT/config.json" <<EOF
{
  "apiUrl": "$API_URL",
  "casdoor": {
    "serverUrl": "$ISSUER",
    "clientId": "$CLIENT_ID",
    "organizationName": "$ORG",
    "appName": "$APP",
    "redirectPath": "/callback"
  }
}
EOF
echo "→ $OUT/config.json: apiUrl=$API_URL casdoor=$ISSUER"
