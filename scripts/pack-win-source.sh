#!/usr/bin/env bash
# 打「发到其他 Windows 电脑」用的源码包（不含依赖缓存与密钥）
#
# 用法（macOS / Linux，仓库根目录）：
#   ./scripts/pack-win-source.sh
#
# 产物：
#   dist/win-source/tg-matrix-win-src-YYYYMMDD-HHMM.zip
#   dist/win-source/MANIFEST.txt
#
# 对方机器：解压到本机盘（如 C:\build\tg-matrix），见 docs/Windows打包.md

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

STAMP="$(date +%Y%m%d-%H%M)"
OUT_DIR="$ROOT/dist/win-source"
STAGE="$OUT_DIR/stage-tg-matrix"
ZIP="$OUT_DIR/tg-matrix-win-src-${STAMP}.zip"

rm -rf "$STAGE"
mkdir -p "$STAGE" "$OUT_DIR"

# 发版必需目录 / 文件（按需拷贝，避免整仓促销工程与缓存）
copy_path() {
  local rel="$1"
  if [[ ! -e "$ROOT/$rel" ]]; then
    echo "!! 跳过不存在: $rel" >&2
    return 0
  fi
  mkdir -p "$STAGE/$(dirname "$rel")"
  if [[ -d "$ROOT/$rel" ]]; then
    rsync -a --delete \
      --exclude node_modules \
      --exclude target \
      --exclude dist \
      --exclude .data \
      --exclude sessions \
      --exclude '*.session' \
      --exclude '*.db' \
      --exclude '*.db-*' \
      --exclude .env \
      --exclude .env.* \
      --exclude binaries \
      --exclude gen \
      "$ROOT/$rel/" "$STAGE/$rel/"
  else
    cp -a "$ROOT/$rel" "$STAGE/$rel"
  fi
  echo "  + $rel"
}

echo "==> 组装 Windows 发版源码包"
copy_path "apps/desktop"
copy_path "services/sidecar"
copy_path "scripts"
copy_path "docs"
copy_path "package.json"
copy_path "package-lock.json"
copy_path "README.md"
copy_path "AGENTS.md"
# 可选：根目录 lock / 其它配置
[[ -f "$ROOT/Cargo.toml" ]] && copy_path "Cargo.toml" || true

# 确保 Windows 脚本在包内
for f in setup-win.cmd setup-win.ps1 release-win.cmd release.ps1; do
  if [[ ! -f "$STAGE/scripts/$f" ]]; then
    echo "缺少 scripts/$f" >&2
    exit 1
  fi
done

# 放入对方开箱说明（根目录快捷入口）
cat >"$STAGE/先读我-Windows打包.txt" <<EOF
1. 解压到本机盘，例如 C:\\build\\tg-matrix（不要放 Z: 映射盘）
2. 管理员 CMD：
     cd /d C:\\build\\tg-matrix
     scripts\\setup-win.cmd
3. Visual Studio Installer 勾选「使用 C++ 的桌面开发」，新开终端
4. 发版：
     scripts\\release-win.cmd
5. 详细说明见 docs\\Windows打包.md

注意：要打给普通 PC 用的包，请用 x64 Windows（不要用 ARM 虚拟机）。
EOF

# 生成清单
{
  echo "stamp=$STAMP"
  echo "from=$ROOT"
  echo "files:"
  (cd "$STAGE" && find . -type f | sort)
} >"$OUT_DIR/MANIFEST.txt"

rm -f "$ZIP"
# zip 相对路径，解压得到 tg-matrix/ 目录
(
  cd "$OUT_DIR"
  rm -rf "tg-matrix"
  mv stage-tg-matrix tg-matrix
  if command -v zip >/dev/null; then
    zip -r -q "$(basename "$ZIP")" tg-matrix
  else
    tar -a -cf "${ZIP%.zip}.tar.gz" tg-matrix
    ZIP="${ZIP%.zip}.tar.gz"
  fi
  rm -rf tg-matrix
)

echo ""
echo "════════════════════════════════════════"
echo " 已生成: $ZIP"
echo " 清单:   $OUT_DIR/MANIFEST.txt"
echo " 拷到 x64 Windows 后按 docs/Windows打包.md 操作"
echo "════════════════════════════════════════"
ls -lh "$ZIP" 2>/dev/null || ls -lh "$OUT_DIR"/tg-matrix-win-src-* 
