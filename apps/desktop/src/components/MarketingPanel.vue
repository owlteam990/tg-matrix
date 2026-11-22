<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  createMarketingSendJob,
  deleteMarketingProduct,
  listJoinedGroups,
  listMarketingLeads,
  listMarketingProducts,
  listMarketingSendJobs,
  listMarketingSendPolicies,
  marketingCollect,
  patchMarketingLead,
  refreshJoinedGroups,
  upsertMarketingProduct,
  type JoinedGroup,
  type MarketingLead,
  type MarketingProduct,
  type MarketingSendJob,
  type MarketingSendPolicy,
} from '../api/sidecar'
import { loadSettings } from '../lib/settings'

const props = defineProps<{
  selectedIds: string[]
  gotdConfigured: boolean
  sidecarOk: boolean
}>()

const emit = defineEmits<{
  log: [line: string]
}>()

const tab = ref<'products' | 'campaign'>('products')
const products = ref<MarketingProduct[]>([])
const leads = ref<MarketingLead[]>([])
const running = ref(false)

const form = ref({
  id: '',
  name: '',
  sellingPoints: '',
  audience: '',
  avoidAudience: '',
  offer: '',
  landingUrl: '',
  tone: '口语短句',
  bannedWords: '',
  cta: '',
  status: 'active',
})

const campaignProductId = ref('')
const joinedGroups = ref<JoinedGroup[]>([])
const selectedPeerId = ref<number | null>(null)
const groupTarget = ref('')
const groupTitle = ref('')
const historyLimit = ref(200)
const statusFilter = ref('')
/** 有选中目标群时，默认只看该群线索 */
const onlyCurrentGroup = ref(true)
const selectedLeadIds = ref<string[]>([])
const leadsRefreshing = ref(false)

/** 发送策略选用与队列（策略 CRUD 在设置页） */
const sendPolicies = ref<MarketingSendPolicy[]>([])
const sendJobs = ref<MarketingSendJob[]>([])
const selectedPolicyId = ref('')
const sendPoolIds = ref<string[]>([])
const enqueueBusy = ref(false)
let jobsPollTimer: ReturnType<typeof setInterval> | null = null

/** 话术生成结果弹窗 */
const resultModal = ref<{
  title: string
  summary: string
  items: { name: string; detail: string; ok: boolean }[]
  credits: number
} | null>(null)

const primaryAccount = computed(() => props.selectedIds[0] || '')
/** 号池候选项：中栏勾选 ∪ 已选入池 */
const uniquePoolCandidates = computed(() => {
  const set = new Set<string>([...props.selectedIds, ...sendPoolIds.value])
  return [...set]
})

const filteredLeads = computed(() => {
  let list = leads.value
  // 1. 按当前目标群过滤（避免其它群旧线索混在一起，误以为刷新无效）
  if (onlyCurrentGroup.value && groupTarget.value) {
    const gt = groupTarget.value
    list = list.filter((l) => l.groupTarget === gt)
  }
  if (statusFilter.value) {
    list = list.filter((l) => l.status === statusFilter.value)
  }
  return list
})

/** WebView 本地缓存键（SQLite 落库失败时仍可展示线索字段） */
function leadCacheStorageKey(productId: string): string {
  return `tg-matrix:mkt-lead-ai:${productId}`
}

/**
 * 读取产品下线索本地缓存。
 *
 * @param productId 产品 ID
 */
