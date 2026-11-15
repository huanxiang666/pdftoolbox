package webui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	resultTTL               = time.Hour
	maxStoredResults        = 64
	maxStoredResultBytes    = 256 << 20
	maxResultHeaderNameSize = 160
)

type storedFile struct {
	name        string
	data        []byte
	at          time.Time
	contentType string
}

var resultStore = struct {
	sync.Mutex
	items map[string]storedFile
	bytes int64
}{items: map[string]storedFile{}}

// storeResult 将处理结果放到带过期时间和容量上限的内存存储中。
// ID 使用随机值，避免结果被枚举。
func storeResult(name string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("处理结果为空，无法提供下载")
	}
	if int64(len(data)) > maxStoredResultBytes {
		return "", fmt.Errorf("结果文件过大，无法提供下载")
	}

	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		return "", fmt.Errorf("生成下载编号失败：%w", err)
	}
	id := "r" + hex.EncodeToString(idBytes)

	resultStore.Lock()
	defer resultStore.Unlock()
	now := time.Now()
	for k, v := range resultStore.items {
		if now.Sub(v.at) > resultTTL {
			resultStore.bytes -= int64(len(v.data))
			delete(resultStore.items, k)
		}
	}
	// 先删最旧项，确保新增结果不会造成无限内存增长。
	for len(resultStore.items) >= maxStoredResults || resultStore.bytes+int64(len(data)) > maxStoredResultBytes {
		var oldestID string
		var oldest time.Time
		for k, v := range resultStore.items {
			if oldestID == "" || v.at.Before(oldest) {
				oldestID, oldest = k, v.at
			}
		}
		if oldestID == "" {
			break
		}
		resultStore.bytes -= int64(len(resultStore.items[oldestID].data))
		delete(resultStore.items, oldestID)
	}

	name = safeUploadName(name)
	if len([]rune(name)) > maxResultHeaderNameSize {
		name = string([]rune(name)[:maxResultHeaderNameSize])
	}
	resultStore.items[id] = storedFile{
		name:        name,
		data:        data,
		at:          now,
		contentType: resultContentType(name),
	}
	resultStore.bytes += int64(len(data))
	return id, nil
}

func resultContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	default:
		return "application/octet-stream"
	}
}

// contentDisposition 同时提供 ASCII 回退名与 UTF-8 文件名。
// 去除 CR/LF，防止响应头注入。
func contentDisposition(name string) string {
	fallback := strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '"' || r == '\\' || r < 0x20 || r > 0x7e {
			return '_'
		}
		return r
	}, name)
	if fallback == "" {
		fallback = "result"
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, fallback, url.PathEscape(name))
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeAPIError(w, http.StatusMethodNotAllowed, fmt.Errorf("下载接口只接受 GET 或 HEAD 请求"))
		return
	}
	id := r.URL.Query().Get("id")
	resultStore.Lock()
	f, ok := resultStore.items[id]
	resultStore.Unlock()
	if !ok {
		writeAPIError(w, http.StatusNotFound, fmt.Errorf("结果不存在或已过期，请重新处理"))
		return
	}
	w.Header().Set("Content-Disposition", contentDisposition(f.name))
	w.Header().Set("Content-Type", f.contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(f.data)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(f.data)
}
