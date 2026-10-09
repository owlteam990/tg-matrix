package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tg-matrix/sidecar/internal/api"
	"github.com/tg-matrix/sidecar/internal/config"
	"github.com/tg-matrix/sidecar/internal/store"
	"github.com/tg-matrix/sidecar/internal/tgclient"
)

/**
 * sidecar 入口：加载配置、打开 SQLite、初始化 gotd 管理器、启动本地 HTTP 服务。
 *
 * @author Auto
 * @date 2026-10-08
 */
func main() {
	cfg := config.Load()

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	tgMgr := tgclient.NewManager(st, cfg.DataDir, cfg.AppID, cfg.AppHash)
	defer tgMgr.Close()

	srvAPI := &api.Server{
		Store:   st,
		TG:      tgMgr,
		DataDir: cfg.DataDir,
		Started: time.Now(),
	}

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srvAPI.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf(
			"tg-sidecar listening on http://%s (data=%s store=sqlite gotdConfigured=%v)",
			cfg.Addr, cfg.DataDir, tgMgr.Configured(),
		)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	// 等待中断信号后优雅退出
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	log.Println("tg-sidecar stopped")
}
