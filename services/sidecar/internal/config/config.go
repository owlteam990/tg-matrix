package config

import (
	"os"
	"path/filepath"
	"strconv"
)

// Config 保存 sidecar 运行时配置。
type Config struct {
	// Addr 本地 HTTP 监听地址
	Addr string
	// DataDir 本地数据根目录（SQLite、session 等）
	DataDir string
	// WebDir 前端静态资源目录（可空）
	WebDir string
	// OpenBrowser 启动后是否打开系统浏览器
	OpenBrowser bool
	// AppID Telegram API ID
	AppID int
	// AppHash Telegram API Hash
	AppHash string
}

/**
 * 从环境变量加载配置，并补齐默认值。
 *
 * @return 配置对象
 * @author Auto
 * @date 2026-10-08
 */
func Load() Config {
	// 1. 读取监听地址，默认本机回环
	addr := os.Getenv("TG_SIDECAR_ADDR")
	if addr == "" {
		addr = "127.0.0.1:17888"
	}

	// 2. 读取数据目录，默认用户配置目录下的 tg-matrix
	dataDir := os.Getenv("TG_SIDECAR_DATA")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dataDir = filepath.Join(home, ".tg-matrix")
	}

	// 3. 静态前端目录：环境变量优先，否则尝试可执行文件旁 web/
	webDir := os.Getenv("TG_WEB_DIR")
	if webDir == "" {
		if exe, err := os.Executable(); err == nil {
			candidate := filepath.Join(filepath.Dir(exe), "web")
			if st, err := os.Stat(candidate); err == nil && st.IsDir() {
				webDir = candidate
			}
		}
	}

	// 4. Telegram API 凭证（未配置时 gotd 业务调用不可用，健康检查仍可用）
	appID, _ := strconv.Atoi(os.Getenv("TG_APP_ID"))
	appHash := os.Getenv("TG_APP_HASH")

	openBrowser := os.Getenv("TG_OPEN_BROWSER") == "1" || os.Getenv("TG_OPEN_BROWSER") == "true"

	return Config{
		Addr:        addr,
		DataDir:     dataDir,
		WebDir:      webDir,
		OpenBrowser: openBrowser,
		AppID:       appID,
		AppHash:     appHash,
	}
}
