//go:build windows

package main

import (
	"runtime"
	"syscall"
	"unsafe"
)

func goos() string      { return runtime.GOOS }
func goarch() string    { return runtime.GOARCH }
func goversion() string { return runtime.Version() }

func reportGUIError(message string) {
	printGUIError(message)
	text, _ := syscall.UTF16PtrFromString(message)
	title, _ := syscall.UTF16PtrFromString("PDF 工具箱 — 启动提示")
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}

// setConsoleUTF8 把控制台输入/输出代码页切到 UTF-8，
// 让中文帮助与结果在 cmd/PowerShell 里不乱码。
func setConsoleUTF8() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	kernel32.NewProc("SetConsoleOutputCP").Call(65001)
	kernel32.NewProc("SetConsoleCP").Call(65001)
}
