//go:build !windows

package main

import "runtime"

func goos() string      { return runtime.GOOS }
func goarch() string    { return runtime.GOARCH }
func goversion() string { return runtime.Version() }

// setConsoleUTF8 在非 Windows 平台无需操作（终端默认 UTF-8）。
func setConsoleUTF8() {}
