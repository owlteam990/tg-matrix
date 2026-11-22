#!/usr/bin/env bash
# 编译 sidecar + 桌面前端；打包原生桌面 App；编译并重启 Go 服务
#
# 用法：
#   ./scripts/build.sh              # 编译 sidecar + 前端静态资源
#   ./scripts/build.sh app          # 打包原生桌面 App（开发/内测）
#   ./scripts/build.sh sidecar      # 仅编译 Go sidecar（并同步到 Tauri binaries）
#   ./scripts/build.sh restart      # 编译 Go sidecar 并重启本地服务（推荐开发热更）
#   npm run build
#   npm run build:app
#   npm run sidecar:build
#   npm run sidecar:restart
#   cd apps/desktop && npm run tauri:dev
#
# 正式 release（版本号 / dmg / checksum / 更新清单）请用：
#   ./scripts/release.sh [版本号]
#   npm run release -- 0.2.0
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

MODE="${1:-build}"

export GOPROXY="${GOPROXY:-https://goproxy.io,direct}"
export GOSUMDB="${GOSUMDB:-off}"
if [[ -d "$ROOT/.gocache/mod" ]]; then
  export GOMODCACHE="${GOMODCACHE:-$ROOT/.gocache/mod}"
fi

DIST="$ROOT/dist"
SIDECAR_OUT="$DIST/sidecar"
DESKTOP_OUT="$DIST/desktop"
APP_OUT="$DIST/app"
TAURI_DIR="$ROOT/apps/desktop/src-tauri"
SIDECAR_BIN="$SIDECAR_OUT/tg-sidecar"
SIDECAR_ADDR="${TG_SIDECAR_ADDR:-127.0.0.1:17888}"

mkdir -p "$SIDECAR_OUT" "$DESKTOP_OUT" "$APP_OUT"

# 解析 sidecar 数据目录：环境变量 > Tauri App Data > 仓库 .data
resolve_sidecar_data_dir() {
  if [[ -n "${TG_SIDECAR_DATA:-}" ]]; then
    echo "$TG_SIDECAR_DATA"
    return
  fi
  local tauri_data="$HOME/Library/Application Support/local.tgmatrix.desktop"
  if [[ -d "$tauri_data" ]]; then
    echo "$tauri_data"
    return
  fi
  echo "$ROOT/.data"
}

# 编译 Go sidecar，并复制到 dist 与 Tauri binaries
build_sidecar() {
  echo "==> 编译 Go sidecar"
  command -v go >/dev/null || { echo "缺少 go"; exit 1; }
  (
    cd "$ROOT/services/sidecar"
    go build -trimpath -ldflags="-s -w" -o "$SIDECAR_BIN" ./cmd/sidecar
  )
  mkdir -p "$TAURI_DIR/binaries"
  cp "$SIDECAR_BIN" "$TAURI_DIR/binaries/tg-sidecar"
  chmod +x "$SIDECAR_BIN" "$TAURI_DIR/binaries/tg-sidecar"
  echo "  -> $SIDECAR_BIN"
  echo "  -> $TAURI_DIR/binaries/tg-sidecar"
}

# 结束占用端口的旧 sidecar 进程（必须确认端口释放，否则热更会假成功）
stop_sidecar() {
  local addr="$SIDECAR_ADDR"
  local port="${addr##*:}"
  echo "==> stop old sidecar on ${addr}"
  local i pids
  for i in 1 2 3 4 5 6; do
    pids="$(lsof -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null || true)"
    if [[ -z "$pids" ]]; then
      break
    fi
    # shellcheck disable=SC2086
    kill $pids 2>/dev/null || true
    sleep 0.3
    pids="$(lsof -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null || true)"
    if [[ -n "$pids" ]]; then
      # shellcheck disable=SC2086
      kill -9 $pids 2>/dev/null || true
    fi
    pkill -9 -f '/tg-sidecar' 2>/dev/null || true
    sleep 0.3
  done
  if lsof -tiTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "!! 端口 ${port} 仍被占用，热更可能失败；请手动 kill 后重试" >&2
    lsof -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null || true
  fi
}

# 后台启动 sidecar，并做 /health 探活
start_sidecar() {
  local data_dir
  local addr="$SIDECAR_ADDR"
  data_dir="$(resolve_sidecar_data_dir)"
  mkdir -p "$data_dir"
  mkdir -p "$ROOT/.data"

  echo "==> start sidecar"
  echo "  addr: ${addr}"
  echo "  data: ${data_dir}"

  nohup env \
    TG_SIDECAR_ADDR="${addr}" \
    TG_SIDECAR_DATA="${data_dir}" \
    TG_OPEN_BROWSER=0 \
    "$SIDECAR_BIN" \
    >>"$ROOT/.data/sidecar-restart.log" 2>&1 &
  disown || true

  local i body uptime
  for i in 1 2 3 4 5 6 7 8 9 10; do
    if body="$(curl -fsS "http://${addr}/health" 2>/dev/null)"; then
      # 刚启动的进程 uptime 应接近 0；若仍是数分钟说明旧进程没杀掉
      uptime="$(printf '%s' "$body" | sed -n 's/.*"uptime":"\([^"]*\)".*/\1/p')"
      case "$uptime" in
        ''|0s|[0-9]s|[0-9][0-9]s)
          echo "==> sidecar ready: http://${addr}/health"
          printf '%s\n' "$body"
          echo ""
          return 0
          ;;
        *)
          echo "!! health 返回旧进程 uptime=${uptime}，继续尝试停旧进程…" >&2
          stop_sidecar
          nohup env \
            TG_SIDECAR_ADDR="${addr}" \
            TG_SIDECAR_DATA="${data_dir}" \
            TG_OPEN_BROWSER=0 \
            "$SIDECAR_BIN" \
            >>"$ROOT/.data/sidecar-restart.log" 2>&1 &
          disown || true
          ;;
      esac
    fi
    sleep 0.3
  done
  echo "sidecar start timeout, see log: $ROOT/.data/sidecar-restart.log"
  tail -n 30 "$ROOT/.data/sidecar-restart.log" 2>/dev/null || true
  exit 1
}

