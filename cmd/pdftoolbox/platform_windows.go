//go:build windows

package main

import (
	"runtime"
	"syscall"
)

func goos() string      { return runtime.GOOS }
func goarch() string    { return runtime.GOARCH }
func goversion() string { return runtime.Version() }

// setConsoleUTF8 把控制台输入/输出代码页切到 UTF-8，
// 让中文帮助与结果在 cmd/PowerShell 里不乱码。
func setConsoleUTF8() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	kernel32.NewProc("SetConsoleOutputCP").Call(65001)
	kernel32.NewProc("SetConsoleCP").Call(65001)
}
