<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import type { Account, ProxyItem } from '../api/sidecar'

const props = defineProps<{
  accounts: Account[]
  selectedIds: string[]
  loading: boolean
  error: string
  proxies?: ProxyItem[]
}>()

const emit = defineEmits<{
  toggle: [id: string]
  refresh: []
  delete: [id: string]
  edit: [id: string]
}>()

type MenuState = {
  x: number
  y: number
  id: string
  label: string
}

const menu = ref<MenuState | null>(null)
const confirmDelete = ref<{ id: string; label: string } | null>(null)

/**
 * 判断协议号是否已勾选。
 *
 * @param id 协议号 ID
 * @returns 是否选中
 */
function isSelected(id: string): boolean {
  return props.selectedIds.includes(id)
}

/**
 * 协议号是否可勾选为执行账号。
 *
 * @param status 状态
 * @returns 是否可选
 */
function canSelect(status: string): boolean {
  return status === 'available' || status === 'unknown' || status === 'limited'
}

/**
 * 展示名称。
 *
 * @param account 协议号
 */
function displayName(account: Account): string {
  return account.label || account.username || account.phone || account.id
}

/**
 * 右键打开菜单。
 *
 * @param ev 鼠标事件
 * @param account 协议号
 */
function onContextMenu(ev: MouseEvent, account: Account): void {
  ev.preventDefault()
  ev.stopPropagation()
  menu.value = {
    x: ev.clientX,
    y: ev.clientY,
    id: account.id,
    label: displayName(account),
  }
}

/**
 * 关闭右键菜单。
 */
function closeMenu(): void {
  menu.value = null
}

/**
 * 右键「编辑」：打开账号页编辑区。
 */
function onMenuEdit(): void {
  if (!menu.value) {
    return
  }
  const id = menu.value.id
  closeMenu()
  emit('edit', id)
}

/**
 * 点击菜单「删除」→ 打开确认条（不用系统 confirm，Tauri 常拦截）。
 */
function onMenuDelete(): void {
  if (!menu.value) {
    return
  }
  confirmDelete.value = { id: menu.value.id, label: menu.value.label }
  closeMenu()
}

/**
 * 已绑代理展示文案。
 *
 * @param account 协议号
 */
function proxyLabel(account: Account): string {
  if (!account.proxyId) {
    return '未绑代理'
  }
  const p = (props.proxies || []).find((x) => x.id === account.proxyId)
  if (p) {
    return `${p.name || p.host}:${p.port}`
  }
  return '已绑代理'
}

/**
 * 规范化为 @username。
 *
 * @param username 用户名
 * @return 带 @ 的展示串；无则空
 */
function atUsername(username?: string): string {
  const u = (username || '').trim().replace(/^@+/, '')
  return u ? `@${u}` : ''
}

/**
 * 点击复制用户名（阻止冒泡，避免切换勾选）。
 *
 * @param ev 点击事件
 * @param username 用户名
 */
async function copyUsername(ev: MouseEvent, username?: string): Promise<void> {
  ev.preventDefault()
  ev.stopPropagation()
  const text = atUsername(username)
  if (!text) {
    return
  }
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.left = '-9999px'
    document.body.appendChild(ta)
    ta.select()
    document.execCommand('copy')
    document.body.removeChild(ta)
  }
}

/**
 * 确认删除。
 */
function confirmDoDelete(): void {
  if (!confirmDelete.value) {
    return
  }
  const id = confirmDelete.value.id
  confirmDelete.value = null
  emit('delete', id)
}

/**
 * 取消删除确认。
 */
function cancelDelete(): void {
  confirmDelete.value = null
}

/**
 * 全局点击关闭菜单。
 */
function onDocClick(): void {
  closeMenu()
}

onMounted(() => {
  document.addEventListener('click', onDocClick)
  document.addEventListener('contextmenu', onDocClick)
})

onUnmounted(() => {
  document.removeEventListener('click', onDocClick)
  document.removeEventListener('contextmenu', onDocClick)
})
</script>

