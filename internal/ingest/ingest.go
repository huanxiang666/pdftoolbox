// Package ingest 负责把各类输入（PDF / 图片 / Word）统一变成 PDF 文件路径。
//
// 与 Python 版语义一致：
//   - PDF 直接透传（加密 PDF 明确报错）
//   - 图片按 DPI 定尺寸生成单页 PDF，手机照片 EXIF 方向自动纠正
//   - Word 通过外部 LibreOffice（soffice）转换；找不到时给出可读的安装指引
package ingest

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif" // gif 首帧
	"image/jpeg"
	_ "image/png" // png
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/bmp"  // bmp
	_ "golang.org/x/image/tiff" // tif/tiff
	_ "golang.org/x/image/webp" // webp

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"

	"github.com/huanxiang666/pdftoolbox/internal/cjkfont"
	"github.com/huanxiang666/pdftoolbox/internal/common"
	"github.com/huanxiang666/pdftoolbox/internal/docx2pdf"
)

// 支持的扩展名（与 Python 版一致）。
var (
	PDFExtensions   = []string{".pdf"}
	ImageExtensions = []string{".png", ".jpg", ".jpeg", ".bmp", ".tif", ".tiff", ".gif", ".webp"}
	WordExtensions  = []string{".doc", ".docx", ".odt", ".rtf", ".wps"}
)

// SupportedInputExtensions 返回全部受输入的扩展名。
func SupportedInputExtensions() []string {
	out := append([]string{}, PDFExtensions...)
	out = append(out, ImageExtensions...)
	out = append(out, WordExtensions...)
	return out
}

// EnvWordConverter 是强制指定 soffice 的环境变量。
const EnvWordConverter = "PDFTOOLBOX_WORD_CONVERTER"

// 转换器类型。
const (
	KIND_SOFFICE = "soffice"
	KIND_COM     = "com"
)

// WordInstallHint 是找不到 Word 转换器时的安装指引。
const WordInstallHint = `No Word converter found — .doc/.docx/.odt/.rtf input requires one of:
  1) LibreOffice (free, all platforms): https://www.libreoffice.org/download/
     Linux: sudo apt install libreoffice-writer  (or libreoffice)
     Then 'soffice' is auto-detected; override with
     PDFTOOLBOX_WORD_CONVERTER=/path/to/soffice
PDF and image inputs work without any external tool.
未找到 Word 转换器。转换 .doc/.docx 需要安装 LibreOffice（免费，全平台，
Linux: sudo apt install libreoffice-writer）。PDF 与图片输入无需任何外部工具。`

var sofficeCandidates = []string{
	"soffice",
	"libreoffice",
	"/usr/bin/soffice",
	"/usr/lib/libreoffice/program/soffice",
	"/snap/bin/libreoffice",
	"/opt/libreoffice/program/soffice",
	"/Applications/LibreOffice.app/Contents/MacOS/soffice",
	"C:/Program Files/LibreOffice/program/soffice.exe",
	"C:/Program Files (x86)/LibreOffice/program/soffice.exe",
}

// WordConverter 描述一个可用的 Word→PDF 转换器。
type WordConverter struct {
	Kind string // KIND_SOFFICE | KIND_COM
	Path string // soffice 可执行文件 / COM ProgID
}

// DisplayName 返回展示名。
func (w WordConverter) DisplayName() string {
	if w.Kind == KIND_COM {
		if strings.HasPrefix(w.Path, "Word") {
			return "Microsoft Word (COM)"
		}
		return "WPS Office (COM)"
	}
	return "LibreOffice (" + w.Path + ")"
}

// DetectWordConverter 按 环境变量 → COM(Word/WPS) → soffice 的顺序探测。
// 都没有也没关系：.docx 还有内嵌的纯 Go 渲染器兜底（见 ConvertWordToPDF）。
func DetectWordConverter() *WordConverter {
	if env := strings.TrimSpace(os.Getenv(EnvWordConverter)); env != "" {
		if p, err := exec.LookPath(env); err == nil {
			return &WordConverter{Kind: KIND_SOFFICE, Path: p}
		}
		if st, err := os.Stat(env); err == nil && !st.IsDir() {
			return &WordConverter{Kind: KIND_SOFFICE, Path: env}
		}
		fmt.Fprintf(os.Stderr, "warning: %s=%q is not an executable file; falling back to auto-detection\n", EnvWordConverter, env)
	}
	if c := detectCOMConverter(); c != nil {
		return &WordConverter{Kind: KIND_COM, Path: c.ProgID}
	}
	for _, cand := range sofficeCandidates {
		if strings.ContainsRune(cand, '/') || strings.ContainsRune(cand, '\\') {
			if st, err := os.Stat(cand); err == nil && !st.IsDir() {
				return &WordConverter{Kind: KIND_SOFFICE, Path: cand}
			}
			continue
		}
		if p, err := exec.LookPath(cand); err == nil {
			return &WordConverter{Kind: KIND_SOFFICE, Path: p}
		}
	}
	return nil
}

