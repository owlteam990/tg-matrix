<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  contactBatch,
  listContacts,
  refreshContacts,
  type ContactAction,
  type ContactListItem,
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

type TabKey = 'list' | ContactAction
const tab = ref<TabKey>('list')

const action = computed<ContactAction>(() => (tab.value === 'delete' ? 'delete' : 'add'))
const targetsText = ref('')
const intervalMs = ref(1500)
const failStopAfter = ref(5)
const running = ref(false)

const contacts = ref<ContactListItem[]>([])
const listLoading = ref(false)
const refreshing = ref(false)
const filterText = ref('')
const syncedAt = ref(0)
const listSource = ref('')

const accountId = computed(() => props.selectedIds[0] || '')

const syncedLabel = computed(() => {
  if (!syncedAt.value) {
    return '尚未从 Telegram 同步过'
  }
  return `本地缓存 · 上次同步 ${new Date(syncedAt.value * 1000).toLocaleString()}`
})

onMounted(() => {
  const s = loadSettings()
  intervalMs.value = s.defaultIntervalMs
  failStopAfter.value = s.failStopAfter
})

const targetCount = computed(() => parseTargets(targetsText.value).length)

const filteredContacts = computed(() => {
  const q = filterText.value.trim().toLowerCase()
  if (!q) {
    return contacts.value
  }
  return contacts.value.filter((c) => {
    const hay = [c.firstName, c.lastName, c.username, c.phone, String(c.userId)]
      .filter(Boolean)
      .join(' ')
      .toLowerCase()
    return hay.includes(q)
  })
})

watch(
  () => [tab.value, accountId.value, props.sidecarOk] as const,
  () => {
    // 切 Tab / 换协议号：只读本地 SQLite，不打 TG
    if (tab.value === 'list' && accountId.value && props.sidecarOk) {
      void loadList()
    }
  },
  { immediate: true },
)

/**
 * 解析名单行。
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

/**
 * 在线状态中文。
 *
 * @param status API 状态码
 * @return 展示文案
 */
function statusLabel(status?: string): string {
  switch (status) {
    case 'online':
      return '在线'
    case 'recently':
      return '最近'
    case 'lastWeek':
      return '一周内'
    case 'lastMonth':
      return '一月内'
    case 'offline':
      return '离线'
    default:
      return status || '—'
  }
}

/**
 * 从本地 SQLite 加载通讯录（不请求 Telegram）。
 */
