<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  bindAccountProxy,
  checkAccount,
  checkSpamBot,
  createProxy,
  deleteAccount,
  fetchProxies,
  importAccount,
  updateAccount,
  type Account,
  type ProxyItem,
} from '../api/sidecar'

const props = defineProps<{
  accounts: Account[]
  selectedIds: string[]
  gotdConfigured: boolean
  /** 中栏右键「编辑」聚焦的协议号 */
  focusAccountId?: string
}>()

const emit = defineEmits<{
  refresh: []
  log: [line: string]
}>()

const label = ref('')
const pickedNames = ref<string[]>([])
const pendingFiles = ref<{ name: string; text?: string; base64?: string }[]>([])
const importing = ref(false)

const proxies = ref<ProxyItem[]>([])
const proxyHost = ref('')
const proxyPort = ref(1080)
const proxyUser = ref('')
const proxyPass = ref('')
const proxyName = ref('')
const proxyProtocol = ref('socks5')
const busy = ref(false)

/** 编辑中的协议号（优先 focus，否则取已选第一个） */
const editingId = ref('')
const editLabel = ref('')
const editProxyId = ref('')

const editingAccount = computed(() => props.accounts.find((a) => a.id === editingId.value) || null)

/**
 * 同步编辑表单到指定协议号。
 *
 * @param id 协议号 ID
 */
function loadEditForm(id: string): void {
  const acc = props.accounts.find((a) => a.id === id)
  if (!acc) {
    editingId.value = ''
    return
  }
  editingId.value = acc.id
  editLabel.value = acc.label || ''
  editProxyId.value = acc.proxyId || ''
}

watch(
  () => props.focusAccountId,
  (id) => {
    if (id) {
      loadEditForm(id)
    }
  },
)

watch(
  () => props.selectedIds.slice(),
  (ids) => {
    if (ids.length === 1) {
      loadEditForm(ids[0])
    } else if (ids.length === 0) {
      editingId.value = ''
    } else if (editingId.value && !ids.includes(editingId.value)) {
      loadEditForm(ids[0])
    }
  },
)

watch(
  () => props.accounts,
  () => {
    if (editingId.value) {
      loadEditForm(editingId.value)
    }
  },
)

/**
 * 刷新代理列表。
 */
async function refreshProxies(): Promise<void> {
  proxies.value = await fetchProxies()
}

/**
 * 文件转 base64（用于 .session 二进制）。
 *
 * @param file 文件
 * @return base64 字符串
 */
function fileToBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const result = String(reader.result || '')
      const idx = result.indexOf(',')
      resolve(idx >= 0 ? result.slice(idx + 1) : result)
    }
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}

/**
 * 选择 .json / .session（可多选，同名自动配对）。
 */
async function onPickFiles(ev: Event): Promise<void> {
  const input = ev.target as HTMLInputElement
  const list = input.files
  if (!list || list.length === 0) {
    return
  }
  const files: { name: string; text?: string; base64?: string }[] = []
  for (const file of Array.from(list)) {
    const lower = file.name.toLowerCase()
    if (lower.endsWith('.session')) {
      files.push({ name: file.name, base64: await fileToBase64(file) })
    } else {
      files.push({ name: file.name, text: await file.text() })
    }
  }
  pendingFiles.value = files
  pickedNames.value = files.map((f) => f.name)
  if (!label.value && files.length === 1) {
    label.value = files[0].name.replace(/\.(json|session)$/i, '')
  }
  emit('log', `已选择 ${files.length} 个文件：${pickedNames.value.join(', ')}`)
  input.value = ''
}

/**
 * 导入协议号（.json / .session）。
 */
