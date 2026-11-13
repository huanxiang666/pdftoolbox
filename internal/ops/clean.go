package ops

import (
	"bytes"
	"context"
	"fmt"

	"github.com/pdfcpu/pdfcpu/pkg/api"

	"github.com/huanxiang666/pdftoolbox/internal/common"
)

// FindBlankPages 返回视觉空白页的 0-based 索引。
//
// 判定：无文字操作、无图像/XObject、无着色、无任何非白绘制。
// 与 Python 版"有文字的页面永不删除"一致：含文字操作即视为有内容。
// threshold/dpi 参数保留以兼容 CLI，纯 Go 实现不使用栅格化。
func FindBlankPages(data []byte, threshold float64, dpi int) ([]int, error) {
	scans, err := scanPages(data)
	if err != nil {
		return nil, common.Err("cannot analyze pages: %v", err)
	}
	var blanks []int
	for i, s := range scans {
		if s.HasText || s.HasImage || s.HasShading || s.HasInk {
			continue
		}
		blanks = append(blanks, i)
	}
	return blanks, nil
}

// CleanBlank 删除视觉空白页。dryRun 只报告不写盘。
func CleanBlank(src, output string, threshold float64, dpi int, dryRun bool) (*Result, error) {
	if !(threshold > 0 && threshold < 1) {
		return nil, common.Err("threshold must be in (0,1), got %v", threshold)
	}
	data, total, err := Open(src)
	if err != nil {
		return nil, err
	}
	blanks, err := FindBlankPages(data, threshold, dpi)
	if err != nil {
		return nil, err
	}
	oneBased := make([]int, len(blanks))
	for i, b := range blanks {
		oneBased[i] = b + 1
	}
	res := &Result{Pages: total, Details: map[string]any{"blank_pages": oneBased}}

	if dryRun {
		res.Messages = []string{fmt.Sprintf("%d blank page(s): %v", len(blanks), orNone(oneBased))}
		return res, nil
	}
	if len(blanks) == 0 {
		res.Messages = []string{"no blank pages found; output identical to input"}
		if output != "" {
			if err := saveAtomic(data, output); err != nil {
				return nil, common.Err("cannot write %s: %v", output, err)
			}
			res.Output = output
		} else {
			res.Output = src
		}
		return res, nil
	}
	if len(blanks) == total {
		return nil, common.Err("all %d pages are blank; refusing to produce an empty PDF", total)
	}
	keepSet := map[int]bool{}
	for _, b := range blanks {
		keepSet[b] = true
	}
	keep := make([]string, 0, total-len(blanks))
	for i := 0; i < total; i++ {
		if !keepSet[i] {
			keep = append(keep, fmt.Sprintf("%d", i+1))
		}
	}
	var buf bytes.Buffer
	if err := api.Collect(context.Background(), bytes.NewReader(data), &buf, keep, conf()); err != nil {
		return nil, common.Err("clean failed: %v", err)
	}
	target := output
	if target == "" {
		target = defaultOutput(src, "clean")
	}
	if err := saveAtomic(buf.Bytes(), target); err != nil {
		return nil, common.Err("cannot write %s: %v", target, err)
	}
	res.Output = target
	res.Pages = total - len(blanks)
	res.Messages = []string{fmt.Sprintf("removed %d blank page(s) %v; %d page(s) kept",
		len(blanks), oneBased, total-len(blanks))}
	return res, nil
}

func orNone(v []int) any {
	if len(v) == 0 {
		return "none"
	}
	return v
}
