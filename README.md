# TG 多帐号管理（tg-matrix）

本地多协议号 Telegram 操作工具（Win / mac）。

## 技术栈

- 桌面 UI：Vue 3 + TypeScript + Vite（微信风三栏壳；Tauri 2 封装后续接入）
- Sidecar：Go + **modernc.org/sqlite** + **gotd**（一协议号一 Client，可按号绑代理）
- 交互：`docs/Interaction-Logic.md`
## 目录

```text
apps/desktop/          # 前端三栏壳
services/sidecar/      # 本地服务
docs/                  # 需求与计划
```

## 编译

需安装：Node.js 20+、Go 1.22+。

```bash
# 编译当前平台 sidecar + 桌面前端 → dist/
./scripts/build.sh
# 或
npm run build

# 同时交叉编译 darwin/amd64、darwin/arm64、windows/amd64
BUILD_CROSS=1 ./scripts/build.sh
```

产物：

- `dist/sidecar/tg-sidecar`（当前平台二进制）
- `dist/desktop/`（静态前端）

## 本地开发

```bash
# 终端 1：sidecar（可选配置 Telegram API，用于 gotd 检查）
export TG_APP_ID=你的api_id
export TG_APP_HASH=你的api_hash
npm run sidecar:dev

# 终端 2：桌面 UI
cd apps/desktop && npm install && npm run dev
```

默认 sidecar：`http://127.0.0.1:17888`  

| 接口 | 说明 |
|---|---|
| `GET /health` | `store=sqlite`，`gotdConfigured` |
| `GET /v1/accounts` | 协议号列表 |
| `POST /v1/accounts/{id}/check` | gotd 授权检查（需配置 API 与已有 session） |

数据目录默认 `~/.tg-matrix`；开发脚本使用仓库内 `.data/`（`app.db`）。

拉 Go 依赖若遇代理问题：

```bash
export GOPROXY=https://goproxy.io,direct
export GOSUMDB=off
export GOMODCACHE="$PWD/.gocache/mod"
```

## 当前进度（P1）

- [x] Sidecar health / accounts
- [x] SQLite + gotd 接入
- [x] 微信风三栏 UI + 连通状态
- [ ] Tauri 2 打包壳
- [ ] Session 导入与按协议号代理绑定
- [ ] 账号检查 UI 联调
