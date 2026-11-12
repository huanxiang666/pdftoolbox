// Package cjkfont 内嵌一份 Noto Sans SC 子集字体（SIL OFL 1.1），
// 供页码/水印的中文文字使用，保证单文件 exe 零依赖也能渲染中文。
//
// 首次需要时把字体释放到用户缓存目录并注册进 pdfcpu 的用户字体库。
package cjkfont

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	pdfcpufont "github.com/pdfcpu/pdfcpu/pkg/font"

	_ "embed"
)

//go:embed assets/notosanssc.ttf
var fontData []byte

// Name 是注册后的字体名（pdfcpu 用文件主名引用用户字体）。
const Name = "notosanssc"

var (
	once sync.Once
	err0 error
	// installedName 是 pdfcpu 实际注册的字体名（取自字体内部 family 名）。
	installedName string
)

// FontName 返回可用于水印 desc 的字体名。须在 EnsureInstalled 之后调用。
func FontName() string {
	if installedName != "" {
		return installedName
	}
	return Name
}

// detectInstalledName 从 ListFonts 输出里取出用户字体段的第一项。
func detectInstalledName() {
	names, err := api.ListFonts(context.Background())
	if err != nil {
		return
	}
	inUser := false
	for _, line := range names {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "Userfonts") {
			inUser = true
			continue
		}
		if !inUser || t == "" {
			continue
		}
		// 形如 "NotoSansSC-Thin (7447 glyphs)"
		if i := strings.Index(t, " ("); i > 0 {
			t = t[:i]
		}
		installedName = t
		return
	}
}

// EnsureInstalled 释放并注册内嵌字体，进程内只执行一次。
func EnsureInstalled() error {
	once.Do(func() {
		// pdfcpu 的用户字体目录来自其应用配置；库模式下若从未初始化，
		// InstallFonts 会报 "user font directory: missing configuration"。
		// 先加载配置，不存在则初始化一套默认配置树。
		if _, err := api.LoadConfiguration(api.ConfigurationOptions{}); err != nil {
			if _, err2 := api.InitializeConfigurationWithOptions(api.ConfigurationOptions{}); err2 != nil {
				err0 = fmt.Errorf("cannot initialize pdfcpu configuration: %w", err2)
				return
			}
		}
		dir, err := os.UserCacheDir()
		if err != nil || dir == "" {
			dir = os.TempDir()
		}
		dir = filepath.Join(dir, "pdftoolbox", "fonts")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			err0 = fmt.Errorf("cannot create font dir: %w", err)
			return
		}
		path := filepath.Join(dir, Name+".ttf")
		if st, err := os.Stat(path); err != nil || st.Size() != int64(len(fontData)) {
			if err := os.WriteFile(path, fontData, 0o644); err != nil {
				err0 = fmt.Errorf("cannot write embedded font: %w", err)
				return
			}
		}
		// pdfcpu 库模式下全局用户字体目录可能未初始化（CLI 才会设置），
		// 这里显式指向我们的字体目录，InstallFonts 才能工作。
		pdfcpufont.UserFontDir = dir
		if err := api.InstallFonts(context.Background(), []string{path}); err != nil {
			err0 = fmt.Errorf("cannot install embedded font: %w", err)
			return
		}
		detectInstalledName()
	})
	return err0
}

// Data 返回内嵌字体字节（测试用）。
func Data() []byte { return fontData }
