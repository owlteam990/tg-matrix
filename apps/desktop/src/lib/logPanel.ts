/**
 * 任务日志面板高度（全模块共用，localStorage 持久化）。
 */

export const LOG_PANEL_HEIGHT_KEY = 'tgmatrix.logPanelHeight.v1'

/** 最小高度（px） */
export const LOG_PANEL_MIN = 96

/** 默认高度（px） */
export const LOG_PANEL_DEFAULT = 180

/** 绝对上限（px）；实际还会受视口限制 */
export const LOG_PANEL_MAX = 520

/**
 * 按视口限制后的最大日志高度。
 *
 * @return px
 */
export function logPanelMaxForViewport(): number {
  if (typeof window === 'undefined') {
    return LOG_PANEL_MAX
  }
  return Math.min(LOG_PANEL_MAX, Math.floor(window.innerHeight * 0.55))
}

/**
 * 将高度限制在合法区间。
 *
 * @param px 原始高度
 * @return 合法高度
 */
export function clampLogPanelHeight(px: number): number {
  const n = Math.round(Number(px) || LOG_PANEL_DEFAULT)
  const max = logPanelMaxForViewport()
  if (n < LOG_PANEL_MIN) {
    return LOG_PANEL_MIN
  }
  if (n > max) {
    return max
  }
  return n
}

/**
 * 读取已保存的任务日志高度。
 *
 * @return px
 */
export function loadLogPanelHeight(): number {
  try {
    const raw = localStorage.getItem(LOG_PANEL_HEIGHT_KEY)
    if (raw == null || raw === '') {
      return LOG_PANEL_DEFAULT
    }
    return clampLogPanelHeight(Number(raw))
  } catch {
    return LOG_PANEL_DEFAULT
  }
}

/**
 * 保存任务日志高度（全页面统一）。
 *
 * @param px 高度
 */
export function saveLogPanelHeight(px: number): void {
  const v = clampLogPanelHeight(px)
  localStorage.setItem(LOG_PANEL_HEIGHT_KEY, String(v))
}
