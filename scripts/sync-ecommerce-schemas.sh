#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
ecommerce_root=${ECOMMERCE_ROOT:-"$root/../ecommerce"}
destination="$root/services/config/internal/schema/schemas"
services=(address behavior cart inventory merchant order payment product search user)
sources=()

for service in "${services[@]}"; do
  relative="backend/services/$service/configs/bootstrap.schema.json"
  source_file="$ecommerce_root/$relative"
  if [ ! -f "$source_file" ]; then
    echo "缺少 Schema: $source_file" >&2
    exit 1
  fi
  sources+=("$relative")
done

if ! git -C "$ecommerce_root" diff --quiet -- "${sources[@]}" ||
  ! git -C "$ecommerce_root" diff --cached --quiet -- "${sources[@]}"; then
  echo "Schema 有未提交改动；先在 ecommerce 提交，再同步来源 revision" >&2
  exit 1
fi

for service in "${services[@]}"; do
  mkdir -p "$destination/$service"
  cp "$ecommerce_root/backend/services/$service/configs/bootstrap.schema.json" \
    "$destination/$service/bootstrap.schema.json"
done

git -C "$ecommerce_root" rev-parse HEAD > "$destination/ecommerce-source-revision.txt"
echo "已同步 ${#services[@]} 份 Bootstrap Schema，来源 $(cat "$destination/ecommerce-source-revision.txt")"
