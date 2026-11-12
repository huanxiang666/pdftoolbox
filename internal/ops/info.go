package ops

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/pdfcpu/pdfcpu/pkg/api"

	"github.com/huanxiang666/pdftoolbox/internal/common"
)

// PDFInfo 汇总 PDF：页数、页面尺寸分布、旋转、文本页统计、元数据。
func PDFInfo(src string) (*Result, error) {
	data, total, err := Open(src)
	if err != nil {
		return nil, err
	}
	info, err := api.PDFInfo(context.Background(), bytes.NewReader(data), src, nil, false, conf())
	if err != nil {
		return nil, common.Err("cannot read PDF %s: %v", src, err)
	}

	sizes := map[string]int{}
	rotated := 0
	for _, pb := range info.PageBoundaries {
		r := pb.Crop
		if r == nil || r.Rect == nil {
			r = pb.Media
		}
		if r != nil && r.Rect != nil {
			key := fmt.Sprintf("%.0fx%.0fpt", r.Rect.Width(), r.Rect.Height())
			sizes[key]++
		}
		if pb.Rot%360 != 0 {
			rotated++
		}
	}

	scans, err := scanPages(data)
	if err != nil {
		return nil, err
	}
	textPages := 0
	for _, s := range scans {
		if s.HasText {
			textPages++
		}
	}

	meta := map[string]any{}
	put := func(k, v string) {
		if v != "" {
			meta[k] = v
		}
	}
	put("title", info.Title)
	put("author", info.Author)
	put("subject", info.Subject)
	put("producer", info.Producer)
	put("creator", info.Creator)
	put("creationDate", info.CreationDate)
	put("modificationDate", info.ModificationDate)
	if len(info.Keywords) > 0 {
		meta["keywords"] = info.Keywords
	}

	st, _ := os.Stat(src)
	var sizeBytes int64
	if st != nil {
		sizeBytes = st.Size()
	}

	details := map[string]any{
		"file":            src,
		"bytes":           sizeBytes,
		"pages":           total,
		"page_sizes":      sizes,
		"rotated_pages":   rotated,
		"pages_with_text": textPages,
		"metadata":        meta,
	}
	return &Result{
		Output:  src,
		Pages:   total,
		Details: details,
		Messages: []string{fmt.Sprintf("%d pages, %.2f MB, %d page(s) with text, sizes: %v",
			total, float64(sizeBytes)/1e6, textPages, sizes)},
	}, nil
}
