<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  checkPhones,
  listJoinedGroups,
  refreshJoinedGroups,
  scrapeGroup,
  type JoinedGroup,
  type PhoneCheckItem,
  type ScrapeRow,
  type ScrapeSource,
} from '../api/sidecar'
import { saveCsv } from '../lib/saveCsv'
import { failStopAfterForApi, loadSettings } from '../lib/settings'

const props = defineProps<{
  selectedIds: string[]
  gotdConfigured: boolean
  sidecarOk: boolean
}>()

const emit = defineEmits<{
  log: [line: string]
}>()

const tab = ref<'scrape' | 'phones'>('scrape')
const group = ref('')
const joinedGroups = ref<JoinedGroup[]>([])
const selectedPeerId = ref<number | null>(null)
const sourceMembers = ref(true)
const sourceSpeakers = ref(false)
const memberLimit = ref(200)
const historyLimit = ref(200)
const phonesText = ref('')
const intervalMs = ref(1500)
const failStopAfter = ref(5)
const running = ref(false)

const rows = ref<ScrapeRow[]>([])
const phoneItems = ref<PhoneCheckItem[]>([])

const phoneCount = computed(() => parseLines(phonesText.value).length)
const primaryAccount = computed(() => props.selectedIds[0] || '')

/**
 * 已加入群转为采集目标串（优先 @username；基础群 chat:id；超群 peer:id:hash）。
 *
 * @param g 已加入群
 * @return 目标串
 */
function scrapeTargetOf(g: JoinedGroup): string {
  if (g.username) {
    return `@${g.username}`
  }
  if (g.kind === 'group') {
    return `chat:${g.peerId}`
  }
  return `peer:${g.peerId}:${g.accessHash ?? 0}`
}

/**
 * 下拉选项展示文案。
 *
 * @param g 已加入群
 * @return 展示名
 */
function groupOptionLabel(g: JoinedGroup): string {
  const name = g.title || (g.username ? `@${g.username}` : `id:${g.peerId}`)
  const uname = g.username ? ` @${g.username}` : '（无私有用户名）'
  return `${name}${uname} · ${g.kind}`
}

/**
 * 从本地 SQLite 加载已加入群供下拉（不打 TG）。
 */
