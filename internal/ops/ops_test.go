package ops

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// makePDF 生成一个最小合法 PDF：每页一条内容流。
func makePDF(t *testing.T, contents ...string) string {
	t.Helper()
	n := len(contents)
	var objs bytes.Buffer
	pageRefs := ""
	for i := 0; i < n; i++ {
		pageObj := 3 + i*2
		contObj := pageObj + 1
		pageRefs += fmt.Sprintf("%d 0 R ", pageObj)
		_ = contObj
	}
	// 对象布局: 1 catalog, 2 pages, 之后每页 page+content
	fmt.Fprintf(&objs, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	fmt.Fprintf(&objs, "2 0 obj\n<< /Type /Pages /Count %d /Kids [%s] >>\nendobj\n", n, pageRefs)
	offsets := map[int]int64{}
	offsets[1] = 0
	offsets[2] = 0
	body := objs.String()
	// 重新计算偏移：先拼完整 body
	var pages bytes.Buffer
	pages.WriteString(body)
	off := int64(len("%PDF-1.4\n"))
	// 记录 1、2 的偏移
	offsets[1] = off
	off += int64(len("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"))
	offsets[2] = off
	off += int64(len(fmt.Sprintf("2 0 obj\n<< /Type /Pages /Count %d /Kids [%s] >>\nendobj\n", n, pageRefs)))
	for i := 0; i < n; i++ {
		pageObj := 3 + i*2
		contObj := pageObj + 1
		res := "<< /Font << /F1 9 0 R >> >>"
		pstr := fmt.Sprintf("%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources %s /Contents %d 0 R >>\nendobj\n", pageObj, res, contObj)
		cstr := fmt.Sprintf("%d 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", contObj, len(contents[i]), contents[i])
		offsets[pageObj] = off
		off += int64(len(pstr))
		offsets[contObj] = off
		off += int64(len(cstr))
		pages.WriteString(pstr)
		pages.WriteString(cstr)
	}
	// 字体对象 9
	fontStr := "9 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n"
	offsets[9] = off
	off += int64(len(fontStr))
	pages.WriteString(fontStr)

	maxObj := 3 + n*2
	if maxObj <= 9 {
		maxObj = 10
	} else {
		maxObj = 9
		if 3+n*2 > maxObj {
			maxObj = 3 + n*2
		}
	}
	total := maxObj
	if total < 9 {
		total = 9
	}
	var xref bytes.Buffer
	xrefPos := off
	fmt.Fprintf(&xref, "xref\n0 %d\n", total+1)
	xref.WriteString("0000000000 65535 f \n")
	for i := 1; i <= total; i++ {
		o, ok := offsets[i]
		if !ok {
			o = 0
		}
		fmt.Fprintf(&xref, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&xref, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", total+1, xrefPos)

	dir := t.TempDir()
	p := filepath.Join(dir, "in.pdf")
	os.WriteFile(p, append([]byte("%PDF-1.4\n"), append(pages.Bytes(), xref.Bytes()...)...), 0o644)
	return p
}

func pageCount(t *testing.T, p string) int {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n, err := api.PageCount(context.Background(), f, conf())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRotateAddsToExisting(t *testing.T) {
	src := makePDF(t, "0 0 0 RG 1 w 50 50 m 500 700 l S", "")
	out := filepath.Join(t.TempDir(), "rot.pdf")
	res, err := Rotate(src, out, 90, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Pages != 2 {
		t.Errorf("pages=%d", res.Pages)
	}
	info, err := api.PDFInfo(context.Background(), bytes.NewReader(readFile(t, out)), "", nil, false, conf())
	if err != nil {
		t.Fatal(err)
	}
	if info.PageBoundaries[0].Rot != 90 || info.PageBoundaries[1].Rot != 90 {
		t.Errorf("rot=%v %v", info.PageBoundaries[0].Rot, info.PageBoundaries[1].Rot)
	}
	// 再转 90 → 180
	out2 := filepath.Join(t.TempDir(), "rot2.pdf")
	if _, err := Rotate(out, out2, 90, ""); err != nil {
		t.Fatal(err)
	}
	info2, _ := api.PDFInfo(context.Background(), bytes.NewReader(readFile(t, out2)), "", nil, false, conf())
	if info2.PageBoundaries[0].Rot != 180 {
		t.Errorf("叠加旋转应为 180，得到 %d", info2.PageBoundaries[0].Rot)
	}
}

func TestRotateValidation(t *testing.T) {
	src := makePDF(t, "")
	if _, err := Rotate(src, "", 45, ""); err == nil {
		t.Error("45 度应报错")
	}
	if _, err := Rotate(src, "", 0, ""); err == nil {
		t.Error("0 度应报错")
	}
	if _, err := Rotate(src, "", 90, "9"); err == nil {
		t.Error("越界页应报错")
	}
}

func TestSplitPerPageAndRanges(t *testing.T) {
	src := makePDF(t, "q Q", "q Q", "q Q")
	dir := t.TempDir()
	res, err := Split(src, dir, true, "", "pre")
	if err != nil {
		t.Fatal(err)
	}
	if res.Details["count"] != 3 {
		t.Errorf("count=%v", res.Details["count"])
	}
	for _, name := range []string{"pre-0001.pdf", "pre-0002.pdf", "pre-0003.pdf"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("缺少 %s", name)
		}
	}
	// ranges 去重
	dir2 := t.TempDir()
	res2, err := Split(src, dir2, false, "1-2,1-2", "")
	if err != nil {
		t.Fatal(err)
	}
	if res2.Details["count"] != 2 {
		t.Errorf("重复段应产出 2 个文件，得到 %v", res2.Details["count"])
	}
	// 无模式报错
	if _, err := Split(src, dir2, false, "", ""); err == nil {
		t.Error("应要求 --per-page 或 --ranges")
	}
}

func TestCleanBlank(t *testing.T) {
	src := makePDF(t,
		"0 0 0 RG 1 w 50 50 m 500 700 l S",  // 有墨
		"",                                  // 空白
		"BT /F1 12 Tf 72 720 Td (hi) Tj ET", // 有文字
	)
	// dry-run
	res, err := CleanBlank(src, "", 0.002, 48, true)
	if err != nil {
		t.Fatal(err)
	}
	blanks := res.Details["blank_pages"].([]int)
	if len(blanks) != 1 || blanks[0] != 2 {
		t.Errorf("blank=%v want [2]", blanks)
	}
	// 实删
	out := filepath.Join(t.TempDir(), "clean.pdf")
	res, err = CleanBlank(src, out, 0.002, 48, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := pageCount(t, out); n != 2 {
		t.Errorf("clean 后应 2 页，得到 %d", n)
	}
}

func TestNumberStampsText(t *testing.T) {
	src := makePDF(t, "q Q", "q Q", "q Q")
	out := filepath.Join(t.TempDir(), "num.pdf")
	res, err := NumberPages(src, out, 2, 10, "page {n} of {t}", "bottom-center", 24, 11, "black", 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pages != 2 {
		t.Errorf("stamped=%d", res.Pages)
	}
	all := allStreamText(t, out)
	if !strings.Contains(all, "page 10 of 3") {
		t.Errorf("应含 'page 10 of 3'")
	}
	if strings.Contains(all, "page 9 of 3") || strings.Contains(all, "page 12 of 3") {
		t.Error("编号计算错误")
	}
	// 校验错误
	if _, err := NumberPages(src, out, 1, 1, "notoken", "center", 1, 1, "black", 1); err == nil {
		t.Error("缺 {n} 应报错")
	}
	if _, err := NumberPages(src, out, 1, 1, "{x}", "center", 1, 1, "black", 1); err == nil {
		t.Error("未知占位符应报错")
	}
	if _, err := NumberPages(src, out, 9, 1, "{n}", "center", 1, 1, "black", 1); err == nil {
		t.Error("start-page 越界应报错")
	}
}

func TestWatermarkTextAndValidation(t *testing.T) {
	src := makePDF(t, "q Q", "q Q")
	out := filepath.Join(t.TempDir(), "wm.pdf")
	if _, err := Watermark(src, out, "SECRET", "", "", 0.3, 45, 30, "gray", "center", false, 0.25); err != nil {
		t.Fatal(err)
	}
	all := allStreamText(t, out)
	if !strings.Contains(all, "SECRET") {
		t.Errorf("应含水印文字")
	}
	// 互斥校验
	if _, err := Watermark(src, out, "", "", "", 0.3, 45, 30, "gray", "center", false, 0.25); err == nil {
		t.Error("text/image 全缺应报错")
	}
	if _, err := Watermark(src, out, "a", "b.png", "", 0.3, 45, 30, "gray", "center", false, 0.25); err == nil {
		t.Error("text/image 同时给应报错")
	}
	if _, err := Watermark(src, out, "a", "", "", 0, 45, 30, "gray", "center", false, 0.25); err == nil {
		t.Error("opacity 0 应报错")
	}
}

func TestMergeImagesAndPDF(t *testing.T) {
	pdf1 := makePDF(t, "q Q", "q Q")
	img1 := makePNG(t, 200, 100)
	img2 := makePNG(t, 100, 300)
	out := filepath.Join(t.TempDir(), "merged.pdf")
	res, err := Merge([]string{pdf1, img1, img2}, out, 96, 300e9)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pages != 4 {
		t.Errorf("pages=%d want 4", res.Pages)
	}
	if n := pageCount(t, out); n != 4 {
		t.Errorf("实际页数 %d", n)
	}
	info, _ := api.PDFInfo(context.Background(), bytes.NewReader(readFile(t, out)), "", nil, false, conf())
	// 第 3 页应为 200x100 px @96dpi = 150x75 pt
	d := info.PageBoundaries[2]
	r := d.Crop
	if r == nil || r.Rect == nil {
		r = d.Media
	}
	if r == nil || r.Rect == nil {
		t.Fatal("无页面尺寸")
	}
	if w := r.Rect.Width(); w < 149 || w > 151 {
		t.Errorf("图片页宽应为 150pt，得到 %.1f", w)
	}
}

func TestOpenRejects(t *testing.T) {
	if _, _, err := Open(filepath.Join(t.TempDir(), "nope.pdf")); err == nil {
		t.Error("不存在文件应报错")
	}
	txt := filepath.Join(t.TempDir(), "a.txt")
	os.WriteFile(txt, []byte("x"), 0o644)
	if _, _, err := Open(txt); err == nil {
		t.Error("非 PDF 扩展名应报错")
	}
}

func TestInfo(t *testing.T) {
	src := makePDF(t, "q Q", "BT /F1 12 Tf 72 720 Td (hi) Tj ET")
	res, err := PDFInfo(src)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pages != 2 {
		t.Errorf("pages=%d", res.Pages)
	}
	if res.Details["pages_with_text"] != 1 {
		t.Errorf("text pages=%v", res.Details["pages_with_text"])
	}
	sizes := res.Details["page_sizes"].(map[string]int)
	if sizes["595x842pt"] != 2 {
		t.Errorf("sizes=%v", sizes)
	}
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
