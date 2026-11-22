/**
 * 桌面版本检查：平台识别、semver 比较、更新判定。
 */

export type AppUpdatePlatform = 'mac' | 'windows'

export type AppUpdateInfo = {
  platform: AppUpdatePlatform
  latestVersion: string
  minSupportedVersion?: string
  forceUpdate: boolean
  downloadUrl: string
  releaseNotes?: string
  /** 客服联系方式（如 Telegram @username） */
  supportContact?: string
  publishedAt?: string
}

export type AppUpdateDecision = {
  /** 本地版本 */
  localVersion: string
  /** 平台；linux 等为 null 表示跳过检查 */
  platform: AppUpdatePlatform | null
  /** 是否有更高版本 */
  hasUpdate: boolean
  /** 是否强制门禁 */
  force: boolean
  /** 云端信息（检查成功时） */
  remote: AppUpdateInfo | null
  /** 检查失败原因（不阻断） */
  error?: string
}

/**
 * 将客服联系方式转为可打开的外链（Telegram / http）；无法识别则返回空。
 *
 * @param raw supportContact 原文
 * @return URL 或空串
 */
export function resolveSupportContactUrl(raw: string): string {
  const s = String(raw || '').trim()
  if (!s) {
    return ''
  }
  if (/^https?:\/\//i.test(s)) {
    return s
  }
  if (/^t\.me\//i.test(s)) {
    return `https://${s}`
  }
  if (s.startsWith('@') && s.length > 1) {
    return `https://t.me/${s.slice(1)}`
  }
  // 纯用户名（无空格、无中文标点）
  if (/^[A-Za-z][\w]{3,31}$/.test(s)) {
    return `https://t.me/${s}`
  }
  return ''
}

/**
 * 解析 semver 前三段为数字。
 *
 * @param raw 版本字符串
 * @return [major, minor, patch]
 */
export function parseSemver(raw: string): [number, number, number] {
  const m = String(raw || '')
    .trim()
    .replace(/^v/i, '')
    .match(/^(\d+)\.(\d+)\.(\d+)/)
  if (!m) {
    return [0, 0, 0]
  }
  return [Number(m[1]) || 0, Number(m[2]) || 0, Number(m[3]) || 0]
}

/**
 * 比较两个 semver：a>b 返回 1，a<b 返回 -1，相等 0。
 *
 * @param a 版本 A
 * @param b 版本 B
 */
export function compareSemver(a: string, b: string): number {
  const pa = parseSemver(a)
  const pb = parseSemver(b)
  for (let i = 0; i < 3; i++) {
    if (pa[i] > pb[i]) {
      return 1
    }
    if (pa[i] < pb[i]) {
      return -1
    }
  }
  return 0
}

/**
 * 识别更新检查用的平台。
 *
 * @return mac / windows；其它平台返回 null（跳过）
 */
export function detectUpdatePlatform(): AppUpdatePlatform | null {
  const env = String(import.meta.env.TAURI_ENV_PLATFORM || '').toLowerCase()
  if (env === 'darwin' || env === 'macos' || env === 'mac') {
    return 'mac'
  }
  if (env === 'windows' || env === 'win32' || env === 'win') {
    return 'windows'
  }
  const ua = navigator.userAgent.toLowerCase()
  if (ua.includes('mac os') || ua.includes('macintosh')) {
    return 'mac'
  }
  if (ua.includes('windows')) {
    return 'windows'
  }
  return null
}

/**
 * 读取本地应用版本（Tauri getVersion，失败则回退 package 版本）。
 */
export async function getLocalAppVersion(): Promise<string> {
  try {
    const { getVersion } = await import('@tauri-apps/api/app')
    const v = await getVersion()
    if (v && String(v).trim()) {
      return String(v).trim()
    }
  } catch {
    // 非 Tauri 或未就绪
  }
  return String(import.meta.env.VITE_APP_VERSION || '0.1.0')
}

/**
 * 根据本地版本与云端信息判定是否更新/强制。
 *
 * @param localVersion 本地版本
 * @param remote 云端 data
 */
export function decideAppUpdate(
  localVersion: string,
  remote: AppUpdateInfo,
): Pick<AppUpdateDecision, 'hasUpdate' | 'force'> {
  const latest = (remote.latestVersion || '').trim()
  const hasUpdate = !!latest && compareSemver(latest, localVersion) > 0
  const belowMin =
    !!(remote.minSupportedVersion || '').trim() &&
    compareSemver(localVersion, remote.minSupportedVersion || '') < 0
  const force = !!(remote.forceUpdate || belowMin) && (hasUpdate || belowMin)
  return {
    hasUpdate: hasUpdate || belowMin,
    force,
  }
}
