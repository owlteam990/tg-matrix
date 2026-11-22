<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import type { ModuleKey } from './IconRail.vue'
import type { Account, Health } from '../api/sidecar'
import { saveTextFile } from '../lib/saveCsv'
import { getLocalAppVersion } from '../lib/appUpdate'
import {
  clampLogPanelHeight,
  loadLogPanelHeight,
  saveLogPanelHeight,
} from '../lib/logPanel'
import AccountsPanel from './AccountsPanel.vue'
import MessagesPanel from './MessagesPanel.vue'
import GroupsPanel from './GroupsPanel.vue'
import ContactsPanel from './ContactsPanel.vue'
import ScrapePanel from './ScrapePanel.vue'
import MarketingPanel from './MarketingPanel.vue'
import SendQueuePanel from './SendQueuePanel.vue'
import TasksPanel from './TasksPanel.vue'
import SettingsPanel from './SettingsPanel.vue'

const props = defineProps<{
  module: ModuleKey
  accounts: Account[]
  selectedIds: string[]
  focusAccountId?: string
  health: Health | null
  healthError: string
  appVersion?: string
}>()

const emit = defineEmits<{
  refresh: []
}>()

const logs = ref<string[]>([])

/** 全模块共用的任务日志高度（拖拽分割线后写入 localStorage） */
const logHeight = ref(loadLogPanelHeight())
const logDragging = ref(false)
let dragCleanup: (() => void) | null = null

const logStyle = computed(() => ({
  height: `${logHeight.value}px`,
  flex: '0 0 auto',
}))

/**
 * 开始拖拽调整任务日志高度（上拖增高、下拖减矮）。
 *
 * @param e 指针事件
 */
function onLogSplitterDown(e: PointerEvent): void {
  e.preventDefault()
  if (dragCleanup) {
    dragCleanup()
  }
  logDragging.value = true
  const startY = e.clientY
  const startH = logHeight.value

  const onMove = (ev: PointerEvent): void => {
    // 分割线在日志上方：指针上移 → 日志变高
    logHeight.value = clampLogPanelHeight(startH + (startY - ev.clientY))
  }
  const onUp = (): void => {
    logDragging.value = false
    saveLogPanelHeight(logHeight.value)
    window.removeEventListener('pointermove', onMove)
    window.removeEventListener('pointerup', onUp)
    window.removeEventListener('pointercancel', onUp)
    dragCleanup = null
  }
  window.addEventListener('pointermove', onMove)
  window.addEventListener('pointerup', onUp)
  window.addEventListener('pointercancel', onUp)
  dragCleanup = onUp
}

onUnmounted(() => {
  if (dragCleanup) {
    dragCleanup()
  }
})

const titles: Record<ModuleKey, string> = {
  accounts: '账号',
  messages: '消息',
  groups: '群组',
  contacts: '联系人',
  scrape: '采集',
  marketing: '营销',
  send_queue: '发送队列',
  tasks: '任务中心',
  settings: '设置',
}

const hints: Record<ModuleKey, string> = {
  accounts: '导入 .json / .session；中栏右键可编辑备注/附加代理，或删除协议号。',
  messages: '会话列表、历史与收发；「群发」支持多号策略调度与多文案轮换。请先在中栏勾选协议号。',
  groups: '已加入群按协议号缓存在本地；仅点「刷新」才请求 Telegram。支持加群/退出/邀请。',
  contacts: '通讯录按协议号缓存在本地 SQLite；仅点「刷新」才请求 Telegram。',
  scrape: '成员采集与手机号检查；结果需标注来源。',
  marketing: '产品库 → 采集发言 → 手动填写话术 → 审核后入队发送。',
  send_queue: '汇总营销与群发明细；待发送可改文案、换协议号、按策略计算可发时间并定时发送。',
  tasks: '查看全局任务队列、进度与导出。',
  settings: '本地节奏、发送策略（营销/群发共用）与版本信息。',
}

/**
 * 追加任务日志行。
 *
 * @param line 日志内容
 */
