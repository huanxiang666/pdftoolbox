package ops

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"github.com/huanxiang666/pdftoolbox/internal/common"
)

// Result 是每个操作的统一结果（字段语义与 Python 版 OpResult 一致）。
type Result struct {
	Output   string
	Pages    int
	Messages []string
	Details  map[string]any
}

// conf 返回宽松校验配置（容忍现实世界里的不规范 PDF）。
func conf() *model.Configuration {
	c := model.NewDefaultConfiguration()
	c.ValidationMode = model.ValidationRelaxed
	return c
}

// Open 读取 PDF 到内存并返回页数。加密 PDF 报可读错误。
func Open(src string) ([]byte, int, error) {
	if _, err := os.Stat(src); err != nil {
		return nil, 0, common.Err("input not found: %s", src)
	}
	if strings.ToLower(filepath.Ext(src)) != ".pdf" {
		return nil, 0, common.Err("%s: this operation takes a PDF input (use 'merge' to combine Word/images into a PDF)", filepath.Base(src))
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, 0, common.Err("cannot read %s: %v", src, err)
	}
	ctx := context.Background()
	mctx, err := api.ReadContext(ctx, bytes.NewReader(data), conf())
	if err != nil {
		return nil, 0, common.Err("cannot open PDF %s: %v", filepath.Base(src), err)
	}
	if mctx.Encrypt != nil {
		return nil, 0, common.Err("%s is password-protected; decrypt it first", filepath.Base(src))
	}
	n, err := api.PageCount(ctx, bytes.NewReader(data), conf())
	if err != nil {
		return nil, 0, common.Err("cannot open PDF %s: %v", filepath.Base(src), err)
	}
	return data, n, nil
}

// defaultOutput 生成默认输出路径：<input>-<suffix>.pdf。
func defaultOutput(src, suffix string) string {
	return strings.TrimSuffix(src, filepath.Ext(src)) + "-" + suffix + ".pdf"
}

// saveAtomic 原子写盘：临时文件 + rename。
func saveAtomic(pdfBytes []byte, output string) error {
	return common.SaveAtomic(func(tmp string) error {
		return os.WriteFile(tmp, pdfBytes, 0o644)
	}, output)
}
