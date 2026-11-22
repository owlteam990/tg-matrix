<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  answerJoinChallenge,
  BULK_SEND_PRODUCT_ID,
  chatMediaURL,
  createBulkSendJob,
  getChatHistory,
  listChatDialogs,
  listMarketingSendJobs,
  listMarketingSendPolicies,
  markChatRead,
  openChallengeWebView,
  sendChatLocation,
  sendChatMediaFile,
  sendChatMessage,
  upsertMarketingSendPolicy,
  type ChatDialog,
  type ChatMedia,
  type ChatMessage,
  type ChatMessageButton,
  type DialogPeerKind,
  type MarketingSendJob,
  type MarketingSendPolicy,
  type MessageMode,
} from '../api/sidecar'
import { failStopAfterForApi, loadSettings } from '../lib/settings'
import { openExternalUrl } from '../lib/openUrl'

const props = defineProps<{
  selectedIds: string[]
  gotdConfigured: boolean
  sidecarOk: boolean
}>()

const emit = defineEmits<{
  log: [line: string]
}>()

/** 主 Tab：聊天 / 群发 */
const panelTab = ref<'chat' | 'bulk'>('chat')

// —— 聊天 ——
const dialogs = ref<ChatDialog[]>([])
const dialogsLoading = ref(false)
/** 会话列表 Tab：私信 / 群 */
const dialogTab = ref<'user' | 'group'>('user')
const activeDialog = ref<ChatDialog | null>(null)
const messages = ref<ChatMessage[]>([])
const historyLoading = ref(false)
const loadingOlder = ref(false)
/** 已无更早历史 */
const historyExhausted = ref(false)
const draft = ref('')
const sendingChat = ref(false)
const pollMs = 3500
let pollTimer: ReturnType<typeof setInterval> | null = null
const messagesEl = ref<HTMLElement | null>(null)
const olderPageSize = 40
/** 附件菜单 / 表情面板 */
const attachOpen = ref(false)
const emojiOpen = ref(false)
const locationOpen = ref(false)
const locLat = ref('')
const locLong = ref('')
const mediaFileInput = ref<HTMLInputElement | null>(null)
const docFileInput = ref<HTMLInputElement | null>(null)
/** 正在点击的 inline 按钮 key */
const answeringKey = ref('')
/** 粘贴任意 TG 链接用协议号打开 */
const openLinkText = ref('')
const openingLink = ref(false)


/** 常用表情（Unicode，与 TG 文本表情一致） */
const EMOJI_LIST = [
  '😀', '😁', '😂', '🤣', '😊', '😍', '😘', '😎', '🤔', '😢',
  '😭', '😤', '👍', '👎', '👏', '🙏', '🔥', '✨', '🎉', '❤️',
  '💔', '💯', '✅', '❌', '⭐', '🌈', '🍕', '☕', '🚀', '💡',
]

const accountId = computed(() => props.selectedIds[0] || '')

// —— 群发（多号策略 + 多文案随机，复用营销调度） ——
const mode = ref<MessageMode>('username')
const targetsText = ref('')
/** 文案池（不限条数；入队时每目标随机选一条） */
const variants = ref<string[]>([''])
const failStopAfter = ref(5)
const sending = ref(false)
const sendPolicies = ref<MarketingSendPolicy[]>([])
const selectedPolicyId = ref('')
const bulkJobs = ref<MarketingSendJob[]>([])
let bulkJobsPoll: ReturnType<typeof setInterval> | null = null

const variantCount = computed(() => variants.value.filter((v) => v.trim()).length)

onMounted(() => {
  const s = loadSettings()
  failStopAfter.value = s.failStopAfter
  if (panelTab.value === 'bulk') {
    void loadBulkPolicies()
    void loadBulkJobs(true)
  }
})

onBeforeUnmount(() => {
  stopPoll()
  stopBulkJobsPolling()
})

const placeholders: Record<MessageMode, string> = {
  username: '每行一个用户名，可带或不带 @',
  phone: '每行一个手机号（国际格式，如 +86138...）\n未匹配 ≠ 未注册',
  userId: '每行一个用户 ID（数字）\n需本协议号已有实体缓存，禁止跨号复用',
}

const targetCount = computed(() => parseTargets(targetsText.value).length)

const selectedPolicyLabel = computed(() => {
  const p = sendPolicies.value.find((x) => x.id === selectedPolicyId.value)
  if (!p) {
    return '未选策略'
  }
  return `${p.name}（${p.windowMinutes}分 / ${p.maxPerWindow}条）`
})

watch(panelTab, (tab) => {
  if (tab === 'bulk') {
    void loadBulkPolicies()
    void loadBulkJobs(true)
  } else {
    stopBulkJobsPolling()
  }
})

/**
 * 解析名单：按行拆分并去重。
 *
 * @param raw 原始文本
 * @return 目标列表
 */
function parseTargets(raw: string): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const line of raw.split(/\r?\n/)) {
    const v = line.trim()
    if (!v || seen.has(v)) {
      continue
    }
    seen.add(v)
    out.push(v)
  }
  return out
}

watch(mode, () => {
  targetsText.value = ''
  emit('log', `已切换定位方式：${mode.value}（名单已清空，正文保留）`)
})

watch(
  () =>
    [
      accountId.value,
      activeDialog.value?.peerId,
      activeDialog.value?.peerType,
    ] as const,
  () => {
    },
)

watch(
  () => [props.selectedIds.join(','), panelTab.value, props.sidecarOk, props.gotdConfigured] as const,
  () => {
    if (panelTab.value !== 'chat') {
      stopPoll()
      return
    }
    if (!accountId.value || !props.sidecarOk || !props.gotdConfigured) {
      dialogs.value = []
      activeDialog.value = null
      messages.value = []
      stopPoll()
      return
    }
    void loadDialogs()
  },
  { immediate: true },
)

watch(dialogTab, () => {
  if (panelTab.value === 'chat' && accountId.value) {
    activeDialog.value = null
    messages.value = []
    stopPoll()
    void loadDialogs()
  }
})

/**
 * 当前会话列表筛选 kind。
 *
 * @return peerKind
 */
function currentPeerKind(): DialogPeerKind {
  return dialogTab.value === 'group' ? 'group' : 'user'
}

/**
 * 加载会话列表。
 */