<template>
  <aside class="pool">
    <header class="pool-header">
      <div>
        <h2>协议号</h2>
        <p>可多选 · 右键编辑 / 删除</p>
      </div>
      <button type="button" class="link-btn" @click="emit('refresh')">刷新</button>
    </header>

    <div v-if="confirmDelete" class="confirm-bar">
      <span>删除「{{ confirmDelete.label }}」？</span>
      <div class="confirm-actions">
        <button type="button" class="danger" @click="confirmDoDelete">删除</button>
        <button type="button" class="ghost" @click="cancelDelete">取消</button>
      </div>
    </div>

    <div v-if="loading" class="empty">加载中…</div>
    <div v-else-if="error" class="empty error">{{ error }}</div>
    <div v-else-if="accounts.length === 0" class="empty">
      暂无协议号<br />
      请先导入 .json / .session
    </div>
    <ul v-else class="list">
      <li
        v-for="account in accounts"
        :key="account.id"
        class="item"
        :class="{ selected: isSelected(account.id), disabled: !canSelect(account.status) }"
        @click="canSelect(account.status) && emit('toggle', account.id)"
        @contextmenu="onContextMenu($event, account)"
      >
        <input
          type="checkbox"
          :checked="isSelected(account.id)"
          :disabled="!canSelect(account.status)"
          @click.stop
          @change="emit('toggle', account.id)"
        />
        <div class="meta">
          <div class="title">
            {{ displayName(account) }}
            <button
              v-if="atUsername(account.username)"
              type="button"
              class="uname"
              :title="`点击复制 ${atUsername(account.username)}`"
              @click="copyUsername($event, account.username)"
            >
              {{ atUsername(account.username) }}
            </button>
          </div>
          <div class="sub">
            <span class="status">{{ account.status }}</span>
            <span v-if="account.phone"> · {{ account.phone }}</span>
            <span> · {{ proxyLabel(account) }}</span>
          </div>
        </div>
      </li>
    </ul>

    <Teleport to="body">
      <div
        v-if="menu"
        class="ctx-menu"
        :style="{ left: menu.x + 'px', top: menu.y + 'px' }"
        @click.stop
        @contextmenu.stop.prevent
      >
        <button type="button" class="ctx-item" @click.stop="onMenuEdit">编辑</button>
        <button type="button" class="ctx-item danger" @click.stop="onMenuDelete">删除</button>
      </div>
    </Teleport>
  </aside>
</template>

<style scoped>
.pool {
  width: var(--mid-width);
  background: var(--color-bg-sidebar);
  border-right: 1px solid var(--color-border);
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  position: relative;
}

.pool-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  padding: 16px;
  border-bottom: 1px solid var(--color-border);
}

.pool-header h2 {
  margin: 0;
  font-size: 16px;
  color: var(--color-text);
}

.pool-header p {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--color-text-secondary);
}

.link-btn {
  border: none;
  background: transparent;
  color: var(--color-primary);
  cursor: pointer;
  font-size: 13px;
}

.confirm-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  justify-content: space-between;
  padding: 10px 12px;
  background: #fff5f5;
  border-bottom: 1px solid #f0c0c0;
  font-size: 13px;
  color: var(--color-text);
}

.confirm-actions {
  display: flex;
  gap: 6px;
}

.confirm-actions .danger,
.confirm-actions .ghost {
  border: none;
  border-radius: 4px;
  padding: 4px 10px;
  font: inherit;
  cursor: pointer;
  font-size: 12px;
}

.confirm-actions .danger {
  background: var(--color-danger, #c0392b);
  color: #fff;
}

.confirm-actions .ghost {
  background: #fff;
  border: 1px solid var(--color-border);
  color: var(--color-text);
}

.empty {
  padding: 32px 16px;
  color: var(--color-text-secondary);
  font-size: 13px;
  line-height: 1.6;
  text-align: center;
}

.empty.error {
  color: var(--color-danger);
}

.list {
  list-style: none;
  margin: 0;
  padding: 8px;
  overflow: auto;
}

.item {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  padding: 10px;
  border-radius: 6px;
  cursor: pointer;
  user-select: none;
}

.item:hover {
  background: var(--color-bg-hover);
}

.item.selected {
  background: var(--color-bg-selected);
}

.item.disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.meta .title {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 6px;
  font-size: 14px;
  color: var(--color-text);
}

.uname {
  border: none;
  background: transparent;
  padding: 0;
  margin: 0;
  font: inherit;
  font-size: 12px;
  font-weight: 500;
  color: var(--color-primary);
  cursor: copy;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.uname:hover {
  text-decoration: underline;
}

.meta .sub {
  margin-top: 4px;
  font-size: 12px;
  color: var(--color-text-secondary);
}

.status {
  text-transform: lowercase;
}
</style>

<style>
/* 右键菜单挂到 body，避免被侧栏裁切 */
.ctx-menu {
  position: fixed;
  z-index: 9999;
  min-width: 120px;
  padding: 4px;
  background: #fff;
  border: 1px solid var(--color-border, #e5e5e5);
  border-radius: 6px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.12);
}

.ctx-menu .ctx-item {
  display: block;
  width: 100%;
  border: none;
  background: transparent;
  text-align: left;
  padding: 8px 12px;
  font: inherit;
  font-size: 13px;
  cursor: pointer;
  border-radius: 4px;
  color: var(--color-text, #222);
}

.ctx-menu .ctx-item:hover {
  background: #f5f5f5;
}

.ctx-menu .ctx-item.danger {
  color: #c0392b;
}

.ctx-menu .ctx-item.danger:hover {
  background: #fff0f0;
}
</style>
