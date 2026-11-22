# TG Matrix

**开源、免费、纯本地的 Telegram 多协议号桌面工具。**  
一套界面管理多个协议号：导入会话、绑代理、私信/群组/联系人/采集与任务队列——数据只留在你的电脑上。

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](./LICENSE)
[![GitHub](https://img.shields.io/badge/GitHub-owlteam990%2Ftg-matrix-181717?logo=github)](https://github.com/owlteam990/tg-matrix)

> **无注册、无登录、无云端许可、无积分扣费。**  
> 协议号会话、代理、任务与本地库全部保存在本机；本仓库不连接任何自有云端账号或计费接口。

## 技术栈

| 层 | 技术 |
|---|---|
| 桌面壳 | Tauri 2 + Vue 3 + TypeScript |
| 本地服务 | Go sidecar + SQLite + [gotd](https://github.com/gotd/td)（一协议号一 Client，可按号绑代理） |

## 核心能力

- **协议号池**：导入 `.session` + `.json`（或 session_string），按号绑定 HTTP/SOCKS 代理
- **消息**：会话列表、历史、收发；群发支持多号策略与多文案轮换
- **群组 / 联系人**：加退群、邀请、通讯录；已加入群与通讯录可按协议号缓存在本地
- **采集**：群成员 / 发言者采集、手机号检查
- **营销流水线**：产品库 → 线索 → 手动写话术 → 入队限速发送
- **任务中心**：批量任务进度、取消、断点相关状态

本开源版**不包含任何远程 AI / 账号云端接口**；话术与回复均需人工编写。

## 目录

```text
apps/desktop/          # Vue UI + Tauri 原生壳
services/sidecar/      # 本地 Go 服务（127.0.0.1）
scripts/               # 编译 / 发版脚本
docs/                  # 交互说明与打包备忘（部分为本地笔记）
```

## 快速开始（开发）

需安装：Node.js 20+、Go 1.22+、Rust（cargo）。

```bash
# 1) 编译并重启本地 sidecar
npm run sidecar:restart

# 2) 原生窗口开发（热更新前端）
npm run desktop:dev
# 或：cd apps/desktop && npm run tauri:dev
```

可选环境变量（Telegram API 凭据，自备）：

```bash
export TG_APP_ID=你的api_id
export TG_APP_HASH=你的api_hash
```

Sidecar 默认：`http://127.0.0.1:17888`（仅本机）。

拉 Go 依赖若遇代理问题：

```bash
export GOPROXY=https://goproxy.io,direct
export GOSUMDB=off
```

## 编译桌面 App

```bash
./scripts/build.sh app
# 或
npm run build:app
```

产物在 `dist/app/*.app`（macOS）。

## 打正式 Release 包

### macOS

```bash
TG_RELEASE_SKIP_SIGN=1 ./scripts/release.sh
# 升版本
TG_RELEASE_SKIP_SIGN=1 ./scripts/release.sh 0.2.0
```

产物在 `dist/release/vX.Y.Z/`。外发签名见 `scripts/release.sh` 头部注释。

### Windows（须在 Windows 本机）

详见 [`docs/Windows打包.md`](docs/Windows打包.md)。

```bash
# 在 mac 上导出给 Windows 的源码 zip
./scripts/pack-win-source.sh
```

目标机建议 x64 Windows：

```bat
scripts\setup-win.cmd
scripts\release-win.cmd
```

## GitHub Actions 自动发版

推送符合 `v*` 的 tag 后，CI 会在 **macOS** 打 `.dmg`、在 **Windows** 打 NSIS `.exe`，并上传到 GitHub Release（未做 Apple 公证 / Windows 代码签名）。

```bash
git tag v0.1.0
git push origin v0.1.0
```

也可在 Actions → **Release** → Run workflow，填写已有 tag。

## 本地数据

打包 App 的数据目录约在（macOS）：

`~/Library/Application Support/local.tgmatrix.desktop`

开发态可用仓库下 `.data/`（已在 `.gitignore` 中忽略）。

## 许可证

[Apache License 2.0](./LICENSE)。可商用；修改与再分发请保留版权与许可证声明，详见 [NOTICE](./NOTICE)。

## 免责声明

本项目仅供学习与自用运维辅助。请遵守 Telegram 服务条款与当地法律法规；滥用批量行为可能导致账号限制，风险自负。
