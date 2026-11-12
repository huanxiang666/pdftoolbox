package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/huanxiang666/pdftoolbox/internal/webui"
)

// runGUI 启动内置 Web 界面：本地起服务并打开默认浏览器。
// 零外部依赖（不装 Electron/WebView），界面由 exe 内嵌的 HTML 提供。
func runGUI(args []string) int {
	fs := newFlags(args, []string{"no-browser"})
	port := 0
	if v, ok := fs.int("port"); ok {
		port = v
	}
	ln, err := listen(port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot start GUI server: %v\n", err)
		return 1
	}
	addr := ln.Addr().(*net.TCPAddr)
	url := fmt.Sprintf("http://127.0.0.1:%d/", addr.Port)

	srv := &http.Server{Handler: webui.Handler()}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "gui server stopped: %v\n", err)
		}
	}()

	fmt.Printf("pdftoolbox GUI: %s\n", url)
	fmt.Println("保持本窗口运行；关闭浏览器标签不会停止服务，Ctrl+C 退出。")

	if !fs.bool("no-browser") {
		openBrowser(url)
	}

	// 阻塞直到 Ctrl+C
	select {}
}

// listen 在指定端口监听；port=0 时自动选空闲端口（避免占用冲突）。
func listen(port int) (net.Listener, error) {
	if port > 0 {
		return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	}
	// 偏好固定端口 8420，被占用则回退随机端口
	for _, p := range []int{8420, 0} {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			return ln, nil
		}
	}
	return nil, fmt.Errorf("no free port")
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot open browser automatically: %v\n", err)
	}
	go func() {
		time.Sleep(10 * time.Second)
		if cmd.Process != nil {
			cmd.Process.Release()
		}
	}()
}
