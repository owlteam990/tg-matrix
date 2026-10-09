## 2026-10-09

### 模块：工程基建

**变动类型：** 新增

**变动描述：**
- 初始化本地 Git 仓库（无远端）
- 完善 `.gitignore`：依赖、编译产物、`.data`/SQLite、日志临时文件、IDE、密钥与 Go 缓存等

**影响范围：**
- `.gitignore`、本地 `.git`

**变动人：** Auto

---

## 2026-10-08

### 模块：sidecar（依赖接入）

**变动类型：** 优化

**变动描述：**
- 接入 `modernc.org/sqlite`：本地库迁移为 `app.db`（accounts/proxies/tasks/entities 等）
- 接入 `github.com/gotd/td`：`tgclient.Manager` 实现一协议号一 Client，支持按协议号 SOCKS 代理
- 新增 `POST /v1/accounts/{id}/check`；`/health` 返回 `store=sqlite` 与 `gotdConfigured`
- 环境变量：`TG_APP_ID` / `TG_APP_HASH`

**影响范围：**
- 代码：`services/sidecar/**`
- 前端：health 展示字段
- 文档：`README.md`

**变动人：** Auto

---

## 2026-10-08

### 模块：P1 工程骨架

**变动类型：** 新增

**变动描述：**
- 新增 `services/sidecar`：本地 HTTP（`/health`、`/v1/accounts`），JSON 本地存储骨架（因 Go 代理超时暂未接入 SQLite/gotd）
- 新增 `apps/desktop`：Vue3 微信风三栏壳，对接 sidecar 健康检查与协议号列表
- 根目录增加 `README.md`、`package.json` 脚本与 `.gitignore`

**影响范围：**
- 代码：`apps/desktop/**`、`services/sidecar/**`
- 文档：`README.md`

**变动人：** Auto

---

## 2026-10-08

### 模块：需求文档（docs）

**变动类型：** 新增 / 优化

**变动描述：**
- 新增 `docs/Interaction-Logic.md` v1.0：混合三栏（图标导航 + 协议号池 + 工作区）+ 任务流；明确一桌面客户端管理多 TG 协议号
- 更新 `Development-Plan.md` 至 v1.2：引用交互文档；工程原则改为「一协议号一 gotd Client」；P0 增加 D7；P1 按三栏壳落地

**影响范围：**
- 文档：`docs/Interaction-Logic.md`、`docs/Development-Plan.md`
- 下游：P0 UI/PRD 评审、P1 壳层实现

**变动人：** Auto

---

## 2026-10-08

### 模块：需求文档（docs）

**变动类型：** 优化

**变动描述：**
- 背景：使需求闭环分析与功能清单 v1.1 中的自研候选项及优先级保持一致
- 更新 `Requirements-Closure-Analysis.md` 至 v1.2，同步 C01-C07（P1/P2）及活动级/收件人级结果追踪平台候选
- 标明候选均为未闭环、需评审后确定范围；优先级不代表纳入 MVP 或获准开发

**影响范围：**
- 文档：`docs/Requirements-Closure-Analysis.md`
- 下游：MVP PRD、候选功能评审与平台任务/日志规格

**变动人：** Auto

---

## 2026-10-08

### 模块：需求文档（docs）

**变动类型：** 优化

**变动描述：**
- 背景：对照 FeitApp 与 TeleSender 的公开功能介绍，补充自研产品候选功能及建议优先级
- 更新 `Telegram-Solutions-Feature-List.md` 至 v1.1，新增 C01-C07 候选项：活动管理、个性化模板、联系人库、采集筛选、采集到活动工作流、入站自动回复、关键词发现
- 增加 P1 平台候选能力：活动级与收件人级结果追踪；明确候选优先级不等于 MVP 开发批准
- 标注竞品公开宣称未经独立验证，并补充合规、数据来源和可验收边界

**影响范围：**
- 文档：`docs/Telegram-Solutions-Feature-List.md`
- 下游：MVP PRD 评审、平台任务/日志规格

**变动人：** Auto

---

## 2026-10-08

### 模块：需求文档（docs）

**变动类型：** 优化

**变动描述：**
- 更新 `Development-Plan.md` 至 v1.1
- UI：整体采用 PC 微信风格（白底、主绿按钮/选中态），P0 增补 UI 令牌，P1 按风格搭壳
- 产品期次：一期=P0～P4，二期=P5，三期=P7
- 新增 P7 第三期：注册登录、许可证私钥（仅服务端）、服务端风控规则、付费校验算法等后端依赖能力；明确一期离线可用、敏感逻辑不下发客户端

**影响范围：**
- 文档：`docs/Development-Plan.md`
- 下游：MVP PRD、UI 令牌、三期商业化后端 PRD

**变动人：** Auto

---

## 2026-10-08

### 模块：需求文档（docs）

**变动类型：** 新增

**变动描述：**
- 背景：闭环分析 v1.2 与功能清单 v1.1 已对齐范围（含 C01–C07 候选与不做项），需转为可执行排期
- 新增 `docs/Development-Plan.md`：P0 规格 → P1 骨架 → P2 平台 → P3 MVP 业务 → P4 发布 → P5 二期 → P6 候选评审门
- 锁定技术栈 Tauri 2 + Vue 3 + Go/gotd；MVP 约 8–12 周；含裁剪后验收清单与风险对策

**影响范围：**
- 文档：`docs/Development-Plan.md`
- 下游：P0 规格文档与 monorepo 开工顺序

**变动人：** Auto

---

## 2026-10-08

### 模块：需求文档（docs）

**变动类型：** 优化

**变动描述：**
- 背景：未闭环功能无验收标准，决定直接移出产品范围
- 更新 `Requirements-Closure-Analysis.md` 至 v1.1：F05、F17、AI/CRM/转化统计、外部 API/CLI/Webhook 标记为 **明确不做**
- 从二期澄清清单中删除 F05/F17；UI/卖点/排期均不得包含上述项

**影响范围：**
- 文档：`docs/Requirements-Closure-Analysis.md`
- 下游：MVP PRD 不得纳入上述不做项

**变动人：** Auto

---

## 2026-10-08

### 模块：需求文档（docs）

**变动类型：** 新增

**变动描述：**
- 背景：技术栈已定为 Tauri 2 + Vue 3 + TypeScript + Go/gotd + SQLite，需在开工前明确需求闭环状态
- 新增 `docs/Requirements-Closure-Analysis.md`：将 25 个业务模块与平台能力划分为已闭环 / 部分闭环 / 未闭环，并给出 MVP 建议包与后续文档清单
- 明确自研需覆盖 Windows + macOS；F22/F18 等列入二期

**影响范围：**
- 文档：`docs/Requirements-Closure-Analysis.md`
- 下游：MVP PRD、任务状态机、错误码表等待编写文档

**变动人：** Auto
