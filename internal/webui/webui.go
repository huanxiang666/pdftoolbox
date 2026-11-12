// Package webui 提供内嵌的本地 Web 界面。
//
// 设计取向（人体工程）：
//   - 双击 exe 后浏览器自动打开，拖文件进去就能用，不需要记命令
//   - 七个标签页与 CLI 子命令一一对应，参数名与 CLI 相同
//   - 所有处理都在本机完成，文件不出机器
package webui

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/huanxiang666/pdftoolbox/internal/common"
	"github.com/huanxiang666/pdftoolbox/internal/ops"
)

// Handler 返回 GUI 的 HTTP handler。
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/api/op", handleOp)
	mux.HandleFunc("/dl", handleDownload)
	mux.HandleFunc("/api/check", handleCheck)
	return mux
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

// resultStore 保存待下载的结果文件。
var resultStore = struct {
	sync.Mutex
	items map[string]storedFile
	seq   int
}{items: map[string]storedFile{}}

type storedFile struct {
	name string
	data []byte
	at   time.Time
}

func storeResult(name string, data []byte) string {
	resultStore.Lock()
	defer resultStore.Unlock()
	resultStore.seq++
	id := fmt.Sprintf("r%d-%d", time.Now().Unix(), resultStore.seq)
	resultStore.items[id] = storedFile{name: name, data: data, at: time.Now()}
	// 清理 1 小时前的结果
	for k, v := range resultStore.items {
		if time.Since(v.at) > time.Hour {
			delete(resultStore.items, k)
		}
	}
	return id
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	resultStore.Lock()
	f, ok := resultStore.items[id]
	resultStore.Unlock()
	if !ok {
		http.Error(w, "result not found or expired", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeHeader(f.name)))
	w.Header().Set("Content-Type", "application/pdf")
	w.Write(f.data)
}

func sanitizeHeader(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '"' || r == '\r' || r == '\n' {
			return '_'
		}
		return r
	}, s)
}

func handleCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"version": "1.0.0", "ok": true})
}

// handleOp 执行一次操作。multipart 表单：
//
//	op=merge|split|rotate|number|watermark|clean|info
//	params=<json 字符串>
//	files=<一个或多个上传文件>
func handleOp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		writeErr(w, fmt.Errorf("upload too large or malformed: %v", err))
		return
	}
	opName := r.FormValue("op")
	var params map[string]any
	if s := r.FormValue("params"); s != "" {
		if err := json.Unmarshal([]byte(s), &params); err != nil {
			writeErr(w, fmt.Errorf("bad params: %v", err))
			return
		}
	}
	if params == nil {
		params = map[string]any{}
	}

	tmpDir, err := os.MkdirTemp("", "pdftoolbox-web-")
	if err != nil {
		writeErr(w, err)
		return
	}
	defer os.RemoveAll(tmpDir)

	files := r.MultipartForm.File["files"]
	paths, err := saveUploads(files, tmpDir)
	if err != nil {
		writeErr(w, err)
		return
	}

	res, outName, outData, err := dispatch(opName, params, paths, tmpDir)
	if err != nil {
		writeErr(w, err)
		return
	}
	resp := map[string]any{
		"messages": res.Messages,
		"pages":    res.Pages,
		"details":  res.Details,
	}
	if outData != nil {
		id := storeResult(outName, outData)
		resp["download"] = "/dl?id=" + id
		resp["filename"] = outName
	}
	writeJSON(w, resp)
}

func saveUploads(files []*multipart.FileHeader, dir string) ([]string, error) {
	var out []string
	seen := map[string]int{}
	for _, fh := range files {
		base := filepath.Base(fh.Filename)
		if base == "" || base == "." || base == string(filepath.Separator) {
			base = "upload.bin"
		}
		// 同名上传加序号，避免互相覆盖
		if n, ok := seen[strings.ToLower(base)]; ok {
			seen[strings.ToLower(base)] = n + 1
			ext := filepath.Ext(base)
			base = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(base, ext), n+1, ext)
		} else {
			seen[strings.ToLower(base)] = 1
		}
		dst := filepath.Join(dir, base)
		src, err := fh.Open()
		if err != nil {
			return nil, err
		}
		f, err := os.Create(dst)
		if err != nil {
			src.Close()
			return nil, err
		}
		if _, err := io.Copy(f, src); err != nil {
			f.Close()
			src.Close()
			return nil, err
		}
		f.Close()
		src.Close()
		out = append(out, dst)
	}
	return out, nil
}

