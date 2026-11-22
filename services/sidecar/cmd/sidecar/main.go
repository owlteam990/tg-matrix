package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/tg-matrix/sidecar/internal/api"
	"github.com/tg-matrix/sidecar/internal/config"
	"github.com/tg-matrix/sidecar/internal/marketing"
	"github.com/tg-matrix/sidecar/internal/monitor"
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

	// 启动恢复：未完成任务标 interrupted（保留 payload）；需用户在任务中心点「继续」才续跑，避免静默重发
	if n, err := st.MarkInterruptedRunning(); err != nil {
		log.Printf("mark interrupted tasks: %v", err)
	} else if n > 0 {
		log.Printf("recovered: marked %d running/pending/waiting task(s) as interrupted (resume from task center)", n)
	}

	sendSched := marketing.NewScheduler(st, tgMgr)
	sendSched.Start()
	defer sendSched.Stop()

	srvAPI := &api.Server{
		Store:     st,
		TG:        tgMgr,
		Runner:    api.NewRunner(),
		SendSched: sendSched,
		DataDir:   cfg.DataDir,
		WebDir:    cfg.WebDir,
		Started:   time.Now(),
	}

	groupMon := monitor.NewRunner(st, tgMgr)
	groupMon.Start()
	defer groupMon.Stop()

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srvAPI.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf(
			"tg-sidecar listening on http://%s (data=%s web=%s store=sqlite gotdConfigured=%v)",
			cfg.Addr, cfg.DataDir, cfg.WebDir, tgMgr.Configured(),
		)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	// 打包 App 启动时可自动打开浏览器
	if cfg.OpenBrowser {
		go func() {
			time.Sleep(400 * time.Millisecond)
			_ = openBrowser(fmt.Sprintf("http://%s/", cfg.Addr))
		}()
	}

	// 等待中断信号后优雅退出
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	log.Println("tg-sidecar stopped")
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
