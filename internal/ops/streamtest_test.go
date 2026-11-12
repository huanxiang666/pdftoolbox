package ops

import (
	"bytes"
	"compress/zlib"
	"io"
	"os"
	"regexp"
	"testing"
)

// allStreamText 把 PDF 里所有 FlateDecode 流解压拼接，用于断言戳记文字确实写入了文件。
// 戳记被 pdfcpu 放在 Form XObject 里，页面内容流只有 /Fm Do，故需全流搜索。
func allStreamText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`stream\r?\n`)
	var out bytes.Buffer
	for _, loc := range re.FindAllIndex(data, -1) {
		start := loc[1]
		end := bytes.Index(data[start:], []byte("endstream"))
		if end < 0 {
			continue
		}
		chunk := data[start : start+end]
		zr, err := zlib.NewReader(bytes.NewReader(chunk))
		if err != nil {
			continue // 非 flate 流，跳过
		}
		plain, err := io.ReadAll(zr)
		zr.Close()
		if err != nil {
			continue
		}
		out.Write(plain)
		out.WriteByte('\n')
	}
	return out.String()
}
