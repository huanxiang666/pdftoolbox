package ops

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"

	"github.com/huanxiang666/pdftoolbox/internal/common"
	"github.com/huanxiang666/pdftoolbox/internal/ingest"
)

// ---------------------------------------------------------------- merge

// Merge 把 PDF / Word / 图片按给定顺序合并为一个 PDF。
func Merge(inputs []string, output string, dpi int, wordTimeout time.Duration) (*Result, error) {
	if len(inputs) < 1 {
		return nil, common.Err("merge needs at least one input file")
	}
	var pdfFiles, tmps []string
	defer func() {
		for _, t := range tmps {
			os.Remove(t)
		}
	}()

	res := &Result{Output: output}
	total := 0
	conv := ingest.DetectWordConverter()

	for _, in := range inputs {
		ext := strings.ToLower(filepath.Ext(in))
		base := filepath.Base(in)
		switch {
		case ext == ".pdf":
			_, n, err := Open(in)
			if err != nil {
				return nil, err
			}
			pdfFiles = append(pdfFiles, in)
			total += n
			res.Messages = append(res.Messages, fmt.Sprintf("%s: %d page(s)", base, n))
		case isImageExt(ext):
			pdfBytes, err := ingest.ImageToPDF(in, dpi)
			if err != nil {
				return nil, err
			}
			tmp, err := writeTemp(pdfBytes)
			if err != nil {
				return nil, err
			}
			tmps = append(tmps, tmp)
			pdfFiles = append(pdfFiles, tmp)
			total++
			res.Messages = append(res.Messages, fmt.Sprintf("%s: 1 page(s)", base))
		case isWordExt(ext):
			if conv == nil {
				return nil, common.Err("%s", strings.ReplaceAll(ingest.WordInstallHint, "\n", " "))
			}
			dir, err := os.MkdirTemp("", "pdftoolbox-word-")
			if err != nil {
				return nil, err
			}
			pdfPath, err := ingest.ConvertWordToPDF(in, dir, conv, wordTimeout)
			if err != nil {
				os.RemoveAll(dir)
				return nil, err
			}
			n, err := pageCountFile(pdfPath)
			if err != nil {
				os.RemoveAll(dir)
				return nil, err
			}
			pdfFiles = append(pdfFiles, pdfPath)
			tmps = append(tmps, pdfPath)
			total += n
			res.Messages = append(res.Messages, fmt.Sprintf("%s: %d page(s)", base, n))
		default:
			return nil, common.Err("unsupported input type: %s", base)
		}
	}

	var buf bytes.Buffer
	if err := api.Merge(context.Background(), "", pdfFiles, &buf, conf(), false); err != nil {
		return nil, common.Err("merge failed: %v", err)
	}
	if err := saveAtomic(buf.Bytes(), output); err != nil {
		return nil, common.Err("cannot write %s: %v", output, err)
	}
	res.Pages = total
	return res, nil
}

func isImageExt(ext string) bool {
	for _, e := range ingest.ImageExtensions {
		if e == ext {
			return true
		}
	}
	return false
}

func isWordExt(ext string) bool {
	for _, e := range ingest.WordExtensions {
		if e == ext {
			return true
		}
	}
	return false
}

func copyToTemp(src string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	return writeTemp(data)
}