function appendLog(line: string): void {
  const ts = new Date().toLocaleTimeString()
  logs.value = [`[${ts}] ${line}`, ...logs.value].slice(0, 500)
}

/**
 * 导出当前任务日志为文本，便于排查问题。
 */
async function exportLogs(): Promise<void> {
  if (!logs.value.length) {
    appendLog('暂无日志可导出')
    return
  }
  try {
    const ver = await getLocalAppVersion()
    const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19)
    const header = [
      'TG Matrix 任务日志',
      `导出时间: ${new Date().toLocaleString()}`,
      `应用版本: ${ver}`,
      `当前模块: ${titles[props.module]}`,
      '---',
      '',
    ].join('\n')
    // 界面新在上，导出按时间正序（旧→新）
    const body = [...logs.value].reverse().join('\n')
    const result = await saveTextFile(
      `tg-matrix-log-${stamp}.txt`,
      `${header}${body}`,
      '导出任务日志',
    )
    if (result.status === 'saved') {
      appendLog(`日志已导出：${result.path}`)
    } else if (result.status === 'browser') {
      appendLog(`日志已下载：${result.path}`)
    }
  } catch (err) {
    appendLog(`导出日志失败：${err instanceof Error ? err.message : String(err)}`)
  }
}
</script>

<template>
  <main class="work">
    <header class="work-header">
      <div>
        <h1>{{ titles[module] }}</h1>
        <p>{{ hints[module] }}</p>
      </div>
      <div class="health" :class="{ ok: !!health?.ok, bad: !!healthError }">
        <template v-if="health?.ok">服务正常</template>
        <template v-else-if="healthError">服务未连接</template>
        <template v-else>连接中…</template>
      </div>
    </header>

    <section
      class="panel"
      :class="{
        'panel-chat': module === 'messages',
        'panel-wide': module === 'marketing' || module === 'send_queue',
      }"
    >
      <AccountsPanel
        v-if="module === 'accounts'"
        :accounts="accounts"
        :selected-ids="selectedIds"
        :focus-account-id="focusAccountId"
        :gotd-configured="!!health?.gotdConfigured"
        @refresh="emit('refresh')"
        @log="appendLog"
      />
      <MessagesPanel
        v-if="module === 'messages'"
        :selected-ids="selectedIds"
        :gotd-configured="!!health?.gotdConfigured"
        :sidecar-ok="!!health?.ok"
        @log="appendLog"
      />
      <!-- KeepAlive：切换模块回来保留「已加入」Tab 与监控状态，避免全部变成未监控 -->
      <KeepAlive>
        <GroupsPanel
          v-if="module === 'groups'"
          :selected-ids="selectedIds"
          :gotd-configured="!!health?.gotdConfigured"
          :sidecar-ok="!!health?.ok"
          @log="appendLog"
        />
      </KeepAlive>
      <ContactsPanel
        v-if="module === 'contacts'"
        :selected-ids="selectedIds"
        :gotd-configured="!!health?.gotdConfigured"
        :sidecar-ok="!!health?.ok"
        @log="appendLog"
      />
      <ScrapePanel
        v-if="module === 'scrape'"
        :selected-ids="selectedIds"
        :gotd-configured="!!health?.gotdConfigured"
        :sidecar-ok="!!health?.ok"
        @log="appendLog"
      />
      <MarketingPanel
        v-if="module === 'marketing'"
        :selected-ids="selectedIds"
        :gotd-configured="!!health?.gotdConfigured"
        :sidecar-ok="!!health?.ok"
        @log="appendLog"
      />
      <SendQueuePanel
        v-if="module === 'send_queue'"
        :accounts="accounts"
        :sidecar-ok="!!health?.ok"
        @log="appendLog"
      />
      <TasksPanel v-if="module === 'tasks'" @log="appendLog" />
      <SettingsPanel
        v-if="module === 'settings'"
        :app-version="appVersion || ''"
        :sidecar-ok="!!health?.ok"
        @log="appendLog"
      />
      <template
        v-if="
          module !== 'accounts' &&
          module !== 'messages' &&
          module !== 'groups' &&
          module !== 'contacts' &&
          module !== 'scrape' &&
          module !== 'marketing' &&
          module !== 'send_queue' &&
          module !== 'tasks' &&
          module !== 'settings'
        "
      >
        <div class="row">
          <span class="label">已选执行账号</span>
          <strong>{{ selectedIds.length }}</strong>
        </div>
        <p class="note">该模块将在后续迭代接入。请先在「账号」完成导入与检查。</p>
      </template>
    </section>

    <div
      class="log-splitter"
      :class="{ active: logDragging }"
      role="separator"
      aria-orientation="horizontal"
      aria-label="拖拽调整任务日志高度"
      title="上下拖动调整日志高度（全页面同步）"
      @pointerdown="onLogSplitterDown"
    />

    <section class="log" :style="logStyle">
      <div class="log-head">
        <h3>本任务日志</h3>
        <button
          type="button"
          class="ghost log-export"
          :disabled="!logs.length"
          @click="exportLogs"
        >
          导出日志
        </button>
      </div>
      <div class="log-body">
        <template v-if="logs.length">
          <div v-for="(line, i) in logs" :key="i" class="log-line">{{ line }}</div>
        </template>
        <template v-else>暂无任务日志</template>
      </div>
    </section>
  </main>