# ---------- 子命令：仅编译 sidecar ----------
if [[ "$MODE" == "sidecar" ]]; then
  build_sidecar
  echo ""
  echo "仅编译完成。重启服务请执行："
  echo "  ./scripts/build.sh restart"
  echo "  或：npm run sidecar:restart"
  exit 0
fi

# ---------- 子命令：编译并重启 Go 服务 ----------
if [[ "$MODE" == "restart" ]]; then
  build_sidecar
  stop_sidecar
  start_sidecar
  echo ""
  echo "Go 服务已热更。若桌面窗口仍在跑，直接刷新业务页即可（无需重开 Tauri）。"
  echo "若仍 404：彻底退出桌面 App 后再开，或再执行一次 npm run sidecar:restart"
  exit 0
fi

# ---------- 默认 build / app ----------
echo "==> [1/4] 检查工具链"
command -v go >/dev/null || { echo "缺少 go"; exit 1; }
command -v npm >/dev/null || { echo "缺少 npm"; exit 1; }
command -v cargo >/dev/null || { echo "缺少 Rust/cargo（Tauri 需要）"; exit 1; }
go version
node -v
cargo -V

echo "==> [2/4] 编译 Go sidecar"
(
  cd "$ROOT/services/sidecar"
  go mod tidy
  go build -trimpath -ldflags="-s -w" -o "$SIDECAR_BIN" ./cmd/sidecar
)
mkdir -p "$TAURI_DIR/binaries"
cp "$SIDECAR_BIN" "$TAURI_DIR/binaries/tg-sidecar"
chmod +x "$TAURI_DIR/binaries/tg-sidecar"

echo "==> [3/4] 编译桌面前端"
(
  cd "$ROOT/apps/desktop"
  if [[ ! -d node_modules ]]; then
    npm install
  fi
  npm run build
  rm -rf "${DESKTOP_OUT:?}"/*
  cp -R dist/. "$DESKTOP_OUT/"
)

echo ""
echo "基础编译完成："
echo "  sidecar: $SIDECAR_BIN"
echo "  desktop: $DESKTOP_OUT/"

if [[ "$MODE" != "app" ]]; then
  echo ""
  echo "────────────────────────────────────────"
  echo "热更 Go 服务（改 sidecar 后推荐）："
  echo "  ./scripts/build.sh restart"
  echo "  或：npm run sidecar:restart"
  echo ""
  echo "打包原生桌面 App："
  echo "  ./scripts/build.sh app"
  echo ""
  echo "开发态原生窗口："
  echo "  npm run sidecar:restart   # 或 sidecar:build"
  echo "  cd apps/desktop && npm run tauri:dev"
  echo "────────────────────────────────────────"
  exit 0
fi

echo ""
echo "==> [4/4] 打包 Tauri 原生桌面 App（系统窗口，非浏览器）"
export CARGO_HOME="${CARGO_HOME:-$ROOT/.cargo-home}"
export CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-$TAURI_DIR/target}"
mkdir -p "$CARGO_HOME" "$CARGO_TARGET_DIR"

(
  cd "$ROOT/apps/desktop"
  npm run tauri:build
)

rm -rf "${APP_OUT:?}/"*
BUNDLE_DIR="$CARGO_TARGET_DIR/release/bundle"
FOUND=0
if [[ -d "$BUNDLE_DIR/macos" ]]; then
  cp -R "$BUNDLE_DIR/macos/"*.app "$APP_OUT/" 2>/dev/null && FOUND=1 || true
fi
if [[ -d "$BUNDLE_DIR/dmg" ]]; then
  cp -R "$BUNDLE_DIR/dmg/"*.dmg "$APP_OUT/" 2>/dev/null || true
fi
while IFS= read -r app; do
  cp -R "$app" "$APP_OUT/"
  FOUND=1
done < <(find "$CARGO_TARGET_DIR/release/bundle" -maxdepth 3 -name '*.app' 2>/dev/null)

if [[ "$FOUND" -eq 0 ]]; then
  echo "未找到 .app 产物，请检查 $CARGO_TARGET_DIR/release/bundle"
  exit 1
fi

for app in "$APP_OUT"/*.app; do
  [[ -d "$app" ]] || continue
  xattr -cr "$app" 2>/dev/null || true
done

echo ""
echo "原生桌面 App 已生成（系统窗口，不是浏览器）："
ls -la "$APP_OUT" || true
echo ""
echo "运行："
echo "  open \"$APP_OUT\"/*.app"
echo ""
echo "开发调试（原生窗口）："
echo "  npm run sidecar:restart"
echo "  cd apps/desktop && npm run tauri:dev"
