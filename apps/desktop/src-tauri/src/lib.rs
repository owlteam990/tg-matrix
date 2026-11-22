use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;
use std::thread;
use std::time::Duration;

use tauri::{Manager, RunEvent};

struct SidecarState(Mutex<Option<Child>>);

/**
 * 将文本写入用户在「另存为」中选择的路径。
 *
 * @param path 绝对路径
 * @param contents UTF-8 文本（可含 BOM）
 */
#[tauri::command]
fn save_text_file(path: String, contents: String) -> Result<(), String> {
    let p = Path::new(&path);
    if path.trim().is_empty() {
        return Err("path required".into());
    }
    if let Some(parent) = p.parent() {
        if !parent.as_os_str().is_empty() {
            std::fs::create_dir_all(parent).map_err(|e| format!("create dir: {e}"))?;
        }
    }
    std::fs::write(p, contents.as_bytes()).map_err(|e| format!("write file: {e}"))
}

/**
 * 解析 tg-sidecar 可执行文件路径（开发态与打包态）。
 * Windows 打包产物带 .exe 后缀，一并探测。
 */
fn resolve_sidecar_path(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    #[cfg(windows)]
    const SIDECAR_NAMES: &[&str] = &["tg-sidecar.exe", "tg-sidecar"];
    #[cfg(not(windows))]
    const SIDECAR_NAMES: &[&str] = &["tg-sidecar"];

    // 1. 打包后：Resources 内
    if let Ok(resource_dir) = app.path().resource_dir() {
        for name in SIDECAR_NAMES {
            let candidates = [
                resource_dir.join(name),
                resource_dir.join("binaries").join(name),
            ];
            for path in candidates {
                if path.exists() {
                    return Ok(path);
                }
            }
        }
    }

    // 2. 开发态：仓库 dist/sidecar 或 services 构建产物
    let manifest_dir = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    for name in SIDECAR_NAMES {
        let repo_candidates = [
            manifest_dir
                .join("../../..")
                .join("dist")
                .join("sidecar")
                .join(name),
            manifest_dir
                .join("../../..")
                .join("services")
                .join("sidecar")
                .join("bin")
                .join(name),
        ];
        for path in repo_candidates {
            if let Ok(canonical) = path.canonicalize() {
                if canonical.exists() {
                    return Ok(canonical);
                }
            }
        }
    }

    Err("找不到 tg-sidecar，请先执行 ./scripts/build.sh、scripts/release.ps1 或 npm run build:app".into())
}

/**
 * 释放 17888 上残留的监听进程，避免「端口占用 → sidecar 立即退出 → 服务未连接」。
 */
fn free_sidecar_port() {
    let _ = Command::new("lsof")
        .args(["-nP", "-tiTCP:17888", "-sTCP:LISTEN"])
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .output()
        .ok()
        .and_then(|out| {
            let pids = String::from_utf8_lossy(&out.stdout);
            for pid in pids.split_whitespace() {
                let _ = Command::new("kill").args(["-9", pid]).status();
            }
            Some(())
        });
    thread::sleep(Duration::from_millis(200));
}

/**
 * 启动 Go sidecar（仅 API，不打开浏览器）。
 */
fn start_sidecar(app: &tauri::AppHandle) -> Result<(), String> {
    let sidecar = resolve_sidecar_path(app)?;
    let data_dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("app data dir: {e}"))?;
    std::fs::create_dir_all(&data_dir).map_err(|e| format!("create data dir: {e}"))?;

    // 先清掉僵尸监听，再拉起新进程
    free_sidecar_port();

    let mut child = Command::new(&sidecar)
        .env("TG_SIDECAR_ADDR", "127.0.0.1:17888")
        .env("TG_SIDECAR_DATA", &data_dir)
        .env("TG_OPEN_BROWSER", "0")
        // 桌面 App 由 WebView 渲染 UI，无需托管静态站
        .env_remove("TG_WEB_DIR")
        // 避免继承系统 HTTP_PROXY，导致本机 API 被代理劫持超时
        .env_remove("HTTP_PROXY")
        .env_remove("HTTPS_PROXY")
        .env_remove("http_proxy")
        .env_remove("https_proxy")
        .env_remove("ALL_PROXY")
        .env_remove("all_proxy")
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .map_err(|e| format!("启动 sidecar 失败 ({}): {e}", sidecar.display()))?;

    // 短暂等待端口就绪
    thread::sleep(Duration::from_millis(400));
    if let Ok(Some(status)) = child.try_wait() {
        return Err(format!("sidecar 立即退出: {status}"));
    }

    let state = app.state::<SidecarState>();
    *state.0.lock().map_err(|_| "sidecar state lock")? = Some(child);
    Ok(())
}

/**
 * 停止 sidecar 子进程。
 */
fn stop_sidecar(app: &tauri::AppHandle) {
    if let Ok(mut guard) = app.state::<SidecarState>().0.lock() {
        if let Some(mut child) = guard.take() {
            let _ = child.kill();
            let _ = child.wait();
        }
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .manage(SidecarState(Mutex::new(None)))
        .invoke_handler(tauri::generate_handler![save_text_file])
        .setup(|app| {
            if let Err(err) = start_sidecar(app.handle()) {
                eprintln!("tg-matrix: {err}");
                // 开发态允许 UI 先起来，方便排查；正式包也显示错误窗口
            }
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(|app_handle, event| {
            if let RunEvent::Exit = event {
                stop_sidecar(app_handle);
            }
        });
}