async function loadList(): Promise<void> {
  if (!accountId.value || !props.sidecarOk) {
    contacts.value = []
    syncedAt.value = 0
    listSource.value = ''
    return
  }
  listLoading.value = true
  try {
    const res = await listContacts(accountId.value)
    contacts.value = res.items || []
    syncedAt.value = res.syncedAt || 0
    listSource.value = res.source || 'sqlite'
    const hint = syncedAt.value
      ? `本地 ${contacts.value.length} 人 · 上次同步 ${new Date(syncedAt.value * 1000).toLocaleString()}`
      : `本地缓存为空（${contacts.value.length} 人），请点「刷新」从 Telegram 拉取`
    emit('log', `已读本地通讯录：${hint}（账号 ${accountId.value}）`)
  } catch (err) {
    emit('log', `读取本地通讯录失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    listLoading.value = false
  }
}

/**
 * 手动从 Telegram 刷新并写入该协议号本地缓存。
 */
async function onRefreshFromTG(): Promise<void> {
  if (!accountId.value) {
    emit('log', '请先勾选协议号')
    return
  }
  if (!props.sidecarOk) {
    emit('log', '本地服务未连接')
    return
  }
  if (!props.gotdConfigured) {
    emit('log', '未配置 TG_APP_ID / TG_APP_HASH')
    return
  }
  refreshing.value = true
  try {
    const res = await refreshContacts(accountId.value)
    contacts.value = res.items || []
    syncedAt.value = res.syncedAt || Math.floor(Date.now() / 1000)
    listSource.value = res.source || 'telegram'
    emit(
      'log',
      `已从 Telegram 刷新并写入本地：${contacts.value.length} 人（账号 ${accountId.value}）`,
    )
  } catch (err) {
    emit('log', `刷新失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    refreshing.value = false
  }
}

/**
 * 导出当前列表 CSV。
 */
async function exportListCSV(): Promise<void> {
  if (contacts.value.length === 0) {
    emit('log', '暂无联系人可导出')
    return
  }
  try {
    const result = await saveCsv(
      `contacts-${accountId.value || 'list'}-${Date.now()}.csv`,
      ['用户ID', '用户名', '名', '姓', '手机号', '双向', 'Bot', '状态'],
      contacts.value.map((c) => [
        String(c.userId),
        c.username || '',
        c.firstName || '',
        c.lastName || '',
        c.phone || '',
        c.mutual ? '1' : '0',
        c.isBot ? '1' : '0',
        c.status || '',
      ]),
    )
    if (result.status === 'cancelled') {
      emit('log', '已取消导出')
      return
    }
    if (result.status === 'saved') {
      emit('log', `已导出联系人 CSV：${contacts.value.length} 行 → ${result.path}`)
      return
    }
    emit('log', `已导出联系人 CSV：${contacts.value.length} 行（浏览器下载）`)
  } catch (err) {
    emit('log', `导出失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

/**
 * 提交添加 / 删除联系人。
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
  const targets = parseTargets(targetsText.value)
  if (targets.length === 0) {
    emit('log', '请填写目标名单')
    return
  }

  running.value = true
  const verb = action.value === 'add' ? '添加' : '删除'
  emit(
    'log',
    `开始${verb}联系人：目标=${targets.length} 账号=${props.selectedIds.length} 间隔=${intervalMs.value}ms`,
  )
  try {
    const result = await contactBatch({
      accountIds: props.selectedIds,
      action: action.value,
      targets,
      intervalMs: intervalMs.value,
      failStopAfter: failStopAfterForApi(failStopAfter.value),
    })
    const stopHint = result.stopped ? ` · 已停止(${result.stopReason})` : ''
    emit(
      'log',
      `完成：成功 ${result.success} / 失败 ${result.failed}${stopHint}${result.taskId ? ' · task=' + result.taskId : ''} · ${result.note}`,
    )
    for (const item of result.items) {
      const extra = [item.username && `@${item.username}`, item.phone, item.firstName]
        .filter(Boolean)
        .join(' ')
      if (item.ok) {
        emit('log', `✓ ${item.target} ← ${item.accountId} [${item.businessResult}] ${extra}`)
      } else {
        emit(
          'log',
          `✗ ${item.target} ← ${item.accountId} [${item.businessResult}] ${item.errorCode || ''} ${item.errorMessage || ''}`,
        )
      }
    }
    // 操作后只重读本地 SQLite（批量接口已局部更新缓存，不打 TG）
    if (result.success > 0 && tab.value === 'list') {
      void loadList()
    } else if (result.success > 0) {
      // 在添加/删除 Tab 也静默更新内存，切回列表时可见
      void loadList()
    }
  } catch (err) {
    emit('log', `${verb}异常：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    running.value = false
  }
}
</script>

<template>
  <div class="contacts-panel">
    <div class="tabs">
      <button type="button" :class="{ active: tab === 'list' }" @click="tab = 'list'">
        联系人列表
      </button>
      <button type="button" :class="{ active: tab === 'add' }" @click="tab = 'add'">
        添加联系人
      </button>
      <button type="button" :class="{ active: tab === 'delete' }" @click="tab = 'delete'">
        删除联系人
      </button>
    </div>

    <template v-if="tab === 'list'">
      <div class="list-toolbar">
        <input
          v-model="filterText"
          type="search"
          class="filter"
          placeholder="筛选：姓名 / 用户名 / 手机号 / ID"
        />
        <button
          type="button"
          class="ghost"
          :disabled="listLoading || refreshing || !accountId"
          @click="loadList"
        >
          {{ listLoading ? '读取中…' : '读本地' }}
        </button>
        <button
          type="button"
          class="primary"
          :disabled="refreshing || listLoading || !accountId || !sidecarOk || !gotdConfigured"
          @click="onRefreshFromTG"
        >
          {{ refreshing ? '同步中…' : '刷新' }}
        </button>
        <button type="button" class="ghost" :disabled="contacts.length === 0" @click="exportListCSV">
          导出 CSV
        </button>
        <span class="count">
          {{ filteredContacts.length }}
          <template v-if="filterText.trim()"> / {{ contacts.length }}</template>
          人
        </span>
      </div>
      <p class="hint">{{ syncedLabel }}<template v-if="listSource"> · 来源 {{ listSource }}</template></p>

      <p v-if="!accountId" class="hint">请在中栏勾选一个协议号</p>
      <p v-else-if="selectedIds.length > 1" class="hint">列表使用第一个选中账号：{{ accountId }}</p>
      <p v-else-if="listLoading && !contacts.length" class="hint">读取本地通讯录…</p>
      <p v-else-if="!contacts.length" class="hint">
        本地无缓存。点「刷新」从 Telegram 拉取并按协议号写入 SQLite。
      </p>

      <div v-if="filteredContacts.length" class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>姓名</th>
              <th>用户名</th>
              <th>用户ID</th>
              <th>手机号</th>
              <th>双向</th>
              <th>状态</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="c in filteredContacts.slice(0, 500)" :key="c.userId">
              <td>
                {{ [c.firstName, c.lastName].filter(Boolean).join(' ') || '—' }}
                <span v-if="c.isBot" class="tag">bot</span>
                <span v-if="c.isSelf" class="tag">自己</span>
              </td>
              <td>{{ c.username ? '@' + c.username : '—' }}</td>
              <td>{{ c.userId }}</td>
              <td>{{ c.phone || '—' }}</td>
              <td>{{ c.mutual ? '是' : '否' }}</td>
              <td>{{ statusLabel(c.status) }}</td>
            </tr>
          </tbody>
        </table>
        <p v-if="filteredContacts.length > 500" class="hint">
          预览前 500 行，完整数据请导出 CSV。
        </p>
      </div>
      <p v-else-if="contacts.length && filterText.trim()" class="hint">无匹配联系人</p>
    </template>

    <template v-else>
      <div class="field">
        <label>名单（{{ targetCount }}）</label>
        <textarea
          v-if="tab === 'add'"
          v-model="targetsText"
          rows="8"
          placeholder="每行一条：&#10;+8613800138000,张三,李&#10;@username&#10;&#10;手机号未匹配 ≠ 未注册；添加 ≠ 双向好友"
        />
        <textarea
          v-else
          v-model="targetsText"
          rows="8"
          placeholder="每行一条：@username / 手机号 / 用户 ID&#10;删除仅移除通讯录，不等于删除聊天或风控历史"
        />
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
          :disabled="running || !sidecarOk || selectedIds.length === 0"
          @click="onSubmit"
        >
          {{ running ? '执行中…' : tab === 'add' ? '开始添加' : '开始删除' }}
        </button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.contacts-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
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

.list-toolbar {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}

.filter {
  flex: 1;
  min-width: 180px;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 8px 10px;
  font: inherit;
}

.count {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.table-wrap {
  border: 1px solid var(--color-border);
  border-radius: 4px;
  overflow: auto;
  max-height: 48vh;
}

table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

th,
td {
  text-align: left;
  padding: 8px 10px;
  border-bottom: 1px solid var(--color-border);
  white-space: nowrap;
}

th {
  background: var(--color-bg-sidebar);
  color: var(--color-text-secondary);
  font-weight: 600;
  position: sticky;
  top: 0;
}

.tag {
  margin-left: 6px;
  font-size: 10px;
  padding: 1px 5px;
  border-radius: 3px;
  background: var(--color-bg-selected);
  color: var(--color-primary);
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
}

.actions {
  display: flex;
  gap: 8px;
}

.primary,
.ghost {
  border-radius: 4px;
  padding: 10px 18px;
  font: inherit;
  cursor: pointer;
}

.primary {
  border: none;
  background: var(--color-primary);
  color: #fff;
}

.ghost {
  border: 1px solid var(--color-border);
  background: #fff;
  color: var(--color-text);
  padding: 8px 12px;
}

.primary:disabled,
.ghost:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
</style>
