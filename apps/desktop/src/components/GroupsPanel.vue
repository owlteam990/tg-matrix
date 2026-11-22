<script setup lang="ts">
import { computed, onActivated, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  answerJoinChallenge,
  downloadGroupMonitorExportFile,
  exportGroupMonitor,
  groupBatch,
  inviteBatch,
  joinCheck,
  joinConfirm,
  joinPreview,
  listGroupMonitors,
  listJoinChallenges,
  listJoinedGroups,
  openChallengeWebView,
  refreshJoinedGroups,
  startGroupMonitor,
  stopGroupMonitor,
  type ChallengeButton,
  type GroupAction,
  type GroupMonitor,
  type InviteMode,
  type JoinChallenge,
  type JoinedGroup,
  type JoinPreview,
} from '../api/sidecar'
import { failStopAfterForApi, loadSettings } from '../lib/settings'
import { openExternalUrl } from '../lib/openUrl'
import { saveTextFile } from '../lib/saveCsv'

defineOptions({ name: 'GroupsPanel' })

const props = defineProps<{
  selectedIds: string[]
  gotdConfigured: boolean
  sidecarOk: boolean
}>()

const emit = defineEmits<{
  log: [line: string]
}>()

type TabKey = GroupAction | 'invite_contact' | 'invite_username' | 'joined' | 'manual'

const GROUPS_TAB_KEY = 'tgmatrix.groups.tab'

/**
 * 恢复上次群组 Tab（切换模块回来后保持「已加入」等）。
 */
function loadSavedGroupsTab(): TabKey {
  try {
    const v = sessionStorage.getItem(GROUPS_TAB_KEY) || ''
    const allowed: TabKey[] = [
      'join',
      'leave',
      'invite_contact',
      'invite_username',
      'joined',
      'manual',
    ]
    if ((allowed as string[]).includes(v)) {
      return v as TabKey
    }
  } catch {
    // ignore
  }
  return 'join'
}

const tab = ref<TabKey>(loadSavedGroupsTab())
const targetsText = ref('')
const groupTarget = ref('')
const membersText = ref('')
const intervalMs = ref(2000)
const failStopAfter = ref(5)
const running = ref(false)

const joined = ref<JoinedGroup[]>([])
const leaveJoined = ref<JoinedGroup[]>([])
const selectedLeave = ref<Set<number>>(new Set())
const joinedSyncedAt = ref(0)
const previews = ref<JoinPreview[]>([])
const pendingVerify = ref<string[]>([])
const selectedPreview = ref<Set<string>>(new Set())
const challenges = ref<JoinChallenge[]>([])
const answeringKey = ref('')
const refreshingJoined = ref(false)

/** peerId（字符串键，避免大整数精度问题）→ 监控状态 */
const monitorsByPeer = ref<Map<string, GroupMonitor>>(new Map())
const monitorModalOpen = ref(false)
const monitorTarget = ref<JoinedGroup | null>(null)
const monitorHistoryLimit = ref(100)
const monitorBusyPeer = ref(0)
let monitorPollTimer: number | undefined

const HISTORY_LIMIT_OPTIONS = [50, 100, 200, 500] as const

const joinedSyncedLabel = computed(() => {
  if (!joinedSyncedAt.value) {
    return '尚未从 Telegram 同步过'
  }
  return `本地缓存 · 上次同步 ${new Date(joinedSyncedAt.value * 1000).toLocaleString()}`
})

/**
 * peerId 统一为 Map 键。
 *
 * @param peerId 群 peer
 */
function peerKey(peerId: number | string): string {
  return String(peerId)
}

onMounted(() => {
  const s = loadSettings()
  intervalMs.value = s.defaultJoinIntervalMs
  failStopAfter.value = s.failStopAfter
  // 从其它模块回来：若上次停在「已加入」，立即拉列表与监控状态
  if (tab.value === 'joined') {
    void loadJoinedLocal()
    syncMonitorPoll()
  } else if (tab.value === 'leave') {
    void loadLeaveJoinedLocal()
  }
})

// KeepAlive 激活时再拉一次监控，保证与 SQLite 同步
onActivated(() => {
  if (tab.value === 'joined') {
    void refreshMonitors()
    syncMonitorPoll()
  }
})

onUnmounted(() => {
  if (monitorPollTimer !== undefined) {
    window.clearInterval(monitorPollTimer)
  }
})

const targetCount = computed(() => parseLines(targetsText.value).length)
const memberCount = computed(() => parseLines(membersText.value).length)
const leaveSelectedCount = computed(() => selectedLeave.value.size)

const isInvite = computed(
  () => tab.value === 'invite_contact' || tab.value === 'invite_username',
)

const primaryAccount = computed(() => props.selectedIds[0] || '')

/**
 * 按行解析名单。
 *
 * @param raw 原始文本
 * @return 去重列表
 */
