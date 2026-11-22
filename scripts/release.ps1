# 在 Windows 上打正式 release 安装包（NSIS .exe + MSI）
#
# 用法（在仓库根目录；干净系统请先 setup-win）：
#   scripts\release-win.cmd                 # 推荐：自动 Bypass 策略
#   powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\release.ps1
#   .\scripts\release.ps1 0.2.0             # 若已放宽 ExecutionPolicy
#   .\scripts\release.ps1 0.2.0 -DryRun
#
# 环境变量（可选）：
#   GOPROXY / GOSUMDB     同 mac 脚本；默认 goproxy.io、GOSUMDB=off
#   CARGO_HOME            默认 <repo>\.cargo-home
#   CARGO_TARGET_DIR      默认 apps\desktop\src-tauri\target
#
# 产物目录：
#   dist\release\vX.Y.Z\
#     ├── TGMatrix-X.Y.Z-windows.exe
#     ├── TGMatrix-X.Y.Z-windows.msi
#     ├── checksums.sha256
#     └── update-manifest.example.json
#
# 前置：Go、Node/npm、Rust/cargo、WebView2、VS C++ Build Tools；
#       打 MSI 需 WiX；打 NSIS 需 NSIS（Tauri 会按需提示）。
# 说明：须在 Windows 本机执行，不支持从 macOS 交叉编译。

[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [string]$Version = "",

    [switch]$DryRun,

    [switch]$Help
)

$ErrorActionPreference = "Stop"

function Get-WinArch {
    $a = ($env:PROCESSOR_ARCHITECTURE + "").ToUpperInvariant()
    if ($a -eq "ARM64") { return "arm64" }
    return "amd64"
}

$GO_ARCH = Get-WinArch
# CDN 文件名：windows-arm64 / windows-x64（避免 ARM 包被当成 x64）
$PLATFORM = if ($GO_ARCH -eq "arm64") { "windows-arm64" } else { "windows-x64" }

if ($Help) {
    Get-Content -LiteralPath $MyInvocation.MyCommand.Path -TotalCount 28 |
        ForEach-Object { $_ -replace '^# ?', '' }
    exit 0
}

$Root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
Set-Location $Root

# GitHub Actions / cp1252 控制台：避免 Python print 中文报 UnicodeEncodeError
$env:PYTHONUTF8 = "1"
$env:PYTHONIOENCODING = "utf-8"
try {
    [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new()
    $OutputEncoding = [System.Text.UTF8Encoding]::new()
} catch {}

if (-not $env:GOPROXY) { $env:GOPROXY = "https://goproxy.io,direct" }
if (-not $env:GOSUMDB) { $env:GOSUMDB = "off" }
$GoCacheMod = Join-Path $Root ".gocache\mod"
if ((Test-Path $GoCacheMod) -and -not $env:GOMODCACHE) {
    $env:GOMODCACHE = $GoCacheMod
}

$TauriDir = Join-Path $Root "apps\desktop\src-tauri"
$TauriConf = Join-Path $TauriDir "tauri.conf.json"
$DesktopPkg = Join-Path $Root "apps\desktop\package.json"
$RootPkg = Join-Path $Root "package.json"
$CargoToml = Join-Path $TauriDir "Cargo.toml"
$SidecarOut = Join-Path $Root "dist\sidecar\tg-sidecar.exe"
$SidecarBinDir = Join-Path $TauriDir "binaries"
$SidecarResource = Join-Path $SidecarBinDir "tg-sidecar.exe"

if (-not $env:CARGO_HOME) {
    $env:CARGO_HOME = Join-Path $Root ".cargo-home"
}
if (-not $env:CARGO_TARGET_DIR) {
    $env:CARGO_TARGET_DIR = Join-Path $TauriDir "target"
}

function Refresh-Path {
    $env:Path = [System.Environment]::GetEnvironmentVariable("Path", "Machine") + ";" +
                [System.Environment]::GetEnvironmentVariable("Path", "User")
}

function Add-PathIfExists([string]$Dir) {
    if ($Dir -and (Test-Path $Dir)) {
        if (($env:Path -split ";") -notcontains $Dir) {
            $env:Path = $Dir + ";" + $env:Path
        }
    }
}

function Ensure-DevToolPaths {
    Refresh-Path
    $arch = Get-WinArch
    $candidates = @(
        "C:\Program Files\Go\bin",
        "C:\Go\bin",
        (Join-Path $env:USERPROFILE "go\bin"),
        (Join-Path $env:USERPROFILE "sdk\go\bin"),
        (Join-Path $env:USERPROFILE ".cargo\bin"),
        (Join-Path $env:USERPROFILE "AppData\Local\Programs\Rustup\bin"),
        (Join-Path $env:ProgramFiles "nodejs"),
        (Join-Path ${env:ProgramFiles(x86)} "nodejs"),
        (Join-Path $env:LOCALAPPDATA "Programs\Python\Python312"),
        (Join-Path $env:LOCALAPPDATA "Programs\Python\Python312\Scripts"),
        (Join-Path $env:LOCALAPPDATA "Microsoft\WinGet\Links")
    )
    foreach ($d in $candidates) { Add-PathIfExists $d }
}

function Assert-Command([string]$Name) {
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        if ($Name -eq "go") {
            throw @"
缺少依赖：go

此前 setup-win 里 Go 常因 go.dev 超时没装上。请管理员 CMD 执行其一：
  1) winget install --id GoLang.Go -e --accept-package-agreements --accept-source-agreements
  2) 浏览器打开 https://golang.google.cn/dl/ 下载 windows-$($GO_ARCH).msi 安装
装完后关闭终端重开，再跑 scripts\release-win.cmd
"@
        }
        throw "缺少依赖：$Name"
    }
}

