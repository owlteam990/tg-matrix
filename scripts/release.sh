#!/usr/bin/env bash
# 打正式 release 安装包（当前宿主机平台）
#
# 用法：
#   ./scripts/release.sh                 # 用现有版本号打包
#   ./scripts/release.sh 0.2.0           # 写入版本号后打包
#   ./scripts/release.sh 0.2.0 --dry-run # 只改版本号 / 打印计划，不编译
#
# 环境变量（可选）：
#   APPLE_SIGNING_IDENTITY   macOS 签名身份，如 "Developer ID Application: Xxx (TEAMID)"
#   APPLE_ID / APPLE_PASSWORD / APPLE_TEAM_ID   公证（notarize）
#   TG_RELEASE_SKIP_SIGN=1   跳过签名环境检查（仅内测）
#   GOPROXY / GOSUMDB        同 build.sh
#
# 产物目录：
#   dist/release/vX.Y.Z/
#     ├── *.dmg / *.app          （macOS）
#     ├── *.msi / *.exe          （Windows 请用 scripts/release.ps1）
#     ├── checksums.sha256
#     └── update-manifest.example.json   # 对照 docs/更新.md 填云端
#
# 说明：
#   - 本脚本只打「当前 OS」产物；Windows 请在 Win 主机执行 .\scripts\release.ps1
#   - 日常开发热更请用 ./scripts/build.sh / restart

export APPLE_SIGNING_IDENTITY='Developer ID Application: SELAMI KOCAYARRAK (9RL67B83R7)'
export APPLE_TEAM_ID='9RL67B83R7'

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

export GOPROXY="${GOPROXY:-https://goproxy.io,direct}"
export GOSUMDB="${GOSUMDB:-off}"
if [[ -d "$ROOT/.gocache/mod" ]]; then
  export GOMODCACHE="${GOMODCACHE:-$ROOT/.gocache/mod}"
fi

VERSION_ARG=""
DRY_RUN=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    --help|-h)
      sed -n '2,28p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    -*)
      echo "未知参数: $arg" >&2
      exit 1
      ;;
    *)
      if [[ -n "$VERSION_ARG" ]]; then
        echo "版本号只能指定一次" >&2
        exit 1
      fi
      VERSION_ARG="$arg"
      ;;
  esac
done

TAURI_DIR="$ROOT/apps/desktop/src-tauri"
TAURI_CONF="$TAURI_DIR/tauri.conf.json"
DESKTOP_PKG="$ROOT/apps/desktop/package.json"
ROOT_PKG="$ROOT/package.json"
CARGO_TOML="$TAURI_DIR/Cargo.toml"
SIDECAR_OUT="$ROOT/dist/sidecar/tg-sidecar"
CARGO_HOME="${CARGO_HOME:-$ROOT/.cargo-home}"
CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-$TAURI_DIR/target}"
export CARGO_HOME CARGO_TARGET_DIR

uname_s="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$uname_s" in
  darwin) PLATFORM=mac ;;
  mingw*|msys*|cygwin*) PLATFORM=windows ;;
  *)
    if [[ "${OS:-}" == "Windows_NT" ]]; then
      PLATFORM=windows
    else
      PLATFORM=mac
      echo "!! 未识别 OS=$uname_s，按 mac 处理；Windows 请在 Windows 主机执行本脚本" >&2
    fi
    ;;
esac

# 读取当前 tauri.conf.json 中的 version
read_current_version() {
  python3 - <<'PY' "$TAURI_CONF"
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    print(json.load(f).get("version", "0.0.0"))
PY
}

