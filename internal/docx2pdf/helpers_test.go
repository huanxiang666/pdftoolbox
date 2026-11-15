package docx2pdf

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"io"
	"testing"

	"github.com/huanxiang666/pdftoolbox/internal/cjkfont"
)

func newZipWriter(w io.Writer) *zip.Writer { return zip.NewWriter(w) }

func loadTestFont(t *testing.T) []byte {
	t.Helper()
	data := cjkfont.Data()
	if len(data) < 1000 {
		t.Fatal("内嵌字体为空")
	}
	return data
}

// inflateAll 解压 PDF 里全部 FlateDecode 流并拼接。
func inflateAll(t *testing.T, pdf []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	for i := 0; i < len(pdf)-8; i++ {
		if bytes.Equal(pdf[i:i+6], []byte("stream")) {
			start := i + 6
			if start < len(pdf) && pdf[start] == '\r' {
				start++
			}
			if start < len(pdf) && pdf[start] == '\n' {
				start++
			}
			end := bytes.Index(pdf[start:], []byte("endstream"))
			if end < 0 {
				continue
			}
			chunk := pdf[start : start+end]
			zr, err := zlib.NewReader(bytes.NewReader(chunk))
			if err != nil {
				continue
			}
			io.Copy(&out, zr)
			zr.Close()
			out.WriteByte('\n')
		}
	}
	return out.Bytes()
}