function Get-Python {
    foreach ($c in @("python", "python3", "py")) {
        $cmd = Get-Command $c -ErrorAction SilentlyContinue
        if ($cmd) {
            if ($c -eq "py") {
                return @{ Exe = $cmd.Source; PrefixArgs = @("-3") }
            }
            return @{ Exe = $cmd.Source; PrefixArgs = @() }
        }
    }
    throw "缺少 Python 3（python / python3 / py）"
}

# 注意：禁止用参数名 $Args（与 PowerShell 自动变量冲突，会导致 python 无参进入 >>>）
function Invoke-Python {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$PyArgs
    )
    $py = Get-Python
    $all = @()
    if ($py.PrefixArgs) { $all += $py.PrefixArgs }
    $all += $PyArgs
    Write-Host ("  python: " + $py.Exe + " " + ($all -join " "))
    & $py.Exe @all
    if ($LASTEXITCODE -ne 0) {
        throw "Python 退出码 $LASTEXITCODE"
    }
}

# Windows 下 python -c 的双引号会被命令行吃掉；写临时 .py 再跑
function Invoke-PythonScript {
    param(
        [Parameter(Mandatory = $true)][string]$Code,
        [string[]]$ScriptArgs = @()
    )
    $tmp = Join-Path $env:TEMP ("tg-release-" + [guid]::NewGuid().ToString("N") + ".py")
    try {
        $utf8 = New-Object System.Text.UTF8Encoding $false
        [System.IO.File]::WriteAllText($tmp, $Code, $utf8)
        if (-not (Test-Path -LiteralPath $tmp)) {
            throw "未能写入临时脚本: $tmp"
        }
        $call = @($tmp)
        if ($ScriptArgs) { $call += $ScriptArgs }
        Invoke-Python -PyArgs $call
    }
    finally {
        Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue
    }
}

function Invoke-PythonScriptCapture {
    param(
        [Parameter(Mandatory = $true)][string]$Code,
        [string[]]$ScriptArgs = @()
    )
    $tmp = Join-Path $env:TEMP ("tg-release-" + [guid]::NewGuid().ToString("N") + ".py")
    try {
        $utf8 = New-Object System.Text.UTF8Encoding $false
        [System.IO.File]::WriteAllText($tmp, $Code, $utf8)
        $py = Get-Python
        $all = @()
        if ($py.PrefixArgs) { $all += $py.PrefixArgs }
        $all += $tmp
        if ($ScriptArgs) { $all += $ScriptArgs }
        $out = & $py.Exe @all
        if ($LASTEXITCODE -ne 0) {
            throw "Python 退出码 $LASTEXITCODE"
        }
        return $out
    }
    finally {
        Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue
    }
}

function Read-CurrentVersion {
    $code = @'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    print(json.load(f).get("version", "0.0.0"))
'@
    try {
        $out = Invoke-PythonScriptCapture -Code $code -ScriptArgs @($TauriConf)
    }
    catch {
        throw "读取版本失败: $($_.Exception.Message)"
    }
    return ($out | Select-Object -Last 1).ToString().Trim()
}

function Assert-SemVer([string]$V) {
    if ($V -notmatch '^\d+\.\d+\.\d+$') {
        throw "版本号须为 semver：MAJOR.MINOR.PATCH，收到: $V"
    }
}