</template>

<style scoped>
.work {
  flex: 1;
  min-width: 0;
  background: var(--color-bg);
  display: flex;
  flex-direction: column;
}

.work-header {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 20px 24px;
  border-bottom: 1px solid var(--color-border);
}

.work-header h1 {
  margin: 0;
  font-size: 20px;
  color: var(--color-text);
}

.work-header p {
  margin: 6px 0 0;
  font-size: 13px;
  color: var(--color-text-secondary);
}

.health {
  align-self: flex-start;
  font-size: 12px;
  padding: 6px 10px;
  border-radius: 999px;
  background: var(--color-bg-sidebar);
  color: var(--color-text-secondary);
  white-space: nowrap;
}

.health.ok {
  background: var(--color-bg-selected);
  color: var(--color-primary);
}

.health.bad {
  background: #ffecec;
  color: var(--color-danger);
}

.panel {
  flex: 1;
  min-height: 120px;
  padding: 24px;
  overflow: auto;
}

.panel-chat {
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.log-splitter {
  flex: 0 0 6px;
  height: 6px;
  margin: 0;
  cursor: row-resize;
  touch-action: none;
  position: relative;
  background: var(--color-border);
  transition: background 0.12s ease;
}

.log-splitter::after {
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

.log-splitter:hover,
.log-splitter.active {
  background: var(--color-bg-selected);
}

.log-splitter:hover::after,
.log-splitter.active::after {
  background: var(--color-primary);
}

.work:has(.log-splitter.active) {
  user-select: none;
  cursor: row-resize;
}

.row {
  display: flex;
  gap: 12px;
  align-items: baseline;
  margin-bottom: 12px;
}

.label {
  color: var(--color-text-secondary);
  font-size: 13px;
}

.note {
  margin: 0;
  font-size: 13px;
  color: var(--color-text-secondary);
  line-height: 1.6;
}

.log {
  padding: 12px 24px 16px;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
  box-sizing: border-box;
}

.log-head {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}

.log h3 {
  margin: 0;
  font-size: 14px;
  color: var(--color-text);
}

.log-export {
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 4px 10px;
  font: inherit;
  font-size: 12px;
  background: #fff;
  color: var(--color-text);
  cursor: pointer;
}

.log-export:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.log-body {
  flex: 1;
  min-height: 0;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  background: #fafafa;
  padding: 12px;
  font-size: 12px;
  color: var(--color-text-secondary);
  overflow: auto;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.log-line {
  margin-bottom: 4px;
  line-height: 1.5;
}
</style>