// dispatch 把 Web 请求映射到 ops 调用，返回结果与可选的输出文件内容。
func dispatch(opName string, p map[string]any, paths []string, tmpDir string) (*ops.Result, string, []byte, error) {
	str := func(k, def string) string {
		if v, ok := p[k].(string); ok && v != "" {
			return v
		}
		return def
	}
	flt := func(k string, def float64) float64 {
		switch v := p[k].(type) {
		case float64:
			return v
		case string:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return f
			}
		}
		return def
	}
	intg := func(k string, def int) int {
		switch v := p[k].(type) {
		case float64:
			return int(v)
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
		return def
	}
	boo := func(k string) bool {
		if v, ok := p[k].(bool); ok {
			return v
		}
		if s, ok := p[k].(string); ok {
			return s == "true" || s == "1"
		}
		return false
	}

	outPDF := filepath.Join(tmpDir, "result.pdf")

	switch opName {
	case "merge":
		if len(paths) == 0 {
			return nil, "", nil, common.Err("merge needs at least one file")
		}
		res, err := ops.Merge(paths, outPDF, intg("image-dpi", 96), time.Duration(intg("word-timeout", 300))*time.Second)
		if err != nil {
			return nil, "", nil, err
		}
		data, err := os.ReadFile(outPDF)
		return res, "merged.pdf", data, err
	case "split":
		if len(paths) != 1 {
			return nil, "", nil, common.Err("split needs exactly one PDF")
		}
		outDir := filepath.Join(tmpDir, "parts")
		res, err := ops.Split(paths[0], outDir, boo("per-page"), str("ranges", ""), str("prefix", ""))
		if err != nil {
			return nil, "", nil, err
		}
		// 打包成 zip 返回
		zipBytes, err := zipDir(outDir)
		if err != nil {
			return nil, "", nil, err
		}
		return res, "split-parts.zip", zipBytes, nil
	case "rotate":
		if len(paths) != 1 {
			return nil, "", nil, common.Err("rotate needs exactly one PDF")
		}
		res, err := ops.Rotate(paths[0], outPDF, intg("angle", 90), str("pages", ""))
		if err != nil {
			return nil, "", nil, err
		}
		data, err := os.ReadFile(outPDF)
		return res, "rotated.pdf", data, err
	case "number":
		if len(paths) != 1 {
			return nil, "", nil, common.Err("number needs exactly one PDF")
		}
		res, err := ops.NumberPages(paths[0], outPDF,
			intg("start-page", 1), intg("first-number", 1),
			str("format", "{n}"), str("position", "bottom-center"),
			flt("margin", 24), flt("fontsize", 11), str("color", "black"), flt("opacity", 1))
		if err != nil {
			return nil, "", nil, err
		}
		data, err := os.ReadFile(outPDF)
		return res, "numbered.pdf", data, err
	case "watermark":
		if len(paths) != 1 {
			return nil, "", nil, common.Err("watermark needs exactly one PDF (plus optional image as second file)")
		}
		text := str("text", "")
		image := ""
		if len(paths) > 1 {
			image = paths[1]
		}
		res, err := ops.Watermark(paths[0], outPDF, text, image, str("pages", ""),
			flt("opacity", 0.25), flt("angle", 45), flt("fontsize", 36),
			str("color", "gray"), str("position", "center"), boo("tile"), flt("image-scale", 0.25))
		if err != nil {
			return nil, "", nil, err
		}
		data, err := os.ReadFile(outPDF)
		return res, "watermarked.pdf", data, err
	case "clean":
		if len(paths) != 1 {
			return nil, "", nil, common.Err("clean needs exactly one PDF")
		}
		dry := boo("dry-run")
		res, err := ops.CleanBlank(paths[0], outPDF, flt("threshold", 0.002), intg("dpi", 48), dry)
		if err != nil {
			return nil, "", nil, err
		}
		if dry {
			return res, "", nil, nil
		}
		data, err := os.ReadFile(outPDF)
		return res, "cleaned.pdf", data, err
	case "info":
		if len(paths) != 1 {
			return nil, "", nil, common.Err("info needs exactly one PDF")
		}
		res, err := ops.PDFInfo(paths[0])
		return res, "", nil, err
	}
	return nil, "", nil, common.Err("unknown operation %q", opName)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK) // 业务错误用 200 + ok=false，前端统一处理
	json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
}
