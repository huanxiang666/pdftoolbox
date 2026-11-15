package webui

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type opResponse struct {
	OK       bool           `json:"ok"`
	Error    string         `json:"error"`
	Messages []string       `json:"messages"`
	Pages    int            `json:"pages"`
	Details  map[string]any `json:"details"`
	Download string         `json:"download"`
	Filename string         `json:"filename"`
}

func decodeOpResponse(t *testing.T, rr *httptest.ResponseRecorder) opResponse {
	t.Helper()
	var got opResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("响应不是合法 JSON: %v\n%s", err, rr.Body.String())
	}
	return got
}

func useTestTempRoot(t *testing.T) {
	t.Helper()
	old := tempRoot
	tempRoot = t.TempDir()
	t.Cleanup(func() { tempRoot = old })
}

func TestSplitRequestToZipDownload(t *testing.T) {
	useTestTempRoot(t)
	h := Handler()
	r := multipartRequest(t, "split", `{"per-page":true}`, []upload{{name: "sample.pdf", data: minimalPDF(t)}})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("split HTTP %d: %s", rr.Code, rr.Body.String())
	}
	got := decodeOpResponse(t, rr)
	if !got.OK || got.Download == "" {
		t.Fatalf("split 响应缺少下载地址: %+v", got)
	}
	if got.Filename != "split-parts.zip" {
		t.Errorf("文件名错误: %q", got.Filename)
	}

	// 处理完成时临时工作目录已回收。
	entries, err := os.ReadDir(tempRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("临时目录未清理: %v", entries)
	}

	dl := httptest.NewRecorder()
	h.ServeHTTP(dl, httptest.NewRequest(http.MethodGet, got.Download, nil))
	if dl.Code != http.StatusOK {
		t.Fatalf("下载 HTTP %d: %s", dl.Code, dl.Body.String())
	}
	if ct := dl.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("下载类型错误: %q", ct)
	}
	if cd := dl.Header().Get("Content-Disposition"); !strings.Contains(cd, ".zip") {
		t.Errorf("下载文件名头错误: %q", cd)
	}

	zr, err := zip.NewReader(bytes.NewReader(dl.Body.Bytes()), int64(dl.Body.Len()))
	if err != nil {
		t.Fatalf("下载内容不是 ZIP: %v", err)
	}
	if len(zr.File) != 3 {
		t.Fatalf("ZIP 应含 3 页文件，实际 %d", len(zr.File))
	}
	for i, f := range zr.File {
		want := "sample-000" + string(rune('1'+i)) + ".pdf"
		if f.Name != want {
			t.Errorf("ZIP 第 %d 个文件名=%q want %q", i, f.Name, want)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data := make([]byte, 5)
		_, _ = rc.Read(data)
		rc.Close()
		if string(data) != "%PDF-" {
			t.Errorf("%s 内容不是 PDF", f.Name)
		}
	}
}

func TestImageWatermarkRequestToPDFDownload(t *testing.T) {
	useTestTempRoot(t)
	h := Handler()
	r := multipartRequest(t, "watermark", `{"image-scale":0.3,"position":"bottom-right"}`, []upload{
		{name: "input.pdf", data: minimalPDF(t)},
		{name: "watermark.png", data: testPNG(t)},
	})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("图片水印被错误拒绝: HTTP %d: %s", rr.Code, rr.Body.String())
	}
	got := decodeOpResponse(t, rr)
	if !got.OK || got.Download == "" {
		t.Fatalf("图片水印响应异常: %+v", got)
	}

	dl := httptest.NewRecorder()
	h.ServeHTTP(dl, httptest.NewRequest(http.MethodGet, got.Download, nil))
	if dl.Code != http.StatusOK {
		t.Fatalf("下载失败: %s", dl.Body.String())
	}
	if ct := dl.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("下载类型错误: %q", ct)
	}
	if !bytes.HasPrefix(dl.Body.Bytes(), []byte("%PDF-")) {
		t.Error("下载结果不是 PDF")
	}
	if dl.Body.Len() <= len(minimalPDF(t)) {
		t.Error("图片水印未改变输出 PDF")
	}
}

