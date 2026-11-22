<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import {
  deleteMarketingSendPolicy,
  listMarketingSendPolicies,
  upsertMarketingSendPolicy,
  type MarketingSendPolicy,
} from '../api/sidecar'
import { getLocalAppVersion } from '../lib/appUpdate'
import { openExternalUrl } from '../lib/openUrl'
import { loadSettings, saveSettings } from '../lib/settings'

const GITHUB_RELEASES = 'https://github.com/owlteam990/tg-matrix/releases'

const props = defineProps<{
  appVersion?: string
  sidecarOk?: boolean
}>()

const emit = defineEmits<{
  log: [line: string]
}>()

const defaultIntervalMs = ref(1500)
const defaultJoinIntervalMs = ref(2000)
const failStopAfter = ref(5)
const note = ref('本地限速，非 Telegram 官方额度')
const localVersion = ref(props.appVersion || '')

/** 发送策略（营销 / 群发共用） */
const sendPolicies = ref<MarketingSendPolicy[]>([])
const policySaving = ref(false)
const policyForm = ref({
  id: '',
  name: '默认策略',
  windowMinutes: 60,
  maxPerWindow: 5,
  mutualMinIntervalMs: 3000,
  nonMutualMinIntervalMs: 600000,
  nonMutualDailyCap: 20,
  sameTargetCooldownMs: 86400000,
  status: 'active',
})

/**
 * 从 localStorage 读取设置。
 */
function load(): void {
  const data = loadSettings()
  defaultIntervalMs.value = data.defaultIntervalMs
  defaultJoinIntervalMs.value = data.defaultJoinIntervalMs
  failStopAfter.value = data.failStopAfter
}

/**
 * 保存设置到 localStorage。
 */
function save(): void {
  const cur = loadSettings()
  const data = {
    ...cur,
    defaultIntervalMs: Number(defaultIntervalMs.value) || 1500,
    defaultJoinIntervalMs: Number(defaultJoinIntervalMs.value) || 2000,
    failStopAfter: Math.max(0, Number(failStopAfter.value) || 0),
  }
  saveSettings(data)
  emit(
    'log',
    `设置已保存：发送间隔=${data.defaultIntervalMs}ms 加群间隔=${data.defaultJoinIntervalMs}ms 熔断=${data.failStopAfter || '关'}`,
  )
}

/**
 * 打开 GitHub Releases（开源版更新渠道）。
 */
