/**
 * API 基址：桌面 App / 开发态连本地 sidecar；同源静态托管可用相对路径。
 */
function resolveBase(): string {
  if (import.meta.env.VITE_SIDECAR_URL) {
    return import.meta.env.VITE_SIDECAR_URL
  }
  const w = window as Window & { __TAURI_INTERNALS__?: unknown }
  const inTauri = !!w.__TAURI_INTERNALS__ || !!import.meta.env.TAURI_ENV_PLATFORM
  if (inTauri || import.meta.env.DEV) {
    return 'http://127.0.0.1:17888'
  }
  return ''
}

const BASE = resolveBase()

export type Health = {
  ok: boolean
  service: string
  dataDir: string
  uptime: string
  store: string
  gotdConfigured: boolean
}

export type Account = {
  id: string
  label: string
  phone: string
  username: string
  status: string
  proxyId: string | null
}

export type ProxyItem = {
  id: string
  name: string
  protocol: string
  host: string
  port: number
  username: string
  hasPassword: boolean
}

export type CheckResult = {
  accountId: string
  ok: boolean
  userId?: number
  username?: string
  phone?: string
  error?: string
}

async function parseError(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as { message?: string; code?: string }
    return body.message || body.code || res.statusText
  } catch {
    if (res.status === 404) {
      return '接口不存在（404）。请重启桌面应用以加载新版 sidecar'
    }
    return res.statusText || `HTTP ${res.status}`
  }
}

/**
 * 带超时的 fetch，避免僵尸 sidecar 占端口导致 UI 一直「加载中」。
 *
 * @param input URL
 * @param init 选项
 * @param timeoutMs 超时毫秒
 */
async function fetchWithTimeout(
  input: string,
  init: RequestInit = {},
  timeoutMs = 8_000,
): Promise<Response> {
  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), timeoutMs)
  try {
    return await fetch(input, { ...init, signal: ctrl.signal })
  } catch (err) {
    const tip = err instanceof Error ? err.message : String(err)
    if (tip.includes('abort') || tip.includes('Abort')) {
      throw new Error(
        `本地服务超时（${timeoutMs}ms）。请执行 bash scripts/fix-sidecar-write.sh 后刷新`,
      )
    }
    throw err instanceof Error ? err : new Error(tip)
  } finally {
    clearTimeout(timer)
  }
}

/**
 * 拉取 sidecar 健康状态。
 */
export async function fetchHealth(): Promise<Health> {
  const res = await fetchWithTimeout(`${BASE}/health`, {}, 5_000)
  if (!res.ok) {
    throw new Error(`health failed: ${res.status}`)
  }
  return res.json()
}

/**
 * 拉取本地协议号列表。
 */
export async function fetchAccounts(): Promise<Account[]> {
  const res = await fetchWithTimeout(`${BASE}/v1/accounts`, {}, 8_000)
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const body = (await res.json()) as { items: Account[] }
  return body.items ?? []
}

export type ImportFilePart = {
  name: string
  text?: string
  base64?: string
}

export type ImportAccountItem = {
  ok: boolean
  created?: boolean
  source?: string
  account?: Account
  error?: string
  file?: string
}

export type ImportAccountResult = {
  ok: boolean
  account?: Account
  created?: boolean
  items?: ImportAccountItem[]
  note?: string
}

/**
 * 导入协议号（.json / .session 成对或单独，亦可 gotd 原生 JSON）。
 */
