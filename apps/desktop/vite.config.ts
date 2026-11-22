import vue from '@vitejs/plugin-vue'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'

const host = process.env.TAURI_DEV_HOST

const pkgVersion = (() => {
  try {
    const dir = dirname(fileURLToPath(import.meta.url))
    const pkg = JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8')) as { version?: string }
    return pkg.version || '0.1.0'
  } catch {
    return '0.1.0'
  }
})()

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue()],
  clearScreen: false,
  define: {
    'import.meta.env.VITE_APP_VERSION': JSON.stringify(pkgVersion),
  },
  server: {
    port: 1420,
    strictPort: true,
    host: host || false,
    hmr: host
      ? {
          protocol: 'ws',
          host,
          port: 1421,
        }
      : undefined,
    watch: {
      ignored: ['**/src-tauri/**'],
    },
  },
  envPrefix: ['VITE_', 'TAURI_'],
})