async function openGitHubReleases(): Promise<void> {
  try {
    await openExternalUrl(GITHUB_RELEASES)
    emit('log', `已打开：${GITHUB_RELEASES}`)
  } catch (err) {
    emit('log', `打开失败：${err instanceof Error ? err.message : String(err)}`)
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
  } catch (err) {
    emit('log', `加载发送策略失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

/**
 * 编辑已有策略到表单。
 *
 * @param p 策略
 */
function editPolicy(p: MarketingSendPolicy): void {
  policyForm.value = {
    id: p.id,
    name: p.name,
    windowMinutes: p.windowMinutes,
    maxPerWindow: p.maxPerWindow,
    mutualMinIntervalMs: p.mutualMinIntervalMs,
    nonMutualMinIntervalMs: p.nonMutualMinIntervalMs,
    nonMutualDailyCap: p.nonMutualDailyCap,
    sameTargetCooldownMs: p.sameTargetCooldownMs,
    status: p.status || 'active',
  }
}

/**
 * 清空策略表单（新建）。
 */
function resetPolicyForm(): void {
  policyForm.value = {
    id: '',
    name: '默认策略',
    windowMinutes: 60,
    maxPerWindow: 5,
    mutualMinIntervalMs: 3000,
    nonMutualMinIntervalMs: 600000,
    nonMutualDailyCap: 20,
    sameTargetCooldownMs: 86400000,
    status: 'active',
  }
}

/**
 * 保存发送策略（新建或更新）。
 */
async function savePolicy(): Promise<void> {
  if (!props.sidecarOk) {
    emit('log', '本地服务未连接，无法保存策略')
    return
  }
  const name = policyForm.value.name.trim()
  if (!name) {
    emit('log', '请填写策略名称')
    return
  }
  policySaving.value = true
  try {
    const saved = await upsertMarketingSendPolicy({ ...policyForm.value, name })
    emit('log', `发送策略已保存：${saved.name}`)
    await loadSendPolicies()
    policyForm.value.id = saved.id
    policyForm.value.name = saved.name
  } catch (err) {
    emit('log', `保存策略失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    policySaving.value = false
  }
}

/**
 * 删除发送策略。
 *
 * @param id 策略 ID
 */
async function removePolicy(id: string): Promise<void> {
  if (!id) {
    return
  }
  if (!confirm('确认删除该发送策略？营销与群发入队将无法再选用它。')) {
    return
  }
  try {
    await deleteMarketingSendPolicy(id)
    if (policyForm.value.id === id) {
      resetPolicyForm()
    }
    await loadSendPolicies()
    emit('log', '发送策略已删除')
  } catch (err) {
    emit('log', `删除策略失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

watch(
  () => props.sidecarOk,
  (ok) => {
    if (ok) {
      void loadSendPolicies()
    }
  },
)

onMounted(async () => {
  load()
  try {
    localVersion.value = props.appVersion || (await getLocalAppVersion())
  } catch {
    localVersion.value = props.appVersion || '—'
  }
  void loadSendPolicies()
})
</script>

<template>
  <div class="settings-panel">
    <section class="block">
      <h3>本地执行节奏</h3>
      <p class="hint">{{ note }}</p>
      <div class="grid">
        <div class="field">
          <label>发送间隔（毫秒）</label>
          <input v-model.number="defaultIntervalMs" type="number" min="500" step="100" />
        </div>
        <div class="field">
          <label>加群间隔（毫秒）</label>
          <input v-model.number="defaultJoinIntervalMs" type="number" min="500" step="100" />
        </div>
        <div class="field">
          <label>连续失败自动停止（条）</label>
          <input v-model.number="failStopAfter" type="number" min="0" step="1" />
          <p class="hint">连续失败达到该数后暂停当前批次；填 0 表示不自动停止</p>
        </div>
      </div>
      <button type="button" class="primary" @click="save">保存到本机</button>
    </section>

    <section class="block">
      <h3>发送策略</h3>
      <p class="hint">
        营销私信与消息群发共用。每号在滑动窗口内最多发 N 条；非双向额外间隔与日上限；限速未发的保持排队，到点自动续发。
      </p>
      <p v-if="!sidecarOk" class="hint warn">本地服务未连接时无法加载/保存策略。</p>
      <div class="grid">
        <div class="field">
          <label>策略名称</label>
          <input v-model="policyForm.name" type="text" placeholder="例如：默认策略" />
        </div>
        <div class="field">
          <label>窗口分钟</label>
          <select v-model.number="policyForm.windowMinutes">
            <option :value="10">10</option>
            <option :value="30">30</option>
            <option :value="60">60</option>
            <option :value="120">120</option>
            <option :value="240">240</option>
          </select>
        </div>
        <div class="field">
          <label>每号窗口内最多条数</label>
          <input v-model.number="policyForm.maxPerWindow" type="number" min="1" max="200" />
        </div>
        <div class="field">
          <label>双向最小间隔（毫秒）</label>
          <input
            v-model.number="policyForm.mutualMinIntervalMs"
            type="number"
            min="500"
            step="500"
          />
        </div>
        <div class="field">
          <label>非双向最小间隔（毫秒）</label>
          <input
            v-model.number="policyForm.nonMutualMinIntervalMs"
            type="number"
            min="1000"
            step="1000"
          />
        </div>
        <div class="field">
          <label>非双向日上限</label>
          <input v-model.number="policyForm.nonMutualDailyCap" type="number" min="1" max="500" />
        </div>
        <div class="field">
          <label>同目标冷却（毫秒）</label>
          <input
            v-model.number="policyForm.sameTargetCooldownMs"
            type="number"
            min="0"
            step="60000"
          />
        </div>
      </div>
      <div class="actions">
        <button
          type="button"
          class="primary"
          :disabled="policySaving || !sidecarOk"
          @click="savePolicy"
        >
          {{ policyForm.id ? '更新策略' : '新建策略' }}
        </button>
        <button type="button" class="ghost" @click="resetPolicyForm">新建空白</button>
        <button
          v-if="policyForm.id"
          type="button"
          class="ghost"
          :disabled="!sidecarOk"
          @click="removePolicy(policyForm.id)"
        >
          删除当前
        </button>
        <button type="button" class="ghost" :disabled="!sidecarOk" @click="loadSendPolicies">
          刷新列表
        </button>
      </div>
      <div v-if="sendPolicies.length" class="policy-list">
        <button
          v-for="p in sendPolicies"
          :key="p.id"
          type="button"
          class="policy-chip"
          :class="{ active: policyForm.id === p.id }"
          @click="editPolicy(p)"
        >
          {{ p.name }} · {{ p.windowMinutes }}分/{{ p.maxPerWindow }}条
        </button>
      </div>
      <p v-else class="hint">暂无策略，可填写上方表单后点「新建策略」。</p>
    </section>

    <section class="block">
      <h3>应用版本</h3>
      <div class="kv">
        <span>当前版本</span>
        <code>{{ localVersion || appVersion || '—' }}</code>
      </div>
      <p class="hint">开源版请到 GitHub Releases 下载安装包，无云端强制更新门禁。</p>
      <div class="actions">
        <button type="button" class="primary" @click="openGitHubReleases">打开 GitHub Releases</button>
      </div>
    </section>

    <section class="block">
      <h3>说明</h3>
      <p class="hint">
        本工具为开源、免费、纯本地的多协议号操作辅助。账号与任务数据只留在本机；接口成功不代表对方已读；请合理设置发送间隔。
      </p>
    </section>
  </div>
</template>

<style scoped>
.settings-panel {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.block {
  border: 1px solid var(--color-border);
  border-radius: 6px;
  padding: 16px;
  background: #fff;
}

.block h3 {
  margin: 0 0 8px;
  font-size: 15px;
}

.actions {
  display: flex;
  gap: 8px;
  margin-top: 10px;
}

.hint {
  margin: 0 0 12px;
  font-size: 12px;
  color: var(--color-text-secondary);
  line-height: 1.6;
}

.field .hint {
  margin: 0;
}

.grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin-bottom: 12px;
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
.field select {
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 8px 10px;
  font: inherit;
  background: #fff;
}

.primary,
.ghost {
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
  border: 1px solid var(--color-border);
  background: #fff;
  color: var(--color-text);
}

.primary:disabled,
.ghost:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.hint.warn {
  color: var(--color-danger);
}

.policy-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 12px;
}

.policy-chip {
  border: 1px solid var(--color-border);
  background: #fff;
  border-radius: 999px;
  padding: 4px 12px;
  font: inherit;
  font-size: 12px;
  cursor: pointer;
  color: var(--color-text-secondary);
}

.policy-chip.active {
  border-color: #b7d4c4;
  background: var(--color-bg-selected);
  color: var(--color-primary);
  font-weight: 600;
}

.kv {
  display: grid;
  grid-template-columns: 100px 1fr;
  gap: 8px;
  margin-bottom: 8px;
  font-size: 13px;
}

.kv code {
  font-size: 12px;
  word-break: break-all;
  color: var(--color-text);
}

.contact-link {
  border: none;
  padding: 0;
  background: transparent;
  font: inherit;
  font-size: 13px;
  color: var(--color-primary);
  cursor: pointer;
  text-align: left;
  word-break: break-all;
  text-decoration: underline;
}

.muted {
  color: var(--color-text-secondary);
  font-size: 13px;
}
</style>
