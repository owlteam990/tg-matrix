<script setup lang="ts">
export type ModuleKey =
  | 'accounts'
  | 'messages'
  | 'groups'
  | 'contacts'
  | 'scrape'
  | 'tasks'
  | 'settings'

defineProps<{
  active: ModuleKey
}>()

const emit = defineEmits<{
  select: [key: ModuleKey]
}>()

const items: { key: ModuleKey; label: string }[] = [
  { key: 'accounts', label: '账号' },
  { key: 'messages', label: '消息' },
  { key: 'groups', label: '群组' },
  { key: 'contacts', label: '联系人' },
  { key: 'scrape', label: '采集' },
  { key: 'tasks', label: '任务' },
  { key: 'settings', label: '设置' },
]
</script>

<template>
  <nav class="rail" aria-label="主导航">
    <div class="brand">TG</div>
    <button
      v-for="item in items"
      :key="item.key"
      type="button"
      class="rail-item"
      :class="{ active: active === item.key }"
      @click="emit('select', item.key)"
    >
      <span class="dot" />
      <span class="label">{{ item.label }}</span>
    </button>
  </nav>
</template>

<style scoped>
.rail {
  width: var(--rail-width);
  background: var(--color-bg-rail);
  color: var(--color-text-on-dark);
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 12px 0;
  gap: 4px;
  flex-shrink: 0;
}

.brand {
  width: 36px;
  height: 36px;
  border-radius: 8px;
  background: var(--color-primary);
  display: grid;
  place-items: center;
  font-weight: 700;
  font-size: 13px;
  margin-bottom: 16px;
}

.rail-item {
  width: 56px;
  border: none;
  background: transparent;
  color: #cfcfcf;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  padding: 10px 0;
  cursor: pointer;
  border-radius: 8px;
}

.rail-item:hover {
  background: rgba(255, 255, 255, 0.08);
  color: #fff;
}

.rail-item.active {
  color: var(--color-primary);
}

.rail-item.active .dot {
  background: var(--color-primary);
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #777;
}

.label {
  font-size: 11px;
  line-height: 1;
}
</style>
