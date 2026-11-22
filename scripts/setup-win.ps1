# Install Windows release dependencies via winget (clean VM).
#
# Usage (must Bypass ExecutionPolicy):
#   powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\setup-win.ps1
# Or:
#   scripts\setup-win.cmd
#
# Supports AMD64 and ARM64 (Parallels / Hyper-V on Apple Silicon).
# Installs: Git, Python3, Go, Node LTS, Rustup, VS 2022 Build Tools (C++),
#           WebView2, NSIS, WiX Toolset
#
# Network tips (CN / ARM VM): go.dev / sourceforge / msedge CDN may fail;
# this script retries and uses mirrors where possible.
#
# After install: close terminal, reopen, then run scripts\release-win.cmd

[CmdletBinding()]
param(
    [switch]$SkipOptional,
    [switch]$Help
)

$ErrorActionPreference = "Stop"

if ($Help) {
    Get-Content -LiteralPath $MyInvocation.MyCommand.Path -TotalCount 24 |
        ForEach-Object { $_ -replace '^# ?', '' }
    exit 0
}

function Get-WinArch {
    $a = ($env:PROCESSOR_ARCHITECTURE + "").ToUpperInvariant()
    if ($a -eq "ARM64") { return "arm64" }
    return "amd64"
}

function Get-RustHostTriple {
    if ((Get-WinArch) -eq "arm64") {
        return "aarch64-pc-windows-msvc"
    }
    return "x86_64-pc-windows-msvc"
}

function Refresh-Path {
    $env:Path = [System.Environment]::GetEnvironmentVariable("Path", "Machine") + ";" +
                [System.Environment]::GetEnvironmentVariable("Path", "User")
}

function Assert-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $p = New-Object Security.Principal.WindowsPrincipal($id)
    if (-not $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        Write-Host '请用管理员身份运行 PowerShell / CMD（安装 Build Tools 需要）。' -ForegroundColor Yellow
        throw '需要管理员权限'
    }
}

function Assert-Winget {
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
        throw '未找到 winget。请先打开 Microsoft Store 更新“应用安装程序”，或换用较新的 Win10/11 镜像。'
    }
}

function Install-WingetPkg {
    param(
        [Parameter(Mandatory = $true)][string]$Id,
        [string]$Name = $Id,
        [int]$Retries = 1
    )
    Write-Host ""
    Write-Host "==> 安装 $Name ($Id)"
    $ok = $false
    for ($i = 1; $i -le $Retries; $i++) {
        if ($i -gt 1) {
            Write-Host "  重试 $i/$Retries ..."
            Start-Sleep -Seconds (3 * $i)
        }
        & winget install --id $Id -e --accept-package-agreements --accept-source-agreements --disable-interactivity
        $code = $LASTEXITCODE
        if ($code -eq 0 -or $code -eq -1978335189) {
            $ok = $true
            break
        }
        Write-Host "!! winget 退出码 $code" -ForegroundColor Yellow
    }
    return $ok
}

function Test-WebView2Installed {
    $keys = @(
        "HKLM:\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}",
        "HKLM:\SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}",
        "HKCU:\SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"
    )
    foreach ($k in $keys) {
        if (Test-Path $k) {
            $pv = (Get-ItemProperty -Path $k -ErrorAction SilentlyContinue).pv
            if ($pv) {
                Write-Host "已检测到 WebView2: $pv"
                return $true
            }
        }
    }
    # Edge 常自带运行时
    $edge = @(
        "${env:ProgramFiles(x86)}\Microsoft\EdgeWebView\Application",
        "$env:ProgramFiles\Microsoft\EdgeWebView\Application",
        "$env:ProgramFiles\Microsoft\Edge\Application"
    )
    foreach ($d in $edge) {
        if (Test-Path $d) {
            Write-Host "已检测到 Edge/WebView 目录: $d"
            return $true
        }
    }
    return $false
}