// ConvertWordToPDF 把 Word 文档转成 outDir 下的 PDF，返回产物路径。
//
// 转换链：外部转换器（COM Word/WPS 或 LibreOffice）失败或缺失时，
// .docx 回退到内嵌的纯 Go 渲染器（零依赖兜底，覆盖常见排版）。
func ConvertWordToPDF(src, outDir string, conv *WordConverter, timeout time.Duration) (string, error) {
	ext := strings.ToLower(filepath.Ext(src))
	var lastErr error
	if conv != nil {
		var p string
		var err error
		if conv.Kind == KIND_COM {
			p, err = convertWordViaCOM(src, outDir, &comConverter{ProgID: conv.Path}, timeout)
		} else {
			p, err = convertViaSoffice(src, outDir, conv, timeout)
		}
		if err == nil {
			return p, nil
		}
		lastErr = err
	}
	if ext == ".docx" {
		p, err := convertBuiltinDocx(src, outDir)
		if err == nil {
			return p, nil
		}
		if lastErr != nil {
			return "", common.Err("%v; built-in docx renderer also failed: %v", lastErr, err)
		}
		return "", err
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", common.Err("%s", strings.ReplaceAll(WordInstallHint, "\n", " "))
}

// convertBuiltinDocx 用内嵌渲染器把 .docx 转成 PDF（兜底路径）。
func convertBuiltinDocx(src, outDir string) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	pdfBytes, err := docx2pdf.Render(src, cjkfont.Data())
	if err != nil {
		return "", common.Err("built-in docx renderer: %v", err)
	}
	expected := filepath.Join(outDir, trimExt(filepath.Base(src))+".pdf")
	if err := os.WriteFile(expected, pdfBytes, 0o644); err != nil {
		return "", err
	}
	return expected, nil
}

// convertViaSoffice 用 LibreOffice  headless 转换。
func convertViaSoffice(src, outDir string, conv *WordConverter, timeout time.Duration) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	expected := filepath.Join(outDir, trimExt(filepath.Base(src))+".pdf")
	os.Remove(expected)

	// 每次运行使用独立 profile：并行的 soffice 会争抢默认用户配置而随机失败。
	profileDir, err := os.MkdirTemp("", "pdftoolbox-lo-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(profileDir)
	profileURI := "file:///" + filepath.ToSlash(profileDir)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, conv.Path,
		"-env:UserInstallation="+profileURI,
		"--headless", "--norestore",
		"--convert-to", "pdf",
		"--outdir", outDir,
		src,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", common.Err("LibreOffice timed out after %vs on %s", int(timeout.Seconds()), filepath.Base(src))
		}
		return "", common.Err("failed to run %s: %v", conv.Path, err)
	}
	if _, err := os.Stat(expected); err != nil {
		detail := strings.TrimSpace(out.String())
		if len(detail) > 400 {
			detail = detail[len(detail)-400:]
		}
		msg := fmt.Sprintf("LibreOffice produced no PDF for %s", filepath.Base(src))
		if detail != "" {
			msg += ": " + detail
		}
		return "", common.Err("%s", msg)
	}
	return expected, nil
}

func trimExt(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// ImageToPDF 把一张图片转成单页 PDF 字节，页面尺寸 = 像素 / dpi * 72。
// 自动纠正 EXIF 方向；WebP 等 pdfcpu 不能直接解码的格式先重编码为 PNG/JPEG。
func ImageToPDF(src string, dpi int) ([]byte, error) {
	if dpi <= 0 {
		return nil, common.Err("image dpi must be > 0, got %d", dpi)
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return nil, common.Err("cannot read image %s: %v", filepath.Base(src), err)
	}

	orient := exifOrientation(raw)
	ext := strings.ToLower(filepath.Ext(src))

	// 需要重编码的情况：EXIF 方向非 1，或格式 pdfcpu 不支持直接嵌入
	needsReencode := orient != 1 || ext == ".webp" || ext == ".bmp" || ext == ".tif" || ext == ".tiff" || ext == ".gif"
	data := raw
	if needsReencode {
		img, err := decodeOriented(raw, orient)
		if err != nil {
			return nil, common.Err("cannot read image %s: %v", filepath.Base(src), err)
		}
		var buf bytes.Buffer
		if hasAlpha(img) {
			if err := encodePNG(&buf, img); err != nil {
				return nil, common.Err("cannot re-encode image %s: %v", filepath.Base(src), err)
			}
		} else {
			if err := jpeg.Encode(&buf, toRGB(img), &jpeg.Options{Quality: 92}); err != nil {
				return nil, common.Err("cannot re-encode image %s: %v", filepath.Base(src), err)
			}
		}
		data = buf.Bytes()
		// 重编码后尺寸以解码结果为准
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, common.Err("cannot read image size %s: %v", filepath.Base(src), err)
		}
		return imageBytesToPDF(data, cfg.Width, cfg.Height, dpi)
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, common.Err("cannot read image %s: %v", filepath.Base(src), err)
	}
	return imageBytesToPDF(data, cfg.Width, cfg.Height, dpi)
}

