package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

/**
 * macOS .app 启动器：定位 Resources、设置环境变量、拉起 sidecar 并打开浏览器。
 *
 * @author Auto
 * @date 2026-10-09
 */
func main() {
	exe, err := os.Executable()
	if err != nil {
		fatal("resolve executable: %v", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		fatal("eval symlinks: %v", err)
	}

	// .../App.app/Contents/MacOS/tg-matrix → Contents
	contents := filepath.Dir(filepath.Dir(exe))
	res := filepath.Join(contents, "Resources")
	sidecar := filepath.Join(res, "tg-sidecar")
	web := filepath.Join(res, "web")

	if _, err := os.Stat(sidecar); err != nil {
		fatal("sidecar missing: %s (%v)", sidecar, err)
	}
	if _, err := os.Stat(web); err != nil {
		fatal("web ui missing: %s (%v)", web, err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fatal("home dir: %v", err)
	}
	data := filepath.Join(home, "Library", "Application Support", "tg-matrix")
	if err := os.MkdirAll(data, 0o700); err != nil {
		fatal("create data dir: %v", err)
	}

	addr := os.Getenv("TG_SIDECAR_ADDR")
	if addr == "" {
		addr = pickAddr("127.0.0.1", 17888, 17900)
	}

	cmd := exec.Command(sidecar)
	cmd.Env = append(os.Environ(),
		"TG_SIDECAR_DATA="+data,
		"TG_WEB_DIR="+web,
		"TG_SIDECAR_ADDR="+addr,
		"TG_OPEN_BROWSER=0",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		fatal("start sidecar: %v", err)
	}

	go func() {
		time.Sleep(600 * time.Millisecond)
		_ = openBrowser(fmt.Sprintf("http://%s/", addr))
	}()

	err = cmd.Wait()
	if err != nil {
		fatal("sidecar exited: %v\n若提示 address already in use，请先关闭占用端口的旧进程。", err)
	}
}

/**
 * 在 [start,end] 端口范围内找一个可监听地址。
 *
 * @param host 主机
 * @param start 起始端口
 * @param end 结束端口
 * @return host:port
 */
func pickAddr(host string, start, end int) string {
	for p := start; p <= end; p++ {
		addr := fmt.Sprintf("%s:%d", host, p)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			continue
		}
		_ = ln.Close()
		return addr
	}
	return fmt.Sprintf("%s:%d", host, start)
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

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "tg-matrix: "+format+"\n", args...)
	time.Sleep(4 * time.Second)
	os.Exit(1)
}