function Write-Version([string]$Ver) {
    $code = @'
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
print("wrote version", ver)
'@
    Invoke-PythonScript -Code $code -ScriptArgs @($Ver, $TauriConf, $DesktopPkg, $RootPkg, $CargoToml)
}

function Test-NsisAvailable {
    if (Get-Command makensis -ErrorAction SilentlyContinue) { return $true }
    foreach ($d in @(
        "C:\Program Files (x86)\NSIS\makensis.exe",
        "C:\Program Files\NSIS\makensis.exe"
    )) {
        if (Test-Path $d) { return $true }
    }
    return $false
}

function Test-WixAvailable {
    if (Get-Command candle -ErrorAction SilentlyContinue) { return $true }
    if (Get-Command wix -ErrorAction SilentlyContinue) { return $true }
    $roots = @(
        "${env:ProgramFiles(x86)}\WiX Toolset v3.14\bin",
        "${env:ProgramFiles}\WiX Toolset v3.14\bin",
        "${env:ProgramFiles(x86)}\WiX Toolset v3.11\bin"
    )
    foreach ($r in $roots) {
        if (Test-Path (Join-Path $r "candle.exe")) { return $true }
    }
    return $false
}

function Ensure-BundleTargets {
    $targets = @()
    # CI 可设 TG_RELEASE_TARGETS=nsis（逗号分隔），避免缺 WiX 时踩坑
    if ($env:TG_RELEASE_TARGETS) {
        $targets = @($env:TG_RELEASE_TARGETS.Split(",") | ForEach-Object { $_.Trim() } | Where-Object { $_ })
        Write-Host ("TG_RELEASE_TARGETS -> " + ($targets -join ","))
    }
    else {
        if (Test-NsisAvailable) {
            $targets += "nsis"
        }
        else {
            Write-Host "!! 未检测到 NSIS(makensis)，本次不打 .exe 安装包" -ForegroundColor Yellow
        }
        if (Test-WixAvailable) {
            $targets += "msi"
        }
        else {
            Write-Host "!! 未检测到 WiX，本次不打 .msi；可只依赖 NSIS 或稍后补装 WiX" -ForegroundColor Yellow
        }
    }
    if ($targets.Count -eq 0) {
        # 至少让 tauri 产出可执行文件；安装包后补
        Write-Host "!! NSIS/WiX 都没有：targets 设为 nsis（构建可能失败，请先装其一）" -ForegroundColor Yellow
        $targets = @("nsis")
    }
    $targetsCsv = [string]::Join(",", $targets)
    $code = @'
import json, sys
from pathlib import Path

conf_path = Path(sys.argv[1])
want_targets = [t for t in sys.argv[2].split(",") if t]
data = json.loads(conf_path.read_text(encoding="utf-8"))
bundle = data.setdefault("bundle", {})
want_resources = ["binaries/tg-sidecar.exe"]
changed = False
if bundle.get("targets") != want_targets:
    bundle["targets"] = want_targets
    changed = True
if bundle.get("resources") != want_resources:
    bundle["resources"] = want_resources
    changed = True
if changed:
    conf_path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print("updated bundle.targets ->", bundle["targets"])
    print("updated bundle.resources ->", bundle["resources"])
else:
    print("bundle.targets / resources already", want_targets)
'@
    Invoke-PythonScript -Code $code -ScriptArgs @($TauriConf, $targetsCsv)
}

function Test-LikelyNetworkRoot {
    try {
        $drive = (Get-Item -LiteralPath $Root).PSDrive
        if ($drive -and $drive.DisplayRoot) { return $true }
        # Z: 等映射盘常无 DisplayRoot 时，用 DriveType
        $letter = $Root.Substring(0, 1)
        $vol = Get-PSDrive -Name $letter -ErrorAction SilentlyContinue
        if ($vol -and $vol.Provider.Name -eq "FileSystem" -and $vol.DisplayRoot) { return $true }
    } catch {}
    # 共享盘路径启发式
    if ($Root -match '^[A-Za-z]:\\project\\') { return $true }
    return $false
}

function Ensure-LocalGoCaches {
    # 网络盘 + 中文路径上 go mod tidy/walk 可能 panic；缓存放到本机盘
    $localBase = Join-Path $env:LOCALAPPDATA "tg-matrix-go"
    New-Item -ItemType Directory -Force -Path $localBase | Out-Null
    if (-not $env:GOMODCACHE) {
        $env:GOMODCACHE = Join-Path $localBase "mod"
    }
    if (-not $env:GOCACHE) {
        $env:GOCACHE = Join-Path $localBase "cache"
    }
    New-Item -ItemType Directory -Force -Path $env:GOMODCACHE | Out-Null
    New-Item -ItemType Directory -Force -Path $env:GOCACHE | Out-Null
    Write-Host ("  GOMODCACHE=" + $env:GOMODCACHE)
    Write-Host ("  GOCACHE=" + $env:GOCACHE)
}

