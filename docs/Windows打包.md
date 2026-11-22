# Windows 发版指南（拷到另一台电脑）

在 **x64 Windows** 上打用户安装包（`windows-x64`）。  
Apple Silicon 虚拟机里的 Windows ARM 只能打 `windows-arm64`，普通 PC 用不了。

## 1. 从本机导出源码包（macOS）

在仓库根目录：

```bash
./scripts/pack-win-source.sh
```

产物：`dist/win-source/tg-matrix-win-src-*.zip`（已排除 `node_modules` / `target` / `.data` / session 等）。

用 U 盘、网盘或内网拷到目标 Windows。**解压到本机盘**（如 `C:\build\tg-matrix`），不要放在网络映射盘（如 `Z:`）。

## 2. 目标机需要带过去的内容

| 需要 | 说明 |
|------|------|
| 源码 zip（上一步） | 含 `apps/desktop`、`services/sidecar`、`scripts/*win*` |
| 本说明 | `docs/Windows打包.md`（已在 zip 内） |

**不要**拷：`.data`、`*.session`、`.env`、密钥、本机 `node_modules` / Rust `target`（到对方机器重装/重编）。

## 3. 目标机环境（干净机一次装齐）

管理员 CMD：

```bat
cd /d C:\build\tg-matrix
scripts\setup-win.cmd
```

装完后：

1. 打开 **Visual Studio Installer**，确认 Build Tools 勾了「**使用 C++ 的桌面开发**」
2. **关掉终端再开**一个新的
3. 确认：

```bat
go version
rustc -V
cargo -V
node -v
npm -v
```

网络差时：Go 用 https://golang.google.cn/dl/ 下对应 **windows-amd64.msi**；Rust 可用中科大镜像：

```bat
set RUSTUP_DIST_SERVER=https://mirrors.ustc.edu.cn/rust-static
set RUSTUP_UPDATE_ROOT=https://mirrors.ustc.edu.cn/rust-static/rustup
rustup default stable
```

WebView2 / NSIS 若 winget 失败：Win11+Edge 常已有 WebView2；NSIS 可选（没有则只打 MSI，需 WiX）。

## 4. 发版

```bat
cd /d C:\build\tg-matrix
scripts\release-win.cmd
REM 或升版本：
scripts\release-win.cmd 0.2.0
```

产物目录：

```
dist\release\vX.Y.Z\
  TGMatrix-X.Y.Z-windows-x64.msi   （或 .exe，视 NSIS/WiX）
  checksums.sha256
  update-manifest.example.json
```

把安装包拷回发版机器 / CDN；按 `docs/更新.md` 配云端更新接口。

## 5. 常见问题

| 现象 | 处理 |
|------|------|
| 禁止运行脚本 | 用 `scripts\release-win.cmd`，不要直接 `.\release.ps1` |
| `缺少依赖：go` | 装 Go 后**新开**终端；或装到 `C:\Program Files\Go` |
| `go` panic / slice bounds | 不要在映射盘编，改到 `C:\build\...` |
| 产物是 `windows-arm64` | 当前是 ARM 机；换 **x64 Windows** 再打 |
| 只有 MSI 没有 exe | 未装 NSIS，可接受或补装 NSIS 后重打 |