export async function importAccount(payload: {
  label?: string
  sessionJson?: string
  sessionFilePath?: string
  accountJson?: string
  sessionBase64?: string
  sessionFileName?: string
  files?: ImportFilePart[]
}): Promise<ImportAccountResult> {
  const res = await fetch(`${BASE}/v1/accounts/import`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 更新协议号（备注、附加已保存代理）。
 *
 * @param id 协议号 ID
 * @param payload label / proxyId（空字符串解绑）
 */
export async function updateAccount(
  id: string,
  payload: { label?: string; proxyId?: string },
): Promise<Account> {
  const res = await fetch(`${BASE}/v1/accounts/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const body = (await res.json()) as { account: Account }
  return body.account
}

/**
 * 删除协议号。
 */
export async function deleteAccount(id: string): Promise<void> {
  const res = await fetch(`${BASE}/v1/accounts/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
}

/**
 * 检查协议号可用性。
 */
export async function checkAccount(id: string): Promise<CheckResult> {
  const res = await fetch(`${BASE}/v1/accounts/${encodeURIComponent(id)}/check`, {
    method: 'POST',
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

export type SpamBotResult = {
  accountId: string
  ok: boolean
  status: string
  businessResult: string
  snippet?: string
  errorCode?: string
  errorMessage?: string
  waitSeconds?: number
  taskId?: string
}

/**
 * SpamBot 状态检查（F12）。
 */
export async function checkSpamBot(id: string): Promise<SpamBotResult> {
  const res = await fetch(`${BASE}/v1/accounts/${encodeURIComponent(id)}/spambot`, {
    method: 'POST',
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

export type TaskSummary = {
  id: string
  module: string
  status: string
  title: string
  createdAt: number
  updatedAt: number
  finishedAt: number
  itemCount: number
  totalCount?: number
  cursorIndex?: number
  waitUntil?: number
  waitReason?: string
  waitRemainingSec?: number
  pendingCount?: number
  /** 有 payload 且仍有 pending，可点继续 */
  resumable?: boolean
  /** 当前进程内是否仍在 Runner 中执行 */
  running?: boolean
}

export type TaskItemRow = {
  id: string
  taskId: string
  accountId: string
  target: string
  status: string
  localResult: string
  apiResult: string
  businessResult: string
  errorCode: string
  errorMessage: string
  createdAt: number
  updatedAt: number
}

/**
 * 拉取任务列表。
 */
export async function fetchTasks(): Promise<TaskSummary[]> {
  const res = await fetch(`${BASE}/v1/tasks`)
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const body = (await res.json()) as { items: TaskSummary[] }
  return body.items ?? []
}

/**
 * 拉取单个任务。
 *
 * @param id 任务 ID
 */
export async function fetchTask(id: string): Promise<TaskSummary> {
  const res = await fetch(`${BASE}/v1/tasks/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const body = (await res.json()) as { task: TaskSummary }
  return body.task
}

/**
 * 拉取任务明细。
 */
export async function fetchTaskItems(id: string): Promise<TaskItemRow[]> {
  const res = await fetch(`${BASE}/v1/tasks/${encodeURIComponent(id)}/items`)
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const body = (await res.json()) as { items: TaskItemRow[] }
  return body.items ?? []
}

/**
 * 取消后台运行中 / 等待中的任务。
 *
 * @param id 任务 ID
 */
export async function cancelTask(id: string): Promise<{ ok: boolean; status: string }> {
  const res = await fetch(`${BASE}/v1/tasks/${encodeURIComponent(id)}/cancel`, {
    method: 'POST',
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 续跑可恢复任务（interrupted / circuit_break 等，跳过已完成明细）。
 *
 * @param id 任务 ID
 */
export async function resumeTask(id: string): Promise<{ ok: boolean; status: string; task?: TaskSummary }> {
  const res = await fetch(`${BASE}/v1/tasks/${encodeURIComponent(id)}/resume`, {
    method: 'POST',
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 放弃可恢复任务（剩余 pending → cancelled）。
 *
 * @param id 任务 ID
 */
export async function abandonTask(id: string): Promise<{ ok: boolean; status: string }> {
  const res = await fetch(`${BASE}/v1/tasks/${encodeURIComponent(id)}/abandon`, {
    method: 'POST',
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 绑定 / 解绑代理。
 */
export async function bindAccountProxy(accountId: string, proxyId: string): Promise<Account> {
  const res = await fetch(`${BASE}/v1/accounts/${encodeURIComponent(accountId)}/proxy`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ proxyId }),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const body = (await res.json()) as { account: Account }
  return body.account
}

/**
 * 拉取代理列表。
 */
export async function fetchProxies(): Promise<ProxyItem[]> {
  const res = await fetchWithTimeout(`${BASE}/v1/proxies`, {}, 8_000)
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const body = (await res.json()) as { items: ProxyItem[] }
  return body.items ?? []
}

/**
 * 创建代理。
 */
export async function createProxy(payload: {
  name?: string
  protocol?: string
  host: string
  port: number
  username?: string
  password?: string
}): Promise<ProxyItem> {
  const res = await fetch(`${BASE}/v1/proxies`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const body = (await res.json()) as { proxy: ProxyItem }
  return body.proxy
}

/**
 * 删除代理。
 */
export async function deleteProxy(id: string): Promise<void> {
  const res = await fetch(`${BASE}/v1/proxies/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
}

export type MessageMode = 'username' | 'phone' | 'userId'

export type SendItemResult = {
  accountId: string
  target: string
  mode: string
  ok: boolean
  businessResult: string
  errorCode?: string
  errorMessage?: string
  peerId?: number
  waitSeconds?: number
}

export type SendMessagesResult = {
  ok: boolean
  async?: boolean
  taskId?: string
  mode: string
  total: number
  success: number
  failed: number
  stopped?: boolean
  stopReason?: string
  items: SendItemResult[]
  note: string
}

export type ChatDialog = {
  peerId: number
  /** 字符串，避免 JS Number 丢精度导致群历史失败 */
  accessHash?: string
  peerType: string
  title: string
  username?: string
  lastMessage?: string
  lastDate?: number
  unreadCount?: number
  topMessageId?: number
  isBot?: boolean
  isSelf?: boolean
}

/** 聊天媒体类型（与 sidecar 约定） */
export type ChatMediaType =
  | 'photo'
  | 'video'
  | 'document'
  | 'audio'
  | 'voice'
  | 'sticker'
  | 'location'
  | 'contact'
  | 'poll'
  | 'webpage'
  | 'unsupported'
  | ''

export type ChatMedia = {
  type?: ChatMediaType
  fileName?: string
  mimeType?: string
  fileSize?: number
  width?: number
  height?: number
  duration?: number
  lat?: number
  long?: number
  title?: string
  address?: string
}

export type ChatMessageButton = {
  text: string
  kind: string // callback | url | webview | simple_webview
  data?: string
  url?: string
  row: number
  col: number
  adminLikely?: boolean
}

export type ChatMessage = {
  id: number
  peerId: number
  out: boolean
  text: string
  date: number
  fromId?: number
  fromName?: string
  media?: ChatMedia
  /** inline 键盘按钮（验证/菜单等） */
  buttons?: ChatMessageButton[]
  botId?: number
  botAccessHash?: string
}

/**
 * 列出会话。
 */
/** 会话列表筛选：私信 / 群与频道 / 全部 */
export type DialogPeerKind = 'user' | 'group' | 'all'

export async function listChatDialogs(
  accountId: string,
  limit = 50,
  peerKind: DialogPeerKind = 'all',
): Promise<{ accountId: string; peerKind?: string; items: ChatDialog[]; note: string }> {
  const q = new URLSearchParams({
    accountId,
    limit: String(limit),
    peerKind,
  })
  const res = await fetch(`${BASE}/v1/chat/dialogs?${q}`)
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 拉取会话历史。
 */
export async function getChatHistory(payload: {
  accountId: string
  peerType: string
  peerId: number
  accessHash?: string
  limit?: number
  offsetId?: number
  minId?: number
}): Promise<{ accountId: string; peerId: number; peerType: string; items: ChatMessage[]; note: string }> {
  const q = new URLSearchParams({
    accountId: payload.accountId,
    peerType: payload.peerType,
    peerId: String(payload.peerId),
    limit: String(payload.limit ?? 50),
  })
  if (payload.accessHash) {
    q.set('accessHash', payload.accessHash)
  }
  if (payload.offsetId) {
    q.set('offsetId', String(payload.offsetId))
  }
  if (payload.minId) {
    q.set('minId', String(payload.minId))
  }
  const res = await fetch(`${BASE}/v1/chat/history?${q}`)
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

export type ChatSendResult = {
  accountId: string
  peerId: number
  ok: boolean
  businessResult: string
  messageId?: number
  errorCode?: string
  errorMessage?: string
}

/**
 * 会话内发消息。
 */
export async function sendChatMessage(payload: {
  accountId: string
  peerType: string
  peerId: number
  accessHash?: string
  text: string
}): Promise<ChatSendResult> {
  const res = await fetch(`${BASE}/v1/chat/send`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      ...payload,
      accessHash: payload.accessHash ?? '',
    }),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 构造会话媒体下载 URL（供 img/video/a 直接使用）。
 */
export function chatMediaURL(payload: {
  accountId: string
  peerType: string
  peerId: number
  accessHash?: string
  messageId: number
}): string {
  const q = new URLSearchParams({
    accountId: payload.accountId,
    peerType: payload.peerType,
    peerId: String(payload.peerId),
    messageId: String(payload.messageId),
  })
  if (payload.accessHash) {
    q.set('accessHash', payload.accessHash)
  }
  return `${BASE}/v1/chat/media?${q}`
}

/**
 * 会话内发送文件媒体（图片/视频/文档）。
 */
export async function sendChatMediaFile(payload: {
  accountId: string
  peerType: string
  peerId: number
  accessHash?: string
  kind: 'photo' | 'video' | 'document'
  caption?: string
  file: File
}): Promise<ChatSendResult> {
  const form = new FormData()
  form.set('accountId', payload.accountId)
  form.set('peerType', payload.peerType)
  form.set('peerId', String(payload.peerId))
  form.set('accessHash', payload.accessHash ?? '')
  form.set('kind', payload.kind)
  if (payload.caption) {
    form.set('caption', payload.caption)
  }
  form.set('file', payload.file, payload.file.name)
  const res = await fetch(`${BASE}/v1/chat/send-media`, {
    method: 'POST',
    body: form,
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 会话内发送位置。
 */
export async function sendChatLocation(payload: {
  accountId: string
  peerType: string
  peerId: number
  accessHash?: string
  lat: number
  long: number
  caption?: string
}): Promise<ChatSendResult> {
  const res = await fetch(`${BASE}/v1/chat/send-media`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      accountId: payload.accountId,
      peerType: payload.peerType,
      peerId: payload.peerId,
      accessHash: payload.accessHash ?? '',
      kind: 'location',
      lat: payload.lat,
      long: payload.long,
      caption: payload.caption ?? '',
    }),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 将会话标记为已读。
 */
export async function markChatRead(payload: {
  accountId: string
  peerType: string
  peerId: number
  accessHash?: string
  maxId?: number
}): Promise<{ ok: boolean; accountId: string; peerId: number; peerType: string; maxId: number; note: string }> {
  const res = await fetch(`${BASE}/v1/chat/read`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      accountId: payload.accountId,
      peerType: payload.peerType,
      peerId: payload.peerId,
      accessHash: payload.accessHash ?? '',
      maxId: payload.maxId ?? 0,
    }),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 批量发送私信（F01/F02/F03）。async 时立即返回 taskId，可取消。
 */
export async function sendMessages(payload: {
  accountIds: string[]
  mode: MessageMode
  targets: string[]
  text: string
  intervalMs?: number
  failStopAfter?: number
  async?: boolean
}): Promise<SendMessagesResult> {
  const res = await fetch(`${BASE}/v1/messages/send`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

export type GroupAction = 'join' | 'leave'

export type GroupItemResult = {
  accountId: string
  target: string
  action: string
  ok: boolean
  businessResult: string
  errorCode?: string
  errorMessage?: string
  peerId?: number
  waitSeconds?: number
}

export type GroupBatchResult = {
  ok: boolean
  taskId?: string
  action: string
  total: number
  success: number
  failed: number
  stopped?: boolean
  stopReason?: string
  items: GroupItemResult[]
  note: string
}

/**
 * 批量加入 / 退出群组（F06/F07）。
 */
export async function groupBatch(payload: {
  accountIds: string[]
  action: GroupAction
  targets: string[]
  intervalMs?: number
  failStopAfter?: number
}): Promise<GroupBatchResult> {
  const res = await fetch(`${BASE}/v1/groups/batch`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

export type JoinedGroup = {
  peerId: number
  accessHash?: number
  title: string
  username?: string
  kind: string
  participants?: number
  isCreator?: boolean
  isAdmin?: boolean
  joinRequestOut?: boolean
  syncedAt?: number
}

export type JoinedGroupsResult = {
  accountId: string
  items: JoinedGroup[]
  total?: number
  syncedAt?: number
  source?: string
  note: string
}

export type JoinPreview = {
  accountId: string
  target: string
  ok: boolean
  status: string
  title?: string
  username?: string
  peerId?: number
  participants?: number
  requestNeeded: boolean
  alreadyIn: boolean
  inviteHash?: string
  isPublic: boolean
  businessResult: string
  errorCode?: string
  errorMessage?: string
  note?: string
}

/**
 * 读取协议号本地已加入群组（不请求 Telegram）。
 */
export async function listJoinedGroups(
  accountId: string,
  _limit = 100,
): Promise<JoinedGroupsResult> {
  const q = new URLSearchParams({ accountId })
  const res = await fetch(`${BASE}/v1/groups/joined?${q}`)
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 从 Telegram 刷新并写入该协议号本地已加入群缓存。
 */
export async function refreshJoinedGroups(
  accountId: string,
  limit = 200,
): Promise<JoinedGroupsResult> {
  const res = await fetch(`${BASE}/v1/groups/joined/refresh`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ accountId, limit }),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/** 群监控状态 */
export type GroupMonitor = {
  accountId: string
  peerId: number
  accessHash?: number
  peerType: string
  title: string
  status: string
  historyLimit: number
  lastMsgId: number
  msgCount: number
  backfillDone: number
  backfillTotal: number
  errorMessage?: string
  startedAt?: number
  updatedAt?: number
}

export type GroupMonitorExportFile = {
  name: string
  path: string
  count: number
  part: number
}

/**
 * 列出群监控状态。
 *
 * @param accountId 协议号（可空=全部）
 */
export async function listGroupMonitors(accountId: string): Promise<{
  accountId: string
  items: GroupMonitor[]
}> {
  const q = new URLSearchParams()
  if (accountId) {
    q.set('accountId', accountId)
  }
  const res = await fetchWithTimeout(`${BASE}/v1/groups/monitor?${q}`, {}, 8_000)
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 开启群监控并回填历史。
 */
export async function startGroupMonitor(payload: {
  accountId: string
  peerId: number
  accessHash: string | number
  peerType?: string
  kind?: string
  title?: string
  historyLimit: number
}): Promise<{ ok: boolean; item: GroupMonitor; taskId: string; note: string }> {
  const res = await fetchWithTimeout(
    `${BASE}/v1/groups/monitor/start`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        ...payload,
        accessHash: String(payload.accessHash ?? 0),
      }),
    },
    30_000,
  )
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 停止群监控。
 */
export async function stopGroupMonitor(payload: {
  accountId: string
  peerId: number
}): Promise<{ ok: boolean; item?: GroupMonitor }> {
  const res = await fetchWithTimeout(
    `${BASE}/v1/groups/monitor/stop`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    },
    15_000,
  )
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 导出已采集群消息（sidecar 按条数拆分 CSV）。
 */
export async function exportGroupMonitor(payload: {
  accountId: string
  peerId: number
}): Promise<{
  ok: boolean
  dir: string
  total: number
  chunkSize: number
  files: GroupMonitorExportFile[]
  note: string
}> {
  const res = await fetchWithTimeout(
    `${BASE}/v1/groups/monitor/export`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    },
    120_000,
  )
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 下载导出目录中的单个 CSV 文本。
 *
 * @param path sidecar 返回的绝对路径
 */
export async function downloadGroupMonitorExportFile(path: string): Promise<string> {
  const q = new URLSearchParams({ path })
  const res = await fetchWithTimeout(
    `${BASE}/v1/groups/monitor/export/file?${q}`,
    {},
    120_000,
  )
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.text()
}

/**
 * 加群预检（是否需审批）。
 */
export async function joinPreview(payload: {
  accountId: string
  targets: string[]
}): Promise<{ accountId: string; items: JoinPreview[]; note: string }> {
  const res = await fetch(`${BASE}/v1/groups/join/preview`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 手动确认后执行加群 / 提交审批申请。
 */
export async function joinConfirm(payload: {
  accountId: string
  targets: string[]
}): Promise<{
  ok: boolean
  taskId?: string
  total: number
  success: number
  pending: number
  failed: number
  items: GroupItemResult[]
  note: string
}> {
  const res = await fetch(`${BASE}/v1/groups/join/confirm`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 检查是否已真正入群（审批后手动验证）。
 */
export async function joinCheck(payload: {
  accountId: string
  targets: string[]
}): Promise<{
  accountId: string
  joined: number
  total: number
  items: JoinPreview[]
  note: string
}> {
  const res = await fetch(`${BASE}/v1/groups/join/check`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

export type ChallengeButton = {
  text: string
  kind: string // callback | url | webview | simple_webview
  data?: string
  url?: string
  row: number
  col: number
  adminLikely?: boolean
}

export type JoinChallenge = {
  accountId: string
  target: string
  peerId: number
  /** 字符串，避免 JS Number 丢精度导致 CHANNEL_INVALID */
  accessHash?: string
  /** channel=群内验证；user=机器人私信验证 */
  peerType?: string
  msgId: number
  text: string
  botId?: number
  botAccessHash?: string
  botName?: string
  date?: number
  buttons: ChallengeButton[]
  kind: string
}

/**
 * 拉取入群验证题（机器人 inline 键盘）。
 */
export async function listJoinChallenges(payload: {
  accountId: string
  targets: string[]
  limit?: number
}): Promise<{
  accountId: string
  total: number
  items: JoinChallenge[]
  errors?: string[]
  note: string
}> {
  const res = await fetch(`${BASE}/v1/groups/join/challenges`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 点击入群验证按钮。
 */
export async function answerJoinChallenge(payload: {
  accountId: string
  target?: string
  peerType?: string
  peerId: number
  accessHash?: string
  msgId: number
  data: string
}): Promise<{
  accountId: string
  peerId: number
  msgId: number
  ok: boolean
  businessResult: string
  alert?: boolean
  message?: string
  errorCode?: string
  errorMessage?: string
}> {
  const res = await fetch(`${BASE}/v1/groups/join/challenge/answer`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      ...payload,
      peerType: payload.peerType || 'channel',
      accessHash: payload.accessHash ?? '',
    }),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 打开入群验证小程序 / 浏览器链接；
 * t.me/bot?start= → 协议号私信；t.me/频道或邀请链 → 协议号加入。
 */
export async function openChallengeWebView(payload: {
  accountId: string
  target?: string
  peerType?: string
  peerId: number
  accessHash?: string
  msgId: number
  botId?: number
  botAccessHash?: string
  kind: string
  url: string
}): Promise<{
  accountId: string
  peerId: number
  msgId: number
  ok: boolean
  businessResult: string
  mode?: string
  url?: string
  queryId?: number
  botUsername?: string
  startParam?: string
  joinedTarget?: string
  replyText?: string
  challenges?: JoinChallenge[]
  errorCode?: string
  errorMessage?: string
  note?: string
}> {
  const res = await fetch(`${BASE}/v1/groups/join/challenge/webview`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      ...payload,
      peerType: payload.peerType || 'channel',
      accessHash: payload.accessHash ?? '',
      botAccessHash: payload.botAccessHash ?? '',
    }),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 协议号打开机器人入群验证 deep link（messages.startBot）。
 */
export async function startBotDeepLink(payload: {
  accountId: string
  url: string
  target?: string
  waitMs?: number
}): Promise<{
  accountId: string
  ok: boolean
  businessResult: string
  botUsername?: string
  botId?: number
  botAccessHash?: string
  startParam?: string
  replyText?: string
  challenges?: JoinChallenge[]
  errorCode?: string
  errorMessage?: string
  note?: string
}> {
  const res = await fetch(`${BASE}/v1/groups/join/challenge/bot-start`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

export type InviteMode = 'contact' | 'username'

export type InviteItemResult = {
  accountId: string
  group: string
  member: string
  mode: string
  ok: boolean
  businessResult: string
  errorCode?: string
  errorMessage?: string
  peerId?: number
  waitSeconds?: number
}

export type InviteBatchResult = {
  ok: boolean
  taskId?: string
  mode: string
  group: string
  total: number
  success: number
  failed: number
  stopped?: boolean
  stopReason?: string
  items: InviteItemResult[]
  note: string
}

/**
 * 批量邀请入群（F08 联系人 / F09 用户名）。
 */
export async function inviteBatch(payload: {
  accountIds: string[]
  group: string
  members: string[]
  mode: InviteMode
  intervalMs?: number
  failStopAfter?: number
}): Promise<InviteBatchResult> {
  const res = await fetch(`${BASE}/v1/groups/invite`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

export type ContactAction = 'add' | 'delete'

export type ContactListItem = {
  userId: number
  username?: string
  firstName?: string
  lastName?: string
  phone?: string
  mutual?: boolean
  isBot?: boolean
  isSelf?: boolean
  status?: string
  syncedAt?: number
}

export type ContactsListResult = {
  accountId: string
  items: ContactListItem[]
  total: number
  syncedAt?: number
  source?: string
  note: string
}

/**
 * 读取协议号本地 SQLite 通讯录（不请求 Telegram）。
 */
export async function listContacts(accountId: string): Promise<ContactsListResult> {
  const q = new URLSearchParams({ accountId })
  const res = await fetch(`${BASE}/v1/contacts?${q}`)
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 从 Telegram 刷新并写入该协议号本地缓存。
 */
export async function refreshContacts(accountId: string): Promise<ContactsListResult> {
  const res = await fetch(`${BASE}/v1/contacts/refresh`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ accountId }),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

export type ContactItemResult = {
  accountId: string
  target: string
  action: string
  ok: boolean
  businessResult: string
  errorCode?: string
  errorMessage?: string
  peerId?: number
  username?: string
  phone?: string
  firstName?: string
  lastName?: string
  waitSeconds?: number
}

export type ContactBatchResult = {
  ok: boolean
  taskId?: string
  action: string
  total: number
  success: number
  failed: number
  stopped?: boolean
  stopReason?: string
  items: ContactItemResult[]
  note: string
}

/**
 * 批量添加 / 删除联系人（F19/F20）。
 */
export async function contactBatch(payload: {
  accountIds: string[]
  action: ContactAction
  targets: string[]
  intervalMs?: number
  failStopAfter?: number
}): Promise<ContactBatchResult> {
  const res = await fetch(`${BASE}/v1/contacts/batch`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

export type ScrapeSource = 'members' | 'speakers'

export type ScrapeRow = {
  source: string
  userId: number
  username: string
  firstName: string
  lastName: string
  phone: string
  isBot: boolean
  accountId: string
  group: string
  digest?: string
  messageCount?: number
}

export type MarketingProduct = {
  id: string
  name: string
  sellingPoints: string
  audience: string
  avoidAudience: string
  offer: string
  landingUrl: string
  tone: string
  bannedWords: string
  cta: string
  status: string
  createdAt: number
  updatedAt: number
}

export type MarketingLead = {
  id: string
  productId: string
  accountId: string
  groupTarget: string
  groupTitle: string
  userId: number
  username: string
  displayName: string
  digest: string
  fitScore: number
  fitLabel: string
  fitReasons: string
  riskFlags: string
  scriptA: string
  scriptB: string
  scriptChosen: string
  status: string
  sendAccountId: string
  cloudCallIds: string
  errorMessage: string
  createdAt: number
  updatedAt: number
}

export type MarketingCollectResult = {
  ok: boolean
  taskId?: string
  imported: number
  speakersCount: number
  rows: ScrapeRow[]
  errorCode?: string
  errorMessage?: string
  note: string
}

export type ScrapeResult = {
  ok: boolean
  taskId?: string
  accountId: string
  group: string
  sources: string[]
  rows: ScrapeRow[]
  membersCount: number
  speakersCount: number
  businessResult?: string
  errorCode?: string
  errorMessage?: string
  note: string
}

/**
 * 采集群成员 / 发言者（F23）。
 */
export async function scrapeGroup(payload: {
  accountId: string
  group: string
  sources: ScrapeSource[]
  memberLimit?: number
  historyLimit?: number
}): Promise<ScrapeResult> {
  const res = await fetch(`${BASE}/v1/scrape/group`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

export type PhoneCheckItem = {
  accountId: string
  phone: string
  ok: boolean
  businessResult: string
  errorCode?: string
  errorMessage?: string
  peerId?: number
  username?: string
  firstName?: string
  lastName?: string
}

export type PhoneCheckResult = {
  ok: boolean
  taskId?: string
  total: number
  success: number
  failed: number
  stopped?: boolean
  stopReason?: string
  items: PhoneCheckItem[]
  note: string
}

/**
 * 批量手机号检查（F25）。
 */
export async function checkPhones(payload: {
  accountIds: string[]
  phones: string[]
  intervalMs?: number
  failStopAfter?: number
}): Promise<PhoneCheckResult> {
  const res = await fetch(`${BASE}/v1/scrape/phones`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 列出营销产品。
 */
export async function listMarketingProducts(): Promise<MarketingProduct[]> {
  const res = await fetch(`${BASE}/v1/marketing/products`)
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const body = (await res.json()) as { items: MarketingProduct[] }
  return body.items ?? []
}

/**
 * 创建或更新营销产品。
 *
 * @param product 产品字段（空 id 则新建）
 */
export async function upsertMarketingProduct(
  product: Partial<MarketingProduct> & { name: string },
): Promise<MarketingProduct> {
  const res = await fetch(`${BASE}/v1/marketing/products`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(product),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 删除营销产品。
 *
 * @param id 产品 ID
 */
export async function deleteMarketingProduct(id: string): Promise<void> {
  const res = await fetch(`${BASE}/v1/marketing/products/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
}

/**
 * 列出产品下线索。
 *
 * @param productId 产品 ID
 * @param status 可选状态
 */
export async function listMarketingLeads(
  productId: string,
  status?: string,
): Promise<MarketingLead[]> {
  const q = status ? `?status=${encodeURIComponent(status)}` : ''
  const res = await fetch(
    `${BASE}/v1/marketing/products/${encodeURIComponent(productId)}/leads${q}`,
  )
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const body = (await res.json()) as { items: MarketingLead[] }
  return body.items ?? []
}

/**
 * 更新线索字段。
 *
 * @param id 线索 ID
 * @param patch 局部字段
 */
export async function patchMarketingLead(
  id: string,
  patch: Partial<MarketingLead>,
): Promise<MarketingLead> {
  // 使用 POST：Tauri WebView 跨域时 CORS 未放行 PATCH 会直接 Load failed
  const res = await fetch(`${BASE}/v1/marketing/leads/${encodeURIComponent(id)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 采集群发言并写入营销线索。
 *
 * @param payload 产品 / 账号 / 群
 */
export async function marketingCollect(payload: {
  productId: string
  accountId: string
  group: string
  groupTitle?: string
  historyLimit?: number
}): Promise<MarketingCollectResult> {
  const res = await fetch(`${BASE}/v1/marketing/campaign/collect`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/** 营销发送策略 */
export type MarketingSendPolicy = {
  id: string
  name: string
  windowMinutes: number
  maxPerWindow: number
  mutualMinIntervalMs: number
  nonMutualMinIntervalMs: number
  nonMutualDailyCap: number
  sameTargetCooldownMs: number
  status: string
  createdAt: number
  updatedAt: number
}

/** 营销发送任务 */
export type MarketingSendJob = {
  id: string
  productId: string
  policyId: string
  accountIds: string[]
  status: string
  failStopAfter: number
  consecutiveFails: number
  createdAt: number
  updatedAt: number
  queuedCount?: number
  waitingCount?: number
  sendingCount?: number
  sentCount?: number
  failedCount?: number
  totalCount?: number
}

/** 发送队列项 */
export type MarketingSendQueueItem = {
  id: string
  jobId: string
  leadId: string
  accountId: string
  targetMode: string
  target: string
  text: string
  status: string
  notBefore: number
  attempts: number
  lastError: string
  sentAt: number
  createdAt: number
  updatedAt: number
}

/** 发送队列统一视图（营销 + 群发） */
export type SendQueueItemView = MarketingSendQueueItem & {
  source: 'bulk' | 'marketing' | string
  jobStatus: string
  productId: string
  accountIds: string[]
}

/**
 * 列出发送策略。
 */
export async function listMarketingSendPolicies(): Promise<MarketingSendPolicy[]> {
  const res = await fetch(`${BASE}/v1/marketing/send-policies`)
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const body = (await res.json()) as { items: MarketingSendPolicy[] }
  return body.items ?? []
}

/**
 * 创建或更新发送策略。
 *
 * @param policy 策略字段
 */
export async function upsertMarketingSendPolicy(
  policy: Partial<MarketingSendPolicy> & { name: string },
): Promise<MarketingSendPolicy> {
  const res = await fetch(`${BASE}/v1/marketing/send-policies`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(policy),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 删除发送策略。
 *
 * @param id 策略 ID
 */
export async function deleteMarketingSendPolicy(id: string): Promise<void> {
  const res = await fetch(`${BASE}/v1/marketing/send-policies/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
}

/**
 * 列出发送任务。
 *
 * @param productId 可选产品过滤
 */
export async function listMarketingSendJobs(productId?: string): Promise<MarketingSendJob[]> {
  const q = productId ? `?productId=${encodeURIComponent(productId)}` : ''
  const res = await fetch(`${BASE}/v1/marketing/send-jobs${q}`)
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const body = (await res.json()) as { items: MarketingSendJob[] }
  return body.items ?? []
}

/** 群发任务在营销发送表中的 productId 标记 */
export const BULK_SEND_PRODUCT_ID = '__bulk__'

/**
 * 消息群发入队：多协议号 + 发送策略；variants 中每目标随机选一条文案。
 *
 * @param payload 模式 / 目标 / 文案池 / 策略 / 号池
 */
export async function createBulkSendJob(payload: {
  mode: MessageMode
  targets: string[]
  variants: string[]
  policyId: string
  accountIds: string[]
  failStopAfter?: number
}): Promise<{
  ok: boolean
  job: MarketingSendJob
  enqueued: number
  variantCount?: number
  note?: string
}> {
  const res = await fetch(`${BASE}/v1/messages/bulk-send`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 已审核线索入发送队列。
 *
 * @param payload 产品 / 策略 / 线索 / 号池
 */
export async function createMarketingSendJob(payload: {
  productId: string
  policyId: string
  leadIds: string[]
  accountIds: string[]
  failStopAfter?: number
}): Promise<{ ok: boolean; job: MarketingSendJob; enqueued: number }> {
  const res = await fetch(`${BASE}/v1/marketing/send-jobs`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 发送任务队列明细。
 *
 * @param jobId 任务 ID
 */
export async function getMarketingSendJobItems(
  jobId: string,
): Promise<{ job: MarketingSendJob; items: MarketingSendQueueItem[] }> {
  const res = await fetch(
    `${BASE}/v1/marketing/send-jobs/${encodeURIComponent(jobId)}/items`,
  )
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 暂停 / 继续 / 取消发送任务。
 *
 * @param jobId 任务 ID
 * @param action pause|resume|cancel
 */
export async function controlMarketingSendJob(
  jobId: string,
  action: 'pause' | 'resume' | 'cancel',
): Promise<MarketingSendJob> {
  const res = await fetch(
    `${BASE}/v1/marketing/send-jobs/${encodeURIComponent(jobId)}/${action}`,
    { method: 'POST' },
  )
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const body = (await res.json()) as { job: MarketingSendJob }
  return body.job
}

/**
 * 统一列出发送队列（营销 + 群发明细）。
 *
 * @param opts.source bulk|marketing
 * @param opts.status 队列项状态
 * @param opts.limit 上限
 */
export async function listSendQueue(opts?: {
  source?: string
  status?: string
  limit?: number
}): Promise<SendQueueItemView[]> {
  const q = new URLSearchParams()
  if (opts?.source) {
    q.set('source', opts.source)
  }
  if (opts?.status) {
    q.set('status', opts.status)
  }
  if (opts?.limit) {
    q.set('limit', String(opts.limit))
  }
  const qs = q.toString()
  const res = await fetch(`${BASE}/v1/send-queue${qs ? `?${qs}` : ''}`)
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const body = (await res.json()) as { items: SendQueueItemView[] }
  return body.items ?? []
}

/** 协议号可发时间提示 */
export type SendQueueReadyHint = {
  accountId: string
  notBefore: number
  reason: string
  readyNow: boolean
  windowUsage: number
}

/** 下次可发时间计算结果 */
export type SendQueueSuggestResult = {
  itemId: string
  accountId: string
  notBefore: number
  reason: string
  readyNow: boolean
  candidates: SendQueueReadyHint[]
  policyId: string
  policyName: string
}

/**
 * 按策略计算下次可发时间。
 *
 * @param id 队列项 ID
 * @param accountId 可选指定协议号
 */
export async function suggestSendQueueReady(
  id: string,
  accountId?: string,
): Promise<SendQueueSuggestResult> {
  const q = accountId ? `?accountId=${encodeURIComponent(accountId)}` : ''
  const res = await fetch(
    `${BASE}/v1/send-queue/${encodeURIComponent(id)}/suggest${q}`,
  )
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 修改待发送项（文案 / 协议号 / 计划时间）。
 *
 * @param id 队列项 ID
 * @param patch 变更字段
 */
export async function patchSendQueueItem(
  id: string,
  patch: {
    text?: string
    accountId?: string
    notBefore?: number
    autoSchedule?: boolean
  },
): Promise<{ item: MarketingSendQueueItem; suggestReason?: string }> {
  const res = await fetch(`${BASE}/v1/send-queue/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return res.json()
}

/**
 * 删除待发送项。
 *
 * @param id 队列项 ID
 */
export async function deleteSendQueueItem(id: string): Promise<void> {
  const res = await fetch(`${BASE}/v1/send-queue/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
}
