#!/usr/bin/env bash
# 在具备构建工具的目标机执行:产出 releases/zfs-platform-<VER>.tar.gz 一键包。
# 用法: bash scripts/package.sh [版本号]
set -euo pipefail
cd "$(dirname "$0")/.."
VER=${1:-0.92}
OUT=releases/zfs-platform-$VER
rm -rf "$OUT"; mkdir -p "$OUT/bin" "$OUT/deploy"

echo "[pkg] 构建前端(如 dist 缺失或需更新)..."
if [ ! -d web/dist ] || [ -n "${REBUILD_WEB:-}" ]; then
  (cd web && export NODE_OPTIONS=--max-old-space-size=1024 && npm run build >/dev/null 2>&1)
fi
echo "[pkg] 内嵌前端并构建 Manager..."
rm -rf manager/internal/app/static/dist && cp -r web/dist manager/internal/app/static/dist
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo nogit)
BT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
(cd manager && export GOPROXY=https://goproxy.cn,direct GOTOOLCHAIN=local \
  && go build -ldflags "-X zfsmgr/internal/app.Version=$VER -X zfsmgr/internal/app.Commit=$COMMIT -X zfsmgr/internal/app.BuildTime=$BT" \
     -o ../$OUT/bin/zfs-manager ./cmd/zfs-manager)

echo "[pkg] 组装 bundle..."
cp -r agent deploy docs README.md $OUT/ 2>/dev/null || true
rm -rf $OUT/deploy/static 2>/dev/null || true
# deploy 目录仅保留运行时所需(单元/脚本/schema);setup_env 供手工流
cp deploy/schema.sql deploy/zfs-manager.service deploy/zfs-agent.service \
   deploy/setup_env.sh deploy/install.sh $OUT/deploy/
echo "$VER" > $OUT/VERSION

tar -czf releases/zfs-platform-$VER.tar.gz -C releases zfs-platform-$VER
rm -rf "$OUT"
echo "[pkg] 完成: releases/zfs-platform-$VER.tar.gz"
ls -la releases/
