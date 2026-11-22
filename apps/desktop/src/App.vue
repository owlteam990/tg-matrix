<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import IconRail, { type ModuleKey } from './components/IconRail.vue'
import AccountPool from './components/AccountPool.vue'
import WorkArea from './components/WorkArea.vue'
import {
  deleteAccount,
  fetchAccounts,
  fetchHealth,
  fetchProxies,
  type Account,
  type Health,
  type ProxyItem,
} from './api/sidecar'
import { getLocalAppVersion } from './lib/appUpdate'

const activeModule = ref<ModuleKey>('accounts')
const accounts = ref<Account[]>([])
const selectedIds = ref<string[]>([])
const proxies = ref<ProxyItem[]>([])
const focusAccountId = ref('')
const loadingAccounts = ref(false)
const accountsError = ref('')
const health = ref<Health | null>(null)
const healthError = ref('')
const localVersion = ref('0.1.0')
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
    const alive = new Set(accounts.value.map((a) => a.id))
    selectedIds.value = selectedIds.value.filter((id) => alive.has(id))
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

/**
 * 删除单个协议号（中栏右键）。
 *
 * @param id 协议号 ID
 */
async function onDeleteAccount(id: string): Promise<void> {
  try {
    await deleteAccount(id)
    selectedIds.value = selectedIds.value.filter((item) => item !== id)
    if (focusAccountId.value === id) {
      focusAccountId.value = ''
    }
    await refreshAccounts()
  } catch (err) {
    accountsError.value = err instanceof Error ? err.message : '删除失败'
  }
}

/**
 * 中栏右键编辑：切到账号页并聚焦该协议号。
 *
 * @param id 协议号 ID
 */
function onEditAccount(id: string): void {
  activeModule.value = 'accounts'
  selectedIds.value = [id]
  focusAccountId.value = id
}

/**
 * 刷新代理列表（中栏展示已绑代理名）。
 */
async function refreshProxies(): Promise<void> {
  try {
    proxies.value = await fetchProxies()
  } catch {
    // 忽略：列表仍可显示「已绑代理」
  }
}

async function refreshAll(): Promise<void> {
  await Promise.all([refreshAccounts(), refreshProxies()])
}

/**
 * 启动业务壳：健康检查与账号刷新。
 */
async function bootstrapShell(): Promise<void> {
  localVersion.value = await getLocalAppVersion()
  await Promise.all([refreshHealth(), refreshAccounts(), refreshProxies()])
  if (timer !== undefined) {
    window.clearInterval(timer)
  }
  timer = window.setInterval(() => {
    void refreshHealth()
  }, 5000)
}

onMounted(() => {
  void bootstrapShell()
})

onUnmounted(() => {
  if (timer !== undefined) {
    window.clearInterval(timer)
  }
})
</script>

<template>
  <div class="shell">
    <div class="shell-body">
      <IconRail :active="activeModule" @select="activeModule = $event" />
      <AccountPool
        :accounts="accounts"
        :selected-ids="selectedIds"
        :loading="loadingAccounts"
        :error="accountsError"
        :proxies="proxies"
        @toggle="toggleAccount"
        @refresh="refreshAll"
        @delete="onDeleteAccount"
        @edit="onEditAccount"
      />
      <WorkArea
        :module="activeModule"
        :accounts="accounts"
        :selected-ids="selectedIds"
        :focus-account-id="focusAccountId"
        :health="health"
        :health-error="healthError"
        :app-version="localVersion"
        @refresh="refreshAll"
      />
    </div>
  </div>
</template>

<style scoped>
.shell {
  height: 100vh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--color-bg);
}

.shell-body {
  flex: 1;
  min-height: 0;
  display: flex;
  overflow: hidden;
}
</style>
