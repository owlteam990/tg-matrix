<script setup lang="ts">
import type { ModuleKey } from './IconRail.vue'
import type { Health } from '../api/sidecar'

defineProps<{
  module: ModuleKey
  selectedCount: number
  health: Health | null
  healthError: string
}>()

const titles: Record<ModuleKey, string> = {
  accounts: '账号',
  messages: '消息',
  groups: '群组',
  contacts: '联系人',
  scrape: '采集',
  tasks: '任务中心',
  settings: '设置',
}

const hints: Record<ModuleKey, string> = {
  accounts: '导入 Session + JSON，为每个协议号分配代理，并执行账号检查。',
  messages: '按用户名 / 手机号 / 用户 ID 发送私信。请先在中栏勾选执行账号。',
  groups: '批量加入、退出或邀请成员。',
  contacts: '添加或删除联系人。',
  scrape: '成员采集与手机号检查；结果需标注来源。',
  tasks: '查看全局任务队列、进度与导出。',
  settings: '默认节奏、代理模板、存储路径。一期无登录。',
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
        <template v-if="health?.ok">
          sidecar 已连接 · {{ health.store }} · gotd={{ health.gotdConfigured ? '已配置' : '未配置' }} · {{ health.uptime }}
        </template>
        <template v-else-if="healthError">sidecar 未连接</template>
        <template v-else>检查中…</template>
      </div>
    </header>

    <section class="panel">
      <div class="row">
        <span class="label">已选执行账号</span>
        <strong>{{ selectedCount }}</strong>
      </div>
      <p class="note">
        一期骨架：业务表单将在此区域展开。当前可验证三栏布局与本地 sidecar 连通。
      </p>
      <button type="button" class="primary" disabled>
        开始（待业务模块）
      </button>
    </section>

    <section class="log">
      <h3>本任务日志</h3>
      <div class="log-body">暂无任务日志</div>
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
  padding: 24px;
  border-bottom: 1px solid var(--color-border);
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
  margin: 0 0 16px;
  font-size: 13px;
  color: var(--color-text-secondary);
  line-height: 1.6;
}

.primary {
  border: none;
  background: var(--color-primary);
  color: #fff;
  padding: 10px 18px;
  border-radius: 4px;
  font-size: 14px;
  cursor: pointer;
}

.primary:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.log {
  flex: 1;
  padding: 16px 24px 24px;
  display: flex;
  flex-direction: column;
  min-height: 180px;
}

.log h3 {
  margin: 0 0 8px;
  font-size: 14px;
  color: var(--color-text);
}

.log-body {
  flex: 1;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  background: #fafafa;
  padding: 12px;
  font-size: 12px;
  color: var(--color-text-secondary);
  overflow: auto;
}
</style>