func TestMergePreservesUploadOrder(t *testing.T) {
	useTestTempRoot(t)
	h := Handler()
	r := multipartRequest(t, "merge", `{}`, []upload{
		{name: "first.pdf", data: minimalPDF(t)},
		{name: "second.png", data: testPNG(t)},
	})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("merge HTTP %d: %s", rr.Code, rr.Body.String())
	}
	got := decodeOpResponse(t, rr)
	if got.Pages != 4 {
		t.Errorf("合并页数=%d want 4", got.Pages)
	}
	if len(got.Messages) != 2 || !strings.Contains(got.Messages[0], "first.pdf") || !strings.Contains(got.Messages[1], "second.png") {
		t.Errorf("上传顺序被改变: %v", got.Messages)
	}
	dl := httptest.NewRecorder()
	h.ServeHTTP(dl, httptest.NewRequest(http.MethodGet, got.Download, nil))
	if !bytes.HasPrefix(dl.Body.Bytes(), []byte("%PDF-")) {
		t.Error("合并下载不是 PDF")
	}
}

func TestInfoRequest(t *testing.T) {
	useTestTempRoot(t)
	rr := httptest.NewRecorder()
	Handler().ServeHTTP(rr, multipartRequest(t, "info", `{}`, []upload{{name: "in.pdf", data: minimalPDF(t)}}))
	if rr.Code != http.StatusOK {
		t.Fatalf("info HTTP %d: %s", rr.Code, rr.Body.String())
	}
	got := decodeOpResponse(t, rr)
	if !got.OK || got.Pages != 3 {
		t.Fatalf("info 结果错误: %+v", got)
	}
	if got.Details["pages_with_text"] != float64(1) {
		t.Errorf("文本页统计错误: %+v", got.Details)
	}
}

func TestBadRequestsReturnReadableJSON(t *testing.T) {
	pdf := minimalPDF(t)
	cases := []struct {
		name       string
		method     string
		op         string
		params     string
		files      []upload
		wantStatus int
		wantText   string
	}{
		{"method", http.MethodGet, "info", `{}`, []upload{{"in.pdf", pdf}}, http.StatusMethodNotAllowed, "POST"},
		{"bad json", http.MethodPost, "info", `{`, []upload{{"in.pdf", pdf}}, http.StatusBadRequest, "JSON"},
		{"no files", http.MethodPost, "merge", `{}`, nil, http.StatusBadRequest, "文件"},
		{"unknown op", http.MethodPost, "nope", `{}`, []upload{{"in.pdf", pdf}}, http.StatusUnprocessableEntity, "unknown operation"},
		{"split needs one", http.MethodPost, "split", `{"per-page":true}`, []upload{{"a.pdf", pdf}, {"b.pdf", pdf}}, http.StatusUnprocessableEntity, "exactly one"},
		{"text watermark with image", http.MethodPost, "watermark", `{"text":"机密"}`, []upload{{"a.pdf", pdf}, {"b.png", testPNG(t)}}, http.StatusUnprocessableEntity, "文字水印"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useTestTempRoot(t)
			h := Handler()
			var r *http.Request
			if tc.method == http.MethodGet {
				r = httptest.NewRequest(tc.method, "/api/op", nil)
			} else {
				r = multipartRequest(t, tc.op, tc.params, tc.files)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, r)
			if rr.Code != tc.wantStatus {
				t.Fatalf("HTTP %d want %d: %s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("错误响应不是 JSON: %q", ct)
			}
			got := decodeOpResponse(t, rr)
			if got.OK || got.Error == "" || !strings.Contains(got.Error, tc.wantText) {
				t.Errorf("错误信息不清: %+v, 期望包含 %q", got, tc.wantText)
			}
		})
	}
}

