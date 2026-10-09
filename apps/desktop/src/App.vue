<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import IconRail, { type ModuleKey } from './components/IconRail.vue'
import AccountPool from './components/AccountPool.vue'
import WorkArea from './components/WorkArea.vue'
import { fetchAccounts, fetchHealth, type Account, type Health } from './api/sidecar'

const activeModule = ref<ModuleKey>('accounts')
const accounts = ref<Account[]>([])
const selectedIds = ref<string[]>([])
const loadingAccounts = ref(false)
const accountsError = ref('')
const health = ref<Health | null>(null)
const healthError = ref('')
let timer: number | undefined

/**
 * 刷新 sidecar 健康状态。
 */
async function refreshHealth(): Promise<void> {
  try {
    health.value = await fetchHealth()
    healthError.value = ''
  } catch (err) {
    health.value = null
    healthError.value = err instanceof Error ? err.message : 'unknown'
  }
}

/**
 * 刷新协议号列表。
 */
async function refreshAccounts(): Promise<void> {
  loadingAccounts.value = true
  accountsError.value = ''
  try {
    accounts.value = await fetchAccounts()
  } catch (err) {
    accountsError.value = err instanceof Error ? err.message : '加载失败'
  } finally {
    loadingAccounts.value = false
  }
}

/**
 * 切换协议号勾选。
 *
 * @param id 协议号 ID
 */
function toggleAccount(id: string): void {
  if (selectedIds.value.includes(id)) {
    selectedIds.value = selectedIds.value.filter((item) => item !== id)
  } else {
    selectedIds.value = [...selectedIds.value, id]
  }
}

onMounted(async () => {
  await Promise.all([refreshHealth(), refreshAccounts()])
  timer = window.setInterval(() => {
    void refreshHealth()
  }, 5000)
})

onUnmounted(() => {
  if (timer !== undefined) {
    window.clearInterval(timer)
  }
})
</script>

<template>
  <div class="shell">
    <IconRail :active="activeModule" @select="activeModule = $event" />
    <AccountPool
      :accounts="accounts"
      :selected-ids="selectedIds"
      :loading="loadingAccounts"
      :error="accountsError"
      @toggle="toggleAccount"
      @refresh="refreshAccounts"
    />
    <WorkArea
      :module="activeModule"
      :selected-count="selectedIds.length"
      :health="health"
      :health-error="healthError"
    />
  </div>
</template>

<style scoped>
.shell {
  height: 100vh;
  display: flex;
  overflow: hidden;
  background: var(--color-bg);
}
</style>
