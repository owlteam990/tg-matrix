/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_SIDECAR_URL?: string
  readonly VITE_APP_VERSION?: string
  readonly TAURI_ENV_PLATFORM?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