function Install-GoFallback {
    $arch = Get-WinArch
    $goArch = if ($arch -eq "arm64") { "arm64" } else { "amd64" }
    $ver = "1.24.6"
    $msi = "go$ver.windows-$goArch.msi"
    $urls = @(
        "https://golang.google.cn/dl/$msi",
        "https://mirrors.aliyun.com/golang/$msi",
        "https://go.dev/dl/$msi"
    )
    $tmp = Join-Path $env:TEMP $msi
    foreach ($url in $urls) {
        Write-Host "==> Go 备用下载: $url"
        try {
            [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
            Invoke-WebRequest -Uri $url -OutFile $tmp -UseBasicParsing -TimeoutSec 180
            Write-Host "==> 安装 $tmp"
            Start-Process msiexec.exe -ArgumentList "/i `"$tmp`" /qn /norestart" -Wait -NoNewWindow
            Write-Host "Go 备用安装完成"
            return $true
        }
        catch {
            Write-Host ("!! 下载/安装失败: " + $_.Exception.Message) -ForegroundColor Yellow
        }
    }
    return $false
}

function Install-WebView2Fallback {
    if (Test-WebView2Installed) {
        Write-Host "WebView2 已存在，跳过下载"
        return $true
    }
    $arch = Get-WinArch
    # evergreen bootstrapper (arch-specific)
    $urls = @()
    if ($arch -eq "arm64") {
        $urls += "https://go.microsoft.com/fwlink/?linkid=2093504"  # ARM64
    }
    else {
        $urls += "https://go.microsoft.com/fwlink/?linkid=2124701"  # x64
    }
    $urls += "https://go.microsoft.com/fwlink/p/?LinkId=2124703"  # evergreen bootstrapper
    $tmp = Join-Path $env:TEMP "MicrosoftEdgeWebView2RuntimeInstaller.exe"
    foreach ($url in $urls) {
        Write-Host "==> WebView2 备用下载: $url"
        try {
            [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
            Invoke-WebRequest -Uri $url -OutFile $tmp -UseBasicParsing -TimeoutSec 180
            Write-Host "==> 静默安装 WebView2"
            Start-Process -FilePath $tmp -ArgumentList "/silent /install" -Wait -NoNewWindow
            if (Test-WebView2Installed) {
                Write-Host "WebView2 备用安装完成"
                return $true
            }
        }
        catch {
            Write-Host ("!! WebView2 备用失败: " + $_.Exception.Message) -ForegroundColor Yellow
        }
    }
    Write-Host "!! WebView2 未装上。可本机浏览器打开并下载:" -ForegroundColor Yellow
    Write-Host "   https://developer.microsoft.com/microsoft-edge/webview2/"
    Write-Host "   选 Evergreen ARM64 或 x64 Runtime"
    return $false
}

function Install-NsisFallback {
    # SourceForge 在部分网络会 0x80072efd；优先 GitHub release
    $ver = "3.10"
    $exe = "nsis-$ver-setup.exe"
    $urls = @(
        "https://github.com/kichik/nsis/releases/download/v$ver/$exe",
        "https://mirror.ghproxy.com/https://github.com/kichik/nsis/releases/download/v$ver/$exe",
        "https://npmmirror.com/mirrors/nsis/$ver/$exe"
    )
    $tmp = Join-Path $env:TEMP $exe
    foreach ($url in $urls) {
        Write-Host "==> NSIS 备用下载: $url"
        try {
            [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
            Invoke-WebRequest -Uri $url -OutFile $tmp -UseBasicParsing -TimeoutSec 180
            Write-Host "==> 安装 NSIS"
            Start-Process -FilePath $tmp -ArgumentList "/S" -Wait -NoNewWindow
            Refresh-Path
            if (Get-Command makensis -ErrorAction SilentlyContinue) {
                Write-Host "NSIS 备用安装完成"
                return $true
            }
            # 常见安装路径
            $nsisDir = "C:\Program Files (x86)\NSIS"
            if (Test-Path (Join-Path $nsisDir "makensis.exe")) {
                Write-Host "NSIS 已安装到 $nsisDir"
                return $true
            }
            Write-Host "NSIS 安装程序已跑完（请新开终端验证 makensis）"
            return $true
        }
        catch {
            Write-Host ("!! NSIS 备用失败: " + $_.Exception.Message) -ForegroundColor Yellow
        }
    }
    Write-Host "!! NSIS 未装上。可只打 MSI（需 WiX），或浏览器下载 NSIS 安装包。" -ForegroundColor Yellow
    return $false
}

Assert-Admin
Assert-Winget

$arch = Get-WinArch
$rustTriple = Get-RustHostTriple
Write-Host "检测到 CPU 架构: $arch  (Rust: $rustTriple)"
Write-Host '将安装 TG Matrix Windows 发版依赖（可能较久，请保持网络畅通）'
Write-Host '可选跳过 NSIS/WiX: 加 -SkipOptional'

Install-WingetPkg -Id "Git.Git" -Name "Git" | Out-Null
Install-WingetPkg -Id "Python.Python.3.12" -Name "Python 3.12" | Out-Null

$goOk = Install-WingetPkg -Id "GoLang.Go" -Name "Go" -Retries 3
if (-not $goOk) {
    Write-Host 'winget 安装 Go 失败，改用国内镜像 MSI...' -ForegroundColor Yellow
    if (-not (Install-GoFallback)) {
        Write-Host '!! Go 仍未装上。可稍后手动: winget install GoLang.Go 或 golang.google.cn' -ForegroundColor Yellow
    }
}

Install-WingetPkg -Id "OpenJS.NodeJS.LTS" -Name "Node.js LTS" | Out-Null
Install-WingetPkg -Id "Rustlang.Rustup" -Name "Rustup" | Out-Null
Install-WingetPkg -Id "Microsoft.VisualStudio.2022.BuildTools" -Name "VS 2022 Build Tools" | Out-Null

Write-Host ""
Write-Host "==> WebView2 Runtime"
if (Test-WebView2Installed) {
    Write-Host "本机已有 WebView2，跳过 winget（避免 CDN 403）"
}
else {
    $wvOk = Install-WingetPkg -Id "Microsoft.EdgeWebView2Runtime" -Name "WebView2 Runtime" -Retries 1
    if (-not $wvOk) {
        Write-Host 'winget WebView2 失败（常见 403），改用备用链接 / 说明...' -ForegroundColor Yellow
        Install-WebView2Fallback | Out-Null
    }
}

if (-not $SkipOptional) {
    $nsisOk = Install-WingetPkg -Id "NSIS.NSIS" -Name "NSIS" -Retries 2
    if (-not $nsisOk) {
        Write-Host 'winget NSIS 失败（SourceForge 常不可达），改用 GitHub/镜像...' -ForegroundColor Yellow
        Install-NsisFallback | Out-Null
    }
    Install-WingetPkg -Id "WiXToolset.WiXToolset" -Name "WiX Toolset" -Retries 2 | Out-Null
}

Write-Host ""
Write-Host '==> 配置 Rust MSVC 工具链（若 rustup 已在 PATH）'
Refresh-Path
$rustup = Get-Command rustup -ErrorAction SilentlyContinue
if (-not $rustup) {
    $cand = @(
        (Join-Path $env:USERPROFILE ".cargo\bin\rustup.exe"),
        (Join-Path $env:USERPROFILE "AppData\Local\Programs\Rustup\bin\rustup.exe")
    )
    foreach ($c in $cand) {
        if (Test-Path $c) {
            $env:Path = (Split-Path $c) + ";" + $env:Path
            $rustup = Get-Command rustup -ErrorAction SilentlyContinue
            break
        }
    }
}
if ($rustup) {
    # default 要的是 toolchain（stable / stable-<triple>），不是裸 target triple
    $toolchain = "stable-$rustTriple"
    Write-Host "==> rustup default $toolchain"
    & rustup toolchain install $toolchain -c rustfmt -c clippy
    if ($LASTEXITCODE -ne 0) {
        Write-Host '!! 指定 triple 安装失败，改试: rustup default stable' -ForegroundColor Yellow
        & rustup default stable
    }
    else {
        & rustup default $toolchain
    }
    & rustup target add $rustTriple
    & rustup show
}
else {
    Write-Host '当前 shell 还没有 rustup，请新开终端后执行:' -ForegroundColor Yellow
    Write-Host '  rustup default stable'
    Write-Host ("  rustup target add " + $rustTriple)
}

Write-Host ""
Write-Host '========================================'
Write-Host ' 依赖安装流程结束'
Write-Host (" 架构: " + $arch)
Write-Host ' 1. 打开 Visual Studio Installer，确认 Build Tools 勾选了'
Write-Host '    “使用 C++ 的桌面开发”工作负载（若未自动勾选请补装）'
Write-Host ' 2. WebView2/NSIS 若失败：Win11+Edge 常已有 WebView2；NSIS 仅打 .exe 需要'
Write-Host ' 3. 关闭本窗口，重新开一个终端'
Write-Host ' 4. cd 到仓库根目录，执行:'
Write-Host '      scripts\release-win.cmd'
Write-Host '========================================'
