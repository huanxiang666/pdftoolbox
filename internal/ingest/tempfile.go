package ingest

import (
	"bytes"
	"io"
	"os"
)

// writeTempImage 把内存图像字节落到临时文件（pdfcpu 导入需要文件路径）。
func writeTempImage(data []byte) (string, error) {
	f, err := os.CreateTemp("", "pdftoolbox-img-*")
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

func removeTempImage(p string) { os.Remove(p) }

func readerOf(b []byte) io.Reader { return bytes.NewReader(b) }
