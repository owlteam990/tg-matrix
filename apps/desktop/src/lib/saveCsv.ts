/**
 * 将 CSV 保存到用户选择的本地路径（Tauri 另存为；非桌面环境回退浏览器下载）。
 *
 * @param defaultFilename 默认文件名，如 scrape-123.csv
 * @param header 表头
 * @param lines 数据行
 * @return 保存结果：saved / cancelled / 浏览器下载路径说明
 */
export async function saveCsv(
  defaultFilename: string,
  header: string[],
  lines: string[][],
): Promise<{ status: 'saved' | 'cancelled' | 'browser'; path?: string }> {
  const escape = (cell: string) => {
    const v = cell ?? ''
    if (/[",\n]/.test(v)) {
      return `"${v.replace(/"/g, '""')}"`
    }
    return v
  }
  const body = [header, ...lines].map((row) => row.map(escape).join(',')).join('\n')
  const contents = '\uFEFF' + body

  // 桌面 App：系统另存为
  try {
    const { save } = await import('@tauri-apps/plugin-dialog')
    const { invoke } = await import('@tauri-apps/api/core')
    const path = await save({
      title: '导出 CSV',
      defaultPath: defaultFilename,
      filters: [{ name: 'CSV', extensions: ['csv'] }],
    })
    if (!path) {
      return { status: 'cancelled' }
    }
    const finalPath = path.toLowerCase().endsWith('.csv') ? path : `${path}.csv`
    await invoke('save_text_file', { path: finalPath, contents })
    return { status: 'saved', path: finalPath }
  } catch {
    // 非 Tauri / 插件未就绪：回退浏览器下载（通常进系统「下载」文件夹）
  }

  const blob = new Blob([contents], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = defaultFilename
  a.click()
  URL.revokeObjectURL(url)
  return { status: 'browser', path: defaultFilename }
}

/**
 * 将纯文本保存到用户选择的本地路径（日志导出等）。
 *
 * @param defaultFilename 默认文件名
 * @param contents 文本内容
 * @param title 另存为对话框标题
 */
export async function saveTextFile(
  defaultFilename: string,
  contents: string,
  title = '导出文本',
): Promise<{ status: 'saved' | 'cancelled' | 'browser'; path?: string }> {
  const body = contents.endsWith('\n') ? contents : `${contents}\n`
  try {
    const { save } = await import('@tauri-apps/plugin-dialog')
    const { invoke } = await import('@tauri-apps/api/core')
    const ext = defaultFilename.includes('.')
      ? defaultFilename.split('.').pop() || 'txt'
      : 'txt'
    const path = await save({
      title,
      defaultPath: defaultFilename,
      filters: [{ name: 'File', extensions: [ext, 'txt', 'log', 'csv'] }],
    })
    if (!path) {
      return { status: 'cancelled' }
    }
    const lower = path.toLowerCase()
    const finalPath =
      lower.endsWith('.txt') ||
      lower.endsWith('.log') ||
      lower.endsWith('.csv') ||
      lower.endsWith(`.${ext}`)
        ? path
        : `${path}.${ext}`
    await invoke('save_text_file', { path: finalPath, contents: body })
    return { status: 'saved', path: finalPath }
  } catch {
    // 非 Tauri：浏览器下载
  }
  const blob = new Blob([body], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = defaultFilename
  a.click()
  URL.revokeObjectURL(url)
  return { status: 'browser', path: defaultFilename }
}