function Build-Sidecar {
    Write-Host "==> 编译 Go sidecar (windows/$GO_ARCH .exe)"
    Assert-Command "go"
    New-Item -ItemType Directory -Force -Path (Split-Path $SidecarOut) | Out-Null
    New-Item -ItemType Directory -Force -Path $SidecarBinDir | Out-Null
    Ensure-LocalGoCaches

    if (Test-LikelyNetworkRoot) {
        Write-Host "!! 仓库疑似在网络/映射盘 ($Root)" -ForegroundColor Yellow
        Write-Host "   若 go build 仍 panic，请复制到本机盘再打包，例如:" -ForegroundColor Yellow
        Write-Host "   xcopy /E /I /Y `"$Root`" C:\build\tg-matrix" -ForegroundColor Yellow
        Write-Host "   cd /d C:\build\tg-matrix && scripts\release-win.cmd" -ForegroundColor Yellow
    }

    Push-Location (Join-Path $Root "services\sidecar")
    try {
        # 发版默认不 tidy：网络盘上 go mod tidy 会 panic (slice bounds)
        # 需要整理依赖时设 TG_GO_TIDY=1
        if ($env:TG_GO_TIDY -eq "1") {
            Write-Host "  TG_GO_TIDY=1 -> go mod tidy"
            & go mod tidy
            if ($LASTEXITCODE -ne 0) { throw "go mod tidy 失败" }
        }
        else {
            Write-Host "  跳过 go mod tidy（设 TG_GO_TIDY=1 可强制）"
        }
        # 本机架构编译；ARM 虚拟机勿写死 amd64
        $env:CGO_ENABLED = "0"
        $env:GOOS = "windows"
        $env:GOARCH = $GO_ARCH
        & go build -trimpath -ldflags="-s -w" -o $SidecarOut ./cmd/sidecar
        if ($LASTEXITCODE -ne 0) {
            throw @"
go build sidecar 失败

若出现 panic / slice bounds：不要在 Z: 映射盘上编，复制到本机后再编：
  mkdir C:\build & xcopy /E /I /Y `"$Root`" C:\build\tg-matrix
  cd /d C:\build\tg-matrix
  scripts\release-win.cmd
"@
        }
    }
    finally {
        Pop-Location
        Remove-Item Env:GOOS -ErrorAction SilentlyContinue
        Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
        Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
    }

    Copy-Item -Force $SidecarOut $SidecarResource
    Copy-Item -Force $SidecarOut (Join-Path $SidecarBinDir "tg-sidecar")
    Write-Host "  -> $SidecarResource"
}

