package webui

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// zipDir 把目录下的文件打成 zip 字节（split 的多文件结果用）。
// 读取或写入失败都会返回错误，不会静默生成不完整压缩包。
func zipDir(dir string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取拆分结果目录失败：%w", err)
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			zw.Close()
			return nil, fmt.Errorf("读取拆分结果 %s 失败：%w", e.Name(), err)
		}
		header := &zip.FileHeader{Name: e.Name(), Method: zip.Deflate}
		f, err := zw.CreateHeader(header)
		if err != nil {
			zw.Close()
			return nil, fmt.Errorf("创建压缩文件 %s 失败：%w", e.Name(), err)
		}
		if _, err := f.Write(data); err != nil {
			zw.Close()
			return nil, fmt.Errorf("写入压缩文件 %s 失败：%w", e.Name(), err)
		}
		count++
	}
	if count == 0 {
		zw.Close()
		return nil, fmt.Errorf("没有可下载的拆分结果")
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("完成压缩包失败：%w", err)
	}
	return buf.Bytes(), nil
}