func imageBytesToPDF(data []byte, wPx, hPx, dpi int) ([]byte, error) {
	wPt := float64(wPx) * 72.0 / float64(dpi)
	hPt := float64(hPx) * 72.0 / float64(dpi)

	// 输出路径必须不存在：pdfcpu 见到已存在的空文件会当作输入 PDF 打开而报错。
	tmpName := filepath.Join(os.TempDir(), fmt.Sprintf("pdftoolbox-import-%d-%d.pdf", os.Getpid(), time.Now().UnixNano()))
	defer os.Remove(tmpName)

	imp := &pdfcpuImport{
		DPI:  dpi,
		W:    wPt,
		H:    hPt,
		Full: true,
	}
	if err := importImageFile(data, tmpName, imp); err != nil {
		return nil, common.Err("cannot embed image: %v", err)
	}
	out, err := os.ReadFile(tmpName)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// exifOrientation 读取 JPEG EXIF 方向标签（0x0112），无则返回 1。
func exifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 < len(data) {
		if data[i] != 0xFF {
			i++
			continue
		}
		marker := data[i+1]
		if marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			continue
		}
		segLen := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if segLen < 2 || i+2+segLen > len(data) {
			return 1
		}
		if marker == 0xE1 { // APP1
			seg := data[i+4 : i+2+segLen]
			if o, ok := parseExifOrientation(seg); ok {
				return o
			}
			return 1
		}
		if marker == 0xDA { // SOS：图像数据开始，停止
			return 1
		}
		i += 2 + segLen
	}
	return 1
}

func parseExifOrientation(seg []byte) (int, bool) {
	if len(seg) < 6 || string(seg[0:4]) != "Exif" {
		return 0, false
	}
	tiff := seg[6:]
	if len(tiff) < 8 {
		return 0, false
	}
	var bo binary.ByteOrder = binary.BigEndian
	if string(tiff[0:2]) == "II" {
		bo = binary.LittleEndian
	} else if string(tiff[0:2]) != "MM" {
		return 0, false
	}
	ifdOff := int(bo.Uint32(tiff[4:8]))
	if ifdOff+2 > len(tiff) {
		return 0, false
	}
	n := int(bo.Uint16(tiff[ifdOff : ifdOff+2]))
	for k := 0; k < n; k++ {
		e := ifdOff + 2 + k*12
		if e+12 > len(tiff) {
			return 0, false
		}
		if bo.Uint16(tiff[e:e+2]) == 0x0112 {
			return int(bo.Uint16(tiff[e+8 : e+10])), true
		}
	}
	return 0, false
}

// CollectInputs 把文件/目录参数展开为有序输入列表。
// 显式文件保持给定顺序（可故意重复）；目录按自然排序展开（img2 在 img10 前）。
func CollectInputs(paths []string, recursive bool) (files, ignored []string) {
	supported := map[string]bool{}
	for _, e := range SupportedInputExtensions() {
		supported[e] = true
	}
	addFile := func(p string) {
		if supported[strings.ToLower(filepath.Ext(p))] {
			files = append(files, p)
		} else {
			ignored = append(ignored, p)
		}
	}
	for _, raw := range paths {
		p := raw
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			found := false
			entries := listDirSorted(p, recursive)
			for _, e := range entries {
				if supported[strings.ToLower(filepath.Ext(e))] {
					addFile(e)
					found = true
				}
			}
			if !found {
				ignored = append(ignored, p)
			}
			continue
		}
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			addFile(p)
		} else {
			ignored = append(ignored, p)
		}
	}
	return files, ignored
}

var numPart = regexp.MustCompile(`(\d+)`)

func naturalKey(s string) string {
	return numPart.ReplaceAllStringFunc(strings.ToLower(s), func(m string) string {
		n, _ := strconv.Atoi(m)
		return fmt.Sprintf("%012d", n)
	})
}

func listDirSorted(dir string, recursive bool) []string {
	var out []string
	if recursive {
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				out = append(out, p)
			}
			return nil
		})
	} else {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}
		for _, e := range ents {
			if !e.IsDir() {
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return naturalKey(out[i]) < naturalKey(out[j]) })
	return out
}

// 保证依赖被使用（types/model/api 在 ops 里用，这里仅 image 相关）。
var (
	_ = types.POINTS
	_ = model.ValidationRelaxed
	_ = api.PageCount
	_ io.Reader
)
