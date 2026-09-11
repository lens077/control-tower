#!/usr/bin/env bash
# 轮换 ecommerce 各服务 bootstrap.yaml 中的 Casdoor client_id / client_secret。
#
# 这些键存放在 Config Center（<service>/<env>/bootstrap.yaml），业务服务通过
# WatchKeys 热加载；这里按「GetKey → 就地替换 → PutKey」逐个改写，每个键生成
# 一个新版本，可用管理台 Rollback 回退。
#
# 必需环境变量（凭据只从环境给，不写入文件、不回显）：
#   CONFIG_ADMIN_JWT      管理员 access token（浏览器登录管理台后从请求头 Authorization 取）
#   CASDOOR_NEW_ID        新 client_id
#   CASDOOR_NEW_SECRET    新 client_secret
# 可选：
#   CONFIG_API            默认 https://config-api.apikv.com
#   ENVIRONMENTS          默认 "dev pre"，空格分隔
#   SERVICES              默认 10 个业务服务
#   DRY_RUN=1             只打印将要改哪些键，不写回
set -euo pipefail

: "${CONFIG_ADMIN_JWT:?需要 CONFIG_ADMIN_JWT}"
: "${CASDOOR_NEW_ID:?需要 CASDOOR_NEW_ID}"
: "${CASDOOR_NEW_SECRET:?需要 CASDOOR_NEW_SECRET}"
CONFIG_API="${CONFIG_API:-https://config-api.apikv.com}"
ENVIRONMENTS="${ENVIRONMENTS:-dev pre}"
SERVICES="${SERVICES:-user search product order inventory cart merchant address behavior payment}"
DRY_RUN="${DRY_RUN:-0}"

call() { # call <Rpc> <json>
  curl -fsS --max-time 15 \
    -H "Authorization: Bearer $CONFIG_ADMIN_JWT" \
    -H 'Content-Type: application/json' \
    "$CONFIG_API/config.v1.ConfigService/$1" -d "$2"
}

changed=0 skipped=0
for env in $ENVIRONMENTS; do
  for svc in $SERVICES; do
    req=$(python3 -c 'import json,sys; print(json.dumps({"namespace":sys.argv[1],"environment":sys.argv[2],"key":"bootstrap.yaml"}))' "$svc" "$env")
    if ! resp=$(call GetKey "$req" 2>/dev/null); then
      echo "[skip] $svc/$env/bootstrap.yaml 不存在或无权限"; skipped=$((skipped+1)); continue
    fi
    updated=$(CASDOOR_NEW_ID="$CASDOOR_NEW_ID" CASDOOR_NEW_SECRET="$CASDOOR_NEW_SECRET" \
      python3 - "$resp" <<'PY'
import json, os, re, sys
entry = json.loads(sys.argv[1])["entry"]
value = entry.get("value", "")
if value == "******":
    print("MASKED"); sys.exit(0)
new_id, new_secret = os.environ["CASDOOR_NEW_ID"], os.environ["CASDOOR_NEW_SECRET"]
# 只改 auth.casdoor 块内的两行，保留原有引号风格与缩进。
out, in_block, hit = [], False, 0
for line in value.splitlines(keepends=True):
    stripped = line.lstrip()
    if re.match(r"casdoor:\s*$", stripped):
        in_block = True
    elif in_block and stripped and not line.startswith((" ", "\t")):
        in_block = False
    if in_block:
        m = re.match(r'^(\s*client_id:\s*)(["\']?)[^"\'\s]*\2(.*)$', line)
        if m:
            line = f"{m.group(1)}{m.group(2)}{new_id}{m.group(2)}{m.group(3)}\n"; hit += 1
        m = re.match(r'^(\s*client_secret:\s*)(["\']?)[^"\'\s]*\2(.*)$', line)
        if m:
            line = f"{m.group(1)}{m.group(2)}{new_secret}{m.group(2)}{m.group(3)}\n"; hit += 1
    out.append(line)
if hit == 0:
    print("NOCHANGE"); sys.exit(0)
print(json.dumps({"namespace": entry["namespace"], "environment": entry["environment"], "key": entry["key"],
                  "format": entry.get("format", "CONFIG_FORMAT_YAML"), "value": "".join(out),
                  "comment": "rotate casdoor client id/secret", "isSecret": entry.get("isSecret", False),
                  "description": entry.get("description", "")}))
PY
)
    case "$updated" in
      MASKED)   echo "[skip] $svc/$env 为 is_secret，GetKey 已脱敏，需从本地 pre/dev.yml 手工上传"; skipped=$((skipped+1)); continue;;
      NOCHANGE) echo "[skip] $svc/$env 未找到 auth.casdoor.client_id/client_secret"; skipped=$((skipped+1)); continue;;
    esac
    if [[ "$DRY_RUN" == "1" ]]; then echo "[dry-run] 将改写 $svc/$env/bootstrap.yaml"; changed=$((changed+1)); continue; fi
    ver=$(call PutKey "$updated" | python3 -c 'import json,sys; print(json.load(sys.stdin)["entry"]["version"])')
    echo "[ok] $svc/$env/bootstrap.yaml -> v$ver"; changed=$((changed+1))
  done
done
echo "完成：改写 $changed 个键，跳过 $skipped 个。"
