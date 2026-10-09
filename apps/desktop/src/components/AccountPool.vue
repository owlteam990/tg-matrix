<script setup lang="ts">
import type { Account } from '../api/sidecar'

const props = defineProps<{
  accounts: Account[]
  selectedIds: string[]
  loading: boolean
  error: string
}>()

const emit = defineEmits<{
  toggle: [id: string]
  refresh: []
}>()

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
  return status === 'available' || status === 'unknown'
}
</script>

<template>
  <aside class="pool">
    <header class="pool-header">
      <div>
        <h2>协议号</h2>
        <p>可多选作为执行账号</p>
      </div>
      <button type="button" class="link-btn" @click="emit('refresh')">刷新</button>
    </header>

    <div v-if="loading" class="empty">加载中…</div>
    <div v-else-if="error" class="empty error">{{ error }}</div>
    <div v-else-if="accounts.length === 0" class="empty">
      暂无协议号<br />
      请先导入 Session + JSON
    </div>
    <ul v-else class="list">
      <li
        v-for="account in accounts"
        :key="account.id"
        class="item"
        :class="{ selected: isSelected(account.id), disabled: !canSelect(account.status) }"
        @click="canSelect(account.status) && emit('toggle', account.id)"
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
            {{ account.label || account.username || account.phone || account.id }}
          </div>
          <div class="sub">
            <span class="status">{{ account.status }}</span>
            <span v-if="account.proxyId">· 已绑代理</span>
            <span v-else>· 未绑代理</span>
          </div>
        </div>
      </li>
    </ul>
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
  font-size: 14px;
  color: var(--color-text);
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
