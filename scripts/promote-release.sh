#!/usr/bin/env bash
# 把一个发布版本推进到 pre：改 values-pre.yaml 的三个镜像 tag，重新渲染 deploy/pre，跑守门测试。
#
#   scripts/promote-release.sh 0.2.18
#
# 只改工作区、不碰 git：CI（ci.yml release job）在它之后提交并推送到 GitHub 与 GitLab；
# 本地也可以先跑一遍看 diff。版本必须是裸 semver X.Y.Z，与 ci.yml 触发 tag 的写法一致——
# 这个字符串会直接成为镜像 tag，image job 在同一次 workflow 里推过。
set -euo pipefail

VERSION="${1:-}"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "用法: $0 X.Y.Z（收到 '$VERSION'）" >&2; exit 64; }

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
for tool in yq helm python3 go; do command -v "$tool" >/dev/null || { echo "缺少 $tool" >&2; exit 1; }; done

VALUES=deploy/chart/control-tower/values-pre.yaml
before="$(yq '[.config.image.tag, .configWeb.image.tag, .gateway.image.tag] | join(" ")' "$VALUES")"
# yq -i 保留注释与流式列表写法；只改这三个键。
yq -i ".config.image.tag = \"$VERSION\" | .configWeb.image.tag = \"$VERSION\" | .gateway.image.tag = \"$VERSION\"" "$VALUES"
after="$(yq '[.config.image.tag, .configWeb.image.tag, .gateway.image.tag] | join(" ")' "$VALUES")"
echo "→ pre 镜像 tag：$before → $after"

python3 scripts/deploy-render.py
# 渲染产物必须仍通过守门测试（TCR 前缀、tcr-pull、BFF 公网回调……），再做一次渲染一致性比对。
go test -count=1 ./deploy/
python3 scripts/deploy-render.py --check

echo "→ 改动文件："
git status --short -- "$VALUES" deploy/pre