async function onImport(): Promise<void> {
  if (pendingFiles.value.length === 0) {
    emit('log', '请选择 .json 和/或 .session 文件（可成对多选）')
    return
  }
  importing.value = true
  try {
    const result = await importAccount({
      label: label.value.trim() || undefined,
      files: pendingFiles.value,
    })
    if (result.items && result.items.length > 0) {
      for (const item of result.items) {
        if (item.ok && item.account) {
          emit(
            'log',
            item.created
              ? `导入成功：${item.account.label} (${item.account.id}) · ${item.account.phone || ''} · ${item.source || ''}`
              : `已存在，跳过：${item.account.id}`,
          )
        } else {
          emit('log', `导入失败：${item.file || ''} · ${item.error || 'unknown'}`)
        }
      }
      emit('log', result.note || '')
    } else if (result.account) {
      emit(
        'log',
        result.created
          ? `导入成功：${result.account.label} (${result.account.id})`
          : `已存在，跳过重复导入：${result.account.id}`,
      )
    }
    pendingFiles.value = []
    pickedNames.value = []
    label.value = ''
    emit('refresh')
  } catch (err) {
    emit('log', `导入失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    importing.value = false
  }
}

/**
 * 保存协议号备注，并附加/更换已保存代理。
 */
async function onSaveEdit(): Promise<void> {
  if (!editingId.value) {
    emit('log', '请先在中栏勾选或右键「编辑」一个协议号')
    return
  }
  busy.value = true
  try {
    const acc = await updateAccount(editingId.value, {
      label: editLabel.value.trim(),
      proxyId: editProxyId.value,
    })
    const proxyHint = acc.proxyId
      ? proxies.value.find((p) => p.id === acc.proxyId)
      : null
    emit(
      'log',
      `已更新协议号：${acc.label} (${acc.id})` +
        (proxyHint
          ? ` · 代理 ${proxyHint.name} ${proxyHint.host}:${proxyHint.port}`
          : ' · 未绑代理'),
    )
    emit('refresh')
  } catch (err) {
    emit('log', `保存失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    busy.value = false
  }
}

/**
 * 仅附加已保存代理到当前编辑号。
 *
 * @param proxyId 代理 ID
 */
async function onAttachProxyToEditing(proxyId: string): Promise<void> {
  if (!editingId.value) {
    emit('log', '请先选择要附加代理的协议号')
    return
  }
  editProxyId.value = proxyId
  busy.value = true
  try {
    await updateAccount(editingId.value, { proxyId })
    emit('log', `已将代理附加到 ${editingId.value}`)
    emit('refresh')
  } catch (err) {
    emit('log', `附加失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    busy.value = false
  }
}

/**
 * 创建代理。
 */
async function onCreateProxy(): Promise<void> {
  if (!proxyHost.value.trim() || !proxyPort.value) {
    emit('log', '请填写代理主机与端口')
    return
  }
  busy.value = true
  try {
    const item = await createProxy({
      name: proxyName.value.trim() || undefined,
      protocol: proxyProtocol.value,
      host: proxyHost.value.trim(),
      port: Number(proxyPort.value),
      username: proxyUser.value.trim() || undefined,
      password: proxyPass.value || undefined,
    })
    emit('log', `代理已创建：${item.name} (${item.host}:${item.port})`)
    proxyHost.value = ''
    proxyUser.value = ''
    proxyPass.value = ''
    proxyName.value = ''
    await refreshProxies()
  } catch (err) {
    emit('log', `创建代理失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    busy.value = false
  }
}

/**
 * 给已选协议号绑定代理。
 */
async function onBindProxy(proxyId: string): Promise<void> {
  if (props.selectedIds.length === 0) {
    emit('log', '请先在中栏勾选协议号')
    return
  }
  busy.value = true
  try {
    for (const id of props.selectedIds) {
      await bindAccountProxy(id, proxyId)
      emit('log', `已绑定代理到 ${id}`)
    }
    emit('refresh')
  } catch (err) {
    emit('log', `绑定失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    busy.value = false
  }
}

/**
 * 解绑已选协议号代理。
 */
async function onUnbindProxy(): Promise<void> {
  if (props.selectedIds.length === 0) {
    emit('log', '请先勾选协议号')
    return
  }
  busy.value = true
  try {
    for (const id of props.selectedIds) {
      await bindAccountProxy(id, '')
      emit('log', `已解绑代理：${id}`)
    }
    emit('refresh')
  } catch (err) {
    emit('log', `解绑失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    busy.value = false
  }
}

/**
 * 检查已选协议号。
 */
async function onCheckSelected(): Promise<void> {
  if (props.selectedIds.length === 0) {
    emit('log', '请先勾选协议号')
    return
  }
  if (!props.gotdConfigured) {
    emit('log', '未配置 TG_APP_ID / TG_APP_HASH，无法检查')
    return
  }
  busy.value = true
  try {
    for (const id of props.selectedIds) {
      const result = await checkAccount(id)
      if (result.ok) {
        emit('log', `检查通过：${id} @${result.username || ''} ${result.phone || ''}`)
      } else {
        emit('log', `检查失败：${id} → ${result.error || 'unknown'}`)
      }
    }
    emit('refresh')
  } catch (err) {
    emit('log', `检查异常：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    busy.value = false
  }
}

/**
 * SpamBot 检查已选协议号（F12）。
 */
async function onSpamBotSelected(): Promise<void> {
  if (props.selectedIds.length === 0) {
    emit('log', '请先勾选协议号')
    return
  }
  if (!props.gotdConfigured) {
    emit('log', '未配置 TG_APP_ID / TG_APP_HASH，无法检查')
    return
  }
  busy.value = true
  try {
    for (const id of props.selectedIds) {
      emit('log', `SpamBot 检查中：${id}（约数秒）`)
      const result = await checkSpamBot(id)
      const taskHint = result.taskId ? ` · task=${result.taskId}` : ''
      if (result.status === 'good' || result.status === 'limited' || result.status === 'unknown') {
        emit(
          'log',
          `SpamBot：${id} → ${result.status}${result.snippet ? ' · ' + result.snippet.slice(0, 80) : ''}${taskHint}`,
        )
      } else {
        emit(
          'log',
          `SpamBot 失败：${id} → ${result.status} ${result.errorCode || ''} ${result.errorMessage || ''}${taskHint}`,
        )
      }
    }
    emit('refresh')
  } catch (err) {
    emit('log', `SpamBot 异常：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    busy.value = false
  }
}

/**
 * 删除已选协议号（不用系统 confirm：Tauri WebView 常拦截导致「无反应」）。
 */
async function onDeleteSelected(): Promise<void> {
  if (props.selectedIds.length === 0) {
    emit('log', '请先在中栏勾选协议号，或在中栏右键 → 删除')
    return
  }
  const ids = [...props.selectedIds]
  if (!window.confirm(`确认删除选中的 ${ids.length} 个协议号？\n（也可在中栏对单个协议号右键删除）`)) {
    emit('log', '已取消删除')
    return
  }
  busy.value = true
  try {
    for (const id of ids) {
      await deleteAccount(id)
      emit('log', `已删除：${id}`)
    }
    emit('refresh')
  } catch (err) {
    emit('log', `删除失败：${err instanceof Error ? err.message : String(err)}`)
  } finally {
    busy.value = false
  }
}

onMounted(() => {
  void refreshProxies().catch((err) => {
    emit('log', `加载代理失败：${err instanceof Error ? err.message : String(err)}`)
  })
})
</script>

<template>
  <div class="accounts-panel">
    <section class="block edit-block">
      <h3>编辑协议号</h3>
      <p v-if="!editingAccount" class="hint">在中栏勾选一个协议号，或右键 → 编辑</p>
      <template v-else>
        <p class="hint">
          正在编辑：{{ editingAccount.label || editingAccount.id }}
          <span v-if="editingAccount.phone"> · {{ editingAccount.phone }}</span>
          · id={{ editingAccount.id }}
        </p>
        <div class="grid">
          <div class="field">
            <label>备注名</label>
            <input v-model="editLabel" type="text" placeholder="显示名称" />
          </div>
          <div class="field">
            <label>附加已保存代理</label>
            <select v-model="editProxyId">
              <option value="">不使用代理</option>
              <option v-for="p in proxies" :key="p.id" :value="p.id">
                {{ p.name }} · {{ p.protocol }}://{{ p.host }}:{{ p.port }}
              </option>
            </select>
          </div>
        </div>
        <div class="actions">
          <button type="button" class="primary" :disabled="busy" @click="onSaveEdit">
            保存修改
          </button>
          <button
            type="button"
            class="ghost"
            :disabled="busy || !editProxyId"
            @click="editProxyId = ''; onSaveEdit()"
          >
            解绑代理
          </button>
        </div>
      </template>
    </section>

    <section class="block">
      <h3>导入协议号</h3>
      <p class="hint">
        选择常见协议号包中的 <code>.json</code> 与 <code>.session</code>（可多选成对导入）。仅
        .json（含 session_string）或仅 .session 也可。重复按手机号 ID 跳过。
      </p>
      <div class="field">
        <label>备注名（可选，单号导入时生效）</label>
        <input v-model="label" type="text" placeholder="默认用姓名 / 手机号" />
      </div>
      <div class="field">
        <label>已选文件</label>
        <p class="picked">{{ pickedNames.length ? pickedNames.join('、') : '尚未选择' }}</p>
      </div>
      <div class="actions">
        <label class="file-btn">
          选择 .json / .session
          <input
            type="file"
            accept=".json,.session,application/json"
            multiple
            hidden
            @change="onPickFiles"
          />
        </label>
        <button
          type="button"
          class="primary"
          :disabled="importing || pendingFiles.length === 0"
          @click="onImport"
        >
          {{ importing ? '导入中…' : '导入协议号' }}
        </button>
      </div>
    </section>

    <section class="block">
      <h3>代理库（可附加到协议号）</h3>
      <p class="hint">先添加代理，再在上方「编辑协议号」中选择附加；或点「附加到当前」/「绑到已选」。</p>
      <div class="grid">
        <div class="field">
          <label>名称</label>
          <input v-model="proxyName" type="text" placeholder="可选" />
        </div>
        <div class="field">
          <label>协议</label>
          <select v-model="proxyProtocol">
            <option value="socks5">socks5</option>
            <option value="socks4">socks4</option>
          </select>
        </div>
        <div class="field">
          <label>主机</label>
          <input v-model="proxyHost" type="text" placeholder="1.2.3.4" />
        </div>
        <div class="field">
          <label>端口</label>
          <input v-model.number="proxyPort" type="number" min="1" max="65535" />
        </div>
        <div class="field">
          <label>用户名</label>
          <input v-model="proxyUser" type="text" placeholder="可选" />
        </div>
        <div class="field">
          <label>密码</label>
          <input v-model="proxyPass" type="password" placeholder="可选" />
        </div>
      </div>
      <div class="actions">
        <button type="button" class="primary" :disabled="busy" @click="onCreateProxy">添加代理</button>
        <button type="button" class="ghost" :disabled="busy" @click="onUnbindProxy">解绑已选</button>
      </div>
      <ul v-if="proxies.length" class="proxy-list">
        <li v-for="p in proxies" :key="p.id">
          <span>{{ p.name }} · {{ p.protocol }}://{{ p.host }}:{{ p.port }}</span>
          <div class="proxy-actions">
            <button
              type="button"
              class="link"
              :disabled="busy || !editingId"
              @click="onAttachProxyToEditing(p.id)"
            >
              附加到当前
            </button>
            <button type="button" class="link" :disabled="busy" @click="onBindProxy(p.id)">
              绑到已选
            </button>
          </div>
        </li>
      </ul>
      <p v-else class="hint">暂无已保存代理</p>
    </section>

    <section class="block">
      <h3>已选协议号操作</h3>
      <p class="hint">当前已选 {{ selectedIds.length }} 个</p>
      <div class="actions">
        <button type="button" class="primary" :disabled="busy" @click="onCheckSelected">
          账号检查 (F11)
        </button>
        <button type="button" class="ghost" :disabled="busy" @click="onSpamBotSelected">
          SpamBot (F12)
        </button>
        <button type="button" class="danger" :disabled="busy" @click="onDeleteSelected">
          删除已选
        </button>
      </div>
      <p class="hint">SpamBot 结果为 good / limited / unknown / timeout；检查通过 ≠ 全能操作。</p>
      <p v-if="!gotdConfigured" class="warn">
        未检测到 API 凭证：请导入带 api_id/api_hash 的 .json，或设置 TG_APP_ID / TG_APP_HASH。
      </p>
    </section>
  </div>
</template>

<style scoped>
.accounts-panel {
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
  margin: 0 0 6px;
  font-size: 15px;
}

.hint {
  margin: 0 0 12px;
  font-size: 12px;
  color: var(--color-text-secondary);
  line-height: 1.5;
}

.hint code {
  font-size: 12px;
}

.picked {
  margin: 0;
  padding: 8px 10px;
  border: 1px dashed var(--color-border);
  border-radius: 4px;
  font-size: 12px;
  color: var(--color-text-secondary);
  word-break: break-all;
  min-height: 36px;
}

.warn {
  margin: 10px 0 0;
  font-size: 12px;
  color: #c98900;
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
}

.grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px 12px;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
}

.primary,
.ghost,
.danger,
.file-btn,
.link {
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

.danger {
  background: #ffecec;
  color: var(--color-danger);
}

.file-btn {
  background: var(--color-bg-sidebar);
  color: var(--color-text);
  display: inline-block;
}

.link {
  background: transparent;
  color: var(--color-primary);
  padding: 4px 8px;
}

.primary:disabled,
.ghost:disabled,
.danger:disabled,
.link:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.proxy-list {
  list-style: none;
  margin: 12px 0 0;
  padding: 0;
}

.proxy-list li {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  padding: 8px 0;
  border-top: 1px solid var(--color-border);
  font-size: 13px;
}

.proxy-actions {
  display: flex;
  gap: 4px;
  flex-shrink: 0;
}

.edit-block {
  border-color: #b7e0c2;
  background: #f7fcf8;
}
</style>