# 校验 semver：MAJOR.MINOR.PATCH
assert_semver() {
  local v="$1"
  if [[ ! "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "版本号须为 semver：MAJOR.MINOR.PATCH，收到: $v" >&2
    exit 1
  fi
}

# 同步写入各处版本号
write_version() {
  local ver="$1"
  python3 - <<'PY' "$ver" "$TAURI_CONF" "$DESKTOP_PKG" "$ROOT_PKG" "$CARGO_TOML"
import json, re, sys
from pathlib import Path

ver = sys.argv[1]
tauri_conf, desktop_pkg, root_pkg, cargo = map(Path, sys.argv[2:])

def write_json(path: Path, mut):
    data = json.loads(path.read_text(encoding="utf-8"))
    mut(data)
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

write_json(tauri_conf, lambda d: d.__setitem__("version", ver))
write_json(desktop_pkg, lambda d: d.__setitem__("version", ver))
write_json(root_pkg, lambda d: d.__setitem__("version", ver))

text = cargo.read_text(encoding="utf-8")
text2, n = re.subn(r'(?m)^version\s*=\s*"[^"]*"', f'version = "{ver}"', text, count=1)
if n != 1:
    raise SystemExit(f"Cargo.toml 未能更新 version: {cargo}")
cargo.write_text(text2, encoding="utf-8")
print(f"已写入版本 {ver}")
PY
}

# 按平台设置 bundle.targets / resources（正式分发需要安装包格式）
ensure_bundle_targets() {
  python3 - <<'PY' "$TAURI_CONF" "$PLATFORM"
import json, sys
from pathlib import Path

conf_path = Path(sys.argv[1])
platform = sys.argv[2]
data = json.loads(conf_path.read_text(encoding="utf-8"))
bundle = data.setdefault("bundle", {})
changed = False
if platform == "mac":
    want_targets = ["app", "dmg"]
    want_resources = ["binaries/tg-sidecar"]
elif platform == "windows":
    want_targets = ["nsis", "msi"]
    want_resources = ["binaries/tg-sidecar.exe"]
else:
    want_targets = None
    want_resources = None
if want_targets is not None and bundle.get("targets") != want_targets:
    bundle["targets"] = want_targets
    changed = True
if want_resources is not None and bundle.get("resources") != want_resources:
    bundle["resources"] = want_resources
    changed = True
if changed:
    conf_path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"已更新 bundle.targets -> {bundle.get('targets')}")
    print(f"已更新 bundle.resources -> {bundle.get('resources')}")
else:
    print(f"bundle.targets / resources 已是 {platform} 配置")
PY
}

# 检查 macOS 签名相关环境
check_mac_signing() {
  if [[ "${TG_RELEASE_SKIP_SIGN:-}" == "1" ]]; then
    echo "!! TG_RELEASE_SKIP_SIGN=1：跳过签名检查（仅内测，外发用户会遇 Gatekeeper）"
    return
  fi
  if [[ -z "${APPLE_SIGNING_IDENTITY:-}" ]]; then
    echo ""
    echo "!! 未设置 APPLE_SIGNING_IDENTITY"
    echo "   内测可：TG_RELEASE_SKIP_SIGN=1 ./scripts/release.sh"
    echo "   外发请：export APPLE_SIGNING_IDENTITY='Developer ID Application: Your Name (TEAMID)'"
    echo "   公证另需：APPLE_ID / APPLE_PASSWORD（app-specific）/ APPLE_TEAM_ID"
    echo ""
    if [[ -t 0 ]]; then
      read -r -p "无签名继续打包？[y/N] " ans
      if [[ ! "$ans" =~ ^[Yy]$ ]]; then
        exit 1
      fi
    else
      echo "非交互环境且未设置签名，退出。可设 TG_RELEASE_SKIP_SIGN=1" >&2
      exit 1
    fi
  else
    echo "APPLE_SIGNING_IDENTITY=$APPLE_SIGNING_IDENTITY"
    if [[ -n "${APPLE_ID:-}" && -n "${APPLE_PASSWORD:-}" && -n "${APPLE_TEAM_ID:-}" ]]; then
      echo "公证环境变量已设置（APPLE_ID / TEAM_ID）"
    else
      echo "!! 未完整设置公证变量（APPLE_ID / APPLE_PASSWORD / APPLE_TEAM_ID），将只签名不公证"
    fi
  fi
}

# 编译 Go sidecar 并放入 Tauri resources
build_sidecar() {
  echo "==> 编译 Go sidecar"
  command -v go >/dev/null || { echo "缺少 go"; exit 1; }
  mkdir -p "$(dirname "$SIDECAR_OUT")" "$TAURI_DIR/binaries"
  (
    cd "$ROOT/services/sidecar"
    go mod tidy
    go build -trimpath -ldflags="-s -w" -o "$SIDECAR_OUT" ./cmd/sidecar
  )
  cp "$SIDECAR_OUT" "$TAURI_DIR/binaries/tg-sidecar"
  chmod +x "$SIDECAR_OUT" "$TAURI_DIR/binaries/tg-sidecar"
  echo "  -> $TAURI_DIR/binaries/tg-sidecar"
}

# 收集产物到 dist/release/vX.Y.Z 并生成校验与更新清单示例
collect_artifacts() {
  local ver="$1"
  local out="$ROOT/dist/release/v${ver}"
  local bundle="$CARGO_TARGET_DIR/release/bundle"
  mkdir -p "$out"
  # 清空旧产物（保留目录）
  find "$out" -mindepth 1 -maxdepth 1 -exec rm -rf {} +

  echo "==> 收集产物 -> $out"
  local found=0
  if [[ -d "$bundle/dmg" ]]; then
    cp -f "$bundle/dmg/"*.dmg "$out/" 2>/dev/null && found=1 || true
  fi
  if [[ -d "$bundle/macos" ]]; then
    cp -R "$bundle/macos/"*.app "$out/" 2>/dev/null && found=1 || true
  fi
  if [[ -d "$bundle/nsis" ]]; then
    cp -f "$bundle/nsis/"*.exe "$out/" 2>/dev/null && found=1 || true
  fi
  if [[ -d "$bundle/msi" ]]; then
    cp -f "$bundle/msi/"*.msi "$out/" 2>/dev/null && found=1 || true
  fi
  while IFS= read -r f; do
    cp -f "$f" "$out/" 2>/dev/null || true
    found=1
  done < <(find "$bundle" -maxdepth 3 \( -name '*.dmg' -o -name '*.exe' -o -name '*.msi' \) 2>/dev/null)

  if [[ "$PLATFORM" == "mac" ]]; then
    for app in "$out"/*.app; do
      [[ -d "$app" ]] || continue
      xattr -cr "$app" 2>/dev/null || true
      found=1
    done
  fi

  if [[ "$found" -eq 0 ]]; then
    echo "未找到安装包，请检查 $bundle" >&2
    find "$bundle" -maxdepth 4 -type f 2>/dev/null | head -n 50 || true
    exit 1
  fi

  # 带版本号的稳定文件名（便于 CDN）
  local f base ext renamed
  shopt -s nullglob
  for f in "$out"/*.{dmg,exe,msi}; do
    [[ -f "$f" ]] || continue
    base="$(basename "$f")"
    ext="${base##*.}"
    renamed="$out/TGMatrix-${ver}-${PLATFORM}.${ext}"
    if [[ "$f" != "$renamed" ]]; then
      cp -f "$f" "$renamed"
    fi
  done
  shopt -u nullglob

  (
    cd "$out"
    if command -v shasum >/dev/null; then
      shasum -a 256 *.dmg *.exe *.msi 2>/dev/null >checksums.sha256 || true
    elif command -v sha256sum >/dev/null; then
      sha256sum *.dmg *.exe *.msi 2>/dev/null >checksums.sha256 || true
    fi
  )

  local primary=""
  if [[ -f "$out/TGMatrix-${ver}-${PLATFORM}.dmg" ]]; then
    primary="TGMatrix-${ver}-${PLATFORM}.dmg"
  elif [[ -f "$out/TGMatrix-${ver}-${PLATFORM}.exe" ]]; then
    primary="TGMatrix-${ver}-${PLATFORM}.exe"
  elif [[ -f "$out/TGMatrix-${ver}-${PLATFORM}.msi" ]]; then
    primary="TGMatrix-${ver}-${PLATFORM}.msi"
  fi

  python3 - <<'PY' "$out" "$ver" "$PLATFORM" "$primary"
import json, sys
from datetime import datetime, timezone
from pathlib import Path

out, ver, platform, primary = sys.argv[1:5]
manifest = {
    "platform": platform,
    "latestVersion": ver,
    "minSupportedVersion": ver,
    "forceUpdate": False,
    "downloadUrl": f"https://cdn.example.com/{primary}" if primary else "",
    "releaseNotes": f"TG Matrix {ver}",
    "supportContact": "@maotouying_cc",
    "publishedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "_note": "上传安装包到 CDN 后，把 downloadUrl 换成真实 HTTPS 直链，再写入工厂更新接口（见 docs/更新.md）",
}
Path(out, "update-manifest.example.json").write_text(
    json.dumps(manifest, ensure_ascii=False, indent=2) + "\n",
    encoding="utf-8",
)
print("已写 update-manifest.example.json")
PY

  echo ""
  echo "════════════════════════════════════════"
  echo " Release 完成：v${ver} (${PLATFORM})"
  echo " 目录：$out"
  ls -la "$out" || true
  echo ""
  echo "下一步："
  echo "  1) 上传 $out/TGMatrix-${ver}-${PLATFORM}.* 到 CDN"
  echo "  2) 按 update-manifest.example.json 配置云端 /app/update"
  echo "  3) git tag v${ver} && git push origin v${ver}  （需你手动）"
  echo "════════════════════════════════════════"
}

# ---------- main ----------
command -v python3 >/dev/null || { echo "缺少 python3"; exit 1; }
command -v go >/dev/null || { echo "缺少 go"; exit 1; }
command -v npm >/dev/null || { echo "缺少 npm"; exit 1; }
command -v cargo >/dev/null || { echo "缺少 Rust/cargo"; exit 1; }

CURRENT="$(read_current_version)"
if [[ -n "$VERSION_ARG" ]]; then
  assert_semver "$VERSION_ARG"
  VERSION="$VERSION_ARG"
else
  VERSION="$CURRENT"
  assert_semver "$VERSION"
fi

echo "==> release 计划"
echo "  platform : $PLATFORM"
echo "  version  : $VERSION (当前配置 $CURRENT)"
echo "  dry-run  : $DRY_RUN"
echo "  out      : $ROOT/dist/release/v${VERSION}"

if [[ "$VERSION" != "$CURRENT" ]]; then
  write_version "$VERSION"
else
  echo "版本未变更，跳过写入"
fi

ensure_bundle_targets

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo "dry-run 结束（未编译）"
  exit 0
fi

if [[ "$PLATFORM" == "mac" ]]; then
  check_mac_signing
fi

mkdir -p "$CARGO_HOME" "$CARGO_TARGET_DIR" "$ROOT/dist/release"

build_sidecar

echo "==> 安装前端依赖（如需要）"
(
  cd "$ROOT/apps/desktop"
  if [[ ! -d node_modules ]]; then
    npm install
  fi
)

echo "==> Tauri release build"
(
  cd "$ROOT/apps/desktop"
  npm run tauri:build
)

collect_artifacts "$VERSION"
