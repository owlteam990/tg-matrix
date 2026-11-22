<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import {
  abandonTask,
  cancelTask,
  fetchTaskItems,
  fetchTasks,
  resumeTask,
  type TaskItemRow,
  type TaskSummary,
} from '../api/sidecar'
import { saveCsv } from '../lib/saveCsv'

const emit = defineEmits<{
  log: [line: string]
}>()

const tasks = ref<TaskSummary[]>([])
const items = ref<TaskItemRow[]>([])
const selectedId = ref('')
const loading = ref(false)
const cancelling = ref(false)
const resuming = ref(false)
const abandoning = ref(false)
let pollTimer: ReturnType<typeof setInterval> | null = null

/**
 * 刷新任务列表。
 */
async function refresh(): Promise<void> {
  loading.value = true
  try {
    tasks.value = await fetchTasks()
    emit('log', `已加载任务 ${tasks.value.length} 条`)
    syncPoll()
  } catch (err) {
    emit('log', `加载任务失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    loading.value = false
  }
}

/**
 * 有 running / waiting 时自动轮询。
 */
function syncPoll(): void {
  const needPoll = tasks.value.some(
    (t) => t.running || t.status === 'running' || t.status === 'waiting' || t.status === 'pending',
  )
  if (needPoll && !pollTimer) {
    pollTimer = setInterval(() => {
      void refreshQuiet()
    }, 1000)
  } else if (!needPoll && pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

/**
 * 静默刷新（轮询用）。
 */
async function refreshQuiet(): Promise<void> {
  try {
    tasks.value = await fetchTasks()
    if (selectedId.value) {
      items.value = await fetchTaskItems(selectedId.value)
    }
    syncPoll()
  } catch {
    // ignore poll errors
  }
}

/**
 * 查看任务明细。
 *
 * @param id 任务 ID
 */
async function openTask(id: string): Promise<void> {
  selectedId.value = id
  try {
    items.value = await fetchTaskItems(id)
  } catch (err) {
    emit('log', `加载明细失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

/**
 * 取消选中或指定的运行中 / 等待中任务。
 *
 * @param id 任务 ID
 */
async function onCancel(id: string): Promise<void> {
  cancelling.value = true
  try {
    const result = await cancelTask(id)
    emit('log', `已请求取消任务 ${id} → ${result.status}`)
    await refresh()
    if (selectedId.value === id) {
      items.value = await fetchTaskItems(id)
    }
  } catch (err) {
    emit('log', `取消失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    cancelling.value = false
  }
}

/**
 * 从断点续跑。
 *
 * @param id 任务 ID
 */
async function onResume(id: string): Promise<void> {
  resuming.value = true
  try {
    const result = await resumeTask(id)
    emit('log', `已继续任务 ${id} → ${result.status}`)
    await refresh()
    if (selectedId.value === id) {
      items.value = await fetchTaskItems(id)
    }
  } catch (err) {
    emit('log', `续跑失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    resuming.value = false
  }
}

/**
 * 放弃剩余 pending。
 *
 * @param id 任务 ID
 */
async function onAbandon(id: string): Promise<void> {
  abandoning.value = true
  try {
    const result = await abandonTask(id)
    emit('log', `已放弃任务 ${id} → ${result.status}`)
    await refresh()
    if (selectedId.value === id) {
      items.value = await fetchTaskItems(id)
    }
  } catch (err) {
    emit('log', `放弃失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    abandoning.value = false
  }
}

/**
 * 状态展示文案。
 *
 * @param t 任务
 */
function statusLabel(t: TaskSummary): string {
  if (t.status === 'waiting') {
    const sec = t.waitRemainingSec ?? 0
    return `waiting ${sec}s${t.waitReason ? ' · ' + t.waitReason : ''}`
  }
  if (t.running) {
    return 'running'
  }
  return t.status
}

/**
 * 进度文案。
 *
 * @param t 任务
 */
function progressLabel(t: TaskSummary): string {
  const total = t.totalCount || t.itemCount || 0
  const done = t.cursorIndex ?? 0
  const pending = t.pendingCount ?? 0
  if (total > 0) {
    return `${done}/${total}${pending ? ` · 待执行 ${pending}` : ''}`
  }
  return `${t.itemCount} 项`
}

/**
 * 是否可取消。
 *
 * @param t 任务
 */
function canCancel(t: TaskSummary): boolean {
  return Boolean(t.running) || t.status === 'running' || t.status === 'pending' || t.status === 'waiting'
}

/**
 * 是否可续跑。
 *
 * @param t 任务
 */
function canResume(t: TaskSummary): boolean {
  return Boolean(t.resumable) && !t.running
}

/**
 * 是否可放弃。
 *
 * @param t 任务
 */
function canAbandon(t: TaskSummary): boolean {
  return canResume(t) || (t.status === 'interrupted' && (t.pendingCount ?? 0) > 0)
}

/**
 * 格式化时间戳。
 *
 * @param ts unix 秒
 * @return 本地时间字符串
 */
function fmt(ts: number): string {
  if (!ts) {
    return '-'
  }
  return new Date(ts * 1000).toLocaleString()
}

/**
 * 导出当前明细 CSV（另存为选路径）。
 */
async function exportItemsCSV(): Promise<void> {
  if (items.value.length === 0) {
    emit('log', '请先选择任务查看明细')
    return
  }
  try {
    const result = await saveCsv(
      `task-${selectedId.value || 'items'}-${Date.now()}.csv`,
      ['协议号', '目标', '状态', '业务结果', 'API', '本地标记', '错误码', '错误信息'],
      items.value.map((r) => [
        r.accountId,
        r.target,
        r.status,
        r.businessResult,
        r.apiResult,
        r.localResult,
        r.errorCode,
        r.errorMessage,
      ]),
    )
    if (result.status === 'cancelled') {
      emit('log', '已取消导出')
      return
    }
    if (result.status === 'saved') {
      emit('log', `已导出任务明细 CSV：${items.value.length} 行 → ${result.path}`)
      return
    }
    emit('log', `已导出任务明细 CSV：${items.value.length} 行（浏览器下载，通常在「下载」文件夹）`)
  } catch (err) {
    emit('log', `导出失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

onMounted(() => {
  void refresh()
})

onUnmounted(() => {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
})
</script>

<template>
  <div class="tasks-panel">
    <div class="actions">
      <button type="button" class="primary" :disabled="loading" @click="refresh">
        {{ loading ? '加载中…' : '刷新' }}
      </button>
      <button type="button" class="ghost" :disabled="items.length === 0" @click="exportItemsCSV">
        导出明细 CSV
      </button>
      <p class="hint">
        私信异步任务可恢复：FloodWait 显示 waiting 倒计时；进程重启后标 interrupted，点「继续」从 pending
        断点续跑（已成功不重发；崩溃中的条目标 unknown）。
      </p>
    </div>

    <div v-if="tasks.length === 0" class="empty">暂无任务记录</div>
    <ul v-else class="list">
      <li
        v-for="t in tasks"
        :key="t.id"
        :class="{
          active: selectedId === t.id,
          running: t.running || t.status === 'running',
          waiting: t.status === 'waiting',
          interrupted: t.status === 'interrupted',
        }"
        @click="openTask(t.id)"
      >
        <div class="row">
          <div class="title">{{ t.title }}</div>
          <div class="btns">
            <button
              v-if="canResume(t)"
              type="button"
              class="resume"
              :disabled="resuming"
              @click.stop="onResume(t.id)"
            >
              继续
            </button>
            <button
              v-if="canAbandon(t)"
              type="button"
              class="ghost-sm"
              :disabled="abandoning"
              @click.stop="onAbandon(t.id)"
            >
              放弃
            </button>
            <button
              v-if="canCancel(t)"
              type="button"
              class="cancel"
              :disabled="cancelling"
              @click.stop="onCancel(t.id)"
            >
              取消
            </button>
          </div>
        </div>
        <div class="sub">
          {{ t.id }} · {{ t.module }} · {{ statusLabel(t) }} · {{ progressLabel(t) }} ·
          {{ fmt(t.createdAt) }}
        </div>
      </li>
    </ul>

    <div v-if="items.length" class="detail">
      <h4>明细（{{ items.length }}）</h4>
      <table>
        <thead>
          <tr>
            <th>协议号</th>
            <th>目标</th>
            <th>状态</th>
            <th>业务结果</th>
            <th>标记</th>
            <th>摘要</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="item in items" :key="item.id" :class="'st-' + item.status">
            <td>{{ item.accountId }}</td>
            <td>{{ item.target }}</td>
            <td>{{ item.status }}</td>
            <td>{{ item.businessResult }}</td>
            <td>{{ item.localResult }}</td>
            <td>{{ item.errorMessage || item.apiResult }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.tasks-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  align-items: center;
}

.primary,
.ghost,
.ghost-sm,
.cancel,
.resume {
  border: none;
  border-radius: 4px;
  padding: 8px 14px;
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

.ghost-sm {
  padding: 4px 10px;
  font-size: 12px;
  background: var(--color-bg-sidebar);
  color: var(--color-text-secondary);
}

.cancel {
  padding: 4px 10px;
  font-size: 12px;
  background: #fff0f0;
  color: var(--color-danger, #c0392b);
  border: 1px solid #f0c0c0;
}

.resume {
  padding: 4px 10px;
  font-size: 12px;
  background: #e8f8ef;
  color: var(--color-primary, #07c160);
  border: 1px solid #b7e6c9;
}

.primary:disabled,
.ghost:disabled,
.ghost-sm:disabled,
.cancel:disabled,
.resume:disabled {
  opacity: 0.45;
}

.hint {
  margin: 0;
  font-size: 12px;
  color: var(--color-text-secondary);
  flex: 1;
  min-width: 200px;
}

.empty {
  padding: 24px;
  text-align: center;
  color: var(--color-text-secondary);
  font-size: 13px;
}

.list {
  list-style: none;
  margin: 0;
  padding: 0;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  max-height: 280px;
  overflow: auto;
}

.list li {
  padding: 10px 12px;
  border-bottom: 1px solid var(--color-border);
  cursor: pointer;
}

.list li:hover,
.list li.active {
  background: var(--color-bg-selected);
}

.list li.running {
  border-left: 3px solid var(--color-primary);
}

.list li.waiting {
  border-left: 3px solid #e6a23c;
  background: #fffbf0;
}

.list li.interrupted {
  border-left: 3px solid #909399;
}

.row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.btns {
  display: flex;
  gap: 6px;
  flex-shrink: 0;
}

.title {
  font-size: 14px;
}

.sub {
  margin-top: 4px;
  font-size: 12px;
  color: var(--color-text-secondary);
  word-break: break-all;
}

.detail h4 {
  margin: 0 0 8px;
  font-size: 14px;
}

table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}

th,
td {
  border: 1px solid var(--color-border);
  padding: 6px 8px;
  text-align: left;
}

th {
  background: var(--color-bg-sidebar);
}

tr.st-pending td:nth-child(3) {
  color: var(--color-text-secondary);
}

tr.st-ok td:nth-child(3) {
  color: var(--color-primary, #07c160);
}

tr.st-failed td:nth-child(3),
tr.st-unknown td:nth-child(3) {
  color: var(--color-danger, #c0392b);
}

tr.st-waiting td,
tr.st-inflight td:nth-child(3) {
  color: #e6a23c;
}
</style>