async function loadJoinedForScrape(): Promise<void> {
  if (!props.sidecarOk) {
    emit('log', '本地服务未连接')
    return
  }
  if (!primaryAccount.value) {
    emit('log', '请先在中栏勾选一个协议号')
    return
  }
  running.value = true
  try {
    const result = await listJoinedGroups(primaryAccount.value)
    applyJoinedForScrape(result.items ?? [], result.note)
  } catch (err) {
    emit('log', `读取本地已加入群失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    running.value = false
  }
}

/**
 * 从 Telegram 刷新已加入群写入本地后再填充下拉。
 */
async function refreshJoinedForScrape(): Promise<void> {
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
  running.value = true
  try {
    const result = await refreshJoinedGroups(primaryAccount.value, 200)
    applyJoinedForScrape(result.items ?? [], result.note)
  } catch (err) {
    emit('log', `刷新已加入群失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    running.value = false
  }
}

/**
 * 过滤并应用采集下拉数据。
 *
 * @param items 已加入群
 * @param note 备注
 */
function applyJoinedForScrape(items: JoinedGroup[], note: string): void {
  // 与「群组→已加入」一致：仅排除申请中；普通 group 用 chat:id 采集
  joinedGroups.value = items.filter((g) => !g.joinRequestOut)
  if (
    selectedPeerId.value != null &&
    !joinedGroups.value.some((g) => g.peerId === selectedPeerId.value)
  ) {
    selectedPeerId.value = null
    group.value = ''
  }
  emit(
    'log',
    `采集目标 ${joinedGroups.value.length} 个群（账号 ${primaryAccount.value}）· ${note}`,
  )
}

/**
 * 下拉选择变更时同步 group 目标串。
 */
function onSelectJoined(peerId: number | null): void {
  selectedPeerId.value = peerId
  if (peerId == null) {
    group.value = ''
    return
  }
  const g = joinedGroups.value.find((x) => x.peerId === peerId)
  if (!g) {
    group.value = ''
    return
  }
  group.value = scrapeTargetOf(g)
}

watch(tab, (v) => {
  if (v === 'scrape' && joinedGroups.value.length === 0 && primaryAccount.value) {
    void loadJoinedForScrape()
  }
})

watch(
  () => props.selectedIds[0],
  () => {
    if (tab.value === 'scrape') {
      selectedPeerId.value = null
      group.value = ''
      joinedGroups.value = []
      void loadJoinedForScrape()
    }
  },
)

onMounted(() => {
  const s = loadSettings()
  intervalMs.value = s.defaultIntervalMs
  failStopAfter.value = s.failStopAfter
  if (tab.value === 'scrape' && primaryAccount.value) {
    void loadJoinedForScrape()
  }
})

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
 * 执行成员/发言者采集。
 */
async function onScrape(): Promise<void> {
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
  if (!group.value.trim()) {
    emit('log', '请从下拉选择已加入的目标群，或先点刷新')
    return
  }
  const sources: ScrapeSource[] = []
  if (sourceMembers.value) {
    sources.push('members')
  }
  if (sourceSpeakers.value) {
    sources.push('speakers')
  }
  if (sources.length === 0) {
    emit('log', '请至少勾选一种来源：成员列表 / 消息发言者')
    return
  }

  running.value = true
  rows.value = []
  const accountId = props.selectedIds[0]
  emit('log', `开始采集：群=${group.value} 来源=${sources.join('+')} 账号=${accountId}`)
  try {
    const result = await scrapeGroup({
      accountId,
      group: group.value.trim(),
      sources,
      memberLimit: memberLimit.value,
      historyLimit: historyLimit.value,
    })
    if (!result.ok) {
      emit('log', `采集失败：${result.errorCode || ''} ${result.errorMessage || ''}`)
      return
    }
    rows.value = result.rows ?? []
    emit(
      'log',
      `完成：共 ${rows.value.length} 行（成员 ${result.membersCount} / 发言者 ${result.speakersCount}）${result.taskId ? ' · task=' + result.taskId : ''} · ${result.note}`,
    )
  } catch (err) {
    emit('log', `采集异常：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    running.value = false
  }
}

/**
 * 导出采集结果 CSV（另存为选路径）。
 */
async function exportScrapeCSV(): Promise<void> {
  if (rows.value.length === 0) {
    emit('log', '暂无采集结果可导出')
    return
  }
  try {
    const result = await saveCsv(
      `scrape-${Date.now()}.csv`,
      ['来源', '用户ID', '用户名', '名', '姓', '手机号', 'Bot', '协议号', '群'],
      rows.value.map((r) => [
        r.source,
        String(r.userId || ''),
        r.username || '',
        r.firstName || '',
        r.lastName || '',
        r.phone || '',
        r.isBot ? '1' : '0',
        r.accountId || '',
        r.group || '',
      ]),
    )
    if (result.status === 'cancelled') {
      emit('log', '已取消导出')
      return
    }
    if (result.status === 'saved') {
      emit('log', `已导出 CSV：${rows.value.length} 行 → ${result.path}`)
      return
    }
    emit('log', `已导出 CSV：${rows.value.length} 行（浏览器下载，通常在「下载」文件夹）`)
  } catch (err) {
    emit('log', `导出失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

/**
 * 执行手机号检查。
 */
async function onCheckPhones(): Promise<void> {
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
  const phones = parseLines(phonesText.value)
  if (phones.length === 0) {
    emit('log', '请填写手机号名单')
    return
  }
  running.value = true
  phoneItems.value = []
  emit('log', `开始手机号检查：${phones.length} 个，账号=${props.selectedIds.length}`)
  try {
    const result = await checkPhones({
      accountIds: props.selectedIds,
      phones,
      intervalMs: intervalMs.value,
      failStopAfter: failStopAfterForApi(failStopAfter.value),
    })
    phoneItems.value = result.items ?? []
    const stopHint = result.stopped ? ` · 已停止(${result.stopReason})` : ''
    emit(
      'log',
      `完成：匹配 ${result.success} / 其他 ${result.failed}${stopHint}${result.taskId ? ' · task=' + result.taskId : ''} · ${result.note}`,
    )
    for (const item of result.items) {
      if (item.ok) {
        emit(
          'log',
          `✓ ${item.phone} [${item.businessResult}] @${item.username || ''} ${item.firstName || ''}`,
        )
      } else {
        emit(
          'log',
          `✗ ${item.phone} [${item.businessResult}] ${item.errorCode || ''} ${item.errorMessage || ''}`,
        )
      }
    }
  } catch (err) {
    emit('log', `检查异常：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    running.value = false
  }
}

/**
 * 导出手机号检查 CSV（另存为选路径）。
 */
async function exportPhoneCSV(): Promise<void> {
  if (phoneItems.value.length === 0) {
    emit('log', '暂无检查结果可导出')
    return
  }
  try {
    const result = await saveCsv(
      `phones-${Date.now()}.csv`,
      ['手机号', '结果', '用户ID', '用户名', '名', '姓', '协议号', '错误码', '错误信息'],
      phoneItems.value.map((r) => [
        r.phone,
        r.businessResult,
        r.peerId ? String(r.peerId) : '',
        r.username || '',
        r.firstName || '',
        r.lastName || '',
        r.accountId || '',
        r.errorCode || '',
        r.errorMessage || '',
      ]),
    )
    if (result.status === 'cancelled') {
      emit('log', '已取消导出')
      return
    }
    if (result.status === 'saved') {
      emit('log', `已导出 CSV：${phoneItems.value.length} 行 → ${result.path}`)
      return
    }
    emit('log', `已导出 CSV：${phoneItems.value.length} 行（浏览器下载，通常在「下载」文件夹）`)
  } catch (err) {
    emit('log', `导出失败：${err instanceof Error ? err.message : String(err)}`)
  }
}
</script>

<template>
  <div class="scrape-panel">
    <div class="tabs">
      <button type="button" :class="{ active: tab === 'scrape' }" @click="tab = 'scrape'">
        成员采集
      </button>
      <button type="button" :class="{ active: tab === 'phones' }" @click="tab = 'phones'">
        手机号检查
      </button>
    </div>

    <template v-if="tab === 'scrape'">
      <div class="field">
        <label>目标群（已加入）</label>
        <div class="group-pick">
          <select
            :value="selectedPeerId ?? ''"
            :disabled="running || joinedGroups.length === 0"
            @change="
              onSelectJoined(
                ($event.target as HTMLSelectElement).value
                  ? Number(($event.target as HTMLSelectElement).value)
                  : null,
              )
            "
          >
            <option value="">
              {{
                joinedGroups.length === 0
                  ? '暂无列表，请先刷新'
                  : `请选择（${joinedGroups.length}）`
              }}
            </option>
            <option v-for="g in joinedGroups" :key="g.peerId" :value="g.peerId">
              {{ groupOptionLabel(g) }}
            </option>
          </select>
          <button
            type="button"
            class="ghost"
            :disabled="running || !sidecarOk || !gotdConfigured || !primaryAccount"
            @click="refreshJoinedForScrape"
          >
            {{ running ? '同步中…' : '刷新' }}
          </button>
        </div>
        <p class="hint">
          默认读本地已加入群缓存；点「刷新」才请求 Telegram。当前目标：
          <code>{{ group || '未选' }}</code>
        </p>
        <label class="manual-label">或手动填写（@username / t.me 链接 / peer:id:hash）</label>
        <input
          v-model="group"
          type="text"
          class="manual-target"
          placeholder="例如 @gaotrx 或 https://t.me/gaotrx"
          :disabled="running"
          @input="selectedPeerId = null"
        />
      </div>
      <div class="sources">
        <label class="check">
          <input v-model="sourceMembers" type="checkbox" />
          成员列表
        </label>
        <label class="check">
          <input v-model="sourceSpeakers" type="checkbox" />
          消息发言者
        </label>
      </div>
      <p class="hint">可多选；导出时每行带「来源」列。发言者 ≠ 全部成员。</p>
      <div class="row-fields">
        <div class="field">
          <label>成员上限</label>
          <input v-model.number="memberLimit" type="number" min="1" max="2000" />
        </div>
        <div class="field">
          <label>历史消息条数（发言者）</label>
          <input v-model.number="historyLimit" type="number" min="1" max="2000" />
        </div>
      </div>
      <div class="actions">
        <button
          type="button"
          class="primary"
          :disabled="running || !sidecarOk || selectedIds.length === 0"
          @click="onScrape"
        >
          {{ running ? '采集中…' : '开始采集' }}
        </button>
        <button type="button" class="ghost" :disabled="rows.length === 0" @click="exportScrapeCSV">
          导出 CSV
        </button>
        <span class="meta">已选账号 {{ selectedIds.length }}（使用第一个执行）</span>
      </div>
      <div v-if="rows.length" class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>来源</th>
              <th>用户ID</th>
              <th>用户名</th>
              <th>名</th>
              <th>姓</th>
              <th>手机号</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(r, i) in rows.slice(0, 200)" :key="i">
              <td>{{ r.source }}</td>
              <td>{{ r.userId }}</td>
              <td>{{ r.username }}</td>
              <td>{{ r.firstName }}</td>
              <td>{{ r.lastName }}</td>
              <td>{{ r.phone }}</td>
            </tr>
          </tbody>
        </table>
        <p v-if="rows.length > 200" class="hint">预览前 200 行，完整数据请导出 CSV。</p>
      </div>
    </template>

    <template v-else>
      <div class="field">
        <label>手机号名单（{{ phoneCount }}）</label>
        <textarea
          v-model="phonesText"
          rows="8"
          placeholder="每行一个国际格式手机号&#10;结果：匹配 / 未匹配 / 隐私 / 失败 / 待重试&#10;未匹配 ≠ 未注册"
        />
      </div>
      <div class="row-fields">
        <div class="field">
          <label>间隔（毫秒）</label>
          <input v-model.number="intervalMs" type="number" min="500" step="100" />
          <p class="hint">本地限速，非 Telegram 官方额度</p>
        </div>
        <div class="field">
          <label>连续失败熔断</label>
          <input v-model.number="failStopAfter" type="number" min="0" step="1" />
          <p class="hint">0=关闭</p>
        </div>
        <div class="field meta-block">
          <label>已选执行账号</label>
          <strong :class="{ warn: selectedIds.length === 0 }">{{ selectedIds.length }}</strong>
        </div>
      </div>
      <div class="actions">
        <button
          type="button"
          class="primary"
          :disabled="running || !sidecarOk || selectedIds.length === 0"
          @click="onCheckPhones"
        >
          {{ running ? '检查中…' : '开始检查' }}
        </button>
        <button
          type="button"
          class="ghost"
          :disabled="phoneItems.length === 0"
          @click="exportPhoneCSV"
        >
          导出 CSV
        </button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.scrape-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.group-pick {
  display: flex;
  gap: 8px;
  align-items: stretch;
}

.group-pick select {
  flex: 1;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 8px 10px;
  font: inherit;
  background: #fff;
}

.group-pick .ghost {
  flex-shrink: 0;
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

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.field label {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.field input,
.field textarea {
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 8px 10px;
  font: inherit;
}

.sources {
  display: flex;
  gap: 16px;
}

.check {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  cursor: pointer;
}

.manual-label {
  display: block;
  margin-top: 8px;
  font-size: 12px;
  color: var(--color-text-secondary);
}

.manual-target {
  width: 100%;
  margin-top: 4px;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 8px 10px;
  font: inherit;
  font-size: 13px;
  box-sizing: border-box;
}

.hint {
  margin: 0;
  font-size: 12px;
  color: var(--color-text-secondary);
}

.row-fields {
  display: grid;
  grid-template-columns: 1fr 120px 140px;
  gap: 12px;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
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

.meta {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.meta-block strong {
  font-size: 22px;
}

.meta-block strong.warn {
  color: var(--color-danger);
}

.table-wrap {
  border: 1px solid var(--color-border);
  border-radius: 4px;
  overflow: auto;
  max-height: 280px;
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
  white-space: nowrap;
}

th {
  background: var(--color-bg-sidebar);
  position: sticky;
  top: 0;
}
</style>