func TestUploadSizeLimits(t *testing.T) {
	oldReq, oldFile := maxRequestBytes, maxFileBytes
	t.Cleanup(func() { maxRequestBytes, maxFileBytes = oldReq, oldFile })
	useTestTempRoot(t)

	maxFileBytes = 8
	rr := httptest.NewRecorder()
	Handler().ServeHTTP(rr, multipartRequest(t, "info", `{}`, []upload{{name: "big.pdf", data: []byte("123456789")}}))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("单文件超限 HTTP %d want 413: %s", rr.Code, rr.Body.String())
	}
	if got := decodeOpResponse(t, rr); got.OK || got.Error == "" {
		t.Errorf("超限错误不可读: %+v", got)
	}

	maxFileBytes = oldFile
	maxRequestBytes = 16
	rr = httptest.NewRecorder()
	Handler().ServeHTTP(rr, multipartRequest(t, "info", `{}`, []upload{{name: "big.pdf", data: minimalPDF(t)}}))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("总请求超限 HTTP %d want 413: %s", rr.Code, rr.Body.String())
	}
}

func TestSaveUploadsSanitizesNamesAndDuplicates(t *testing.T) {
	dir := t.TempDir()
	files := []upload{
		{name: `..\..\evil.pdf`, data: minimalPDF(t)},
		{name: `../evil.pdf`, data: minimalPDF(t)},
		{name: "CON.pdf", data: minimalPDF(t)},
	}
	// multipartRequest 构造的 FileHeader 保留原始文件名，直接测试保存层。
	r := multipartRequest(t, "info", `{}`, files)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		t.Fatal(err)
	}
	defer r.MultipartForm.RemoveAll()
	paths, err := saveUploads(r.MultipartForm.File["files"], dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Fatalf("保存文件数错误: %d", len(paths))
	}
	for i, p := range paths {
		base := filepath.Base(p)
		if strings.Contains(base, "..") || strings.ContainsAny(base, `/\\`) {
			t.Errorf("文件名未清理: %q", base)
		}
		if _, err := os.Stat(p); err != nil {
			t.Errorf("文件未保存: %v", err)
		}
		if i > 0 && paths[i] == paths[i-1] {
			t.Errorf("同名文件被覆盖: %q", p)
		}
	}
	if filepath.Base(paths[0]) != "evil.pdf" || filepath.Base(paths[1]) != "evil-2.pdf" {
		t.Errorf("同名去重错误: %v", paths)
	}
	if filepath.Base(paths[2]) != "_CON.pdf" {
		t.Errorf("Windows 保留名未处理: %q", filepath.Base(paths[2]))
	}
}

func TestDownloadHeaderSafetyAndHead(t *testing.T) {
	id, err := storeResult("bad\"\r\nreport.zip", []byte("PK\x03\x04test"))
	if err != nil {
		t.Fatal(err)
	}
	h := Handler()
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(method, "/dl?id="+id, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("%s HTTP %d", method, rr.Code)
		}
		cd := rr.Header().Get("Content-Disposition")
		if strings.ContainsAny(cd, "\r\n") {
			t.Errorf("响应头被注入: %q", cd)
		}
		if !strings.Contains(cd, "filename*=UTF-8") {
			t.Errorf("缺少 UTF-8 文件名: %q", cd)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/zip" {
			t.Errorf("类型错误: %q", ct)
		}
		if method == http.MethodHead && rr.Body.Len() != 0 {
			t.Error("HEAD 不应返回正文")
		}
	}
	if _, err := storeResult("empty.pdf", nil); err == nil {
		t.Error("空结果不应被保存")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/dl?id=missing", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("过期结果应返回 404: %d", rr.Code)
	}
	if got := decodeOpResponse(t, rr); got.OK || got.Error == "" {
		t.Error("404 应返回 JSON 错误")
	}
}

func TestIndexHasSafeUIAndShutdown(t *testing.T) {
	rr := httptest.NewRecorder()
	Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "innerHTML") {
		t.Error("前端仍使用 innerHTML")
	}
	for _, want := range []string{"上移", "下移", "清空列表", "退出程序", "/api/shutdown", "wmMode", "处理中"} {
		if !strings.Contains(body, want) {
			t.Errorf("页面缺少 %q", want)
		}
	}
	if csp := rr.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("缺少 CSP: %q", csp)
	}
}