function Collect-Artifacts([string]$Ver) {
    $out = Join-Path $Root "dist\release\v$Ver"
    $bundle = Join-Path $env:CARGO_TARGET_DIR "release\bundle"
    New-Item -ItemType Directory -Force -Path $out | Out-Null
    Get-ChildItem -LiteralPath $out -Force -ErrorAction SilentlyContinue |
        Remove-Item -Recurse -Force

    Write-Host "==> 收集产物 -> $out"
    $found = $false
    foreach ($sub in @("nsis", "msi")) {
        $dir = Join-Path $bundle $sub
        if (-not (Test-Path $dir)) { continue }
        Get-ChildItem -LiteralPath $dir -File -ErrorAction SilentlyContinue |
            Where-Object { $_.Extension -in ".exe", ".msi" } |
            ForEach-Object {
                Copy-Item -Force $_.FullName $out
                $found = $true
            }
    }
    if (Test-Path $bundle) {
        Get-ChildItem -LiteralPath $bundle -Recurse -File -ErrorAction SilentlyContinue |
            Where-Object {
                $_.Extension -in ".exe", ".msi" -and
                $_.FullName -match '[\\/](nsis|msi)[\\/]'
            } |
            ForEach-Object {
                $dest = Join-Path $out $_.Name
                if (-not (Test-Path $dest)) {
                    Copy-Item -Force $_.FullName $out
                    $found = $true
                }
            }
    }

    if (-not $found) {
        Write-Host "未找到安装包，请检查 $bundle" -ForegroundColor Red
        if (Test-Path $bundle) {
            Get-ChildItem -LiteralPath $bundle -Recurse -File -ErrorAction SilentlyContinue |
                Select-Object -First 50 |
                ForEach-Object { $_.FullName }
        }
        throw "未找到 nsis/msi 产物"
    }

    Get-ChildItem -LiteralPath $out -File |
        Where-Object { $_.Extension -in ".exe", ".msi" } |
        ForEach-Object {
            $ext = $_.Extension.TrimStart(".")
            $renamed = Join-Path $out "TGMatrix-$Ver-$PLATFORM.$ext"
            if ($_.FullName -ne $renamed) {
                Copy-Item -Force $_.FullName $renamed
            }
        }

    $checksumPath = Join-Path $out "checksums.sha256"
    $lines = @()
    Get-ChildItem -LiteralPath $out -File |
        Where-Object { $_.Extension -in ".exe", ".msi" } |
        Sort-Object Name |
        ForEach-Object {
            $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $_.FullName).Hash.ToLowerInvariant()
            $lines += "$hash  $($_.Name)"
        }
    $lines | Set-Content -LiteralPath $checksumPath -Encoding utf8

    $primary = ""
    $exeName = "TGMatrix-$Ver-$PLATFORM.exe"
    $msiName = "TGMatrix-$Ver-$PLATFORM.msi"
    if (Test-Path (Join-Path $out $exeName)) {
        $primary = $exeName
    }
    elseif (Test-Path (Join-Path $out $msiName)) {
        $primary = $msiName
    }

    $code = @'
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
    "supportContact": "https://github.com/owlteam990/tg-matrix/issues",
    "publishedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "_note": "Set downloadUrl to the GitHub Release asset URL",
}
Path(out, "update-manifest.example.json").write_text(
    json.dumps(manifest, ensure_ascii=False, indent=2) + "\n",
    encoding="utf-8",
)
print("wrote update-manifest.example.json")
'@
    Invoke-PythonScript -Code $code -ScriptArgs @($out, $Ver, $PLATFORM, $primary)

    Write-Host ""
    Write-Host "========================================"
    Write-Host " Release 完成：v$Ver ($PLATFORM)"
    Write-Host " 目录：$out"
    Get-ChildItem -LiteralPath $out | Format-Table Name, Length, LastWriteTime
    Write-Host "下一步："
    Write-Host "  1) 上传 $out\TGMatrix-$Ver-$PLATFORM.* 到 CDN"
    Write-Host "  2) 按 update-manifest.example.json 配置云端 /app/update"
    Write-Host "  3) git tag v$Ver （需你手动）"
    Write-Host "========================================"
}

# ---------- main ----------
Ensure-DevToolPaths
Assert-Command "go"
Assert-Command "npm"
Assert-Command "cargo"
Get-Python | Out-Null
Write-Host ("go    : " + (go version))
Write-Host ("cargo : " + (cargo -V))
Write-Host ("npm   : " + (npm -v))

$Current = Read-CurrentVersion
if ($Version) {
    Assert-SemVer $Version
    $ReleaseVer = $Version
}
else {
    $ReleaseVer = $Current
    Assert-SemVer $ReleaseVer
}

Write-Host "==> release 计划"
Write-Host "  platform : $PLATFORM"
Write-Host "  version  : $ReleaseVer (当前配置 $Current)"
Write-Host "  dry-run  : $DryRun"
Write-Host "  out      : $(Join-Path $Root "dist\release\v$ReleaseVer")"

if ($ReleaseVer -ne $Current) {
    Write-Version $ReleaseVer
}
else {
    Write-Host "版本未变更，跳过写入"
}

Ensure-BundleTargets

if ($DryRun) {
    Write-Host "dry-run 结束（未编译）"
    exit 0
}

New-Item -ItemType Directory -Force -Path $env:CARGO_HOME | Out-Null
New-Item -ItemType Directory -Force -Path $env:CARGO_TARGET_DIR | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $Root "dist\release") | Out-Null

Build-Sidecar

Write-Host "==> 安装前端依赖（如需要）"
$DesktopDir = Join-Path $Root "apps\desktop"
Push-Location $DesktopDir
try {
    if (-not (Test-Path (Join-Path $DesktopDir "node_modules"))) {
        & npm install
        if ($LASTEXITCODE -ne 0) { throw "npm install 失败" }
    }

    Write-Host "==> Tauri release build"
    & npm run tauri:build
    if ($LASTEXITCODE -ne 0) { throw "tauri build 失败" }
}
finally {
    Pop-Location
}

Collect-Artifacts $ReleaseVer
