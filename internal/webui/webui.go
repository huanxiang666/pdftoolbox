// Package webui 提供内嵌的本地 Web 界面。
//
// 设计取向：
//   - 文件选择、排序、处理与下载都在浏览器内完成
//   - 所有计算都在本机执行，文件不经过外部服务
//   - 后端限制上传大小、清理临时文件并返回可读 JSON 错误
package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeAPIError(w, http.StatusMethodNotAllowed, fmt.Errorf("页面只接受 GET 或 HEAD 请求"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'self'")
	if r.Method == http.MethodGet {
		_, _ = w.Write(indexHTML)
	}
}

func handleCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, fmt.Errorf("该接口只接受 GET 请求"))
		return
	}
	writeJSON(w, map[string]any{"version": "1.0.0", "ok": true})
}

var operationNames = map[string]string{
	"merge":     "合并",
	"split":     "拆分",
	"rotate":    "旋转",
	"number":    "页码",
	"watermark": "水印",
	"clean":     "删空白页",
	"info":      "信息",
}

// handleOp 执行一次操作。multipart 表单：
//
//	op=merge|split|rotate|number|watermark|clean|info
//	params=<JSON 字符串>
//	files=<一个或多个上传文件>
func handleOp(w http.ResponseWriter, r *http.Request) {
	if !requestMethod(w, r) {
		return
	}
	// 即使操作失败也不让裸 panic 穿透到浏览器。
	defer func() {
		if recovered := recover(); recovered != nil {
			writeAPIError(w, http.StatusInternalServerError, fmt.Errorf("处理失败：%v", recovered))
		}
	}()

	if r.ContentLength > maxRequestBytes {
		writeAPIError(w, http.StatusRequestEntityTooLarge, errUploadTooLarge)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		if isRequestTooLarge(err) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, errUploadTooLarge)
		} else {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("上传表单格式不正确：%v", err))
		}
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	opName := r.FormValue("op")
	params := map[string]any{}
	if s := r.FormValue("params"); s != "" {
		if err := json.Unmarshal([]byte(s), &params); err != nil {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("参数 JSON 格式不正确：%v", err))
			return
		}
		if params == nil {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("参数必须是 JSON 对象"))
			return
		}
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("请先选择要处理的文件"))
		return
	}
	if len(files) > maxUploadFiles {
		writeAPIError(w, http.StatusRequestEntityTooLarge, errTooManyFiles)
		return
	}

	tmpDir, err := os.MkdirTemp(tempRoot, "pdftoolbox-web-")
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, fmt.Errorf("创建临时目录失败：%w", err))
		return
	}
	defer os.RemoveAll(tmpDir)

	paths, err := saveUploads(files, tmpDir)
	if err != nil {
		if isRequestTooLarge(err) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, err)
		} else {
			writeAPIError(w, http.StatusBadRequest, err)
		}
		return
	}

	res, outName, outData, err := dispatch(opName, params, paths, tmpDir)
	if err != nil {
		name := operationNames[opName]
		if name == "" {
			name = "操作"
		}
		writeAPIError(w, http.StatusUnprocessableEntity, fmt.Errorf("%s失败：%w", name, err))
		return
	}

	resp := map[string]any{
		"ok":       true,
		"messages": res.Messages,
		"pages":    res.Pages,
		"details":  res.Details,
	}
	if outData != nil {
		id, err := storeResult(outName, outData)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		resp["download"] = "/dl?id=" + id
		resp["filename"] = outName
	}
	writeJSON(w, resp)
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
		data, err := readOutput(outPDF)
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
		data, err := readOutput(outPDF)
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
		data, err := readOutput(outPDF)
		return res, "numbered.pdf", data, err
	case "watermark":
		text := str("text", "")
		image := ""
		// 文字水印只上传 PDF；图片水印必须按“PDF、水印图片”的顺序上传两个文件。
		if text != "" && len(paths) != 1 {
			return nil, "", nil, common.Err("文字水印只需要一个 PDF 文件")
		}
		if text == "" {
			if len(paths) != 2 {
				return nil, "", nil, common.Err("图片水印需要依次上传一个 PDF 和一张图片")
			}
			image = paths[1]
		}
		res, err := ops.Watermark(paths[0], outPDF, text, image, str("pages", ""),
			flt("opacity", 0.25), flt("angle", 45), flt("fontsize", 36),
			str("color", "gray"), str("position", "center"), boo("tile"), flt("image-scale", 0.25))
		if err != nil {
			return nil, "", nil, err
		}
		data, err := readOutput(outPDF)
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
		data, err := readOutput(outPDF)
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

func readOutput(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取处理结果失败：%w", err)
	}
	return data, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
}