function parseLines(raw: string): string[] {
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

/**
 * 刷新当前账号的群监控状态。
 */
async function refreshMonitors(): Promise<void> {
  if (!primaryAccount.value || !props.sidecarOk) {
    // 短暂断连时保留上次状态，避免切换页面后全部变成「未监控」
    return
  }
  try {
    const res = await listGroupMonitors(primaryAccount.value)
    const map = new Map<string, GroupMonitor>()
    for (const item of res.items ?? []) {
      map.set(peerKey(item.peerId), item)
    }
    monitorsByPeer.value = map
  } catch {
    // 轮询失败保留旧状态
  }
}

/**
 * 取某群监控记录。
 *
 * @param g 已加入群
 */
function monitorOf(g: JoinedGroup): GroupMonitor | undefined {
  return monitorsByPeer.value.get(peerKey(g.peerId))
}

/**
 * 监控状态文案。
 *
 * @param g 已加入群
 */
function monitorStatusLabel(g: JoinedGroup): string {
  const m = monitorOf(g)
  if (!m) {
    return '未监控'
  }
  switch (m.status) {
    case 'backfilling':
      return `拉取历史 ${m.backfillDone}/${m.backfillTotal || m.historyLimit}`
    case 'monitoring':
      return `监控中 · ${m.msgCount} 条`
    case 'stopped':
      return `已停止 · ${m.msgCount} 条`
    case 'error':
      return `错误 · ${m.msgCount} 条`
    default:
      return m.status
  }
}

/**
 * 打开开启监控弹窗。
 *
 * @param g 群
 */
function openMonitorModal(g: JoinedGroup): void {
  if (g.joinRequestOut) {
    emit('log', '申请中的群无法开启监控')
    return
  }
  monitorTarget.value = g
  monitorHistoryLimit.value = 100
  monitorModalOpen.value = true
}

/**
 * 确认开启监控。
 */
async function confirmStartMonitor(): Promise<void> {
  const g = monitorTarget.value
  if (!g || !primaryAccount.value) {
    return
  }
  monitorBusyPeer.value = g.peerId
  try {
    const res = await startGroupMonitor({
      accountId: primaryAccount.value,
      peerId: g.peerId,
      accessHash: String(g.accessHash ?? 0),
      kind: g.kind,
      title: g.title,
      historyLimit: monitorHistoryLimit.value,
    })
    emit('log', res.note || `已开启监控：${g.title}`)
    monitorModalOpen.value = false
    await refreshMonitors()
  } catch (err) {
    emit('log', `开启监控失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    monitorBusyPeer.value = 0
  }
}

/**
 * 停止监控。
 *
 * @param g 群
 */
async function onStopMonitor(g: JoinedGroup): Promise<void> {
  if (!primaryAccount.value) {
    return
  }
  monitorBusyPeer.value = g.peerId
  try {
    await stopGroupMonitor({ accountId: primaryAccount.value, peerId: g.peerId })
    emit('log', `已停止监控：${g.title}`)
    await refreshMonitors()
  } catch (err) {
    emit('log', `停止监控失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    monitorBusyPeer.value = 0
  }
}

/**
 * 导出已采集消息（拆分 CSV，逐个另存为）。
 *
 * @param g 群
 */
async function onExportMonitor(g: JoinedGroup): Promise<void> {
  if (!primaryAccount.value) {
    return
  }
  monitorBusyPeer.value = g.peerId
  try {
    const res = await exportGroupMonitor({
      accountId: primaryAccount.value,
      peerId: g.peerId,
    })
    emit('log', res.note || `导出完成 ${res.total} 条`)
    for (const file of res.files ?? []) {
      const text = await downloadGroupMonitorExportFile(file.path)
      const saved = await saveTextFile(file.name, text, `导出群消息 ${file.name}`)
      if (saved.status === 'cancelled') {
        emit('log', `已取消：${file.name}`)
        break
      }
      emit('log', `已保存 ${file.name}（${file.count} 条）→ ${saved.path}`)
    }
  } catch (err) {
    emit('log', `导出失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    monitorBusyPeer.value = 0
  }
}

/**
 * 已加入 Tab 时轮询监控状态。
 */
function syncMonitorPoll(): void {
  if (monitorPollTimer !== undefined) {
    window.clearInterval(monitorPollTimer)
    monitorPollTimer = undefined
  }
  if (tab.value !== 'joined' || !props.sidecarOk) {
    return
  }
  void refreshMonitors()
  monitorPollTimer = window.setInterval(() => {
    void refreshMonitors()
  }, 4000)
}

watch(tab, (v) => {
  try {
    sessionStorage.setItem(GROUPS_TAB_KEY, v)
  } catch {
    // ignore
  }
  // 切 Tab 只读本地 SQLite，不打 TG
  if (v === 'joined') {
    void loadJoinedLocal()
  }
  if (v === 'leave') {
    void loadLeaveJoinedLocal()
  }
  if (v === 'invite_contact' || v === 'invite_username') {
    membersText.value = ''
  }
  syncMonitorPoll()
})

watch(
  () => props.selectedIds[0],
  () => {
    if (tab.value === 'joined') {
      void loadJoinedLocal()
    }
    if (tab.value === 'leave') {
      void loadLeaveJoinedLocal()
    }
    syncMonitorPoll()
  },
)

watch(
  () => props.sidecarOk,
  () => {
    syncMonitorPoll()
  },
)

/**
 * 从本地 SQLite 读取已加入群组。
 */
async function loadJoinedLocal(): Promise<void> {
  if (!props.sidecarOk) {
    emit('log', '本地服务未连接')
    return
  }
  if (!primaryAccount.value) {
    joined.value = []
    joinedSyncedAt.value = 0
    emit('log', '请先在中栏勾选一个协议号')
    return
  }
  running.value = true
  try {
    const result = await listJoinedGroups(primaryAccount.value)
    joined.value = result.items ?? []
    joinedSyncedAt.value = result.syncedAt || 0
    await refreshMonitors()
    const hint = joinedSyncedAt.value
      ? `本地 ${joined.value.length} 个 · 上次同步 ${new Date(joinedSyncedAt.value * 1000).toLocaleString()}`
      : `本地缓存为空，请点「刷新已加入」从 Telegram 拉取`
    emit('log', `已读本地已加入群：${hint}（账号 ${primaryAccount.value}）`)
  } catch (err) {
    emit('log', `读取本地群组失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    running.value = false
  }
}

/**
 * 手动从 Telegram 刷新已加入群并写入本地。
 */
async function refreshJoinedFromTG(): Promise<void> {
  if (!props.sidecarOk) {
    emit('log', '本地服务未连接')
    return
  }
  if (!props.gotdConfigured) {
    emit('log', '未配置 TG_APP_ID / TG_APP_HASH')
    return
  }
  if (!primaryAccount.value) {
    emit('log', '请先在中栏勾选一个协议号')
    return
  }
  refreshingJoined.value = true
  try {
    const result = await refreshJoinedGroups(primaryAccount.value, 200)
    joined.value = result.items ?? []
    joinedSyncedAt.value = result.syncedAt || Math.floor(Date.now() / 1000)
    leaveJoined.value = joined.value.filter((g) => !g.joinRequestOut)
    selectedLeave.value = new Set()
    await refreshMonitors()
    emit(
      'log',
      `已从 Telegram 刷新并写入本地：${joined.value.length} 个群（账号 ${primaryAccount.value}）`,
    )
  } catch (err) {
    emit('log', `刷新群组失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    refreshingJoined.value = false
  }
}

/**
 * 从本地读取批量退出用的已加入列表。
 */
async function loadLeaveJoinedLocal(): Promise<void> {
  if (!props.sidecarOk) {
    emit('log', '本地服务未连接')
    return
  }
  if (!primaryAccount.value) {
    leaveJoined.value = []
    emit('log', '请先在中栏勾选一个协议号')
    return
  }
  running.value = true
  try {
    const result = await listJoinedGroups(primaryAccount.value)
    leaveJoined.value = (result.items ?? []).filter((g) => !g.joinRequestOut)
    joinedSyncedAt.value = result.syncedAt || 0
    selectedLeave.value = new Set()
    emit(
      'log',
      `退出列表已读本地 ${leaveJoined.value.length} 个群（账号 ${primaryAccount.value}）`,
    )
  } catch (err) {
    emit('log', `读取退出列表失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    running.value = false
  }
}

/**
 * 将已加入群转为退群目标串（优先用户名，否则 peer/chat）。
 *
 * @param g 已加入群摘要
 * @return 退群 target
 */
function leaveTargetOf(g: JoinedGroup): string {
  if (g.username) {
    return `@${g.username}`
  }
  if (g.kind === 'group') {
    return `chat:${g.peerId}`
  }
  return `peer:${g.peerId}:${g.accessHash ?? 0}`
}

/**
 * 切换退群勾选。
 *
 * @param peerId 群 peerId
 */
function toggleLeave(peerId: number): void {
  const next = new Set(selectedLeave.value)
  if (next.has(peerId)) {
    next.delete(peerId)
  } else {
    next.add(peerId)
  }
  selectedLeave.value = next
}

/**
 * 全选 / 清空退群勾选。
 */
function selectAllLeave(): void {
  if (selectedLeave.value.size === leaveJoined.value.length && leaveJoined.value.length > 0) {
    selectedLeave.value = new Set()
    return
  }
  selectedLeave.value = new Set(leaveJoined.value.map((g) => g.peerId))
}

/**
 * 反选退群勾选。
 */
function invertLeave(): void {
  const next = new Set<number>()
  for (const g of leaveJoined.value) {
    if (!selectedLeave.value.has(g.peerId)) {
      next.add(g.peerId)
    }
  }
  selectedLeave.value = next
}

/**
 * 预检结果是否允许勾选确认。
 *
 * @param p 预检项
 * @return 可勾选时 true
 */
function canSelectPreview(p: JoinPreview): boolean {
  if (p.status === 'can_join' || p.status === 'need_approval') {
    return true
  }
  // 兼容：部分响应以 requestNeeded 为准
  if (p.requestNeeded && !p.alreadyIn && p.status !== 'not_found' && p.status !== 'failed') {
    return true
  }
  return false
}

/**
 * 预检加群目标。
 */
async function onPreview(): Promise<void> {
  const targets = parseLines(targetsText.value)
  if (!primaryAccount.value) {
    emit('log', '请勾选一个协议号')
    return
  }
  if (targets.length === 0) {
    emit('log', '请填写目标名单')
    return
  }
  running.value = true
  selectedPreview.value = new Set()
  previews.value = []
  try {
    const result = await joinPreview({ accountId: primaryAccount.value, targets })
    previews.value = result.items ?? []
    // 可加入 / 需审批：默认勾选
    selectedPreview.value = new Set(
      previews.value.filter((p) => canSelectPreview(p)).map((p) => p.target),
    )
    for (const p of previews.value) {
      emit(
        'log',
        `预检 ${p.target} → [${p.status}] ${p.title || ''} ${p.note || p.errorMessage || ''}`,
      )
    }
    const selectable = selectedPreview.value.size
    if (selectable > 0) {
      emit('log', `已自动勾选 ${selectable} 个可确认目标，请点「2. 确认加入」`)
    } else if (previews.value.some((p) => p.status === 'not_found')) {
      emit('log', '预检仍为未找到：请确认本地服务已热更（npm run sidecar:restart）后再预检')
    }
    emit('log', result.note)
  } catch (err) {
    emit('log', `预检失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    running.value = false
  }
}

/**
 * 切换预检行勾选。
 *
 * @param target 目标
 */
function togglePreview(target: string): void {
  const item = previews.value.find((p) => p.target === target)
  if (item && !canSelectPreview(item)) {
    return
  }
  const next = new Set(selectedPreview.value)
  if (next.has(target)) {
    next.delete(target)
  } else {
    next.add(target)
  }
  selectedPreview.value = next
}

/**
 * 手动确认加入勾选目标。
 */
async function onConfirmJoin(): Promise<void> {
  const targets = [...selectedPreview.value]
  if (!primaryAccount.value || targets.length === 0) {
    emit('log', '请先预检并勾选要确认的目标')
    return
  }
  running.value = true
  try {
    const result = await joinConfirm({ accountId: primaryAccount.value, targets })
    emit(
      'log',
      `确认加群：成功 ${result.success} / 待审批 ${result.pending} / 失败 ${result.failed}${result.taskId ? ' · task=' + result.taskId : ''}`,
    )
    const pending: string[] = [...pendingVerify.value]
    const joinedOk: string[] = []
    for (const item of result.items) {
      if (item.businessResult === 'need_approval') {
        if (!pending.includes(item.target)) {
          pending.push(item.target)
        }
        emit('log', `⏳ ${item.target} 已提交审批，请稍后「检查入群」`)
      } else if (item.ok) {
        joinedOk.push(item.target)
        emit('log', `✓ ${item.target} [${item.businessResult}]`)
      } else {
        emit('log', `✗ ${item.target} [${item.businessResult}] ${item.errorMessage || ''}`)
      }
    }
    pendingVerify.value = pending
    emit('log', result.note)
    // 仅「真正加入成功」后拉验证题；审批中 / CHANNEL_PRIVATE 拉不到群消息
    if (joinedOk.length > 0) {
      emit('log', '已入群，正在拉取机器人验证题…')
      await loadChallenges(joinedOk, false)
    } else if (result.failed > 0) {
      emit(
        'log',
        '未入群成功：无法拉取验证题。需审批请等管理员通过后「检查入群」；CHANNEL_PRIVATE 请改用 t.me/+ 邀请链接再加入',
      )
    }
  } catch (err) {
    emit('log', `确认加群失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    running.value = false
  }
}

/**
 * 拉取入群验证题。
 *
 * @param targetsOverride 指定目标；为空则用名单/待验证列表
 * @param manageRunning 是否自行管理 running 状态（嵌套调用时传 false）
 */
async function loadChallenges(targetsOverride?: string[], manageRunning = true): Promise<void> {
  if (!primaryAccount.value) {
    emit('log', '请勾选一个协议号')
    return
  }
  // 验证题只对「已进入群」有效；不要用待审批列表当目标
  const targets =
    targetsOverride && targetsOverride.length > 0
      ? targetsOverride
      : parseLines(targetsText.value)
  if (targets.length === 0) {
    emit('log', '请填写已加入的目标群用户名/链接后再拉验证题')
    return
  }
  if (manageRunning) {
    running.value = true
  }
  try {
    const result = await listJoinChallenges({
      accountId: primaryAccount.value,
      targets,
      limit: 40,
    })
    challenges.value = result.items ?? []
    emit('log', `验证题 ${challenges.value.length} 条 · ${result.note}`)
    for (const e of result.errors ?? []) {
      emit('log', `验证题拉取失败：${e}`)
    }
    for (const c of challenges.value) {
      emit(
        'log',
        `验证题 ${c.target} msg=${c.msgId} bot=${c.botName || c.botId || '?'} 按钮=${c.buttons.length}`,
      )
    }
  } catch (err) {
    emit('log', `拉取验证题失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    if (manageRunning) {
      running.value = false
    }
  }
}

/**
 * 按钮唯一键（用于 loading 态）。
 *
 * @param challenge 验证题
 * @param btn 按钮
 */
function challengeBtnKey(challenge: JoinChallenge, btn: ChallengeButton): string {
  return `${challenge.peerId}:${challenge.msgId}:${btn.kind}:${btn.data || btn.url || btn.text}`
}

/**
 * 合并机器人私信验证题到列表（按 peerId+msgId 去重，私信题置顶）。
 *
 * @param items 新验证题
 */
function mergeChallenges(items: JoinChallenge[]): void {
  if (!items?.length) {
    return
  }
  const map = new Map<string, JoinChallenge>()
  for (const c of items) {
    map.set(`${c.peerType || 'channel'}:${c.peerId}:${c.msgId}`, c)
  }
  for (const c of challenges.value) {
    const k = `${c.peerType || 'channel'}:${c.peerId}:${c.msgId}`
    if (!map.has(k)) {
      map.set(k, c)
    }
  }
  challenges.value = [...map.values()]
}

/**
 * 点击验证题按钮（callback / 协议号私信机器人 / 打开小程序或浏览器）。
 *
 * @param challenge 验证题
 * @param btn 按钮
 */
async function onAnswerChallenge(challenge: JoinChallenge, btn: ChallengeButton): Promise<void> {
  if (!primaryAccount.value) {
    return
  }
  if (btn.adminLikely) {
    emit('log', `跳过疑似管理按钮「${btn.text}」，请点验证/答案选项`)
    return
  }
  const key = challengeBtnKey(challenge, btn)
  answeringKey.value = key
  try {
    const kind = (btn.kind || 'callback').toLowerCase()
    if (kind === 'url' || kind === 'webview' || kind === 'simple_webview') {
      if (!btn.url) {
        emit('log', `按钮「${btn.text}」缺少 URL`)
        return
      }
      const result = await openChallengeWebView({
        accountId: primaryAccount.value,
        target: challenge.target,
        peerType: challenge.peerType || 'channel',
        peerId: challenge.peerId,
        accessHash: challenge.accessHash,
        msgId: challenge.msgId,
        botId: challenge.botId,
        botAccessHash: challenge.botAccessHash,
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
      // 机器人 deep link：协议号已 messages.startBot，合并私信验证题
      if (result.mode === 'bot_dm') {
        mergeChallenges(result.challenges ?? [])
        emit(
          'log',
          `✓ 「${btn.text}」→ 协议号已私信 @${result.botUsername || '?'}?start=${result.startParam || ''} · ${result.note || ''}`,
        )
        if (result.replyText) {
          emit('log', `机器人回复：${result.replyText.slice(0, 160)}`)
        }
        if (!(result.challenges?.length)) {
          emit('log', '暂无私信验证按钮，可稍后再点「在私信中验证」或检查机器人回复')
        }
        return
      }
      // 公开频道/群：协议号 JoinChannel
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
    const peerType = challenge.peerType || 'channel'
    const result = await answerJoinChallenge({
      accountId: primaryAccount.value,
      target: challenge.target,
      peerType,
      peerId: challenge.peerId,
      accessHash: challenge.accessHash,
      msgId: challenge.msgId,
      data: btn.data,
    })
    if (result.ok) {
      emit(
        'log',
        `✓ 已点「${btn.text}」→ ${challenge.target}${result.message ? ' · ' + result.message : ''}${result.alert ? '（弹窗）' : ''}`,
      )
      if (peerType === 'user') {
        // 私信验证题：从列表移除已点过的这条
        challenges.value = challenges.value.filter(
          (c) => !(c.peerId === challenge.peerId && c.msgId === challenge.msgId && (c.peerType || 'channel') === 'user'),
        )
      } else {
        await loadChallenges([challenge.target], false)
      }
    } else {
      emit(
        'log',
        `✗ 点击「${btn.text}」失败 [${result.businessResult}] ${result.errorMessage || ''}`,
      )
    }
  } catch (err) {
    emit('log', `点击验证失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    answeringKey.value = ''
  }
}

/**
 * 按行分组按钮，便于渲染键盘布局。
 *
 * @param challenge 验证题
 * @return 按 row 分组的按钮
 */
function buttonRows(challenge: JoinChallenge): ChallengeButton[][] {
  const map = new Map<number, ChallengeButton[]>()
  for (const b of challenge.buttons ?? []) {
    const row = map.get(b.row) ?? []
    row.push(b)
    map.set(b.row, row)
  }
  return [...map.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([, row]) => row.sort((a, b) => a.col - b.col))
}

/**
 * 检查待验证目标是否已入群。
 */
async function onCheckJoined(): Promise<void> {
  const targets = pendingVerify.value.length > 0 ? pendingVerify.value : parseLines(targetsText.value)
  if (!primaryAccount.value || targets.length === 0) {
    emit('log', '没有待检查目标：请先提交需审批的加群，或填写名单')
    return
  }
  running.value = true
  try {
    const result = await joinCheck({ accountId: primaryAccount.value, targets })
    const stillPending: string[] = []
    for (const p of result.items) {
      if (p.alreadyIn || p.status === 'already_in') {
        emit('log', `✓ 已入群：${p.target} ${p.title || ''}`)
      } else {
        stillPending.push(p.target)
        emit('log', `⏳ 未入群：${p.target} [${p.status}] ${p.note || ''}`)
      }
    }
    pendingVerify.value = stillPending
    emit('log', `检查完成：已入群 ${result.joined}/${result.total} · ${result.note}`)
    if (tab.value === 'joined') {
      await loadJoinedLocal()
    }
  } catch (err) {
    emit('log', `检查入群失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    running.value = false
  }
}

/**
 * 提交群组操作。
 */
async function onSubmit(): Promise<void> {
  if (!props.sidecarOk) {
    emit('log', '本地服务未连接')
    return
  }
  if (!props.gotdConfigured) {
    emit('log', '未配置 TG_APP_ID / TG_APP_HASH')
    return
  }
  if (props.selectedIds.length === 0) {
    emit('log', '请选择至少一个可用协议号')
    return
  }

  running.value = true
  try {
    if (isInvite.value) {
      await runInvite()
    } else {
      await runJoinLeave()
    }
  } finally {
    running.value = false
  }
}

/**
 * 执行加入 / 退出。
 */
async function runJoinLeave(): Promise<void> {
  const action = tab.value as GroupAction
  let targets: string[] = []
  if (action === 'leave') {
    // 从勾选的已加入群生成目标
    targets = leaveJoined.value
      .filter((g) => selectedLeave.value.has(g.peerId))
      .map((g) => leaveTargetOf(g))
    if (targets.length === 0) {
      emit('log', '请勾选要退出的群组')
      return
    }
  } else {
    targets = parseLines(targetsText.value)
    if (targets.length === 0) {
      emit('log', '请填写群组用户名或邀请链接')
      return
    }
  }
  const verb = action === 'join' ? '加入' : '退出'
  emit(
    'log',
    `开始${verb}：目标=${targets.length} 账号=${props.selectedIds.length} 间隔=${intervalMs.value}ms`,
  )
  try {
    const result = await groupBatch({
      accountIds: props.selectedIds,
      action,
      targets,
      intervalMs: intervalMs.value,
      failStopAfter: failStopAfterForApi(failStopAfter.value),
    })
    const stopHint = result.stopped ? ` · 已停止(${result.stopReason})` : ''
    emit(
      'log',
      `完成：成功 ${result.success} / 失败 ${result.failed}${stopHint}${result.taskId ? ' · task=' + result.taskId : ''} · ${result.note}`,
    )
    const pending = [...pendingVerify.value]
    for (const item of result.items) {
      if (item.businessResult === 'need_approval') {
        if (!pending.includes(item.target)) {
          pending.push(item.target)
        }
        emit('log', `⏳ ${item.target} ← ${item.accountId} [need_approval] 请到「手动验证」检查入群`)
      } else if (item.ok) {
        emit('log', `✓ ${item.target} ← ${item.accountId} [${item.businessResult}]`)
      } else {
        emit(
          'log',
          `✗ ${item.target} ← ${item.accountId} [${item.businessResult}] ${item.errorCode || ''} ${item.errorMessage || ''}`,
        )
      }
    }
    pendingVerify.value = pending
    // 退出成功后重读本地列表（缓存可能仍含已退群，需手动「刷新」与 TG 对齐）
    if (action === 'leave' && result.success > 0) {
      await loadLeaveJoinedLocal()
    }
  } catch (err) {
    emit('log', `${verb}异常：${err instanceof Error ? err.message : String(err)}`)
  }
}

/**
 * 执行邀请入群。
 */
async function runInvite(): Promise<void> {
  const mode: InviteMode = tab.value === 'invite_contact' ? 'contact' : 'username'
  if (!groupTarget.value.trim()) {
    emit('log', '请填写目标群用户名')
    return
  }
  const members = parseLines(membersText.value)
  if (members.length === 0) {
    emit('log', '请填写成员名单')
    return
  }
  const label = mode === 'contact' ? '邀请联系人' : '按用户名加成员'
  emit(
    'log',
    `开始${label}：群=${groupTarget.value} 成员=${members.length} 账号=${props.selectedIds.length}`,
  )
  try {
    const result = await inviteBatch({
      accountIds: props.selectedIds,
      group: groupTarget.value.trim(),
      members,
      mode,
      intervalMs: intervalMs.value,
      failStopAfter: failStopAfterForApi(failStopAfter.value),
    })
    const stopHint = result.stopped ? ` · 已停止(${result.stopReason})` : ''
    emit(
      'log',
      `完成：成功 ${result.success} / 失败 ${result.failed}${stopHint}${result.taskId ? ' · task=' + result.taskId : ''} · ${result.note}`,
    )
    for (const item of result.items) {
      if (item.ok) {
        emit('log', `✓ ${item.member} → ${item.group} ← ${item.accountId} [${item.businessResult}]`)
      } else {
        emit(
          'log',
          `✗ ${item.member} → ${item.group} ← ${item.accountId} [${item.businessResult}] ${item.errorCode || ''} ${item.errorMessage || ''}`,
        )
      }
    }
  } catch (err) {
    emit('log', `邀请异常：${err instanceof Error ? err.message : String(err)}`)
  }
}

/**
 * 状态中文。
 *
 * @param status 状态码
 */
function statusText(status: string): string {
  const map: Record<string, string> = {
    can_join: '可加入',
    need_approval: '需审批',
    already_in: '已在群',
    invite_expired: '链接失效',
    not_found: '未找到',
    not_joined: '未入群',
    failed: '失败',
  }
  return map[status] || status
}
</script>

<template>
  <div class="groups-panel">
    <div class="tabs">
      <button type="button" :class="{ active: tab === 'joined' }" @click="tab = 'joined'">
        已加入
      </button>
      <button type="button" :class="{ active: tab === 'manual' }" @click="tab = 'manual'">
        手动验证
      </button>
      <button type="button" :class="{ active: tab === 'join' }" @click="tab = 'join'">
        批量加入
      </button>
      <button type="button" :class="{ active: tab === 'leave' }" @click="tab = 'leave'">
        批量退出
      </button>
      <button
        type="button"
        :class="{ active: tab === 'invite_contact' }"
        @click="tab = 'invite_contact'"
      >
        邀请联系人
      </button>
      <button
        type="button"
        :class="{ active: tab === 'invite_username' }"
        @click="tab = 'invite_username'"
      >
        按用户名加成员
      </button>
    </div>

    <!-- 已加入群组 -->
    <template v-if="tab === 'joined'">
      <p class="hint">
        按协议号缓存在本地 SQLite；仅点「刷新已加入」才请求 Telegram。待审批会标「申请中」。
      </p>
      <p class="hint">{{ joinedSyncedLabel }}</p>
      <div class="actions">
        <button
          type="button"
          class="ghost"
          :disabled="running || refreshingJoined || !sidecarOk || !primaryAccount"
          @click="loadJoinedLocal"
        >
          {{ running ? '读取中…' : '读本地' }}
        </button>
        <button
          type="button"
          class="primary"
          :disabled="running || refreshingJoined || !sidecarOk || !gotdConfigured || !primaryAccount"
          @click="refreshJoinedFromTG"
        >
          {{ refreshingJoined ? '同步中…' : '刷新已加入' }}
        </button>
        <span class="meta-inline">账号 {{ primaryAccount || '未选' }} · {{ joined.length }} 个</span>
      </div>
      <div v-if="joined.length === 0" class="empty">本地无缓存，点「刷新已加入」从 Telegram 拉取</div>
      <table v-else class="table">
        <thead>
          <tr>
            <th>标题</th>
            <th>类型</th>
            <th>用户名</th>
            <th>人数</th>
            <th>状态</th>
            <th>监控</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="g in joined" :key="g.peerId">
            <td>{{ g.title }}</td>
            <td>{{ g.kind }}</td>
            <td>{{ g.username ? '@' + g.username : '—' }}</td>
            <td>{{ g.participants || '—' }}</td>
            <td>
              <span v-if="g.joinRequestOut" class="tag warn">申请中</span>
              <span v-else-if="g.isCreator" class="tag">创建者</span>
              <span v-else-if="g.isAdmin" class="tag">管理</span>
              <span v-else class="tag ok">已加入</span>
            </td>
            <td>
              <span
                class="tag"
                :class="{
                  ok: monitorOf(g)?.status === 'monitoring',
                  warn:
                    monitorOf(g)?.status === 'backfilling' ||
                    monitorOf(g)?.status === 'error',
                }"
                :title="monitorOf(g)?.errorMessage || ''"
              >
                {{ monitorStatusLabel(g) }}
              </span>
            </td>
            <td class="ops">
              <button
                v-if="
                  !monitorOf(g) ||
                  monitorOf(g)?.status === 'stopped' ||
                  monitorOf(g)?.status === 'error'
                "
                type="button"
                class="ghost mini"
                :disabled="
                  running ||
                  monitorBusyPeer === g.peerId ||
                  !sidecarOk ||
                  !gotdConfigured ||
                  g.joinRequestOut
                "
                @click="openMonitorModal(g)"
              >
                开启监控
              </button>
              <button
                v-if="
                  monitorOf(g)?.status === 'monitoring' ||
                  monitorOf(g)?.status === 'backfilling'
                "
                type="button"
                class="ghost mini"
                :disabled="monitorBusyPeer === g.peerId"
                @click="onStopMonitor(g)"
              >
                停止
              </button>
              <button
                v-if="(monitorOf(g)?.msgCount || 0) > 0"
                type="button"
                class="ghost mini"
                :disabled="monitorBusyPeer === g.peerId || !sidecarOk"
                @click="onExportMonitor(g)"
              >
                导出
              </button>
            </td>
          </tr>
        </tbody>
      </table>

      <div
        v-if="monitorModalOpen"
        class="modal-mask"
        @click.self="monitorModalOpen = false"
      >
        <div class="modal-card">
          <h4>开启群监控</h4>
          <p class="hint">
            将主动拉取最近历史消息写入本地 SQLite，之后后台轮询新消息。群：
            <strong>{{ monitorTarget?.title || '—' }}</strong>
          </p>
          <div class="field">
            <label>拉取历史条数</label>
            <select v-model.number="monitorHistoryLimit">
              <option v-for="n in HISTORY_LIMIT_OPTIONS" :key="n" :value="n">{{ n }}</option>
            </select>
          </div>
          <div class="actions">
            <button type="button" class="ghost" @click="monitorModalOpen = false">取消</button>
            <button
              type="button"
              class="primary"
              :disabled="!monitorTarget || monitorBusyPeer === monitorTarget?.peerId"
              @click="confirmStartMonitor"
            >
              {{ monitorBusyPeer && monitorTarget ? '启动中…' : '确认开启' }}
            </button>
          </div>
        </div>
      </div>
    </template>

    <!-- 手动验证加群 -->
    <template v-else-if="tab === 'manual'">
      <p class="hint">
        两种验证不要混用：① 预检「需审批」= 管理员审批，确认后等通过，用「检查入群」；若报
        CHANNEL_PRIVATE 必须改用邀请链接 t.me/+hash。② 机器人算术/点选验证 = 必须先成功入群，再点「拉取验证题」点答案。
      </p>
      <div class="field">
        <label>目标名单（{{ targetCount }}）</label>
        <textarea
          v-model="targetsText"
          rows="6"
          placeholder="每行一个：@username 或 t.me/+邀请链接"
        />
      </div>
      <div class="actions">
        <button
          type="button"
          class="primary"
          :disabled="running || !sidecarOk || !primaryAccount"
          @click="onPreview"
        >
          1. 预检
        </button>
        <button
          type="button"
          class="primary"
          :disabled="running || selectedPreview.size === 0"
          @click="onConfirmJoin"
        >
          2. 确认加入（{{ selectedPreview.size }}）
        </button>
        <button type="button" class="ghost" :disabled="running" @click="onCheckJoined">
          3. 检查入群（{{ pendingVerify.length }}）
        </button>
        <button
          type="button"
          class="primary"
          :disabled="running || !sidecarOk || !primaryAccount"
          @click="loadChallenges()"
        >
          4. 拉取验证题（{{ challenges.length }}）
        </button>
      </div>

      <div v-if="previews.length" class="preview-list">
        <h4>预检结果（已勾选 {{ selectedPreview.size }}）</h4>
        <p
          v-if="previews.some((p) => p.status === 'not_found')"
          class="hint warn-hint"
        >
          仍显示「未找到」时请再点一次「1. 预检」（需审批群应变为「需审批」后即可勾选）。
        </p>
        <label
          v-for="p in previews"
          :key="p.target"
          class="preview-row"
          :class="{ disabled: !canSelectPreview(p) }"
        >
          <input
            type="checkbox"
            :checked="selectedPreview.has(p.target)"
            :disabled="!canSelectPreview(p)"
            @change="togglePreview(p.target)"
          />
          <div>
            <div class="title">
              {{ p.title || p.target }}
              <span
                class="tag"
                :class="{
                  warn: p.requestNeeded || p.status === 'need_approval',
                  ok: p.alreadyIn || p.status === 'can_join',
                }"
              >
                {{ statusText(p.status) }}
              </span>
            </div>
            <div class="sub">{{ p.target }} · {{ p.note || p.errorMessage || '' }}</div>
          </div>
        </label>
      </div>

      <div v-if="pendingVerify.length" class="pending-box">
        <h4>待审批入群（{{ pendingVerify.length }}）</h4>
        <ul>
          <li v-for="t in pendingVerify" :key="t">{{ t }}</li>
        </ul>
      </div>

      <div v-if="challenges.length" class="challenge-box">
        <h4>入群验证（{{ challenges.length }}）</h4>
        <p class="hint">
          支持：算术点选、机器人私信验证（如 nmBot「在私信中验证」）、浏览器链接、小程序。点「在私信中验证」会用当前协议号带 start 参数私聊机器人，不会打开本机 Telegram；灰色为管理按钮已禁用。
        </p>
        <div
          v-for="c in challenges"
          :key="(c.peerType || 'channel') + '-' + c.peerId + '-' + c.msgId"
          class="challenge-card"
        >
          <div class="challenge-meta">
            <strong>{{ c.botName || '验证机器人' }}</strong>
            <span class="tag warn">{{ c.kind }}</span>
            <span v-if="c.peerType === 'user'" class="tag ok">私信</span>
            <span class="sub">{{ c.target }} · msg {{ c.msgId }}</span>
          </div>
          <pre class="challenge-text">{{ c.text || '（无文字，请直接点下方按钮）' }}</pre>
          <div class="challenge-keyboard">
            <div v-for="(row, ri) in buttonRows(c)" :key="ri" class="challenge-row">
              <button
                v-for="btn in row"
                :key="(btn.data || btn.url || btn.text) + '-' + btn.col"
                type="button"
                class="challenge-btn"
                :class="{
                  admin: btn.adminLikely,
                  web: btn.kind === 'webview' || btn.kind === 'simple_webview',
                  link: btn.kind === 'url',
                }"
                :disabled="running || answeringKey !== '' || btn.adminLikely"
                :title="btn.adminLikely ? '疑似管理按钮，已禁用' : btn.kind + (btn.url ? ' · ' + btn.url : '')"
                @click="onAnswerChallenge(c, btn)"
              >
                {{ answeringKey === challengeBtnKey(c, btn) ? '…' : btn.text }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </template>

    <!-- 批量退出：勾选已加入群 -->
    <template v-else-if="tab === 'leave'">
      <p class="hint">
        默认读本地缓存勾选退出；与 Telegram 对齐请点「刷新」。「读本地」不打 TG。
      </p>
      <p class="hint">{{ joinedSyncedLabel }}</p>
      <div class="actions">
        <button
          type="button"
          class="ghost"
          :disabled="running || refreshingJoined || !sidecarOk || !primaryAccount"
          @click="loadLeaveJoinedLocal"
        >
          {{ running ? '读取中…' : '读本地' }}
        </button>
        <button
          type="button"
          class="primary"
          :disabled="running || refreshingJoined || !sidecarOk || !gotdConfigured || !primaryAccount"
          @click="refreshJoinedFromTG"
        >
          {{ refreshingJoined ? '同步中…' : '刷新' }}
        </button>
        <button type="button" class="ghost" :disabled="leaveJoined.length === 0" @click="selectAllLeave">
          {{
            leaveSelectedCount === leaveJoined.length && leaveJoined.length > 0
              ? '取消全选'
              : '全选'
          }}
        </button>
        <button type="button" class="ghost" :disabled="leaveJoined.length === 0" @click="invertLeave">
          反选
        </button>
        <span class="meta-inline">
          账号 {{ primaryAccount || '未选' }} · 已选 {{ leaveSelectedCount }} / {{ leaveJoined.length }}
        </span>
      </div>

      <div v-if="leaveJoined.length === 0" class="empty">暂无已加入群，请先刷新或确认协议号已入群</div>
      <div v-else class="leave-list">
        <label v-for="g in leaveJoined" :key="g.peerId" class="preview-row">
          <input
            type="checkbox"
            :checked="selectedLeave.has(g.peerId)"
            @change="toggleLeave(g.peerId)"
          />
          <div>
            <div class="title">
              {{ g.title }}
              <span class="tag">{{ g.kind }}</span>
              <span v-if="g.isCreator" class="tag">创建者</span>
              <span v-else-if="g.isAdmin" class="tag">管理</span>
            </div>
            <div class="sub">
              {{ g.username ? '@' + g.username : leaveTargetOf(g) }}
              <template v-if="g.participants"> · {{ g.participants }} 人</template>
            </div>
          </div>
        </label>
      </div>

      <div class="row-fields">
        <div class="field">
          <label>操作间隔（毫秒）</label>
          <input v-model.number="intervalMs" type="number" min="500" step="100" />
          <p class="hint">本地限速，非 Telegram 官方额度</p>
        </div>
        <div class="field">
          <label>连续失败熔断</label>
          <input v-model.number="failStopAfter" type="number" min="0" step="1" />
          <p class="hint">0=关闭</p>
        </div>
        <div class="field meta">
          <label>已选执行账号</label>
          <strong :class="{ warn: selectedIds.length === 0 }">{{ selectedIds.length }}</strong>
        </div>
      </div>

      <div class="actions">
        <button
          type="button"
          class="primary"
          :disabled="running || !sidecarOk || selectedIds.length === 0 || leaveSelectedCount === 0"
          @click="onSubmit"
        >
          {{ running ? '执行中…' : `批量退出（${leaveSelectedCount}）` }}
        </button>
      </div>
    </template>

    <!-- 批量加入 / 邀请 -->
    <template v-else>
      <template v-if="!isInvite">
        <div class="field">
          <label>目标（{{ targetCount }}）</label>
          <textarea
            v-model="targetsText"
            rows="8"
            placeholder="每行一个：@username、t.me/xxx 或邀请链接 t.me/+hash&#10;需审批结果请到「手动验证」检查是否真正入群"
          />
        </div>
      </template>
      <template v-else>
        <div class="field">
          <label>目标群（公开用户名）</label>
          <input v-model="groupTarget" type="text" placeholder="@mygroup 或 t.me/mygroup" />
        </div>
        <div class="field">
          <label>成员名单（{{ memberCount }}）</label>
          <textarea
            v-if="tab === 'invite_contact'"
            v-model="membersText"
            rows="7"
            placeholder="每行一个已保存联系人：手机号 / @username / 用户 ID"
          />
          <textarea
            v-else
            v-model="membersText"
            rows="7"
            placeholder="每行一个公开用户名 @user"
          />
        </div>
      </template>

      <div class="row-fields">
        <div class="field">
          <label>操作间隔（毫秒）</label>
          <input v-model.number="intervalMs" type="number" min="500" step="100" />
          <p class="hint">本地限速，非 Telegram 官方额度</p>
        </div>
        <div class="field">
          <label>连续失败熔断</label>
          <input v-model.number="failStopAfter" type="number" min="0" step="1" />
          <p class="hint">0=关闭</p>
        </div>
        <div class="field meta">
          <label>已选执行账号</label>
          <strong :class="{ warn: selectedIds.length === 0 }">{{ selectedIds.length }}</strong>
        </div>
      </div>

      <div class="actions">
        <button
          type="button"
          class="primary"
          :disabled="running || !sidecarOk || selectedIds.length === 0"
          @click="onSubmit"
        >
          {{
            running
              ? '执行中…'
              : tab === 'join'
                ? '开始加入'
                : '开始邀请'
          }}
        </button>
        <button
          v-if="tab === 'join' && pendingVerify.length"
          type="button"
          class="ghost"
          @click="tab = 'manual'"
        >
          去手动验证（{{ pendingVerify.length }}）
        </button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.groups-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  border-bottom: 1px solid var(--color-border);
  padding-bottom: 8px;
}

.tabs button {
  border: none;
  background: transparent;
  padding: 8px 12px;
  font: inherit;
  cursor: pointer;
  color: var(--color-text-secondary);
  border-radius: 4px 4px 0 0;
  font-size: 13px;
}

.tabs button.active {
  color: var(--color-primary);
  background: var(--color-bg-selected);
  font-weight: 600;
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
}

.meta strong {
  font-size: 22px;
}

.meta strong.warn {
  color: var(--color-danger);
}

.hint {
  margin: 0;
  font-size: 12px;
  color: var(--color-text-secondary);
  line-height: 1.5;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
}

.meta-inline {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.primary,
.ghost {
  border: none;
  border-radius: 4px;
  padding: 10px 18px;
  font: inherit;
  cursor: pointer;
}

.primary {
  background: var(--color-primary);
  color: #fff;
}

.ghost {
  background: var(--color-bg-sidebar);
  color: var(--color-text);
}

.primary:disabled,
.ghost:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.empty {
  padding: 24px;
  text-align: center;
  color: var(--color-text-secondary);
  font-size: 13px;
}

.table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}

.table th,
.table td {
  border: 1px solid var(--color-border);
  padding: 6px 8px;
  text-align: left;
}

.table th {
  background: var(--color-bg-sidebar);
}

.tag {
  display: inline-block;
  padding: 1px 6px;
  border-radius: 3px;
  font-size: 11px;
  background: #eee;
  margin-left: 4px;
}

.tag.ok {
  background: #e6f6ea;
  color: #1a7f37;
}

.tag.warn {
  background: #fff4e0;
  color: #a15c00;
}

.preview-list h4,
.pending-box h4 {
  margin: 0 0 8px;
  font-size: 13px;
}

.preview-row {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  padding: 8px;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  margin-bottom: 6px;
  cursor: pointer;
}

.preview-row.disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.hint.warn-hint {
  color: #a15c00;
  background: #fff4e0;
  border-radius: 4px;
  padding: 6px 8px;
  margin-bottom: 8px;
}

.challenge-box {
  border: 1px solid var(--color-border);
  border-radius: 6px;
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.challenge-box h4 {
  margin: 0;
  font-size: 13px;
}

.challenge-card {
  border: 1px solid var(--color-border);
  border-radius: 6px;
  padding: 10px;
  background: var(--color-bg-sidebar);
}

.challenge-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
  font-size: 12px;
  margin-bottom: 6px;
}

.challenge-text {
  margin: 0 0 10px;
  white-space: pre-wrap;
  word-break: break-word;
  font: inherit;
  font-size: 13px;
  line-height: 1.45;
}

.challenge-keyboard {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.challenge-row {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.challenge-btn {
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 8px 12px;
  font: inherit;
  font-size: 13px;
  cursor: pointer;
  background: #fff;
  color: var(--color-text);
  min-width: 40px;
}

.challenge-btn:hover:not(:disabled) {
  border-color: var(--color-primary);
  color: var(--color-primary);
}

.challenge-btn.admin {
  opacity: 0.45;
  cursor: not-allowed;
  background: #f3f3f3;
}

.challenge-btn.web {
  background: #e8f1ff;
  border-color: #8eb6e8;
  color: #1a5fb4;
  font-weight: 600;
}

.challenge-btn.link {
  background: #eef8f0;
  border-color: #8ec99a;
  color: #1a7f37;
  font-weight: 600;
}

.challenge-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.preview-row .title {
  font-size: 13px;
}

.preview-row .sub {
  margin-top: 2px;
  font-size: 12px;
  color: var(--color-text-secondary);
  word-break: break-all;
}

.pending-box {
  border: 1px dashed #e0b060;
  background: #fffaf0;
  border-radius: 6px;
  padding: 10px 12px;
}

.pending-box ul {
  margin: 0;
  padding-left: 18px;
  font-size: 12px;
}

.leave-list {
  max-height: 360px;
  overflow: auto;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  padding: 6px;
}

.ops {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.mini {
  padding: 4px 8px;
  font-size: 12px;
}

.modal-mask {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.35);
  display: grid;
  place-items: center;
  z-index: 40;
}

.modal-card {
  width: min(420px, 92vw);
  background: #fff;
  border-radius: 8px;
  padding: 18px 20px;
  border: 1px solid var(--color-border);
  box-shadow: 0 8px 28px rgba(0, 0, 0, 0.12);
}

.modal-card h4 {
  margin: 0 0 8px;
  font-size: 16px;
}

.modal-card .field {
  margin: 12px 0;
}

.modal-card select {
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 8px 10px;
  font: inherit;
  width: 100%;
}

.modal-card .actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 8px;
}
</style>