async function loadDialogs(): Promise<void> {
  if (!accountId.value || !props.sidecarOk || !props.gotdConfigured) {
    return
  }
  dialogsLoading.value = true
  try {
    const res = await listChatDialogs(accountId.value, 80, currentPeerKind())
    const active = activeDialog.value
    dialogs.value = (res.items || []).map((item) => {
      // 正在查看的会话保持已读，避免刷新把角标刷回来
      if (
        active &&
        item.peerId === active.peerId &&
        item.peerType === active.peerType
      ) {
        return { ...item, unreadCount: 0 }
      }
      return item
    })
    // 若当前会话仍在列表中则保留，否则清空
    if (active) {
      const still = dialogs.value.find(
        (d) => d.peerId === active.peerId && d.peerType === active.peerType,
      )
      if (still) {
        activeDialog.value = { ...still, unreadCount: 0 }
      } else {
        activeDialog.value = null
        messages.value = []
        stopPoll()
      }
    }
  } catch (err) {
    emit('log', `加载会话失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    dialogsLoading.value = false
  }
}

/**
 * 本地清掉指定会话的未读角标。
 *
 * @param peerType 类型
 * @param peerId id
 */
function clearUnreadBadge(peerType: string, peerId: number): void {
  dialogs.value = dialogs.value.map((item) => {
    if (item.peerId === peerId && item.peerType === peerType) {
      return { ...item, unreadCount: 0 }
    }
    return item
  })
  if (
    activeDialog.value &&
    activeDialog.value.peerId === peerId &&
    activeDialog.value.peerType === peerType
  ) {
    activeDialog.value = { ...activeDialog.value, unreadCount: 0 }
  }
}

/**
 * 打开会话：点击即标已读，再拉历史。
 *
 * @param d 会话
 */
async function openDialog(d: ChatDialog): Promise<void> {
  activeDialog.value = { ...d, unreadCount: 0 }
  // 点击立刻清角标，不等接口
  clearUnreadBadge(d.peerType, d.peerId)
  messages.value = []
  historyExhausted.value = false
  stopPoll()
  // 用会话 topMessageId 先打已读（与拉历史并行）
  const readPromise = markDialogRead(d, d.topMessageId || 0)
  await loadHistory(false)
  // 历史拉完后再用最新消息 id 确认已读
  const latestId = messages.value.length
    ? Math.max(...messages.value.map((m) => m.id), d.topMessageId || 0)
    : d.topMessageId || 0
  await readPromise
  if (latestId > 0) {
    await markDialogRead(d, latestId)
  }
  startPoll()
}

/**
 * 将指定会话标为已读。
 *
 * @param d 会话
 * @param maxId 读到的最大消息 id
 */
async function markDialogRead(d: ChatDialog, maxId: number): Promise<void> {
  if (!accountId.value || !props.sidecarOk) {
    return
  }
  try {
    await markChatRead({
      accountId: accountId.value,
      peerType: d.peerType,
      peerId: d.peerId,
      accessHash: d.accessHash,
      maxId,
    })
    clearUnreadBadge(d.peerType, d.peerId)
  } catch (err) {
    emit('log', `标已读失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

/**
 * 将当前会话标为已读（工具栏按钮）。
 */
async function markActiveRead(): Promise<void> {
  const d = activeDialog.value
  if (!d) {
    return
  }
  const maxId =
    (messages.value.length ? Math.max(...messages.value.map((m) => m.id)) : 0) ||
    d.topMessageId ||
    0
  clearUnreadBadge(d.peerType, d.peerId)
  await markDialogRead(d, maxId)
}

/**
 * 拉取历史；poll=true 时仅追加新消息。
 *
 * @param poll 是否轮询增量
 */
async function loadHistory(poll: boolean): Promise<void> {
  const d = activeDialog.value
  if (!d || !accountId.value) {
    return
  }
  if (!poll) {
    historyLoading.value = true
  }
  try {
    const maxId = poll && messages.value.length
      ? Math.max(...messages.value.map((m) => m.id))
      : 0
    const res = await getChatHistory({
      accountId: accountId.value,
      peerType: d.peerType,
      peerId: d.peerId,
      accessHash: d.accessHash,
      limit: poll ? 30 : 60,
      minId: poll ? maxId : 0,
    })
    const items = res.items || []
    if (!poll) {
      messages.value = items
      // 首屏不足一页，视为没有更早消息
      historyExhausted.value = items.length < 60
      await nextTick()
      scrollToBottom()
      return
    }
    if (items.length === 0) {
      return
    }
    const known = new Set(messages.value.map((m) => m.id))
    const added = items.filter((m) => !known.has(m.id))
    if (added.length === 0) {
      return
    }
    messages.value = [...messages.value, ...added]
    await nextTick()
    scrollToBottom()
    // 刷新列表摘要
    void loadDialogs()
    // P1：对方新消息 → 防抖自动出建议（合并同批未读）
    const inbound = added.some((m) => !m.out)
    if (inbound) {
    }
  } catch (err) {
    if (!poll) {
      emit('log', `加载历史失败：${err instanceof Error ? err.message : String(err)}`)
    }
  } finally {
    historyLoading.value = false
  }
}

/**
 * 上滑加载更早的历史（offsetId = 当前最旧消息 id）。
 */
async function loadOlderHistory(): Promise<void> {
  const d = activeDialog.value
  const el = messagesEl.value
  if (
    !d ||
    !accountId.value ||
    !el ||
    loadingOlder.value ||
    historyLoading.value ||
    historyExhausted.value ||
    messages.value.length === 0
  ) {
    return
  }
  const oldestId = Math.min(...messages.value.map((m) => m.id))
  if (!oldestId) {
    return
  }
  loadingOlder.value = true
  const prevHeight = el.scrollHeight
  const prevTop = el.scrollTop
  try {
    const res = await getChatHistory({
      accountId: accountId.value,
      peerType: d.peerType,
      peerId: d.peerId,
      accessHash: d.accessHash,
      limit: olderPageSize,
      offsetId: oldestId,
    })
    const items = res.items || []
    const known = new Set(messages.value.map((m) => m.id))
    // 只保留比当前最旧更早的消息
    const older = items.filter((m) => m.id < oldestId && !known.has(m.id))
    if (older.length === 0) {
      historyExhausted.value = true
      return
    }
    if (older.length < olderPageSize) {
      historyExhausted.value = true
    }
    messages.value = [...older, ...messages.value]
    await nextTick()
    // 保持视觉位置：补上增高量
    el.scrollTop = prevTop + (el.scrollHeight - prevHeight)
  } catch (err) {
    emit('log', `加载更早消息失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    loadingOlder.value = false
  }
}

/**
 * 消息列表滚动：接近顶部时加载更早历史。
 */
function onMsgScroll(): void {
  const el = messagesEl.value
  if (!el) {
    return
  }
  if (el.scrollTop <= 48) {
    void loadOlderHistory()
  }
}

/**
 * 滚动到底部。
 */
function scrollToBottom(): void {
  const el = messagesEl.value
  if (el) {
    el.scrollTop = el.scrollHeight
  }
}

/**
 * 启动轮询收消息。
 */
function startPoll(): void {
  stopPoll()
  pollTimer = setInterval(() => {
    void loadHistory(true)
  }, pollMs)
}

/**
 * 停止轮询。
 */
function stopPoll(): void {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

/**
 * 发送当前会话文本消息。
 */
async function onSendChat(): Promise<void> {
  const d = activeDialog.value
  const text = draft.value.trim()
  if (!d || !text || !accountId.value) {
    return
  }
  if (!props.sidecarOk || !props.gotdConfigured) {
    emit('log', '本地服务未就绪，无法发送')
    return
  }
  sendingChat.value = true
  attachOpen.value = false
  emojiOpen.value = false
  try {
    const res = await sendChatMessage({
      accountId: accountId.value,
      peerType: d.peerType,
      peerId: d.peerId,
      accessHash: d.accessHash,
      text,
    })
    if (!res.ok) {
      emit('log', `发送失败：${res.errorCode || ''} ${res.errorMessage || res.businessResult}`)
      return
    }
    draft.value = ''
    // 本地乐观追加，再拉一次确认
    if (res.messageId) {
      messages.value = [
        ...messages.value,
        {
          id: res.messageId,
          peerId: d.peerId,
          out: true,
          text,
          date: Math.floor(Date.now() / 1000),
        },
      ]
      await nextTick()
      scrollToBottom()
    }
    await loadHistory(true)
    void loadDialogs()
  } catch (err) {
    emit('log', `发送异常：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    sendingChat.value = false
  }
}

/**
 * 输入框回车发送（Shift+Enter 换行）。
 *
 * @param e 键盘事件
 */
function onDraftKeydown(e: KeyboardEvent): void {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    void onSendChat()
  }
}

/**
 * 切换附件菜单。
 */
function toggleAttach(): void {
  attachOpen.value = !attachOpen.value
  if (attachOpen.value) {
    emojiOpen.value = false
  }
}

/**
 * 切换表情面板。
 */
function toggleEmoji(): void {
  emojiOpen.value = !emojiOpen.value
  if (emojiOpen.value) {
    attachOpen.value = false
  }
}

/**
 * 将表情插入草稿末尾。
 *
 * @param emoji 表情字符
 */
function insertEmoji(emoji: string): void {
  draft.value = `${draft.value}${emoji}`
}

/**
 * 选择「图片或视频」附件。
 */
function pickPhotoVideo(): void {
  attachOpen.value = false
  mediaFileInput.value?.click()
}

/**
 * 选择「文档」附件。
 */
function pickDocument(): void {
  attachOpen.value = false
  docFileInput.value?.click()
}

/**
 * 打开位置发送面板。
 */
function openLocationPanel(): void {
  attachOpen.value = false
  locationOpen.value = true
  locLat.value = ''
  locLong.value = ''
}

/**
 * 使用浏览器定位填充经纬度。
 */
function useBrowserLocation(): void {
  if (!navigator.geolocation) {
    emit('log', '当前环境不支持定位')
    return
  }
  navigator.geolocation.getCurrentPosition(
    (pos) => {
      locLat.value = String(pos.coords.latitude)
      locLong.value = String(pos.coords.longitude)
    },
    (err) => {
      emit('log', `定位失败：${err.message}`)
    },
    { enableHighAccuracy: true, timeout: 15000 },
  )
}

/**
 * 发送位置消息。
 */
async function onSendLocation(): Promise<void> {
  const d = activeDialog.value
  const lat = Number(locLat.value)
  const long = Number(locLong.value)
  if (!d || !accountId.value) {
    return
  }
  if (!Number.isFinite(lat) || !Number.isFinite(long)) {
    emit('log', '请填写有效经纬度')
    return
  }
  if (!props.sidecarOk || !props.gotdConfigured) {
    emit('log', '本地服务未就绪，无法发送')
    return
  }
  sendingChat.value = true
  try {
    const res = await sendChatLocation({
      accountId: accountId.value,
      peerType: d.peerType,
      peerId: d.peerId,
      accessHash: d.accessHash,
      lat,
      long,
      caption: draft.value.trim() || undefined,
    })
    if (!res.ok) {
      emit('log', `位置发送失败：${res.errorCode || ''} ${res.errorMessage || res.businessResult}`)
      return
    }
    draft.value = ''
    locationOpen.value = false
    emit('log', '位置已发送')
    await loadHistory(true)
    void loadDialogs()
  } catch (err) {
    emit('log', `位置发送异常：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    sendingChat.value = false
  }
}

/**
 * 处理图片/视频文件选择。
 *
 * @param e change 事件
 */
async function onMediaFileChange(e: Event): Promise<void> {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) {
    return
  }
  const mime = (file.type || '').toLowerCase()
  const kind = mime.startsWith('video/') ? 'video' : 'photo'
  await sendSelectedFile(file, kind)
}

/**
 * 处理文档文件选择。
 *
 * @param e change 事件
 */
async function onDocFileChange(e: Event): Promise<void> {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) {
    return
  }
  await sendSelectedFile(file, 'document')
}

/**
 * 上传并发送选中文件。
 *
 * @param file 文件
 * @param kind 媒体类型
 */
async function sendSelectedFile(
  file: File,
  kind: 'photo' | 'video' | 'document',
): Promise<void> {
  const d = activeDialog.value
  if (!d || !accountId.value) {
    return
  }
  if (!props.sidecarOk || !props.gotdConfigured) {
    emit('log', '本地服务未就绪，无法发送')
    return
  }
  sendingChat.value = true
  try {
    const caption = draft.value.trim()
    const res = await sendChatMediaFile({
      accountId: accountId.value,
      peerType: d.peerType,
      peerId: d.peerId,
      accessHash: d.accessHash,
      kind,
      caption: caption || undefined,
      file,
    })
    if (!res.ok) {
      emit('log', `媒体发送失败：${res.errorCode || ''} ${res.errorMessage || res.businessResult}`)
      return
    }
    draft.value = ''
    emit('log', `已发送${kind === 'photo' ? '图片' : kind === 'video' ? '视频' : '文件'}：${file.name}`)
    await loadHistory(true)
    void loadDialogs()
  } catch (err) {
    emit('log', `媒体发送异常：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    sendingChat.value = false
  }
}

/**
 * 消息是否带可渲染媒体。
 *
 * @param m 消息
 * @return 是否有媒体
 */
function hasRenderableMedia(m: ChatMessage): boolean {
  const t = m.media?.type
  return !!t && t !== 'unsupported' && t !== 'webpage' && t !== 'poll' && t !== 'contact'
}

/**
 * 媒体下载地址。
 *
 * @param m 消息
 * @return URL
 */
function mediaSrc(m: ChatMessage): string {
  const d = activeDialog.value
  if (!d || !accountId.value) {
    return ''
  }
  return chatMediaURL({
    accountId: accountId.value,
    peerType: d.peerType,
    peerId: d.peerId,
    accessHash: d.accessHash,
    messageId: m.id,
  })
}

/**
 * 打开地图查看位置。
 *
 * @param media 媒体摘要
 */
function openLocationMap(media?: ChatMedia): void {
  if (!media || media.lat == null || media.long == null) {
    return
  }
  const url = `https://www.openstreetmap.org/?mlat=${media.lat}&mlon=${media.long}#map=16/${media.lat}/${media.long}`
  void openExternalUrl(url)
}

/**
 * 格式化文件大小。
 *
 * @param n 字节
 * @return 展示
 */
function formatSize(n?: number): string {
  if (!n || n <= 0) {
    return ''
  }
  if (n < 1024) {
    return `${n} B`
  }
  if (n < 1024 * 1024) {
    return `${(n / 1024).toFixed(1)} KB`
  }
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

/**
 * 气泡正文：有媒体时不重复展示占位标签。
 *
 * @param m 消息
 * @return 正文
 */
function bubbleText(m: ChatMessage): string {
  const raw = (m.text || '').trim()
  const t = m.media?.type
  if (!t) {
    return raw || '[非文本消息]'
  }
  // 后端在无 caption 时填了 [图片] 等标签，有真实媒体时隐藏
  if (/^\[(图片|视频|文件|音频|语音|贴纸|位置|联系人|投票|链接|媒体)/.test(raw)) {
    return ''
  }
  return raw
}

/**
 * inline 按钮唯一 key。
 *
 * @param m 消息
 * @param btn 按钮
 * @return key
 */
function msgBtnKey(m: ChatMessage, btn: ChatMessageButton): string {
  return `${m.id}-${btn.row}-${btn.col}-${btn.kind}-${btn.text}`
}

/**
 * 按行分组 inline 按钮。
 *
 * @param m 消息
 * @return 按 row 分组
 */
function messageButtonRows(m: ChatMessage): ChatMessageButton[][] {
  const map = new Map<number, ChatMessageButton[]>()
  for (const b of m.buttons ?? []) {
    const row = map.get(b.row) ?? []
    row.push(b)
    map.set(b.row, row)
  }
  return [...map.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([, row]) => row.sort((a, b) => a.col - b.col))
}

/**
 * 点击聊天消息中的 inline 按钮（callback / 私信机器人 / 打开小程序）。
 *
 * @param m 消息
 * @param btn 按钮
 */
async function onClickMessageButton(m: ChatMessage, btn: ChatMessageButton): Promise<void> {
  const d = activeDialog.value
  if (!d || !accountId.value) {
    return
  }
  if (btn.adminLikely) {
    emit('log', `跳过疑似管理按钮「${btn.text}」`)
    return
  }
  const key = msgBtnKey(m, btn)
  answeringKey.value = key
  try {
    const kind = (btn.kind || 'callback').toLowerCase()
    const peerType = d.peerType || 'user'
    if (kind === 'url' || kind === 'webview' || kind === 'simple_webview') {
      if (!btn.url) {
        emit('log', `按钮「${btn.text}」缺少 URL`)
        return
      }
      const result = await openChallengeWebView({
        accountId: accountId.value,
        target: d.username ? `@${d.username}` : String(d.peerId),
        peerType,
        peerId: d.peerId,
        accessHash: d.accessHash,
        msgId: m.id,
        botId: m.botId || (peerType === 'user' ? d.peerId : undefined),
        botAccessHash: m.botAccessHash || (peerType === 'user' ? d.accessHash : undefined),
        kind,
        url: btn.url,
      })
      if (!result.ok) {
        emit(
          'log',
          `✗ 打开「${btn.text}」失败 [${result.businessResult}] ${result.errorMessage || ''}`,
        )
        return
      }
      if (result.mode === 'bot_dm') {
        emit(
          'log',
          `✓ 「${btn.text}」→ 协议号已私信 @${result.botUsername || '?'}?start=${result.startParam || ''} · ${result.note || ''}`,
        )
        if (result.replyText) {
          emit('log', `机器人回复：${result.replyText.slice(0, 160)}`)
        }
        await loadHistory(false)
        return
      }
      // 公开频道/群链接：协议号已 JoinChannel，勿开浏览器
      if (result.mode === 'join_channel') {
        emit('log', `✓ 「${btn.text}」→ ${result.note || '协议号已加入频道/群'}`)
        return
      }
      if (!result.url) {
        emit('log', `✗ 打开「${btn.text}」失败：未返回可打开地址`)
        return
      }
      await openExternalUrl(result.url)
      emit('log', `✓ 已打开「${btn.text}」→ ${result.note || '请在浏览器完成验证'}`)
      return
    }

    if (!btn.data) {
      emit('log', `按钮「${btn.text}」无 callback 数据`)
      return
    }
    const result = await answerJoinChallenge({
      accountId: accountId.value,
      target: d.username ? `@${d.username}` : String(d.peerId),
      peerType,
      peerId: d.peerId,
      accessHash: d.accessHash,
      msgId: m.id,
      data: btn.data,
    })
    if (result.ok) {
      emit(
        'log',
        `✓ 已点「${btn.text}」${result.message ? ' · ' + result.message : ''}${result.alert ? '（弹窗）' : ''}`,
      )
      await loadHistory(false)
    } else {
      emit(
        'log',
        `✗ 点击「${btn.text}」失败 [${result.businessResult}] ${result.errorMessage || ''}`,
      )
    }
  } catch (err) {
    emit('log', `点击按钮失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    answeringKey.value = ''
  }
}

/**
 * 用当前协议号打开任意 TG 链接（bot start / Mini App / 普通 URL）。
 */
async function onOpenTgLink(): Promise<void> {
  const raw = openLinkText.value.trim()
  if (!raw || !accountId.value) {
    emit('log', '请填写 TG 链接并选中协议号')
    return
  }
  const d = activeDialog.value
  openingLink.value = true
  try {
    const result = await openChallengeWebView({
      accountId: accountId.value,
      target: d?.username ? `@${d.username}` : '',
      peerType: d?.peerType || 'user',
      peerId: d?.peerId || 0,
      accessHash: d?.accessHash,
      msgId: 0,
      botId: d?.peerType === 'user' ? d.peerId : undefined,
      botAccessHash: d?.peerType === 'user' ? d.accessHash : undefined,
      kind: 'url',
      url: raw,
    })
    if (!result.ok) {
      emit('log', `✗ 打开链接失败 [${result.businessResult}] ${result.errorMessage || ''}`)
      return
    }
    if (result.mode === 'bot_dm') {
      emit(
        'log',
        `✓ 协议号已私信 @${result.botUsername || '?'}?start=${result.startParam || ''} · ${result.note || ''}`,
      )
      if (result.replyText) {
        emit('log', `机器人回复：${result.replyText.slice(0, 160)}`)
      }
      openLinkText.value = ''
      if (d) {
        await loadHistory(false)
      }
      return
    }
    if (result.mode === 'join_channel') {
      emit('log', `✓ ${result.note || '协议号已加入频道/群'}`)
      openLinkText.value = ''
      return
    }
    if (result.url) {
      await openExternalUrl(result.url)
      emit('log', `✓ ${result.note || '已打开链接'}`)
      openLinkText.value = ''
    }
  } catch (err) {
    emit('log', `打开链接失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    openingLink.value = false
  }
}

/**
 * 格式化会话时间。
 *
 * @param sec unix 秒
 * @return 展示字符串
 */
function formatTime(sec?: number): string {
  if (!sec) {
    return ''
  }
  const d = new Date(sec * 1000)
  const now = new Date()
  const sameDay =
    d.getFullYear() === now.getFullYear() &&
    d.getMonth() === now.getMonth() &&
    d.getDate() === now.getDate()
  if (sameDay) {
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }
  return `${d.getMonth() + 1}/${d.getDate()} ${d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
}

/**
 * 加载群发可用的发送策略（与营销共用）。
 */
async function loadBulkPolicies(): Promise<void> {
  if (!props.sidecarOk) {
    return
  }
  try {
    sendPolicies.value = await listMarketingSendPolicies()
    if (!selectedPolicyId.value && sendPolicies.value.length) {
      selectedPolicyId.value = sendPolicies.value[0].id
    }
  } catch (err) {
    emit('log', `加载发送策略失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

/**
 * 若无策略则创建一条默认策略（与营销默认一致）。
 */
async function ensureDefaultBulkPolicy(): Promise<void> {
  if (sendPolicies.value.length) {
    return
  }
  try {
    const saved = await upsertMarketingSendPolicy({
      name: '默认策略',
      windowMinutes: 60,
      maxPerWindow: 5,
      mutualMinIntervalMs: 3000,
      nonMutualMinIntervalMs: 600000,
      nonMutualDailyCap: 20,
      sameTargetCooldownMs: 86400000,
      status: 'active',
    })
    sendPolicies.value = [saved]
    selectedPolicyId.value = saved.id
    emit('log', `已创建默认发送策略：${saved.name}`)
  } catch (err) {
    emit('log', `创建默认策略失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

/**
 * 加载群发任务列表。
 *
 * @param silent 是否静默
 */
async function loadBulkJobs(silent = true): Promise<void> {
  if (!props.sidecarOk) {
    return
  }
  try {
    bulkJobs.value = await listMarketingSendJobs(BULK_SEND_PRODUCT_ID)
    if (!silent) {
      emit('log', `群发任务 ${bulkJobs.value.length} 个`)
    }
    ensureBulkJobsPolling()
  } catch (err) {
    emit('log', `加载群发任务失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

/**
 * running 任务时轮询进度。
 */
function ensureBulkJobsPolling(): void {
  const need = bulkJobs.value.some((j) => j.status === 'running')
  if (need && !bulkJobsPoll) {
    bulkJobsPoll = setInterval(() => {
      void loadBulkJobs(true)
    }, 3000)
  }
  if (!need) {
    stopBulkJobsPolling()
  }
}

/**
 * 停止群发任务轮询。
 */
function stopBulkJobsPolling(): void {
  if (bulkJobsPoll) {
    clearInterval(bulkJobsPoll)
    bulkJobsPoll = null
  }
}

/**
 * 新增一条空白文案。
 */
function addVariant(): void {
  variants.value = [...variants.value, '']
}

/**
 * 删除一条文案。
 *
 * @param index 下标
 */
function removeVariant(index: number): void {
  if (variants.value.length <= 1) {
    variants.value = ['']
    return
  }
  variants.value = variants.value.filter((_, i) => i !== index)
}

/**
 * 群发提交：多号 + 策略调度，文案随机。
 */
async function onSend(): Promise<void> {
  if (!props.sidecarOk) {
    emit('log', '本地服务未连接，无法发送')
    return
  }
  if (!props.gotdConfigured) {
    emit('log', '未配置 TG_APP_ID / TG_APP_HASH')
    return
  }
  if (props.selectedIds.length === 0) {
    emit('log', '请在中栏勾选至少一个可用协议号（作为号池）')
    return
  }
  const targets = parseTargets(targetsText.value)
  if (targets.length === 0) {
    emit('log', '请填写目标名单')
    return
  }
  const texts = variants.value.map((v) => v.trim()).filter(Boolean)
  if (!texts.length) {
    emit('log', '请至少填写一条文案')
    return
  }
  await loadBulkPolicies()
  if (!selectedPolicyId.value) {
    await ensureDefaultBulkPolicy()
  }
  if (!selectedPolicyId.value) {
    emit('log', '请选择发送策略（可在「设置 → 发送策略」新建，或点「快速创建默认」）')
    return
  }

  sending.value = true
  emit(
    'log',
    `群发入队：mode=${mode.value} 目标=${targets.length} 文案=${texts.length} 号池=${props.selectedIds.length} 策略=${selectedPolicyLabel.value} 熔断=${failStopAfter.value || '关'}`,
  )
  try {
    const result = await createBulkSendJob({
      mode: mode.value,
      targets,
      variants: texts,
      policyId: selectedPolicyId.value,
      accountIds: props.selectedIds,
      failStopAfter: failStopAfterForApi(failStopAfter.value),
    })
    emit(
      'log',
      `✓ 已入队 ${result.enqueued} 条 · job=${result.job?.id?.slice(0, 10) || '?'}… · ${result.note || '后台按策略发送'}`,
    )
    await loadBulkJobs(true)
  } catch (err) {
    emit('log', `入队失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <div
    class="messages-panel"
    :class="{ 'is-chat': panelTab === 'chat', 'is-bulk': panelTab === 'bulk' }"
  >
    <div class="main-tabs">
      <button type="button" :class="{ active: panelTab === 'chat' }" @click="panelTab = 'chat'">
        聊天
      </button>
      <button type="button" :class="{ active: panelTab === 'bulk' }" @click="panelTab = 'bulk'">
        群发
      </button>
    </div>

    <!-- 聊天界面 -->
    <div v-if="panelTab === 'chat'" class="chat-shell">
      <aside class="dialog-list">
        <div class="dialog-toolbar">
          <div class="dialog-tabs">
            <button
              type="button"
              :class="{ active: dialogTab === 'user' }"
              @click="dialogTab = 'user'"
            >
              私信
            </button>
            <button
              type="button"
              :class="{ active: dialogTab === 'group' }"
              @click="dialogTab = 'group'"
            >
              群
            </button>
          </div>
          <button
            type="button"
            class="ghost"
            :disabled="dialogsLoading || !accountId"
            @click="loadDialogs"
          >
            {{ dialogsLoading ? '…' : '刷新' }}
          </button>
        </div>
        <p v-if="!accountId" class="empty-hint">请在中栏勾选一个协议号</p>
        <p v-else-if="selectedIds.length > 1" class="empty-hint">
          聊天使用第一个选中账号：{{ accountId }}
        </p>
        <p v-else-if="dialogsLoading && !dialogs.length" class="empty-hint">加载会话中…</p>
        <p v-else-if="!dialogs.length" class="empty-hint">
          {{ dialogTab === 'group' ? '暂无群/频道会话' : '暂无私信会话' }}
        </p>
        <button
          v-for="d in dialogs"
          :key="`${d.peerType}:${d.peerId}`"
          type="button"
          class="dialog-item"
          :class="{ active: activeDialog?.peerId === d.peerId && activeDialog?.peerType === d.peerType }"
          @click="openDialog(d)"
        >
          <div class="dialog-top">
            <strong>
              {{ d.title || d.username || d.peerId }}
            </strong>
            <span class="time">{{ formatTime(d.lastDate) }}</span>
          </div>
          <div class="dialog-bottom">
            <span class="preview">{{ d.lastMessage || '—' }}</span>
            <span v-if="d.unreadCount" class="badge">{{ d.unreadCount }}</span>
          </div>
        </button>
      </aside>

      <section class="chat-main">
        <header v-if="activeDialog" class="chat-header">
          <div>
            <strong>{{ activeDialog.title || activeDialog.username || activeDialog.peerId }}</strong>
            <span class="sub">
              {{ activeDialog.peerType }}
              <template v-if="activeDialog.username"> · @{{ activeDialog.username }}</template>
              · {{ activeDialog.peerId }}
              <template v-if="activeDialog.unreadCount"> · 未读 {{ activeDialog.unreadCount }}</template>
            </span>
          </div>
          <div class="header-actions">
            <button
              type="button"
              class="ghost"
              :disabled="!sidecarOk || historyLoading"
              @click="markActiveRead"
            >
              标为已读
            </button>
          </div>
        </header>
        <div v-if="activeDialog" class="open-link-bar">
          <input
            v-model="openLinkText"
            type="text"
            placeholder="粘贴 TG 链接用协议号打开（t.me/频道 / bot?start= / Mini App / 普通 URL）"
            :disabled="openingLink || !sidecarOk || !accountId"
            @keydown.enter.prevent="onOpenTgLink"
          />
          <button
            type="button"
            class="ghost"
            :disabled="openingLink || !sidecarOk || !accountId || !openLinkText.trim()"
            @click="onOpenTgLink"
          >
            {{ openingLink ? '打开中…' : '打开链接' }}
          </button>
        </div>
        <div v-else class="chat-placeholder">选择左侧会话开始聊天</div>

        <div v-if="activeDialog" ref="messagesEl" class="msg-list" @scroll.passive="onMsgScroll">
          <p v-if="historyLoading && !messages.length" class="empty-hint">加载历史…</p>
          <p v-else-if="loadingOlder" class="empty-hint top-hint">加载更早消息…</p>
          <p v-else-if="historyExhausted && messages.length" class="empty-hint top-hint">
            已到最早消息
          </p>
          <div
            v-for="m in messages"
            :key="m.id"
            class="bubble-row"
            :class="{ out: m.out }"
          >
            <div class="bubble">
              <div v-if="!m.out && m.fromName" class="from">{{ m.fromName }}</div>

              <!-- 图片 -->
              <a
                v-if="m.media?.type === 'photo' || m.media?.type === 'sticker'"
                class="media-photo"
                :href="mediaSrc(m)"
                target="_blank"
                rel="noopener"
              >
                <img :src="mediaSrc(m)" :alt="m.media.fileName || '图片'" loading="lazy" />
              </a>

              <!-- 视频 -->
              <video
                v-else-if="m.media?.type === 'video'"
                class="media-video"
                controls
                preload="metadata"
                :src="mediaSrc(m)"
              />

              <!-- 音频 / 语音 -->
              <audio
                v-else-if="m.media?.type === 'audio' || m.media?.type === 'voice'"
                class="media-audio"
                controls
                preload="metadata"
                :src="mediaSrc(m)"
              />

              <!-- 文档 -->
              <a
                v-else-if="m.media?.type === 'document'"
                class="media-doc"
                :href="mediaSrc(m)"
                target="_blank"
                rel="noopener"
              >
                <span class="doc-icon">📄</span>
                <span class="doc-meta">
                  <strong>{{ m.media.fileName || '文件' }}</strong>
                  <small>
                    {{ m.media.mimeType || 'document' }}
                    <template v-if="m.media.fileSize"> · {{ formatSize(m.media.fileSize) }}</template>
                  </small>
                </span>
              </a>

              <!-- 位置 -->
              <button
                v-else-if="m.media?.type === 'location'"
                type="button"
                class="media-loc"
                @click="openLocationMap(m.media)"
              >
                <span class="loc-pin">📍</span>
                <span class="loc-meta">
                  <strong>{{ m.media.title || '位置' }}</strong>
                  <small v-if="m.media.address">{{ m.media.address }}</small>
                  <small v-else>{{ m.media.lat }}, {{ m.media.long }}</small>
                </span>
              </button>

              <!-- 其他媒体摘要（webpage 只展示正文，避免与 text 重复） -->
              <div
                v-else-if="m.media?.type && !hasRenderableMedia(m) && m.media.type !== 'webpage'"
                class="media-fallback"
              >
                {{ m.text || `[${m.media.type}]` }}
              </div>

              <div v-if="bubbleText(m)" class="text">{{ bubbleText(m) }}</div>

              <!-- inline 键盘（验证/菜单按钮） -->
              <div
                v-if="m.buttons?.length"
                class="msg-keyboard"
              >
                <div
                  v-for="(row, ri) in messageButtonRows(m)"
                  :key="ri"
                  class="msg-keyboard-row"
                >
                  <button
                    v-for="btn in row"
                    :key="msgBtnKey(m, btn)"
                    type="button"
                    class="msg-kbd-btn"
                    :class="{
                      admin: btn.adminLikely,
                      web: btn.kind === 'webview' || btn.kind === 'simple_webview',
                      link: btn.kind === 'url',
                    }"
                    :disabled="
                      sendingChat ||
                      answeringKey !== '' ||
                      btn.adminLikely ||
                      !sidecarOk
                    "
                    :title="
                      btn.adminLikely
                        ? '疑似管理按钮，已禁用'
                        : btn.kind + (btn.url ? ' · ' + btn.url : '')
                    "
                    @click="onClickMessageButton(m, btn)"
                  >
                    {{ answeringKey === msgBtnKey(m, btn) ? '…' : btn.text }}
                  </button>
                </div>
              </div>

              <div class="meta">{{ formatTime(m.date) }}</div>
            </div>
          </div>
        </div>


        <footer v-if="activeDialog" class="composer">
          <input
            ref="mediaFileInput"
            type="file"
            class="hidden-file"
            accept="image/*,video/*"
            @change="onMediaFileChange"
          />
          <input
            ref="docFileInput"
            type="file"
            class="hidden-file"
            @change="onDocFileChange"
          />

          <div class="composer-tools">
            <div class="tool-wrap">
              <button
                type="button"
                class="icon-btn"
                title="附件"
                :disabled="sendingChat || !sidecarOk"
                @click="toggleAttach"
              >
                <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true">
                  <path
                    fill="currentColor"
                    d="M16.5 6.5v9.25a4.75 4.75 0 1 1-9.5 0V6.2a3.2 3.2 0 0 1 6.4 0v8.55a1.65 1.65 0 1 1-3.3 0V7.25h1.5v7.5a.15.15 0 0 0 .3 0V6.2a1.7 1.7 0 0 0-3.4 0v9.55a3.25 3.25 0 0 0 6.5 0V6.5h1.5z"
                  />
                </svg>
              </button>
              <div v-if="attachOpen" class="attach-menu">
                <button type="button" @click="pickPhotoVideo">
                  <span class="am-icon">🖼️</span>
                  图片或视频
                </button>
                <button type="button" @click="pickDocument">
                  <span class="am-icon">📄</span>
                  文档
                </button>
                <button type="button" @click="openLocationPanel">
                  <span class="am-icon">📍</span>
                  位置
                </button>
              </div>
            </div>

            <div class="tool-wrap">
              <button
                type="button"
                class="icon-btn"
                title="表情"
                :disabled="sendingChat"
                @click="toggleEmoji"
              >
                <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true">
                  <path
                    fill="currentColor"
                    d="M12 2a10 10 0 1 0 .001 20.001A10 10 0 0 0 12 2zm0 18a8 8 0 1 1-.001-15.999A8 8 0 0 1 12 20zm-3.5-7.5a1.25 1.25 0 1 0 0-2.5 1.25 1.25 0 0 0 0 2.5zm7 0a1.25 1.25 0 1 0 0-2.5 1.25 1.25 0 0 0 0 2.5zM12 17.2c-2.1 0-3.9-1.2-4.6-3h1.7c.5 1 1.6 1.6 2.9 1.6s2.4-.6 2.9-1.6h1.7c-.7 1.8-2.5 3-4.6 3z"
                  />
                </svg>
              </button>
              <div v-if="emojiOpen" class="emoji-panel">
                <button
                  v-for="em in EMOJI_LIST"
                  :key="em"
                  type="button"
                  @click="insertEmoji(em)"
                >
                  {{ em }}
                </button>
              </div>
            </div>
          </div>

          <textarea
            v-model="draft"
            rows="2"
            placeholder="输入消息，Enter 发送，Shift+Enter 换行"
            :disabled="sendingChat"
            @keydown="onDraftKeydown"
            @focus="attachOpen = false"
          />
          <button
            type="button"
            class="primary"
            :disabled="sendingChat || !draft.trim() || !sidecarOk"
            @click="onSendChat"
          >
            {{ sendingChat ? '发送中…' : '发送' }}
          </button>
        </footer>

        <!-- 位置发送弹层 -->
        <div v-if="locationOpen" class="loc-modal" @click.self="locationOpen = false">
          <div class="loc-card">
            <h4>发送位置</h4>
            <div class="loc-fields">
              <label>
                纬度
                <input v-model="locLat" type="number" step="any" placeholder="如 31.2304" />
              </label>
              <label>
                经度
                <input v-model="locLong" type="number" step="any" placeholder="如 121.4737" />
              </label>
            </div>
            <div class="loc-actions">
              <button type="button" class="ghost" @click="useBrowserLocation">使用当前位置</button>
              <button type="button" class="ghost" @click="locationOpen = false">取消</button>
              <button
                type="button"
                class="primary"
                :disabled="sendingChat || !sidecarOk"
                @click="onSendLocation"
              >
                发送
              </button>
            </div>
          </div>
        </div>
      </section>
    </div>

    <!-- 群发：多号策略 + 多文案随机（内容区独立纵向滚动，不被下方任务日志挡住） -->
    <div v-else class="bulk-scroll">
      <p class="bulk-lead">
        中栏勾选多协议号作为号池 → 选发送策略（与营销共用）→ 手动填写文案池 →
        入队后后台按策略限速，每目标随机选一条文案。
      </p>
      <div class="tabs">
        <button
          type="button"
          :class="{ active: mode === 'username' }"
          @click="mode = 'username'"
        >
          按用户名
        </button>
        <button type="button" :class="{ active: mode === 'phone' }" @click="mode = 'phone'">
          按手机号
        </button>
        <button type="button" :class="{ active: mode === 'userId' }" @click="mode = 'userId'">
          按用户 ID
        </button>
      </div>

      <div class="field">
        <label>目标名单（{{ targetCount }}）</label>
        <textarea v-model="targetsText" rows="7" :placeholder="placeholders[mode]" />
      </div>

      <div class="field">
        <div class="variants-head">
          <label>文案池（{{ variantCount }} 条有效 · 发送时随机选用）</label>
          <div class="variants-ops">
            <button type="button" class="ghost mini" @click="addVariant">+ 添加</button>
          </div>
        </div>
        <div v-for="(_v, i) in variants" :key="i" class="variant-row">
          <textarea
            v-model="variants[i]"
            rows="3"
            :placeholder="i === 0 ? '第 1 条文案' : `文案 ${i + 1}`"
          />
          <button type="button" class="ghost mini" title="删除" @click="removeVariant(i)">
            删
          </button>
        </div>
        <p class="hint">可手动添加多条文案轮换；建议 3～15 条降低重复感。</p>
      </div>

      <div class="row-fields">
        <div class="field">
          <label>发送策略</label>
          <select v-model="selectedPolicyId" @focus="loadBulkPolicies">
            <option value="">请选择</option>
            <option v-for="p in sendPolicies" :key="p.id" :value="p.id">
              {{ p.name }}（{{ p.windowMinutes }}分 / {{ p.maxPerWindow }}条）
            </option>
          </select>
          <p class="hint">
            与「营销」共用；新建/编辑请到「设置 → 发送策略」。无策略时可
            <button type="button" class="linkish" @click="ensureDefaultBulkPolicy">创建              创建默认
            </button>
          </p>
        </div>
        <div class="field">
          <label>连续失败熔断</label>
          <input v-model.number="failStopAfter" type="number" min="0" step="1" />
          <p class="hint">0=关闭</p>
        </div>
        <div class="field meta">
          <label>号池（中栏勾选）</label>
          <strong :class="{ warn: selectedIds.length === 0 }">{{ selectedIds.length }}</strong>
        </div>
      </div>

      <div class="actions">
        <button
          type="button"
          class="primary"
          :disabled="sending || !sidecarOk || selectedIds.length === 0"
          @click="onSend"
        >
          {{ sending ? '入队中…' : '按策略入队发送' }}
        </button>
      </div>

      <p v-if="bulkJobs.length" class="hint queue-tip">
        已有 {{ bulkJobs.length }} 个群发任务
        <template v-if="bulkJobs.some((j) => j.status === 'running' || j.status === 'paused')">
          （进行中
          {{ bulkJobs.filter((j) => j.status === 'running' || j.status === 'paused').length }} 个）
        </template>
        。明细、改文案/删除请到左侧「队列」。
      </p>

      <p v-if="mode === 'userId'" class="warn">
        无本协议号 access_hash 的条目将记为失败，不会借用其他协议号的实体缓存。
      </p>
      <p v-if="mode === 'phone'" class="hint">
        「未匹配」不等于「未注册」。非双向联系人将按策略使用较长间隔与日上限。
      </p>
    </div>
  </div>
</template>

<style scoped>
.messages-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-height: 0;
}

.messages-panel.is-chat {
  flex: 1;
  height: 100%;
}

.messages-panel.is-bulk {
  flex: 1;
  height: 100%;
  min-height: 0;
  overflow: hidden;
}

.bulk-scroll {
  flex: 1;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding-bottom: 8px;
  -webkit-overflow-scrolling: touch;
}

.main-tabs {
  display: flex;
  gap: 4px;
  border-bottom: 1px solid var(--color-border);
  padding-bottom: 8px;
  flex-shrink: 0;
}

.main-tabs button,
.tabs button {
  border: none;
  background: transparent;
  padding: 8px 14px;
  font: inherit;
  cursor: pointer;
  color: var(--color-text-secondary);
  border-radius: 4px 4px 0 0;
}

.main-tabs button.active,
.tabs button.active {
  color: var(--color-primary);
  background: var(--color-bg-selected);
  font-weight: 600;
}

.tabs {
  display: flex;
  gap: 4px;
  border-bottom: 1px solid var(--color-border);
  padding-bottom: 8px;
}

.bulk-lead {
  margin: 0 0 10px;
  font-size: 12px;
  color: var(--color-text-secondary);
  line-height: 1.5;
}

.variants-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 6px;
}

.variants-ops {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}

.variants-ops input[type='number'] {
  width: 64px;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 4px 6px;
  font: inherit;
  font-size: 12px;
}

.variant-row {
  display: grid;
  grid-template-columns: 1fr auto;
  gap: 6px;
  margin-bottom: 8px;
}

.variant-row textarea {
  width: 100%;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 8px 10px;
  font: inherit;
  font-size: 13px;
  resize: vertical;
  box-sizing: border-box;
}

.linkish {
  border: none;
  background: none;
  padding: 0;
  color: var(--color-primary);
  font: inherit;
  font-size: inherit;
  cursor: pointer;
  text-decoration: underline;
}

.bulk-jobs {
  margin-top: 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.bulk-jobs h4 {
  margin: 0;
  font-size: 13px;
}

.bulk-job {
  border: 1px solid var(--color-border);
  border-radius: 6px;
  padding: 10px 12px;
  background: #fff;
}

.bulk-job header {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  font-size: 12px;
  margin-bottom: 8px;
}

.bulk-job .badge {
  background: var(--color-bg-selected);
  color: var(--color-primary);
  border-radius: 999px;
  padding: 2px 8px;
  font-weight: 600;
}

.bulk-job .meta-id {
  color: var(--color-text-secondary);
  font-family: ui-monospace, monospace;
}

.row-fields select {
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 8px 10px;
  font: inherit;
  font-size: 13px;
  background: #fff;
}

.chat-shell {
  display: grid;
  grid-template-columns: 280px 1fr;
  gap: 0;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  overflow: hidden;
  min-height: 420px;
  flex: 1;
  background: #fff;
}

.dialog-list {
  border-right: 1px solid var(--color-border);
  background: var(--color-bg-sidebar);
  display: flex;
  flex-direction: column;
  overflow: auto;
  max-height: min(58vh, 640px);
}

.dialog-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border-bottom: 1px solid var(--color-border);
  flex-shrink: 0;
}

.dialog-tabs {
  display: flex;
  gap: 2px;
  background: rgba(0, 0, 0, 0.04);
  border-radius: 4px;
  padding: 2px;
}

.dialog-tabs button {
  border: none;
  background: transparent;
  border-radius: 3px;
  padding: 4px 12px;
  font: inherit;
  font-size: 12px;
  color: var(--color-text-secondary);
  cursor: pointer;
}

.dialog-tabs button.active {
  background: #fff;
  color: var(--color-primary);
  font-weight: 600;
  box-shadow: 0 0 0 1px var(--color-border);
}

.ghost {
  border: 1px solid var(--color-border);
  background: #fff;
  border-radius: 4px;
  padding: 4px 10px;
  font: inherit;
  font-size: 12px;
  cursor: pointer;
}

.ghost:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.dialog-item {
  display: block;
  width: 100%;
  text-align: left;
  border: none;
  border-bottom: 1px solid var(--color-border);
  background: transparent;
  padding: 10px 12px;
  cursor: pointer;
  font: inherit;
}

.dialog-item:hover {
  background: rgba(0, 0, 0, 0.03);
}

.dialog-item.active {
  background: var(--color-bg-selected);
}

.dialog-top {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 4px;
}

.dialog-top strong {
  font-size: 13px;
  color: var(--color-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dialog-top .time {
  font-size: 11px;
  color: var(--color-text-secondary);
  flex-shrink: 0;
}

.dialog-bottom {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  align-items: center;
}

.preview {
  font-size: 12px;
  color: var(--color-text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.badge {
  flex-shrink: 0;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: 999px;
  background: var(--color-primary);
  color: #fff;
  font-size: 11px;
  line-height: 18px;
  text-align: center;
}

.chat-main {
  position: relative;
  display: flex;
  flex-direction: column;
  min-width: 0;
  max-height: min(58vh, 640px);
  background: #f7f8fa;
}

.chat-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--color-border);
  background: #fff;
  flex-shrink: 0;
}

.chat-header strong {
  display: block;
  font-size: 14px;
  color: var(--color-text);
}

.chat-header .sub {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.header-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  justify-content: flex-end;
}


.chat-placeholder {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-secondary);
  font-size: 13px;
}

.msg-list {
  flex: 1;
  overflow: auto;
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.top-hint {
  text-align: center;
  margin: 0 0 8px;
  flex-shrink: 0;
}

.bubble-row {
  display: flex;
  justify-content: flex-start;
}

.bubble-row.out {
  justify-content: flex-end;
}

.bubble {
  max-width: 72%;
  padding: 8px 12px;
  border-radius: 10px;
  background: #fff;
  border: 1px solid var(--color-border);
  box-shadow: 0 1px 1px rgba(0, 0, 0, 0.04);
}

.bubble-row.out .bubble {
  background: #dcf8c6;
  border-color: #c5e8a8;
}

.bubble .from {
  font-size: 11px;
  color: var(--color-primary);
  margin-bottom: 2px;
}

.bubble .text {
  font-size: 13px;
  color: var(--color-text);
  white-space: pre-wrap;
  word-break: break-word;
  line-height: 1.45;
}

.bubble .meta {
  margin-top: 4px;
  font-size: 10px;
  color: var(--color-text-secondary);
  text-align: right;
}

.open-link-bar {
  display: flex;
  gap: 8px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--color-border);
  background: var(--color-bg, #f7f8fa);
  flex-shrink: 0;
}

.open-link-bar input {
  flex: 1;
  min-width: 0;
  height: 32px;
  padding: 0 10px;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  font-size: 12px;
  background: #fff;
}

.msg-keyboard {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 8px;
}

.msg-keyboard-row {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.msg-kbd-btn {
  flex: 1 1 auto;
  min-width: 72px;
  padding: 6px 10px;
  border: 1px solid var(--color-primary, #2481cc);
  border-radius: 6px;
  background: rgba(36, 129, 204, 0.08);
  color: var(--color-primary, #2481cc);
  font-size: 12px;
  cursor: pointer;
  line-height: 1.3;
}

.msg-kbd-btn:hover:not(:disabled) {
  background: rgba(36, 129, 204, 0.16);
}

.msg-kbd-btn:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.msg-kbd-btn.admin {
  border-color: #bbb;
  color: #999;
  background: #f0f0f0;
}

.msg-kbd-btn.web,
.msg-kbd-btn.link {
  border-style: dashed;
}

.media-photo {
  display: block;
  margin: 0 0 6px;
  border-radius: 8px;
  overflow: hidden;
  max-width: 280px;
}

.media-photo img {
  display: block;
  max-width: 100%;
  max-height: 320px;
  object-fit: contain;
  background: rgba(0, 0, 0, 0.04);
}

.media-video {
  display: block;
  max-width: 280px;
  max-height: 320px;
  margin: 0 0 6px;
  border-radius: 8px;
  background: #000;
}

.media-audio {
  display: block;
  width: min(260px, 100%);
  margin: 0 0 6px;
}

.media-doc,
.media-loc {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  max-width: 280px;
  margin: 0 0 6px;
  padding: 8px 10px;
  border-radius: 8px;
  border: 1px solid var(--color-border);
  background: rgba(255, 255, 255, 0.7);
  text-align: left;
  font: inherit;
  color: inherit;
  cursor: pointer;
  text-decoration: none;
}

.bubble-row.out .media-doc,
.bubble-row.out .media-loc {
  background: rgba(255, 255, 255, 0.45);
}

.doc-icon,
.loc-pin {
  font-size: 22px;
  line-height: 1;
  flex-shrink: 0;
}

.doc-meta,
.loc-meta {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.doc-meta strong,
.loc-meta strong {
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.doc-meta small,
.loc-meta small {
  font-size: 11px;
  color: var(--color-text-secondary);
}

.media-fallback {
  font-size: 13px;
  color: var(--color-text-secondary);
  margin-bottom: 4px;
}

.composer {
  display: flex;
  gap: 8px;
  padding: 12px;
  border-top: 1px solid var(--color-border);
  background: #fff;
  flex-shrink: 0;
  align-items: flex-end;
}

.composer-tools {
  display: flex;
  gap: 2px;
  flex-shrink: 0;
  padding-bottom: 4px;
}

.tool-wrap {
  position: relative;
}

.icon-btn {
  border: none;
  background: transparent;
  color: var(--color-text-secondary);
  width: 36px;
  height: 36px;
  border-radius: 50%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
}

.icon-btn:hover:not(:disabled) {
  background: rgba(0, 0, 0, 0.06);
  color: var(--color-text);
}

.icon-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.attach-menu {
  position: absolute;
  bottom: calc(100% + 6px);
  left: 0;
  min-width: 160px;
  background: #fff;
  border: 1px solid var(--color-border);
  border-radius: 10px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.12);
  padding: 6px;
  z-index: 20;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.attach-menu button {
  display: flex;
  align-items: center;
  gap: 10px;
  border: none;
  background: transparent;
  padding: 10px 12px;
  border-radius: 8px;
  font: inherit;
  font-size: 13px;
  color: var(--color-text);
  cursor: pointer;
  white-space: nowrap;
}

.attach-menu button:hover {
  background: rgba(0, 0, 0, 0.05);
}

.am-icon {
  width: 22px;
  text-align: center;
}

.emoji-panel {
  position: absolute;
  bottom: calc(100% + 6px);
  left: 0;
  width: 260px;
  max-height: 180px;
  overflow: auto;
  background: #fff;
  border: 1px solid var(--color-border);
  border-radius: 10px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.12);
  padding: 8px;
  z-index: 20;
  display: grid;
  grid-template-columns: repeat(6, 1fr);
  gap: 4px;
}

.emoji-panel button {
  border: none;
  background: transparent;
  border-radius: 6px;
  font-size: 20px;
  line-height: 1.4;
  cursor: pointer;
  padding: 4px;
}

.emoji-panel button:hover {
  background: rgba(0, 0, 0, 0.06);
}

.hidden-file {
  display: none;
}

.loc-modal {
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, 0.28);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 30;
  padding: 16px;
}

.loc-card {
  width: min(360px, 100%);
  background: #fff;
  border-radius: 10px;
  padding: 16px;
  box-shadow: 0 12px 32px rgba(0, 0, 0, 0.16);
}

.loc-card h4 {
  margin: 0 0 12px;
  font-size: 15px;
}

.loc-fields {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
  margin-bottom: 14px;
}

.loc-fields label {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12px;
  color: var(--color-text-secondary);
}

.loc-fields input {
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 8px 10px;
  font: inherit;
}

.loc-actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  flex-wrap: wrap;
}

.composer textarea {
  flex: 1;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  padding: 8px 10px;
  font: inherit;
  resize: none;
}

.empty-hint {
  margin: 12px;
  font-size: 12px;
  color: var(--color-text-secondary);
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.field label {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.field textarea,
.field input {
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 8px 10px;
  font: inherit;
}

.row-fields {
  display: grid;
  grid-template-columns: 1fr 120px 160px;
  gap: 16px;
  align-items: start;
}

.check {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--color-text-secondary);
}

.meta strong {
  font-size: 22px;
  color: var(--color-text);
}

.meta strong.warn {
  color: var(--color-danger);
}

.hint {
  margin: 0;
  font-size: 12px;
  color: var(--color-text-secondary);
}

.warn {
  margin: 0;
  font-size: 12px;
  color: #c98900;
}

.actions {
  display: flex;
  gap: 8px;
}

.primary {
  border: none;
  border-radius: 4px;
  padding: 10px 18px;
  font: inherit;
  cursor: pointer;
  background: var(--color-primary);
  color: #fff;
  white-space: nowrap;
}

.primary:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
</style>
