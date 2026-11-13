package ops

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strings"
	"unicode"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"

	"github.com/huanxiang666/pdftoolbox/internal/cjkfont"
	"github.com/huanxiang666/pdftoolbox/internal/common"
)

// allowedFmtFields 是格式串允许的占位符字段。
var allowedFmtFields = map[string]bool{"n": true, "t": true}

// validateFmt 提前拒绝未知占位符，给出可读错误。
func validateFmt(format string) error {
	depth := 0
	var field strings.Builder
	inField := false
	for _, r := range format {
		switch {
		case r == '{':
			if depth == 0 {
				inField = true
				field.Reset()
			}
			depth++
		case r == '}':
			depth--
			if depth == 0 && inField {
				name := field.String()
				if i := strings.IndexAny(name, ".["); i >= 0 {
					name = name[:i]
				}
				if name != "" && !allowedFmtFields[name] {
					return common.Err("unknown placeholder {%s} in format %q (only {n} = page number and {t} = total pages are supported)", name, format)
				}
				inField = false
			}
		default:
			if inField && depth == 1 {
				field.WriteRune(r)
			}
		}
	}
	return nil
}

// formatNumber 用 {n}/{t} 渲染页码文本。
func formatNumber(format string, n, t int) string {
	r := strings.ReplaceAll(format, "{n}", fmt.Sprintf("%d", n))
	r = strings.ReplaceAll(r, "{t}", fmt.Sprintf("%d", t))
	return r
}

// isASCII 判断文本是否纯 ASCII（决定用 Helvetica 还是内嵌 CJK 字体）。
func isASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// mkTextWM 构造一个文字戳记（stamp，盖在内容上层）。
//
// 位置语义与 Python 版对齐：以九锚点定位，margin 为距页边距离（point）。
// dx/dy 的符号约定通过测试校准确保与 Python 版视觉一致。
func mkTextWM(text string, fontsize float64, col common.RGB, opacity float64, pos common.Position, margin, rotation float64) (*model.Watermark, error) {
	fontPart := "fontname:Helvetica"
	if !isASCII(text) {
		// 中文用内嵌子集字体（带 ToUnicode，可搜索可复制）。
		// scriptname:Hans 是非嵌入字体，部分阅读器缺字且无法提取文本。
		if err := cjkfont.EnsureInstalled(); err != nil {
			return nil, err
		}
		fontPart = "fontname:" + cjkfont.FontName()
	}
	dx, dy := 0.0, 0.0
	switch pos.FX {
	case 0:
		dx = margin
	case 1:
		dx = -margin
	}
	switch pos.FY {
	case 0:
		dy = margin
	case 1:
		dy = -margin
	}
	desc := fmt.Sprintf(
		"points:%d, color:#%02x%02x%02x, opacity:%.3f, rotation:%.1f, position:%s, offset:%.2f %.2f, %s",
		int(math.Round(fontsize)),
		clamp255(col.R), clamp255(col.G), clamp255(col.B),
		opacity, rotation, pdfcpuPosName(pos), dx, dy, fontPart,
	)
	wm, err := api.TextWatermark(context.Background(), text, desc, true, false, types.POINTS, conf())
	if err != nil {
		return nil, common.Err("stamp configuration failed: %v", err)
	}
	return wm, nil
}

func clamp255(v float64) int {
	x := int(math.Round(v * 255))
	if x < 0 {
		x = 0
	}
	if x > 255 {
		x = 255
	}
	return x
}

// NumberPages 从 startPage 开始盖页码。
//
// 页 P 的编号值 = firstNumber + (P - startPage)。
// format 支持 {n}（当页编号）与 {t}（文档总页数）。
func NumberPages(
	src, output string,
	startPage, firstNumber int,
	format, position string,
	margin, fontsize float64,
	colorSpec string,
	opacity float64,
) (*Result, error) {
	if !strings.Contains(format, "{n}") {
		return nil, common.Err("number format must contain {n}")
	}
	if err := validateFmt(format); err != nil {
		return nil, err
	}
	if startPage < 1 {
		return nil, common.Err("start-page must be >= 1, got %d", startPage)
	}
	if fontsize <= 0 {
		return nil, common.Err("fontsize must be > 0, got %v", fontsize)
	}
	if !(opacity > 0 && opacity <= 1) {
		return nil, common.Err("opacity must be in (0, 1], got %v", opacity)
	}
	data, total, err := Open(src)
	if err != nil {
		return nil, err
	}
	if output == "" {
		output = defaultOutput(src, "numbered")
	}
	if startPage > total {
		return nil, common.Err("start-page %d is beyond the last page (%d)", startPage, total)
	}
	pos, err := common.ResolvePosition(position, "bottom-center")
	if err != nil {
		return nil, err
	}
	rgb, err := common.ParseColor(colorSpec, common.RGB{R: 0, G: 0, B: 0})
	if err != nil {
		return nil, err
	}

	m := map[int]*model.Watermark{}
	stamped := 0
	for i := startPage - 1; i < total; i++ {
		n := firstNumber + (i - (startPage - 1))
		text := formatNumber(format, n, total)
		wm, err := mkTextWM(text, fontsize, rgb, opacity, pos, margin, 0)
		if err != nil {
			return nil, err
		}
		m[i+1] = wm
		stamped++
	}

	var buf bytes.Buffer
	if err := api.AddWatermarksMap(context.Background(), bytes.NewReader(data), &buf, m, conf()); err != nil {
		return nil, common.Err("numbering failed: %v", err)
	}
	if err := saveAtomic(buf.Bytes(), output); err != nil {
		return nil, common.Err("cannot write %s: %v", output, err)
	}
	return &Result{
		Output: output,
		Pages:  stamped,
		Messages: []string{fmt.Sprintf("numbered pages %d-%d (first number %d, format %q)",
			startPage, stamped+startPage-1, firstNumber, format)},
	}, nil
}
