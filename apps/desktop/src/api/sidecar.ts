const BASE = import.meta.env.VITE_SIDECAR_URL ?? 'http://127.0.0.1:17888'

export type Health = {
  ok: boolean
  service: string
  dataDir: string
  uptime: string
  store: string
  gotdConfigured: boolean
}

export type Account = {
  id: string
  label: string
  phone: string
  username: string
  status: string
  proxyId: string | null
}

/**
 * 拉取 sidecar 健康状态。
 *
 * @returns 健康检查结果
 * @author Auto
 * @date 2026-10-08
 */
export async function fetchHealth(): Promise<Health> {
  const res = await fetch(`${BASE}/health`)
  if (!res.ok) {
    throw new Error(`health failed: ${res.status}`)
  }
  return res.json()
}

/**
 * 拉取本地协议号列表。
 *
 * @returns 协议号数组
 * @author Auto
 * @date 2026-10-08
 */
export async function fetchAccounts(): Promise<Account[]> {
  const res = await fetch(`${BASE}/v1/accounts`)
  if (!res.ok) {
    throw new Error(`accounts failed: ${res.status}`)
  }
  const body = (await res.json()) as { items: Account[] }
  return body.items ?? []
}
