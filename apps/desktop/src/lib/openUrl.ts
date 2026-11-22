/**
 * 在系统浏览器中打开链接（Tauri shell，失败则回退 window.open）。
 *
 * @param url 目标地址
 */
export async function openExternalUrl(url: string): Promise<void> {
  const target = url.trim()
  if (!target) {
    return
  }
  try {
    const { open } = await import('@tauri-apps/plugin-shell')
    await open(target)
  } catch {
    window.open(target, '_blank', 'noopener,noreferrer')
  }
}
