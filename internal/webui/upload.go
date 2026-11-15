package webui

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	defaultMaxRequestBytes int64 = 256 << 20 // 一次请求总量上限（含表单字段）
	defaultMaxFileBytes    int64 = 64 << 20  // 单个上传文件上限
	maxUploadFiles               = 32
)

// 这些变量允许测试使用较小限制，生产环境始终保持默认值。
var (
	maxRequestBytes = defaultMaxRequestBytes
	maxFileBytes    = defaultMaxFileBytes
	tempRoot        string
)

var (
	errUploadTooLarge = errors.New("上传内容超过大小限制")
	errTooManyFiles   = errors.New("一次最多上传 32 个文件")
)

// safeUploadName 只保留文件名本身，阻止目录穿越和控制字符。
// Windows 与 Unix 的分隔符都处理；保留中文，同时兼顾 Windows 保留名。
func safeUploadName(raw string) string {
	raw = strings.ReplaceAll(raw, "\\", "/")
	name := path.Base(raw)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == 0 {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	name = strings.TrimRight(name, " .")
	if name == "" || name == "." || name == ".." {
		name = "upload.bin"
	}
	if isWindowsReservedName(name) {
		name = "_" + name
	}
	// 限制长度，并尽量保留扩展名。
	runes := []rune(name)
	if len(runes) > 160 {
		ext := filepath.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		keep := 160 - len([]rune(ext))
		if keep < 1 {
			keep = 1
		}
		stemRunes := []rune(stem)
		if len(stemRunes) > keep {
			stem = string(stemRunes[:keep])
		}
		name = stem + ext
	}
	return name
}

func isWindowsReservedName(name string) bool {
	stem := strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name)))
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9' {
		return true
	}
	return false
}

// saveUploads 把 multipart 文件按上传顺序安全地写入 dir。
func saveUploads(files []*multipart.FileHeader, dir string) ([]string, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("请先选择要处理的文件")
	}
	if len(files) > maxUploadFiles {
		return nil, errTooManyFiles
	}

	out := make([]string, 0, len(files))
	seen := map[string]int{}
	for _, fh := range files {
		if fh.Size > maxFileBytes {
			return nil, errUploadTooLarge
		}
		base := safeUploadName(fh.Filename)
		key := strings.ToLower(base)
		if n, ok := seen[key]; ok {
			seen[key] = n + 1
			ext := filepath.Ext(base)
			base = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(base, ext), n+1, ext)
			key = strings.ToLower(base)
		} else {
			seen[key] = 1
		}

		dst := filepath.Join(dir, base)
		src, err := fh.Open()
		if err != nil {
			return nil, fmt.Errorf("打开上传文件失败：%w", err)
		}
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			src.Close()
			return nil, fmt.Errorf("创建临时文件失败：%w", err)
		}

		written, copyErr := io.Copy(f, io.LimitReader(src, maxFileBytes+1))
		closeErr := f.Close()
		srcErr := src.Close()
		if copyErr != nil || written > maxFileBytes || closeErr != nil || srcErr != nil {
			os.Remove(dst)
			if copyErr != nil {
				return nil, classifyUploadError(copyErr)
			}
			if written > maxFileBytes {
				return nil, errUploadTooLarge
			}
			if closeErr != nil {
				return nil, fmt.Errorf("保存临时文件失败：%w", closeErr)
			}
			return nil, fmt.Errorf("关闭上传文件失败：%w", srcErr)
		}
		out = append(out, dst)
	}
	return out, nil
}

func classifyUploadError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "request body too large") {
		return errUploadTooLarge
	}
	return fmt.Errorf("读取上传文件失败：%w", err)
}

// isRequestTooLarge 同时处理 MaxBytesReader 与分块上传的超限错误。
func isRequestTooLarge(err error) bool {
	return errors.Is(err, errUploadTooLarge) || (err != nil && strings.Contains(err.Error(), "request body too large"))
}

// requestMethod 限制写操作只接受 POST。
func requestMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodPost {
		return true
	}
	w.Header().Set("Allow", http.MethodPost)
	writeAPIError(w, http.StatusMethodNotAllowed, fmt.Errorf("该接口只接受 POST 请求"))
	return false
}
