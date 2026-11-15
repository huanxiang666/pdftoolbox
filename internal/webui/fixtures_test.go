package webui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
)

type testingT interface {
	Helper()
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	TempDir() string
}

// minimalPDF 构造合法的三页 PDF：文字页、空白页、描边页。
// 不依赖仓库外文件，测试在任何机器上都可重复执行。
func minimalPDF(t testingT) []byte {
	t.Helper()
	contents := []string{
		"BT /F1 12 Tf 40 100 Td (hello) Tj ET",
		"",
		"0 0 0 RG 1 w 10 10 m 150 150 l S",
	}
	var body bytes.Buffer
	body.WriteString("%PDF-1.4\n")
	offsets := map[int]int{}
	obj := func(n int, s string) {
		offsets[n] = body.Len()
		fmt.Fprintf(&body, "%d 0 obj\n%s\nendobj\n", n, s)
	}
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	obj(2, "<< /Type /Pages /Count 3 /Kids [3 0 R 5 0 R 7 0 R] >>")
	for i, content := range contents {
		pageObj := 3 + i*2
		contentObj := pageObj + 1
		obj(pageObj, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /Font << /F1 9 0 R >> >> /Contents %d 0 R >>", contentObj))
		obj(contentObj, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}
	obj(9, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	maxObj := 9
	var xref bytes.Buffer
	xrefPos := body.Len()
	fmt.Fprintf(&xref, "xref\n0 %d\n", maxObj+1)
	xref.WriteString("0000000000 65535 f \n")
	for i := 1; i <= maxObj; i++ {
		fmt.Fprintf(&xref, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&xref, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", maxObj+1, xrefPos)
	return append(body.Bytes(), xref.Bytes()...)
}

func testPNG(t testingT) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 180, G: 30, B: 30, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type upload struct {
	name string
	data []byte
}

func multipartRequest(t testingT, op, params string, files []upload) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("op", op); err != nil {
		t.Fatal(err)
	}
	if err := mw.WriteField("params", params); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		part, err := mw.CreateFormFile("files", f.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(part, bytes.NewReader(f.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/op", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r
}

func writeTempPDF(t testingT, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.pdf")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
