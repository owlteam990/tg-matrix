<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  controlMarketingSendJob,
  deleteSendQueueItem,
  listSendQueue,
  patchSendQueueItem,
  suggestSendQueueReady,
  type Account,
  type SendQueueItemView,
} from '../api/sidecar'

const JOBS_HEIGHT_KEY = 'tgmatrix.sendQueue.jobsHeight.v1'
const JOBS_HEIGHT_MIN = 72
const JOBS_HEIGHT_DEFAULT = 120
const JOBS_HEIGHT_MAX = 360

const props = defineProps<{
  accounts: Account[]
  sidecarOk: boolean
}>()

const emit = defineEmits<{
  log: [line: string]
}>()

const items = ref<SendQueueItemView[]>([])
const loading = ref(false)
const sourceFilter = ref('')
const statusFilter = ref('')
const editingId = ref('')
const editText = ref('')
const editAccountId = ref('')
const editNotBeforeLocal = ref('')
const suggestHint = ref('')
const suggesting = ref(false)
const saving = ref(false)
const busyId = ref('')
const jobsHeight = ref(loadJobsHeight())
const jobsDragging = ref(false)
let pollTimer: ReturnType<typeof setInterval> | null = null
let jobsDragCleanup: (() => void) | null = null

/**
 * 限制任务批次区高度。
 *
 * @param px 原始高度
 */
function clampJobsHeight(px: number): number {
  const n = Math.round(Number(px) || JOBS_HEIGHT_DEFAULT)
  if (n < JOBS_HEIGHT_MIN) {
    return JOBS_HEIGHT_MIN
  }
  if (n > JOBS_HEIGHT_MAX) {
    return JOBS_HEIGHT_MAX
  }
  return n
}

/**
 * 读取已保存的任务批次高度。
 */
function loadJobsHeight(): number {
  try {
    const raw = localStorage.getItem(JOBS_HEIGHT_KEY)
    if (raw == null || raw === '') {
      return JOBS_HEIGHT_DEFAULT
    }
    return clampJobsHeight(Number(raw))
  } catch {
    return JOBS_HEIGHT_DEFAULT
  }
}

/**
 * 开始拖拽调整任务批次 / 发送明细分割比例。
 *
 * @param e 指针事件
 */
function onJobsSplitterDown(e: PointerEvent): void {
  e.preventDefault()
  if (jobsDragCleanup) {
    jobsDragCleanup()
  }
  jobsDragging.value = true
  const startY = e.clientY
  const startH = jobsHeight.value

  const onMove = (ev: PointerEvent): void => {
    // 分割线在批次下方：指针下移 → 批次变高，明细变矮
    jobsHeight.value = clampJobsHeight(startH + (ev.clientY - startY))
  }
  const onUp = (): void => {
    jobsDragging.value = false
    try {
      localStorage.setItem(JOBS_HEIGHT_KEY, String(jobsHeight.value))
    } catch {
      // ignore
    }
    window.removeEventListener('pointermove', onMove)
    window.removeEventListener('pointerup', onUp)
    window.removeEventListener('pointercancel', onUp)
    jobsDragCleanup = null
  }
  window.addEventListener('pointermove', onMove)
  window.addEventListener('pointerup', onUp)
  window.addEventListener('pointercancel', onUp)
  jobsDragCleanup = onUp
}

const accountMap = computed(() => {
  const m = new Map<string, Account>()
  for (const a of props.accounts) {
    m.set(a.id, a)
  }
  return m
})

/**
 * 协议号展示名。
 *
 * @param id 账号 ID
 */
function accountLabel(id: string): string {
  if (!id) {
    return ''
  }
  const a = accountMap.value.get(id)
  if (!a) {
    return id.length > 14 ? `${id.slice(0, 12)}…` : id
  }
  const name = a.label || a.username || a.phone || id
  return name
}

/**
 * 队列项协议号展示（未分配时显示号池）。
 *
 * @param it 队列项
 */
