package webui

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
)

// zipDir 把目录下的文件打成 zip 字节（split 的多文件结果用）。
func zipDir(dir string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		f, err := zw.Create(e.Name())
		if err != nil {
			continue
		}
		if _, err := f.Write(data); err != nil {
			zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
