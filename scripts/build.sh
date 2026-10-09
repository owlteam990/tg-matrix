#!/usr/bin/env bash
# 编译 sidecar（Go）与桌面前端（Vite），产物输出到 dist/
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

export GOPROXY="${GOPROXY:-https://goproxy.io,direct}"
export GOSUMDB="${GOSUMDB:-off}"
if [[ -d "$ROOT/.gocache/mod" ]]; then
  export GOMODCACHE="${GOMODCACHE:-$ROOT/.gocache/mod}"
fi

DIST="$ROOT/dist"
SIDECAR_OUT="$DIST/sidecar"
DESKTOP_OUT="$DIST/desktop"
mkdir -p "$SIDECAR_OUT" "$DESKTOP_OUT"

echo "==> [1/3] 检查工具链"
command -v go >/dev/null || { echo "缺少 go"; exit 1; }
command -v npm >/dev/null || { echo "缺少 npm"; exit 1; }
go version
node -v
npm -v

echo "==> [2/3] 编译 sidecar"
(
  cd "$ROOT/services/sidecar"
  go mod tidy
  # 当前平台
  go build -trimpath -ldflags="-s -w" -o "$SIDECAR_OUT/tg-sidecar" ./cmd/sidecar
  # 可选：交叉编译（设置 BUILD_CROSS=1）
  if [[ "${BUILD_CROSS:-0}" == "1" ]]; then
    echo "    交叉编译 darwin/amd64、windows/amd64"
    GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags="-s -w" \
      -o "$SIDECAR_OUT/tg-sidecar-darwin-amd64" ./cmd/sidecar
    GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" \
      -o "$SIDECAR_OUT/tg-sidecar-windows-amd64.exe" ./cmd/sidecar
    GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" \
      -o "$SIDECAR_OUT/tg-sidecar-darwin-arm64" ./cmd/sidecar
  fi
)

echo "==> [3/3] 编译桌面前端"
(
  cd "$ROOT/apps/desktop"
  if [[ ! -d node_modules ]]; then
    npm install
  fi
  npm run build
  rm -rf "$DESKTOP_OUT"/*
  cp -R dist/. "$DESKTOP_OUT/"
)

echo ""
echo "编译完成："
echo "  sidecar: $SIDECAR_OUT/tg-sidecar"
echo "  desktop: $DESKTOP_OUT/"
echo ""
echo "运行 sidecar 示例："
echo "  TG_SIDECAR_DATA=$ROOT/.data $SIDECAR_OUT/tg-sidecar"
echo "预览前端："
echo "  npx --prefix apps/desktop vite preview --outDir ../../dist/desktop"