function itemAccountLabel(it: SendQueueItemView): string {
  if (it.accountId) {
    return accountLabel(it.accountId)
  }
  const pool = (it.accountIds || []).map((id) => accountLabel(id)).filter(Boolean)
  if (pool.length) {
    return `待分配（号池 ${pool.slice(0, 3).join('、')}${pool.length > 3 ? '…' : ''}）`
  }
  return '（未分配）'
}

/**
 * 状态中文标签。
 *
 * @param status 队列项状态
 */
function statusLabel(status: string): string {
  const map: Record<string, string> = {
    queued: '待发送',
    waiting: '等待中',
    sending: '发送中',
    sent: '已发送',
    failed: '失败',
    skipped: '已跳过',
    cancelled: '已取消',
  }
  return map[status] || status
}

/**
 * 来源标签。
 *
 * @param source bulk|marketing
 */
function sourceLabel(source: string): string {
  return source === 'bulk' ? '群发' : '营销'
}

/**
 * 是否可编辑/删除。
 *
 * @param status 队列项状态
 */
function isPending(status: string): boolean {
  return status === 'queued' || status === 'waiting'
}

/**
 * 发送错误中文展示（兼容历史英文 RPC 原文）。
 *
 * @param raw 原始错误
 */
function formatSendError(raw: string): string {
  const s = (raw || '').trim()
  if (!s) {
    return ''
  }
  if (s.includes('发送失败') || s.includes('发送暂缓')) {
    return s
  }
  const u = s.toUpperCase()
  if (u.includes('PEER_FLOOD')) {
    return '发送失败：该协议号触发 Telegram 防刷限制（PEER_FLOOD）。请勿对该目标重复再发；请换健康号或降低频率后再试。'
  }
  if (u.includes('FLOOD_WAIT')) {
    return '发送暂缓：触发限流（FLOOD_WAIT），请稍后再试，勿连续重发。'
  }
  if (u.includes('USER_IS_BLOCKED') || u.includes('YOU_BLOCKED_USER')) {
    return '发送失败：拉黑关系限制，请勿重复发送。'
  }
  if (u.includes('PRIVACY')) {
    return '发送失败：对方隐私设置限制私信，请勿重复发送。'
  }
  if (u.includes('DEACTIVATED')) {
    return '发送失败：对方账号已停用/注销，请勿重复发送。'
  }
  return s.startsWith('rpc') || u.includes('RPC ERROR') ? `发送失败：${s}` : s
}

/**
 * 格式化时间。
 *
 * @param ms 毫秒或秒时间戳
 */
function fmtTime(ms: number): string {
  if (!ms) {
    return '—'
  }
  const t = ms > 1e12 ? ms : ms * 1000
  try {
    return new Date(t).toLocaleString()
  } catch {
    return String(ms)
  }
}

/**
 * 毫秒 → datetime-local 值。
 *
 * @param ms 毫秒时间戳
 */
