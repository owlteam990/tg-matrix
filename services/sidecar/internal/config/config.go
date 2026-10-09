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

	// 3. Telegram API 凭证（未配置时 gotd 业务调用不可用，健康检查仍可用）
	appID, _ := strconv.Atoi(os.Getenv("TG_APP_ID"))
	appHash := os.Getenv("TG_APP_HASH")

	return Config{
		Addr:    addr,
		DataDir: dataDir,
		AppID:   appID,
		AppHash: appHash,
	}
}