func writeTemp(data []byte) (string, error) {
	f, err := os.CreateTemp("", "pdftoolbox-*.pdf")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func pageCountFile(p string) (int, error) {
	n, err := api.PageCount(context.Background(), mustOpen(p), conf())
	if err != nil {
		return 0, common.Err("converted PDF unreadable for %s: %v", filepath.Base(p), err)
	}
	return n, nil
}

func mustOpen(p string) *os.File {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	return f
}

// ---------------------------------------------------------------- split

func sanitizeName(name string) string {
	var b strings.Builder
	for _, c := range name {
		if c == '-' || c == '_' || c == '.' || isAlnum(c) {
			b.WriteRune(c)
		}
	}
	if b.Len() == 0 {
		return "part"
	}
	return b.String()
}

func isAlnum(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		c > 127 // 保留中文等字符
}

// Split 拆分 PDF：--per-page 每页一个文件；--ranges 每段一个文件。
func Split(src, outDir string, perPage bool, ranges, prefix string) (*Result, error) {
	data, total, err := Open(src)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	if prefix == "" {
		prefix = trimExt(filepath.Base(src))
	}
	prefix = sanitizeName(prefix)

	var outputs []string
	emit := func(spec, name string) error {
		var buf bytes.Buffer
		if err := api.Collect(context.Background(), bytes.NewReader(data), &buf, []string{spec}, conf()); err != nil {
			return common.Err("split failed on %q: %v", spec, err)
		}
		target := filepath.Join(outDir, name)
		if err := saveAtomic(buf.Bytes(), target); err != nil {
			return common.Err("cannot write %s: %v", target, err)
		}
		outputs = append(outputs, target)
		return nil
	}

	switch {
	case perPage:
		for i := 1; i <= total; i++ {
			name := fmt.Sprintf("%s-%04d.pdf", prefix, i)
			if err := emit(fmt.Sprintf("%d", i), name); err != nil {
				return nil, err
			}
		}
	case ranges != "":
		used := map[string]bool{}
		for _, seg := range strings.Split(ranges, ",") {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				continue
			}
			// 先校验该段合法
			if _, err := common.ParsePages(seg, total); err != nil {
				return nil, err
			}
			name := fmt.Sprintf("%s-%s.pdf", prefix, sanitizeName(strings.ReplaceAll(seg, " ", "")))
			base := name
			for n := 2; used[strings.ToLower(name)]; n++ {
				name = strings.Replace(base, ".pdf", fmt.Sprintf("-%d.pdf", n), 1)
			}
			used[strings.ToLower(name)] = true
			if err := emit(seg, name); err != nil {
				return nil, err
			}
		}
	default:
		return nil, common.Err("split needs --per-page or --ranges")
	}

	return &Result{
		Output:   outDir,
		Pages:    total,
		Messages: []string{fmt.Sprintf("%d file(s) written to %s", len(outputs), outDir)},
		Details:  map[string]any{"files": outputs, "count": len(outputs)},
	}, nil
}

func trimExt(name string) string { return strings.TrimSuffix(name, filepath.Ext(name)) }

// ---------------------------------------------------------------- rotate

// Rotate 顺时针旋转 angle 度（90/180/270，可为负），与页面已有旋转叠加。
func Rotate(src, output string, angle int, pages string) (*Result, error) {
	if angle%90 != 0 {
		return nil, common.Err("angle must be a multiple of 90, got %d", angle)
	}
	angle = ((angle % 360) + 360) % 360
	if angle == 0 {
		return nil, common.Err("angle 0 rotates nothing (use 90, 180 or 270)")
	}
	data, total, err := Open(src)
	if err != nil {
		return nil, err
	}
	if output == "" {
		output = defaultOutput(src, fmt.Sprintf("rot%d", angle))
	}
	indices, err := common.ParsePages(pages, total)
	if err != nil {
		return nil, err
	}
	// pdfcpu 的 Rotate 是叠加语义（与页面已有旋转相加），与 Python 版一致。
	var buf bytes.Buffer
	if err := api.Rotate(context.Background(), bytes.NewReader(data), &buf, angle, indicesToSpec(indices), conf()); err != nil {
		return nil, common.Err("rotate failed: %v", err)
	}
	if err := saveAtomic(buf.Bytes(), output); err != nil {
		return nil, common.Err("cannot write %s: %v", output, err)
	}
	return &Result{
		Output:   output,
		Pages:    len(indices),
		Messages: []string{fmt.Sprintf("rotated page(s) %s by %d°", common.FormatPages(indices, total), angle)},
	}, nil
}

// indicesToSpec 把 0-based 索引列表转成 pdfcpu 的 1-based 选择串切片。
// 注意：pdfcpu 要求每个元素是单个页号或单个区间，逗号必须拆成多个元素。
func indicesToSpec(indices []int) []string {
	parts := make([]string, len(indices))
	for i, v := range indices {
		parts[i] = fmt.Sprintf("%d", v+1)
	}
	return parts
}

// specSlice 把用户给的页码描述拆成 pdfcpu 需要的元素切片。
func specSlice(spec string) []string {
	if strings.TrimSpace(spec) == "" {
		return nil
	}
	return strings.Split(spec, ",")
}
