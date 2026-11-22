/**
 * 本机设置（localStorage）。
 */

export const SETTINGS_KEY = 'tgmatrix.settings.v1'

export type AppSettings = {
  /** 默认发送 / 检查间隔（毫秒） */
  defaultIntervalMs: number
  /** 默认加群 / 邀请间隔（毫秒） */
  defaultJoinIntervalMs: number
  /**
   * 连续失败熔断次数；默认 5。
   * 传给 API 时：正数生效；若用户设为 0 则发 -1 关闭熔断。
   */
  failStopAfter: number
}

const DEFAULTS: AppSettings = {
  defaultIntervalMs: 1500,
  defaultJoinIntervalMs: 2000,
  failStopAfter: 5,
}

/**
 * 读取本机设置。
 *
 * @return 设置对象
 */
export function loadSettings(): AppSettings {
  try {
    const raw = localStorage.getItem(SETTINGS_KEY)
    if (!raw) {
      return { ...DEFAULTS }
    }
    const data = JSON.parse(raw) as Partial<AppSettings>
    return {
      defaultIntervalMs: Number(data.defaultIntervalMs) || DEFAULTS.defaultIntervalMs,
      defaultJoinIntervalMs: Number(data.defaultJoinIntervalMs) || DEFAULTS.defaultJoinIntervalMs,
      failStopAfter:
        data.failStopAfter === 0
          ? 0
          : Number(data.failStopAfter) || DEFAULTS.failStopAfter,
    }
  } catch {
    return { ...DEFAULTS }
  }
}

/**
 * 写入本机设置。
 *
 * @param data 完整设置
 */
export function saveSettings(data: AppSettings): void {
  localStorage.setItem(SETTINGS_KEY, JSON.stringify(data))
}

/**
 * 转为 API 的 failStopAfter：0 → -1（关闭）；其余原样。
 *
 * @param v 设置值
 * @return API 参数
 */
export function failStopAfterForApi(v: number): number {
  if (v <= 0) {
    return -1
  }
  return v
}