function toLocalInput(ms: number): string {
  if (!ms) {
    return ''
  }
  const t = ms > 1e12 ? ms : ms * 1000
  const d = new Date(t)
  if (Number.isNaN(d.getTime())) {
    return ''
  }
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/**
 * datetime-local → 毫秒。
 *
 * @param local 本地输入值
 */
function fromLocalInput(local: string): number {
  const s = local.trim()
  if (!s) {
    return 0
  }
  const t = new Date(s).getTime()
  return Number.isNaN(t) ? 0 : t
}

/**
 * 编辑时可选协议号列表（号池 + 全部账号去重）。
 *
 * @param it 队列项
 */
function accountOptionsFor(it: SendQueueItemView): { id: string; label: string }[] {
  const ids: string[] = []
  const push = (id: string) => {
    const x = id.trim()
    if (!x || ids.includes(x)) {
      return
    }
    ids.push(x)
  }
  for (const id of it.accountIds || []) {
    push(id)
  }
  if (it.accountId) {
    push(it.accountId)
  }
  for (const a of props.accounts) {
    push(a.id)
  }
  return ids.map((id) => ({ id, label: accountLabel(id) || id }))
}

/**
 * 加载队列。
 *
 * @param quiet 静默刷新
 */
async function load(quiet = false): Promise<void> {
  if (!props.sidecarOk) {
    items.value = []
    return
  }
  if (!quiet) {
    loading.value = true
  }
  try {
    items.value = await listSendQueue({
      source: sourceFilter.value || undefined,
      status: statusFilter.value || undefined,
      limit: 500,
    })
    if (!quiet) {
      emit('log', `发送队列 ${items.value.length} 条`)
    }
    syncPoll()
  } catch (err) {
    if (!quiet) {
      emit('log', `加载发送队列失败：${err instanceof Error ? err.message : String(err)}`)
    }
  } finally {
    loading.value = false
  }
}

/**
 * 有进行中任务时轮询。
 */
function syncPoll(): void {
  const need = items.value.some(
    (it) =>
      it.status === 'queued' ||
      it.status === 'waiting' ||
      it.status === 'sending' ||
      it.jobStatus === 'running' ||
      it.jobStatus === 'paused',
  )
  if (need && !pollTimer) {
    pollTimer = setInterval(() => {
      void load(true)
    }, 2500)
  } else if (!need && pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

/**
 * 开始编辑（文案 / 协议号 / 定时）。
 *
 * @param it 队列项
 */
function startEdit(it: SendQueueItemView): void {
  editingId.value = it.id
  editText.value = it.text
  editAccountId.value = it.accountId || (it.accountIds?.[0] ?? '')
  editNotBeforeLocal.value = toLocalInput(it.notBefore)
  suggestHint.value = ''
}

/**
 * 取消编辑。
 */
function cancelEdit(): void {
  editingId.value = ''
  editText.value = ''
  editAccountId.value = ''
  editNotBeforeLocal.value = ''
  suggestHint.value = ''
}

/**
 * 按策略计算下次可发时间并填入。
 *
 * @param id 队列项 ID
 */
async function onSuggestReady(id: string): Promise<void> {
  suggesting.value = true
  suggestHint.value = ''
  try {
    const sug = await suggestSendQueueReady(id, editAccountId.value || undefined)
    if (sug.accountId && !editAccountId.value) {
      editAccountId.value = sug.accountId
    }
    editNotBeforeLocal.value = toLocalInput(sug.notBefore)
    const when = fmtTime(sug.notBefore)
    suggestHint.value = sug.readyNow
      ? `按策略当前可发（${sug.policyName || '策略'}）`
      : `建议 ${when}${sug.reason ? ` · ${sug.reason}` : ''}`
    emit('log', `可发时间：${when}${sug.reason ? `（${sug.reason}）` : ''}`)
  } catch (err) {
    emit('log', `计算可发时间失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    suggesting.value = false
  }
}

/**
 * 保存编辑（文案 / 协议号 / 定时）。
 *
 * @param id 队列项 ID
 */
async function saveEdit(id: string): Promise<void> {
  const text = editText.value.trim()
  if (!text) {
    emit('log', '文案不能为空')
    return
  }
  if (!editAccountId.value) {
    emit('log', '请选择发送协议号')
    return
  }
  const notBefore = fromLocalInput(editNotBeforeLocal.value)
  saving.value = true
  try {
    const res = await patchSendQueueItem(id, {
      text,
      accountId: editAccountId.value,
      notBefore,
    })
    const when = fmtTime(res.item.notBefore)
    emit(
      'log',
      `已更新 ${id.slice(0, 8)}… → ${accountLabel(res.item.accountId) || res.item.accountId}，计划 ${when}`,
    )
    cancelEdit()
    await load(true)
  } catch (err) {
    emit('log', `修改失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    saving.value = false
  }
}

/**
 * 一键：按策略算时间并保存（改号 + 定时）。
 *
 * @param it 队列项
 */
async function onAutoSchedule(it: SendQueueItemView): Promise<void> {
  const accountId = editAccountId.value || it.accountId || ''
  busyId.value = it.id
  try {
    const res = await patchSendQueueItem(it.id, {
      accountId: accountId || undefined,
      autoSchedule: true,
    })
    const when = fmtTime(res.item.notBefore)
    emit(
      'log',
      `已定时 ${it.id.slice(0, 8)}… → ${accountLabel(res.item.accountId) || res.item.accountId || '号池'}，${when}${
        res.suggestReason ? `（${res.suggestReason}）` : ''
      }`,
    )
    if (editingId.value === it.id) {
      editAccountId.value = res.item.accountId
      editNotBeforeLocal.value = toLocalInput(res.item.notBefore)
      suggestHint.value = res.suggestReason || '已按策略写入计划时间'
    }
    await load(true)
  } catch (err) {
    emit('log', `定时失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    busyId.value = ''
  }
}

/**
 * 删除待发送项。
 *
 * @param it 队列项
 */
async function onDelete(it: SendQueueItemView): Promise<void> {
  if (!confirm(`删除待发送项？\n目标：${it.target}\n文案：${it.text.slice(0, 80)}`)) {
    return
  }
  busyId.value = it.id
  try {
    await deleteSendQueueItem(it.id)
    emit('log', `已删除队列项 ${it.id.slice(0, 8)}…`)
    if (editingId.value === it.id) {
      cancelEdit()
    }
    await load(true)
  } catch (err) {
    emit('log', `删除失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    busyId.value = ''
  }
}

/**
 * 任务级暂停/继续/取消。
 *
 * @param jobId 任务 ID
 * @param action 操作
 */
async function onJobControl(
  jobId: string,
  action: 'pause' | 'resume' | 'cancel',
): Promise<void> {
  if (action === 'cancel' && !confirm('取消该任务下所有未完成发送？')) {
    return
  }
  busyId.value = jobId
  try {
    const job = await controlMarketingSendJob(jobId, action)
    emit('log', `任务 ${jobId.slice(0, 8)}… → ${job.status}`)
    await load(true)
  } catch (err) {
    emit('log', `任务操作失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    busyId.value = ''
  }
}

/** 按任务聚合，便于展示任务级按钮 */
const jobSummaries = computed(() => {
  const map = new Map<
    string,
    { jobId: string; source: string; jobStatus: string; total: number; pending: number }
  >()
  for (const it of items.value) {
    let row = map.get(it.jobId)
    if (!row) {
      row = {
        jobId: it.jobId,
        source: it.source,
        jobStatus: it.jobStatus,
        total: 0,
        pending: 0,
      }
      map.set(it.jobId, row)
    }
    row.total += 1
    if (isPending(it.status) || it.status === 'sending') {
      row.pending += 1
    }
    row.jobStatus = it.jobStatus
  }
  return [...map.values()]
})

watch([sourceFilter, statusFilter], () => {
  void load(false)
})

watch(
  () => props.sidecarOk,
  (ok) => {
    if (ok) {
      void load(false)
    }
  },
)

onMounted(() => {
  void load(false)
})

onUnmounted(() => {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
  if (jobsDragCleanup) {
    jobsDragCleanup()
  }
})
</script>

<template>
  <div class="sq-panel" :class="{ dragging: jobsDragging }">
    <div class="toolbar">
      <label class="field">
        <span>来源</span>
        <select v-model="sourceFilter">
          <option value="">全部</option>
          <option value="marketing">营销</option>
          <option value="bulk">群发</option>
        </select>
      </label>
      <label class="field">
        <span>状态</span>
        <select v-model="statusFilter">
          <option value="">全部</option>
          <option value="queued">待发送</option>
          <option value="waiting">等待中</option>
          <option value="sending">发送中</option>
          <option value="sent">已发送</option>
          <option value="failed">失败</option>
          <option value="cancelled">已取消</option>
          <option value="skipped">已跳过</option>
        </select>
      </label>
      <button type="button" class="ghost" :disabled="loading || !sidecarOk" @click="load(false)">
        {{ loading ? '加载中…' : '刷新' }}
      </button>
      <span class="count">共 {{ items.length }} 条</span>
    </div>

    <section
      v-if="jobSummaries.length"
      class="jobs"
      :style="{ height: `${jobsHeight}px` }"
    >
      <h3>任务批次</h3>
      <div class="job-list">
        <article v-for="j in jobSummaries" :key="j.jobId" class="job-card">
          <header>
            <span class="badge src">{{ sourceLabel(j.source) }}</span>
            <span class="badge">{{ j.jobStatus }}</span>
            <code>{{ j.jobId.slice(0, 10) }}…</code>
            <span class="meta">{{ j.pending }}/{{ j.total }} 未完成</span>
          </header>
          <div class="actions">
            <button
              v-if="j.jobStatus === 'running'"
              type="button"
              class="ghost mini"
              :disabled="busyId === j.jobId"
              @click="onJobControl(j.jobId, 'pause')"
            >
              暂停
            </button>
            <button
              v-if="j.jobStatus === 'paused'"
              type="button"
              class="primary mini"
              :disabled="busyId === j.jobId"
              @click="onJobControl(j.jobId, 'resume')"
            >
              继续
            </button>
            <button
              v-if="j.jobStatus === 'running' || j.jobStatus === 'paused'"
              type="button"
              class="ghost mini"
              :disabled="busyId === j.jobId"
              @click="onJobControl(j.jobId, 'cancel')"
            >
              取消任务
            </button>
          </div>
        </article>
      </div>
    </section>

    <div
      v-if="jobSummaries.length"
      class="sq-splitter"
      :class="{ active: jobsDragging }"
      role="separator"
      aria-orientation="horizontal"
      aria-label="拖拽调整任务批次与发送明细高度"
      title="上下拖动调整区域高度"
      @pointerdown="onJobsSplitterDown"
    />

    <section class="items">
      <h3>发送明细</h3>
      <p v-if="!sidecarOk" class="hint">Sidecar 未连接</p>
      <p v-else-if="!loading && !items.length" class="hint">暂无队列项。请在「营销」或「消息 → 群发」入队。</p>
      <div v-else class="item-list">
        <article v-for="it in items" :key="it.id" class="item-card" :class="`st-${it.status}`">
          <header class="item-head">
            <span class="badge src">{{ sourceLabel(it.source) }}</span>
            <span class="badge" :class="it.status">{{ statusLabel(it.status) }}</span>
            <span class="meta">协议号：{{ itemAccountLabel(it) }}</span>
            <span class="meta">目标：{{ it.targetMode }} · {{ it.target }}</span>
          </header>
          <div v-if="editingId === it.id" class="edit-box">
            <textarea v-model="editText" rows="3" />
            <div class="edit-grid">
              <label class="field">
                <span>发送协议号</span>
                <select v-model="editAccountId">
                  <option value="">请选择</option>
                  <option
                    v-for="opt in accountOptionsFor(it)"
                    :key="opt.id"
                    :value="opt.id"
                  >
                    {{ opt.label }}
                  </option>
                </select>
              </label>
              <label class="field">
                <span>计划发送时间</span>
                <input v-model="editNotBeforeLocal" type="datetime-local" />
              </label>
            </div>
            <p v-if="suggestHint" class="hint suggest">{{ suggestHint }}</p>
            <div class="actions">
              <button
                type="button"
                class="ghost mini"
                :disabled="suggesting || saving"
                @click="onSuggestReady(it.id)"
              >
                {{ suggesting ? '计算中…' : '计算下次可发' }}
              </button>
              <button
                type="button"
                class="ghost mini"
                :disabled="busyId === it.id || saving"
                @click="onAutoSchedule(it)"
              >
                按策略定时
              </button>
              <button
                type="button"
                class="primary mini"
                :disabled="saving"
                @click="saveEdit(it.id)"
              >
                {{ saving ? '保存中…' : '保存' }}
              </button>
              <button type="button" class="ghost mini" :disabled="saving" @click="cancelEdit">
                取消
              </button>
            </div>
          </div>
          <p v-else class="text">{{ it.text || '（无文案）' }}</p>
          <footer class="item-foot">
            <span class="meta">计划：{{ fmtTime(it.notBefore) }}</span>
            <span v-if="it.sentAt" class="meta">已发：{{ fmtTime(it.sentAt) }}</span>
            <span v-if="it.lastError" class="err">{{ formatSendError(it.lastError) }}</span>
            <div class="actions grow">
              <button
                v-if="isPending(it.status) && editingId !== it.id"
                type="button"
                class="ghost mini"
                @click="startEdit(it)"
              >
                编辑/定时
              </button>
              <button
                v-if="isPending(it.status) && editingId !== it.id"
                type="button"
                class="ghost mini"
                :disabled="busyId === it.id"
                @click="onAutoSchedule(it)"
              >
                按策略定时
              </button>
              <button
                v-if="isPending(it.status)"
                type="button"
                class="ghost mini danger"
                :disabled="busyId === it.id"
                @click="onDelete(it)"
              >
                删除
              </button>
            </div>
          </footer>
        </article>
      </div>
    </section>
  </div>
</template>

<style scoped>
.sq-panel {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-height: 0;
  height: 100%;
  overflow: hidden;
}

.sq-panel.dragging {
  user-select: none;
  cursor: row-resize;
}

.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 10px;
  flex: 0 0 auto;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12px;
  color: var(--color-text-muted, #666);
}

.field select {
  min-width: 120px;
}

.count {
  margin-left: auto;
  font-size: 12px;
  color: var(--color-text-muted, #666);
}

.jobs,
.items {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-height: 0;
}

.jobs {
  flex: 0 0 auto;
  overflow: hidden;
}

.items {
  flex: 1 1 auto;
  overflow: hidden;
  min-height: 120px;
}

.items h3,
.jobs h3 {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--color-text-secondary, #555);
  flex: 0 0 auto;
}

.sq-splitter {
  flex: 0 0 6px;
  height: 6px;
  margin: 0;
  cursor: row-resize;
  touch-action: none;
  position: relative;
  background: var(--color-border, #e5e5e5);
  border-radius: 3px;
  transition: background 0.12s ease;
}

.sq-splitter::after {
  content: '';
  position: absolute;
  left: 50%;
  top: 50%;
  width: 36px;
  height: 3px;
  margin: -1.5px 0 0 -18px;
  border-radius: 2px;
  background: #c5c9d0;
}

.sq-splitter:hover,
.sq-splitter.active {
  background: var(--color-bg-selected, #e8f5e9);
}

.sq-splitter:hover::after,
.sq-splitter.active::after {
  background: var(--color-primary, #2e7d32);
}

.job-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
}

.job-card,
.item-card {
  border: 1px solid var(--color-border, #e5e5e5);
  border-radius: 8px;
  padding: 10px 12px;
  background: var(--color-bg-panel, #fff);
}

.job-card header,
.item-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}

.item-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  overflow: auto;
  flex: 1;
  min-height: 0;
  padding-bottom: 8px;
}

.badge {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 999px;
  background: #eee;
  font-size: 11px;
}

.badge.src {
  background: #e8f5e9;
  color: #2e7d32;
}

.badge.queued,
.badge.waiting {
  background: #fff3e0;
  color: #e65100;
}

.badge.sending {
  background: #e3f2fd;
  color: #1565c0;
}

.badge.sent {
  background: #e8f5e9;
  color: #2e7d32;
}

.badge.failed {
  background: #ffebee;
  color: #c62828;
}

.badge.cancelled,
.badge.skipped {
  background: #f5f5f5;
  color: #757575;
}

.meta {
  color: var(--color-text-muted, #666);
}

.text {
  margin: 8px 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 13px;
  line-height: 1.45;
}

.edit-box {
  margin: 8px 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px;
  border-radius: 8px;
  background: var(--color-bg-muted, #f7f8fa);
  border: 1px solid var(--color-border, #e5e5e5);
}

.edit-box textarea {
  width: 100%;
  resize: vertical;
  font: inherit;
  padding: 8px;
  border-radius: 6px;
  border: 1px solid var(--color-border, #ccc);
  box-sizing: border-box;
}

.edit-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
}

@media (max-width: 720px) {
  .edit-grid {
    grid-template-columns: 1fr;
  }
}

.edit-box .field select,
.edit-box .field input {
  min-width: 0;
  width: 100%;
}

.hint.suggest {
  color: #1565c0;
}

.item-foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}

.actions.grow {
  margin-left: auto;
}

.mini {
  font-size: 12px;
  padding: 4px 10px;
}

.danger {
  color: #c62828;
}

.err {
  color: #c62828;
  max-width: 100%;
  word-break: break-word;
}

.hint {
  margin: 0;
  color: var(--color-text-muted, #666);
  font-size: 13px;
}
</style>