function readLeadAiCache(productId: string): Record<string, Partial<MarketingLead>> {
  try {
    const raw = localStorage.getItem(leadCacheStorageKey(productId))
    if (!raw) {
      return {}
    }
    const parsed = JSON.parse(raw) as Record<string, Partial<MarketingLead>>
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

/**
 * 写入单条线索字段到 localStorage。
 *
 * @param productId 产品 ID
 * @param lead 线索（取适配/话术字段）
 */
function writeLeadAiCache(productId: string, lead: MarketingLead): void {
  if (!hasFitResult(lead) && !lead.scriptA && !lead.scriptB && !lead.scriptChosen) {
    return
  }
  try {
    const all = readLeadAiCache(productId)
    all[lead.id] = pickFitFields(lead)
    localStorage.setItem(leadCacheStorageKey(productId), JSON.stringify(all))
  } catch {
    // 忽略配额等错误
  }
}

/**
 * 用缓存补齐服务端缺失的适配/话术字段。
 *
 * @param productId 产品 ID
 * @param items 线索列表
 */
function mergeLeadsWithAiCache(
  productId: string,
  items: MarketingLead[],
): MarketingLead[] {
  const cache = readLeadAiCache(productId)
  return items.map((row) => {
    const cached = cache[row.id]
    if (!cached) {
      return row
    }
    if (
      (hasFitResult(cached as MarketingLead) && !hasFitResult(row)) ||
      (!!cached.scriptA && !row.scriptA) ||
      (!!cached.scriptB && !row.scriptB) ||
      (!!cached.scriptChosen && !row.scriptChosen)
    ) {
      return { ...row, ...cached, digest: row.digest }
    }
    return row
  })
}

/**
 * 是否已有适配备注可展示。
 *
 * @param l 线索
 */
function hasFitResult(l: MarketingLead): boolean {
  return !!(l.fitLabel || l.fitScore > 0 || l.fitReasons || l.riskFlags)
}

/**
 * 适配分样式档位。
 *
 * @param score 分数
 * @param label 标签
 */
function fitTier(score: number, label: string): string {
  if (label.includes('不适合') || score < 30) {
    return 'bad'
  }
  if (label.includes('高') || score >= 70) {
    return 'good'
  }
  if (label.includes('中') || score >= 50) {
    return 'mid'
  }
  return 'low'
}

/**
 * 关闭结果弹窗。
 */
function closeResultModal(): void {
  resultModal.value = null
}

/**
 * 加载产品列表。
 */
async function loadProducts(): Promise<void> {
  if (!props.sidecarOk) {
    return
  }
  try {
    products.value = await listMarketingProducts()
    if (!campaignProductId.value && products.value.length) {
      campaignProductId.value = products.value[0].id
    }
  } catch (err) {
    emit('log', `加载产品失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

/**
 * 恢复中断的 analyzing/scripting（页面重进时无内存任务）。
 *
 * @param items 线索
 */
function recoverInterruptedLeads(items: MarketingLead[]): MarketingLead[] {
  return items.map((l) => {
    if (l.status === 'analyzing') {
      return {
        ...l,
        status: hasFitResult(l) ? 'analyzed' : 'pending',
        errorMessage: hasFitResult(l)
          ? ''
          : l.errorMessage || '上次分析中断，可手动填写话术后审核',
      }
    }
    if (l.status === 'scripting') {
      return {
        ...l,
        status: l.scriptA || l.scriptB ? 'scripted' : 'analyzed',
        errorMessage:
          l.scriptA || l.scriptB
            ? ''
            : l.errorMessage || '上次话术生成中断，可手动填写话术后审核',
      }
    }
    return l
  })
}

/**
 * 从本地库加载当前产品线索（内部用，可静默）。
 *
 * @param silent 为 true 时不写成功日志
 */
async function loadLeads(silent = true): Promise<void> {
  if (!campaignProductId.value) {
    leads.value = []
    if (!silent) {
      emit('log', '请先选择产品后再刷新线索')
    }
    return
  }
  if (!props.sidecarOk) {
    leads.value = []
    if (!silent) {
      emit('log', '本地服务未连接，无法刷新线索')
    }
    return
  }
  try {
    const prevSelected = new Set(selectedLeadIds.value)
    const prevById = new Map(leads.value.map((l) => [l.id, l]))
    const items = await listMarketingLeads(campaignProductId.value)
    // 合并：内存 > localStorage 缓存 > SQLite（落库失败时仍能展示）
    const withCache = mergeLeadsWithAiCache(campaignProductId.value, items)
    const merged = withCache.map((row) => {
      const local = prevById.get(row.id)
      if (!local) {
        return row
      }
      if (local.status === 'analyzing' || local.status === 'scripting') {
        return { ...row, ...local, digest: row.digest || local.digest }
      }
      if (
        (hasFitResult(local) && !hasFitResult(row)) ||
        (!!local.scriptA && !row.scriptA) ||
        (!!local.scriptB && !row.scriptB)
      ) {
        return { ...row, ...pickFitFields(local), digest: row.digest || local.digest }
      }
      return row
    })
    const recovered = recoverInterruptedLeads(merged)
    leads.value = recovered
    for (const after of recovered) {
      if (hasFitResult(after) || after.scriptA || after.scriptChosen) {
        writeLeadAiCache(campaignProductId.value, after)
      }
    }
    // 把内存/缓存里更完整的结果尽量写回 SQLite（只读库时静默跳过）
    for (const after of recovered) {
      const before = items.find((x) => x.id === after.id)
      if (
        before &&
        (hasFitResult(after) && !hasFitResult(before)
          ? true
          : before.status !== after.status ||
            (!!after.scriptA && !before.scriptA) ||
            (!!after.scriptChosen && !before.scriptChosen))
      ) {
        void patchMarketingLead(after.id, pickFitFields(after)).catch(() => {
          /* 只读库时忽略 */
        })
      }
    }
    selectedLeadIds.value = leads.value.map((l) => l.id).filter((id) => prevSelected.has(id))
    if (!silent) {
      const scope =
        onlyCurrentGroup.value && groupTarget.value
          ? `，当前群可见 ${filteredLeads.value.length} 条`
          : ''
      const analyzedN = leads.value.filter((l) => hasFitResult(l)).length
      emit(
        'log',
        `线索已刷新：本地共 ${leads.value.length} 条（已分析 ${analyzedN}${scope}）`,
      )
    }
  } catch (err) {
    emit('log', `加载线索失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

/**
 * 取出适配结果字段（合并用）。
 *
 * @param l 线索
 */
function pickFitFields(l: MarketingLead): Partial<MarketingLead> {
  return {
    fitScore: l.fitScore,
    fitLabel: l.fitLabel,
    fitReasons: l.fitReasons,
    riskFlags: l.riskFlags,
    status: l.status,
    cloudCallIds: l.cloudCallIds,
    scriptA: l.scriptA,
    scriptB: l.scriptB,
    scriptChosen: l.scriptChosen,
    errorMessage: l.errorMessage,
  }
}

/**
 * 用户点击「刷新线索」：强制重读并写日志。
 */
async function refreshLeads(): Promise<void> {
  if (leadsRefreshing.value) {
    return
  }
  leadsRefreshing.value = true
  try {
    await loadLeads(false)
  } finally {
    leadsRefreshing.value = false
  }
}

/**
 * 重置产品表单为新建。
 */
function resetForm(): void {
  form.value = {
    id: '',
    name: '',
    sellingPoints: '',
    audience: '',
    avoidAudience: '',
    offer: '',
    landingUrl: '',
    tone: '口语短句',
    bannedWords: '',
    cta: '',
    status: 'active',
  }
}

/**
 * 编辑已有产品。
 *
 * @param p 产品
 */
function editProduct(p: MarketingProduct): void {
  form.value = {
    id: p.id,
    name: p.name,
    sellingPoints: p.sellingPoints,
    audience: p.audience,
    avoidAudience: p.avoidAudience,
    offer: p.offer,
    landingUrl: p.landingUrl,
    tone: p.tone || '口语短句',
    bannedWords: p.bannedWords,
    cta: p.cta,
    status: p.status || 'active',
  }
  tab.value = 'products'
}

/**
 * 保存产品。
 */
async function saveProduct(): Promise<void> {
  if (!form.value.name.trim()) {
    emit('log', '请填写产品名称')
    return
  }
  running.value = true
  try {
    const saved = await upsertMarketingProduct({
      id: form.value.id || undefined,
      name: form.value.name.trim(),
      sellingPoints: form.value.sellingPoints,
      audience: form.value.audience,
      avoidAudience: form.value.avoidAudience,
      offer: form.value.offer,
      landingUrl: form.value.landingUrl,
      tone: form.value.tone,
      bannedWords: form.value.bannedWords,
      cta: form.value.cta,
      status: form.value.status,
    })
    emit('log', `产品已保存：${saved.name}`)
    resetForm()
    await loadProducts()
  } catch (err) {
    emit('log', `保存产品失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    running.value = false
  }
}

/**
 * 删除产品。
 *
 * @param id 产品 ID
 */
async function removeProduct(id: string): Promise<void> {
  if (!confirm('删除该产品及其全部线索？')) {
    return
  }
  running.value = true
  try {
    await deleteMarketingProduct(id)
    emit('log', '产品已删除')
    if (campaignProductId.value === id) {
      campaignProductId.value = ''
    }
    await loadProducts()
    await loadLeads()
  } catch (err) {
    emit('log', `删除失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    running.value = false
  }
}

/**
 * 已加入群目标串。
 *
 * @param g 群
 */
/**
 * 已加入群 → 采集目标串（与群组页 leaveTarget 一致：基础群用 chat:id）。
 *
 * @param g 已加入群
 */
function scrapeTargetOf(g: JoinedGroup): string {
  if (g.username) {
    return `@${g.username}`
  }
  // 普通基础群无 accessHash，必须用 chat:，否则会 CHANNEL_INVALID
  if (g.kind === 'group') {
    return `chat:${g.peerId}`
  }
  return `peer:${g.peerId}:${g.accessHash ?? 0}`
}

/**
 * 与「群组」页一致：仅排除申请中，保留 megagroup / channel / group。
 *
 * @param items 原始列表
 * @return 可选目标群
 */
function applyJoinedGroups(items: JoinedGroup[]): void {
  joinedGroups.value = (items ?? []).filter((g) => !g.joinRequestOut)
  if (
    selectedPeerId.value != null &&
    !joinedGroups.value.some((g) => g.peerId === selectedPeerId.value)
  ) {
    selectedPeerId.value = null
    groupTarget.value = ''
    groupTitle.value = ''
  }
}

/**
 * 下拉展示文案（与群组页信息对齐）。
 *
 * @param g 已加入群
 */
function groupOptionLabel(g: JoinedGroup): string {
  const name = g.title || (g.username ? `@${g.username}` : `id:${g.peerId}`)
  const uname = g.username ? ` @${g.username}` : '（无公开用户名）'
  return `${name}${uname} · ${g.kind || '?'}`
}

/**
 * 加载本地已加入群。
 */
async function loadJoined(): Promise<void> {
  if (!props.sidecarOk || !primaryAccount.value) {
    emit('log', '请先勾选协议号')
    return
  }
  running.value = true
  try {
    const result = await listJoinedGroups(primaryAccount.value)
    applyJoinedGroups(result.items ?? [])
    emit(
      'log',
      `营销目标群 ${joinedGroups.value.length} 个（账号 ${primaryAccount.value}，与「群组→已加入」同源）`,
    )
  } catch (err) {
    emit('log', `读取已加入群失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    running.value = false
  }
}

/**
 * 从 Telegram 刷新已加入群。
 */
async function refreshJoined(): Promise<void> {
  if (!props.gotdConfigured || !primaryAccount.value) {
    emit('log', '未配置 gotd 或未选协议号')
    return
  }
  running.value = true
  try {
    const result = await refreshJoinedGroups(primaryAccount.value, 200)
    applyJoinedGroups(result.items ?? [])
    emit('log', `已从 TG 刷新群 ${joinedGroups.value.length} 个（账号 ${primaryAccount.value}）`)
  } catch (err) {
    emit('log', `刷新群失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    running.value = false
  }
}

/**
 * 选择群。
 *
 * @param peerId peer
 */
function onSelectGroup(peerId: number | null): void {
  selectedPeerId.value = peerId
  if (peerId == null) {
    groupTarget.value = ''
    groupTitle.value = ''
    return
  }
  const g = joinedGroups.value.find((x) => x.peerId === peerId)
  if (!g) {
    return
  }
  groupTarget.value = scrapeTargetOf(g)
  groupTitle.value = g.title || g.username || String(g.peerId)
}

/**
 * 采集发言并写入线索。
 */
async function collectSpeakers(): Promise<void> {
  if (!campaignProductId.value || !primaryAccount.value || !groupTarget.value) {
    emit('log', '请选择产品、协议号与群')
    return
  }
  if (!props.gotdConfigured) {
    emit('log', '未配置 TG_APP_ID / TG_APP_HASH')
    return
  }
  running.value = true
  try {
    const result = await marketingCollect({
      productId: campaignProductId.value,
      accountId: primaryAccount.value,
      group: groupTarget.value,
      groupTitle: groupTitle.value,
      historyLimit: historyLimit.value,
    })
    if (!result.ok) {
      emit('log', `采集失败：${result.errorMessage || result.errorCode || 'unknown'}`)
      return
    }
    emit('log', `已导入线索 ${result.imported}（发言者 ${result.speakersCount}）`)
    await loadLeads()
  } catch (err) {
    emit('log', `采集失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    running.value = false
  }
}

/**
 * 切换线索勾选。
 *
 * @param id 线索 ID
 * @param checked 是否选中
 */
function toggleLead(id: string, checked: boolean): void {
  if (checked) {
    if (!selectedLeadIds.value.includes(id)) {
      selectedLeadIds.value = [...selectedLeadIds.value, id]
    }
  } else {
    selectedLeadIds.value = selectedLeadIds.value.filter((x) => x !== id)
  }
}

const allFilteredSelected = computed(() => {
  const ids = filteredLeads.value.map((l) => l.id)
  return ids.length > 0 && ids.every((id) => selectedLeadIds.value.includes(id))
})

/**
 * 全选当前筛选列表中的线索。
 */
function selectAllLeads(): void {
  selectedLeadIds.value = filteredLeads.value.map((l) => l.id)
}

/**
 * 清空当前勾选。
 */
function clearLeadSelection(): void {
  selectedLeadIds.value = []
}

/**
 * 反选当前筛选列表。
 */
function invertLeadSelection(): void {
  const set = new Set(selectedLeadIds.value)
  selectedLeadIds.value = filteredLeads.value
    .map((l) => l.id)
    .filter((id) => !set.has(id))
}

/**
 * 将发言摘要拆成可读行（按采集侧 " | " 分隔）。
 *
 * @param digest 摘要原文
 * @return 片段列表
 */
function digestLines(digest: string): string[] {
  const raw = (digest || '').trim()
  if (!raw) {
    return []
  }
  return raw
    .split(/\s*\|\s*/)
    .map((s) => s.trim())
    .filter(Boolean)
}

/**
 * 选用话术并标记审核通过。
 *
 * @param lead 线索
 * @param which A|B|custom
 */
async function approveLead(lead: MarketingLead, which: 'A' | 'B' | 'chosen'): Promise<void> {
  const text =
    which === 'A' ? lead.scriptA : which === 'B' ? lead.scriptB : lead.scriptChosen
  if (!text.trim()) {
    emit('log', '话术为空，请先填写「最终发送文案」')
    return
  }
  try {
    await patchMarketingLead(lead.id, {
      scriptChosen: text,
      status: 'approved',
      sendAccountId: lead.sendAccountId || lead.accountId || primaryAccount.value,
    })
    emit('log', `已审核通过 ${lead.username || lead.userId}`)
    await loadLeads()
  } catch (err) {
    emit('log', `审核失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

/**
 * 跳过线索。
 *
 * @param lead 线索
 */
async function skipLead(lead: MarketingLead): Promise<void> {
  try {
    await patchMarketingLead(lead.id, { status: 'skipped' })
    await loadLeads()
  } catch (err) {
    emit('log', `跳过失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

/**
 * 更新选用话术文本。
 *
 * @param lead 线索
 * @param text 文本
 */
async function saveChosenScript(lead: MarketingLead, text: string): Promise<void> {
  const next = String(text || '')
  leads.value = leads.value.map((row) =>
    row.id === lead.id ? { ...row, scriptChosen: next } : row,
  )
  try {
    await patchMarketingLead(lead.id, { scriptChosen: next })
  } catch (err) {
    emit('log', `保存话术失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

/**
 * 加载发送策略列表。
 */
async function loadSendPolicies(): Promise<void> {
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
 * 加载当前产品的发送任务。
 */
async function loadSendJobs(silent = true): Promise<void> {
  if (!props.sidecarOk || !campaignProductId.value) {
    sendJobs.value = []
    return
  }
  try {
    sendJobs.value = await listMarketingSendJobs(campaignProductId.value)
    if (!silent) {
      emit('log', `发送任务 ${sendJobs.value.length} 个`)
    }
  } catch (err) {
    emit('log', `加载发送任务失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

/**
 * 有 running 任务时轮询进度。
 */
function ensureJobsPolling(): void {
  const need = sendJobs.value.some((j) => j.status === 'running')
  if (need && !jobsPollTimer) {
    jobsPollTimer = setInterval(() => {
      void (async () => {
        await loadSendJobs(true)
        await loadLeads(true)
        if (!sendJobs.value.some((j) => j.status === 'running')) {
          stopJobsPolling()
        }
      })()
    }, 3000)
  }
  if (!need) {
    stopJobsPolling()
  }
}

/**
 * 停止任务轮询。
 */
function stopJobsPolling(): void {
  if (jobsPollTimer) {
    clearInterval(jobsPollTimer)
    jobsPollTimer = null
  }
}

/**
 * 号池与中栏勾选同步：进入活动页时默认用中栏已选。
 */
function syncPoolFromSelected(): void {
  sendPoolIds.value = [...props.selectedIds]
}

/**
 * 切换号池中的协议号。
 *
 * @param id 协议号
 * @param on 是否选中
 */
function togglePoolAccount(id: string, on: boolean): void {
  if (on) {
    if (!sendPoolIds.value.includes(id)) {
      sendPoolIds.value = [...sendPoolIds.value, id]
    }
  } else {
    sendPoolIds.value = sendPoolIds.value.filter((x) => x !== id)
  }
}

/**
 * 已审核线索加入发送队列（Sidecar 后台按策略调度）。
 */
async function enqueueApproved(): Promise<void> {
  if (!campaignProductId.value) {
    emit('log', '请先选择产品')
    return
  }
  if (!selectedPolicyId.value) {
    emit('log', '请先选择发送策略（可在「设置 → 发送策略」新建）')
    return
  }
  const pool = sendPoolIds.value.length ? sendPoolIds.value : [...props.selectedIds]
  if (!pool.length) {
    emit('log', '请勾选号池协议号（中栏勾选后点「同步中栏勾选」，或在号池中勾选）')
    return
  }
  const targets = leads.value.filter(
    (l) =>
      l.status === 'approved' &&
      l.scriptChosen.trim() &&
      (selectedLeadIds.value.length === 0 || selectedLeadIds.value.includes(l.id)),
  )
  if (!targets.length) {
    emit('log', '没有已审核且可选的发送项')
    return
  }
  if (
    !confirm(
      `将 ${targets.length} 条已审核线索加入发送队列？\n协议号 ${pool.length} 个，Sidecar 将按策略限速后台发送。`,
    )
  ) {
    return
  }
  enqueueBusy.value = true
  try {
    const settings = loadSettings()
    const result = await createMarketingSendJob({
      productId: campaignProductId.value,
      policyId: selectedPolicyId.value,
      leadIds: targets.map((l) => l.id),
      accountIds: pool,
      failStopAfter: settings.failStopAfter <= 0 ? -1 : settings.failStopAfter,
    })
    emit(
      'log',
      `已入队 ${result.enqueued} 条，任务 ${result.job.id.slice(0, 8)}… 状态 ${result.job.status}`,
    )
    await loadLeads()
    await loadSendJobs(true)
    ensureJobsPolling()
  } catch (err) {
    emit('log', `入队失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    enqueueBusy.value = false
  }
}

/**
 * 展示名。
 *
 * @param l 线索
 */
function leadLabel(l: MarketingLead): string {
  return l.displayName || (l.username ? `@${l.username}` : String(l.userId))
}

watch(campaignProductId, () => {
  void loadLeads()
  void loadSendJobs(true)
})

watch(
  () => props.sidecarOk,
  (ok) => {
    if (ok) {
      void loadProducts()
      void loadSendPolicies()
      void loadSendJobs(true)
    }
  },
)

watch(sendJobs, () => {
  ensureJobsPolling()
})

// 切换中栏协议号时，重新读该号本地已加入群，避免串号
watch(primaryAccount, (id, prev) => {
  if (!id || id === prev) {
    return
  }
  selectedPeerId.value = null
  groupTarget.value = ''
  groupTitle.value = ''
  if (props.sidecarOk && tab.value === 'campaign') {
    void loadJoined()
  }
})

watch(tab, (t) => {
  if (t === 'campaign' && props.sidecarOk) {
    if (primaryAccount.value) {
      void loadJoined()
    }
    void loadSendPolicies()
    void loadSendJobs(true)
    if (!sendPoolIds.value.length && props.selectedIds.length) {
      syncPoolFromSelected()
    }
  }
})

onMounted(async () => {
  await loadProducts()
  await loadSendPolicies()
  if (campaignProductId.value) {
    await loadLeads()
    await loadSendJobs(true)
  }
  if (tab.value === 'campaign' && props.sidecarOk && primaryAccount.value) {
    await loadJoined()
  }
  if (props.selectedIds.length) {
    syncPoolFromSelected()
  }
  ensureJobsPolling()
})

onUnmounted(() => {
  stopJobsPolling()
})
</script>

<template>
  <div class="mkt-panel">
    <div class="tabs">
      <button type="button" :class="{ active: tab === 'products' }" @click="tab = 'products'">
        产品库
      </button>
      <button type="button" :class="{ active: tab === 'campaign' }" @click="tab = 'campaign'">
        营销活动
      </button>
    </div>

    <template v-if="tab === 'products'">
      <section class="block">
        <h3>{{ form.id ? '编辑产品' : '新建产品' }}</h3>
        <div class="grid-2">
          <div class="field">
            <label>名称</label>
            <input v-model="form.name" type="text" placeholder="产品 / 活动名" />
          </div>
          <div class="field">
            <label>状态</label>
            <select v-model="form.status">
              <option value="draft">草稿</option>
              <option value="active">启用</option>
              <option value="disabled">停用</option>
            </select>
          </div>
        </div>
        <div class="field">
          <label>卖点</label>
          <textarea v-model="form.sellingPoints" rows="2" placeholder="核心利益点" />
        </div>
        <div class="grid-2">
          <div class="field">
            <label>适用人群</label>
            <textarea v-model="form.audience" rows="2" />
          </div>
          <div class="field">
            <label>不适合人群</label>
            <textarea v-model="form.avoidAudience" rows="2" />
          </div>
        </div>
        <div class="grid-2">
          <div class="field">
            <label>优惠 / 价格</label>
            <input v-model="form.offer" type="text" />
          </div>
          <div class="field">
            <label>落地链接</label>
            <input v-model="form.landingUrl" type="text" />
          </div>
        </div>
        <div class="grid-2">
          <div class="field">
            <label>语气</label>
            <input v-model="form.tone" type="text" />
          </div>
          <div class="field">
            <label>CTA</label>
            <input v-model="form.cta" type="text" placeholder="行动号召" />
          </div>
        </div>
        <div class="field">
          <label>禁用词（逗号分隔）</label>
          <input v-model="form.bannedWords" type="text" />
        </div>
        <div class="actions">
          <button type="button" class="primary" :disabled="running" @click="saveProduct">
            保存产品
          </button>
          <button type="button" class="ghost" :disabled="running" @click="resetForm">清空表单</button>
        </div>
      </section>

      <section class="block">
        <h3>已保存产品（{{ products.length }}）</h3>
        <div v-if="!products.length" class="hint">暂无产品，先新建一份内容。</div>
        <div v-else class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>名称</th>
                <th>状态</th>
                <th>卖点</th>
                <th />
              </tr>
            </thead>
            <tbody>
              <tr v-for="p in products" :key="p.id">
                <td>{{ p.name }}</td>
                <td>{{ p.status }}</td>
                <td class="clip">{{ p.sellingPoints }}</td>
                <td class="row-actions">
                  <button type="button" class="link" @click="editProduct(p)">编辑</button>
                  <button
                    type="button"
                    class="link"
                    @click="
                      campaignProductId = p.id;
                      tab = 'campaign'
                    "
                  >
                    做活动
                  </button>
                  <button type="button" class="link danger" @click="removeProduct(p.id)">删除</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </template>

    <template v-else>
      <section class="block">
        <h3>活动配置</h3>
        <p class="hint">
          流程：选产品 → 选协议号与群 → 采集发言 → 手动填写话术并审核 →
          按发送策略入队（后台限速发送）。
        </p>
        <div class="grid-2">
          <div class="field">
            <label>产品</label>
            <select v-model="campaignProductId">
              <option value="">— 选择 —</option>
              <option v-for="p in products" :key="p.id" :value="p.id">{{ p.name }}</option>
            </select>
          </div>
          <div class="field">
            <label>执行协议号</label>
            <input :value="primaryAccount || '请在中栏勾选'" type="text" disabled />
          </div>
        </div>
        <div class="field">
          <label>目标群</label>
          <div class="group-pick">
            <select
              :value="selectedPeerId ?? ''"
              @change="
                onSelectGroup(
                  ($event.target as HTMLSelectElement).value
                    ? Number(($event.target as HTMLSelectElement).value)
                    : null,
                )
              "
            >
              <option value="">— 选择已加入群（{{ joinedGroups.length }}）—</option>
              <option v-for="g in joinedGroups" :key="g.peerId" :value="g.peerId">
                {{ groupOptionLabel(g) }}
              </option>
            </select>
            <button type="button" class="ghost" :disabled="running" @click="loadJoined">本地</button>
            <button type="button" class="ghost" :disabled="running" @click="refreshJoined">
              刷新 TG
            </button>
          </div>
        </div>
        <div class="field">
          <label>历史消息条数</label>
          <input v-model.number="historyLimit" type="number" min="50" max="2000" step="50" />
        </div>
        <div class="actions">
          <button type="button" class="primary" :disabled="running" @click="collectSpeakers">
            采集发言写入线索
          </button>
          <button
            type="button"
            class="ghost"
            :disabled="leadsRefreshing || !campaignProductId"
            @click="refreshLeads"
          >
            {{ leadsRefreshing ? '刷新中…' : '刷新线索' }}
          </button>
        </div>
      </section>

      <section class="block">
        <h3>发送与号池</h3>
        <p class="hint">
          选用策略与消息群发共用；新建/编辑/删除请到「设置 → 发送策略」。
        </p>
        <div class="grid-2">
          <div class="field">
            <label>选用策略（入队时）</label>
            <select v-model="selectedPolicyId" @focus="loadSendPolicies">
              <option value="">— 选择 —</option>
              <option v-for="p in sendPolicies" :key="p.id" :value="p.id">
                {{ p.name }}（{{ p.windowMinutes }}分/{{ p.maxPerWindow }}条）
              </option>
            </select>
          </div>
          <div class="field">
            <label>策略管理</label>
            <p class="hint" style="margin: 8px 0 0">
              请在左侧打开「设置」维护策略列表。
            </p>
          </div>
        </div>

        <h4 class="sub-h">本批协议号池</h4>
        <p class="hint">入队时从号池轮询分配；默认同步中栏勾选。</p>
        <div class="actions">
          <button type="button" class="ghost" @click="syncPoolFromSelected">同步中栏勾选</button>
          <span class="meta-id">已选 {{ sendPoolIds.length }} 个</span>
        </div>
        <div v-if="!props.selectedIds.length && !sendPoolIds.length" class="hint">
          请先在中栏勾选协议号。
        </div>
        <div v-else class="pool-list">
          <label v-for="id in uniquePoolCandidates" :key="id" class="check">
            <input
              type="checkbox"
              :checked="sendPoolIds.includes(id)"
              @change="togglePoolAccount(id, ($event.target as HTMLInputElement).checked)"
            />
            <code>{{ id.slice(0, 12) }}…</code>
          </label>
        </div>
      </section>

      <section v-if="sendJobs.length" class="block queue-tip">
        <p class="hint">
          已有 {{ sendJobs.length }} 个发送任务
          <template v-if="sendJobs.some((j) => j.status === 'running' || j.status === 'paused')">
            （进行中
            {{
              sendJobs.filter((j) => j.status === 'running' || j.status === 'paused').length
            }}
            个）
          </template>
          。明细、改文案/删除请到左侧「队列」。
        </p>
      </section>

      <section class="block leads-block">
        <div class="actions head-actions">
          <h3>
            线索（{{ filteredLeads.length }}
            <template v-if="onlyCurrentGroup && groupTarget && filteredLeads.length !== leads.length">
              / 全部 {{ leads.length }}
            </template>
            ）
            <span v-if="selectedLeadIds.length" class="sel-count"
              >已选 {{ selectedLeadIds.length }}</span
            >
          </h3>
          <div class="head-filters">
            <label class="check">
              <input v-model="onlyCurrentGroup" type="checkbox" :disabled="!groupTarget" />
              仅当前群
            </label>
            <select v-model="statusFilter">
              <option value="">全部状态</option>
              <option value="pending">pending</option>
              <option value="analyzing">analyzing</option>
              <option value="analyzed">analyzed</option>
              <option value="scripting">scripting</option>
              <option value="scripted">scripted</option>
              <option value="approved">approved</option>
              <option value="queued">queued</option>
              <option value="sent">sent</option>
              <option value="skipped">skipped</option>
            </select>
          </div>
        </div>
        <div class="actions select-bar">
          <label class="check master-check">
            <input
              type="checkbox"
              :checked="allFilteredSelected"
              :disabled="!filteredLeads.length"
              @change="
                ($event.target as HTMLInputElement).checked
                  ? selectAllLeads()
                  : clearLeadSelection()
              "
            />
            全选当前列表
          </label>
          <button
            type="button"
            class="ghost"
            :disabled="!filteredLeads.length"
            @click="invertLeadSelection"
          >
            反选
          </button>
          <button
            type="button"
            class="ghost"
            :disabled="!selectedLeadIds.length"
            @click="clearLeadSelection"
          >
            清空勾选
          </button>
          <span class="bar-gap" />
          <button
            type="button"
            class="primary"
            :disabled="running || enqueueBusy"
            @click="enqueueApproved"
          >
            {{ enqueueBusy ? '入队中…' : '加入发送队列' }}
          </button>
        </div>
        <div v-if="!filteredLeads.length" class="hint">暂无线索，先采集发言。</div>
        <div v-else class="lead-list">
          <article v-for="l in filteredLeads" :key="l.id" class="lead-card">
            <header class="lead-head">
              <label class="check">
                <input
                  type="checkbox"
                  :checked="selectedLeadIds.includes(l.id)"
                  @change="
                    toggleLead(l.id, ($event.target as HTMLInputElement).checked)
                  "
                />
                <strong>{{ leadLabel(l) }}</strong>
              </label>
              <span class="badge" :class="{ pulse: l.status === 'analyzing' || l.status === 'scripting' }">
                {{ l.status }}
              </span>
              <span v-if="l.fitLabel" class="badge score">{{ l.fitLabel }} {{ l.fitScore }}</span>
              <span v-if="l.username" class="meta-id">@{{ l.username }}</span>
              <span class="meta-id">id:{{ l.userId }}</span>
            </header>
            <div class="digest" :class="{ empty: !l.digest }">
              <template v-if="digestLines(l.digest).length">
                <div v-for="(line, i) in digestLines(l.digest)" :key="i" class="digest-line">
                  {{ line }}
                </div>
              </template>
              <template v-else>（无发言摘要）</template>
            </div>
            <div
              v-if="hasFitResult(l)"
              class="fit-result"
              :class="fitTier(l.fitScore, l.fitLabel)"
            >
              <div class="fit-result-head">
                <span class="fit-title">适配备注</span>
                <span class="fit-score-pill">
                  {{ l.fitLabel || '—' }} · {{ l.fitScore }} 分
                </span>
              </div>
              <p v-if="l.fitReasons" class="fit-reasons">{{ l.fitReasons }}</p>
              <p v-if="l.riskFlags" class="fit-risk">风险：{{ l.riskFlags }}</p>
            </div>
            <div v-if="l.scriptA || l.scriptB" class="scripts">
              <div class="field">
                <label>话术 A</label>
                <textarea :value="l.scriptA" rows="2" readonly />
                <button type="button" class="link" @click="approveLead(l, 'A')">选用 A 并审核</button>
              </div>
              <div class="field">
                <label>话术 B</label>
                <textarea :value="l.scriptB" rows="2" readonly />
                <button type="button" class="link" @click="approveLead(l, 'B')">选用 B 并审核</button>
              </div>
            </div>
            <div class="field script-chosen">
              <label>最终发送文案（手动填写）</label>
              <textarea
                :value="l.scriptChosen"
                rows="3"
                placeholder="在此填写要发送的话术，保存后点「审核通过」"
                @change="
                  saveChosenScript(l, ($event.target as HTMLTextAreaElement).value)
                "
              />
            </div>
            <p v-if="l.errorMessage" class="meta-line warn">错误：{{ l.errorMessage }}</p>
            <div class="actions">
              <button type="button" class="primary" @click="approveLead(l, 'chosen')">
                审核通过
              </button>
              <button type="button" class="ghost" @click="skipLead(l)">跳过</button>
            </div>
          </article>
        </div>
      </section>
    </template>

    <div
      v-if="resultModal"
      class="modal-mask"
      role="dialog"
      aria-modal="true"
      @click.self="closeResultModal"
    >
      <div class="modal-card">
        <header class="modal-head">
          <h3>{{ resultModal.title }}</h3>
          <button type="button" class="modal-close" @click="closeResultModal">×</button>
        </header>
        <p class="modal-summary">{{ resultModal.summary }}</p>
        <ul v-if="resultModal.items.length" class="modal-list">
          <li
            v-for="(it, i) in resultModal.items"
            :key="i"
            :class="{ ok: it.ok, fail: !it.ok }"
          >
            <strong>{{ it.name }}</strong>
            <span>{{ it.detail }}</span>
          </li>
        </ul>
        <div class="modal-actions">
          <button type="button" class="primary" @click="closeResultModal">知道了</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.mkt-panel {
  display: flex;
  flex-direction: column;
  gap: 16px;
  width: 100%;
  min-width: 0;
}

.leads-block {
  width: 100%;
  min-width: 0;
}

.tabs {
  display: flex;
  gap: 4px;
  border-bottom: 1px solid var(--color-border);
  padding-bottom: 8px;
}

.tabs button {
  border: none;
  background: transparent;
  padding: 8px 14px;
  font: inherit;
  cursor: pointer;
  color: var(--color-text-secondary);
  border-radius: 4px 4px 0 0;
}

.tabs button.active {
  color: var(--color-primary);
  background: var(--color-bg-selected);
  font-weight: 600;
}

.block h3 {
  margin: 0 0 10px;
  font-size: 14px;
}

.grid-2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 10px;
}

.field label {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.field input,
.field textarea,
.field select {
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 8px 10px;
  font: inherit;
  background: #fff;
}

.group-pick {
  display: flex;
  gap: 8px;
}

.group-pick select {
  flex: 1;
}

.sub-h {
  margin: 16px 0 6px;
  font-size: 13px;
}

.policy-list,
.pool-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 12px;
  margin-top: 8px;
}

.job-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.job-card {
  border: 1px solid var(--color-border);
  border-radius: 6px;
  padding: 10px 12px;
}

.job-head {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  margin-bottom: 8px;
  font-size: 13px;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  margin-top: 4px;
}

.head-actions {
  justify-content: space-between;
  margin-bottom: 8px;
}

.head-actions h3 {
  margin: 0;
  display: flex;
  align-items: baseline;
  gap: 10px;
}

.head-filters {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
}

.sel-count {
  font-size: 12px;
  font-weight: 500;
  color: var(--color-primary);
}

.select-bar {
  margin-bottom: 10px;
}

.master-check {
  font-size: 13px;
  margin-right: 4px;
}

.bar-gap {
  flex: 0 0 8px;
  width: 1px;
  height: 20px;
  background: var(--color-border);
  margin: 0 4px;
}

.primary,
.ghost {
  border: none;
  border-radius: 4px;
  padding: 10px 16px;
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

.hint {
  margin: 0 0 10px;
  font-size: 12px;
  color: var(--color-text-secondary);
  line-height: 1.5;
}

.table-wrap {
  border: 1px solid var(--color-border);
  border-radius: 4px;
  overflow: auto;
  max-height: 240px;
}

table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}

th,
td {
  padding: 6px 8px;
  border-bottom: 1px solid var(--color-border);
  text-align: left;
}

th {
  background: var(--color-bg-sidebar);
  position: sticky;
  top: 0;
}

.clip {
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row-actions {
  white-space: nowrap;
}

.link {
  border: none;
  background: none;
  color: var(--color-primary);
  cursor: pointer;
  font: inherit;
  font-size: 12px;
  padding: 0 6px;
}

.link.danger {
  color: var(--color-danger);
}

.lead-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: 100%;
  max-height: min(70vh, 720px);
  overflow: auto;
}

.lead-card {
  border: 1px solid var(--color-border);
  border-radius: 6px;
  padding: 14px 16px;
  background: #fff;
  width: 100%;
  box-sizing: border-box;
  min-width: 0;
}

.lead-head {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  margin-bottom: 10px;
}

.check {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
}

.badge {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 999px;
  background: var(--color-bg-sidebar);
  color: var(--color-text-secondary);
}

.badge.score {
  background: var(--color-bg-selected);
  color: var(--color-primary);
}

.meta-id {
  font-size: 11px;
  color: var(--color-text-secondary);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.digest {
  margin: 0 0 10px;
  padding: 10px 12px;
  font-size: 13px;
  color: var(--color-text);
  line-height: 1.55;
  background: #f7f8fa;
  border-radius: 4px;
  width: 100%;
  box-sizing: border-box;
  word-break: break-word;
  overflow-wrap: anywhere;
}

.digest.empty {
  color: var(--color-text-secondary);
}

.digest-line {
  padding: 2px 0;
}

.digest-line + .digest-line {
  border-top: 1px dashed var(--color-border);
  margin-top: 4px;
  padding-top: 6px;
}

.fit-result {
  margin: 0 0 12px;
  padding: 12px 14px;
  border-radius: 6px;
  border: 1px solid var(--color-border);
  background: #f3faf5;
  width: 100%;
  box-sizing: border-box;
}

.fit-result.good {
  border-color: #8fd6a8;
  background: #f0faf3;
}

.fit-result.mid {
  border-color: #c9d4a8;
  background: #f7faf0;
}

.fit-result.low {
  border-color: #e0d3a8;
  background: #faf7ef;
}

.fit-result.bad {
  border-color: #e8b4b4;
  background: #fff5f5;
}

.fit-result-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}

.fit-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--color-text);
}

.fit-score-pill {
  font-size: 12px;
  font-weight: 600;
  padding: 3px 10px;
  border-radius: 999px;
  background: #fff;
  border: 1px solid currentColor;
  color: var(--color-primary);
}

.fit-result.bad .fit-score-pill {
  color: var(--color-danger);
}

.fit-reasons {
  margin: 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--color-text);
  word-break: break-word;
}

.fit-risk {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--color-danger);
  word-break: break-word;
}

.fit-placeholder {
  margin: 0 0 12px;
  padding: 8px 12px;
  font-size: 12px;
  color: var(--color-text-secondary);
  background: #fafafa;
  border-radius: 4px;
  border: 1px dashed var(--color-border);
}

.fit-placeholder.busy {
  border-style: solid;
  border-color: var(--color-primary);
  color: var(--color-primary);
  background: var(--color-bg-selected);
}

.ai-busy-hint {
  color: var(--color-primary);
}

.badge.pulse {
  animation: pulse-badge 1.2s ease-in-out infinite;
}

@keyframes pulse-badge {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.45;
  }
}

.meta-line {
  margin: 0 0 4px;
  font-size: 12px;
  color: var(--color-text-secondary);
  word-break: break-word;
}

.meta-line.warn {
  color: var(--color-danger);
}

.modal-mask {
  position: fixed;
  inset: 0;
  z-index: 80;
  background: rgba(20, 24, 28, 0.45);
  display: grid;
  place-items: center;
  padding: 24px;
}

.modal-card {
  width: min(560px, 100%);
  max-height: min(80vh, 640px);
  overflow: auto;
  background: #fff;
  border-radius: 10px;
  padding: 18px 20px 16px;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.18);
}

.modal-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}

.modal-head h3 {
  margin: 0;
  font-size: 16px;
}

.modal-close {
  border: none;
  background: transparent;
  font-size: 22px;
  line-height: 1;
  cursor: pointer;
  color: var(--color-text-secondary);
  padding: 0 4px;
}

.modal-summary {
  margin: 0 0 12px;
  font-size: 13px;
  line-height: 1.55;
  color: var(--color-text);
}

.modal-list {
  list-style: none;
  margin: 0 0 14px;
  padding: 0;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  max-height: 320px;
  overflow: auto;
}

.modal-list li {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--color-border);
  font-size: 12px;
}

.modal-list li:last-child {
  border-bottom: none;
}

.modal-list li.ok strong {
  color: var(--color-primary);
}

.modal-list li.fail strong {
  color: var(--color-danger);
}

.modal-list li span {
  color: var(--color-text-secondary);
  word-break: break-word;
  line-height: 1.45;
}

.modal-actions {
  display: flex;
  justify-content: flex-end;
}

.scripts {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  width: 100%;
}

.script-chosen {
  width: 100%;
}

.script-chosen textarea,
.scripts textarea {
  width: 100%;
  box-sizing: border-box;
  min-height: 64px;
  resize: vertical;
}

@media (max-width: 900px) {
  .scripts {
    grid-template-columns: 1fr;
  }
}
</style>
