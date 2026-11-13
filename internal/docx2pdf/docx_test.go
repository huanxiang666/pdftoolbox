package docx2pdf

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// miniDocx 在内存里造一个最小 docx（zip）：两段文字 + 一个两列表格。
func miniDocx(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "mini.docx")
	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>标题一</w:t></w:r></w:p>
<w:p><w:r><w:rPr><w:b/><w:sz w:val="28"/><w:color w:val="FF0000"/></w:rPr><w:t>加粗红色大字 hello</w:t></w:r></w:p>
<w:p><w:r><w:t>普通段落，用于折行测试的较长中文文本，希望超过一行宽度从而触发自动折行逻辑。</w:t></w:r></w:p>
<w:tbl>
<w:tblGrid><w:gridCol w:w="4000"/><w:gridCol w:w="4000"/></w:tblGrid>
<w:tr><w:tc><w:p><w:r><w:t>甲</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>乙</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:p><w:r><w:t>1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>2</w:t></w:r></w:p></w:tc></w:tr>
</w:tbl>
<w:p><w:r><w:t>表格后段落</w:t></w:r></w:p>
</w:body></w:document>`
	styles := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docDefaults><w:rPrDefault><w:rPr><w:sz w:val="21"/></w:rPr></w:rPrDefault></w:docDefaults>
<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:pPr><w:jc w:val="left"/></w:pPr><w:rPr><w:b/><w:sz w:val="44"/></w:rPr></w:style>
</w:styles>`
	writeZip(t, p, map[string]string{
		"word/document.xml": document,
		"word/styles.xml":   styles,
	})
	return p
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := newZipWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRenderMiniDocx(t *testing.T) {
	ttf := loadTestFont(t)
	src := miniDocx(t)
	pdfBytes, err := Render(src, ttf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF-")) {
		t.Fatal("不是 PDF")
	}
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	n, err := api.PageCount(context.Background(), bytes.NewReader(pdfBytes), conf)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("页数 %d", n)
	}
	// 内容流应包含表格边框（描边矩形）与文字操作
	all := inflateAll(t, pdfBytes)
	for _, want := range []string{" re S", " Tm <", "Tj"} {
		if !bytes.Contains(all, []byte(want)) {
			t.Errorf("内容流缺少 %q", want)
		}
	}
}

func TestRenderMissingDocumentXML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.docx")
	writeZip(t, p, map[string]string{"mimetype": "x"})
	if _, err := Render(p, loadTestFont(t)); err == nil {
		t.Error("缺 document.xml 应报错")
	}
}

func TestRenderNotAZip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.docx")
	os.WriteFile(p, []byte("plain text"), 0o644)
	if _, err := Render(p, loadTestFont(t)); err == nil {
		t.Error("非 zip 应报错")
	}
}
