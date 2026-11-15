//go:build windows

// Package ingest 的 Windows COM 桥：用已安装的 Microsoft Word 或 WPS Office
// 把 Word 文档转成 PDF。两者都暴露与 Word 兼容的 COM 自动化接口，
// 保真度最高（等于用 Office 自己打印成 PDF），且不需要额外安装任何东西。
package ingest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

// comProgIDs 按优先级排列的 COM 程序标识。
var comProgIDs = []string{"Word.Application", "KWPS.Application"}

// comConverter 描述一个可用的 COM 转换器。
type comConverter struct {
	ProgID string
}

func (c comConverter) DisplayName() string {
	if strings.HasPrefix(c.ProgID, "Word") {
		return "Microsoft Word (COM)"
	}
	return "WPS Office (COM)"
}

// detectCOMConverter 探测已注册的 Word/WPS COM 服务。
// 可用 PDFTOOLBOX_COM_PROGID 强制指定（如 KWPS.Application），便于调试与偏好设置。
func detectCOMConverter() *comConverter {
	if id := strings.TrimSpace(os.Getenv("PDFTOOLBOX_COM_PROGID")); id != "" {
		if comProgIDRegistered(id) {
			return &comConverter{ProgID: id}
		}
		fmt.Fprintf(os.Stderr, "warning: PDFTOOLBOX_COM_PROGID=%q not registered; falling back\n", id)
	}
	for _, id := range comProgIDs {
		if comProgIDRegistered(id) {
			return &comConverter{ProgID: id}
		}
	}
	return nil
}

// comProgIDRegistered 查注册表 CLSID 键，避免真的启动 Office 来探测。
func comProgIDRegistered(id string) bool {
	// 用 ole 的 CLSIDFromProgID 不会启动进程，只查注册表
	ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED|ole.COINIT_DISABLE_OLE1DDE)
	defer ole.CoUninitialize()
	_, err := ole.ClassIDFrom(id)
	return err == nil
}

// convertWordViaCOM 用 Word/WPS COM 把 src 转成 outDir 下的 PDF。
func convertWordViaCOM(src, outDir string, conv *comConverter, timeout time.Duration) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	expected := filepath.Join(outDir, trimExt(filepath.Base(src))+".pdf")
	os.Remove(expected)

	// COM 调用整体放进 goroutine 以便超时控制。
	// 注意：COM 公寓线程要求创建与调用同线程，因此整个流程在同一个 goroutine 内完成。
	type result struct {
		path string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		p, err := comConvertSync(src, expected, conv.ProgID)
		ch <- result{p, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return "", r.err
		}
		if _, err := os.Stat(expected); err != nil {
			return "", fmt.Errorf("%s produced no PDF for %s", conv.DisplayName(), filepath.Base(src))
		}
		return expected, nil
	case <-time.After(timeout):
		return "", fmt.Errorf("%s timed out after %vs on %s", conv.DisplayName(), int(timeout.Seconds()), filepath.Base(src))
	}
}

// comConvertSync 在单一 goroutine 内完成 COM 初始化、转换与清理。
func comConvertSync(src, dst, progID string) (string, error) {
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}
	absDst, err := filepath.Abs(dst)
	if err != nil {
		return "", err
	}

	ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED|ole.COINIT_DISABLE_OLE1DDE)
	defer ole.CoUninitialize()

	unknown, err := oleutil.CreateObject(progID)
	if err != nil {
		return "", fmt.Errorf("cannot start %s: %w", progID, err)
	}
	defer unknown.Release()

	app, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return "", err
	}
	defer app.Release()

	// 后台静默：不显示窗口、不弹对话框
	oleutil.PutProperty(app, "Visible", false)
	if progID == "Word.Application" {
		oleutil.PutProperty(app, "DisplayAlerts", 0) // wdAlertsNone
	}

	docs, err := oleutil.GetProperty(app, "Documents")
	if err != nil {
		return "", err
	}
	docsDisp := docs.ToIDispatch()
	defer docsDisp.Release()

	// Open(FileName, ConfirmConversions=false, ReadOnly=true, AddToRecentFiles=false)
	openRes, err := oleutil.CallMethod(docsDisp, "Open", absSrc, false, true, false)
	if err != nil {
		return "", fmt.Errorf("%s cannot open %s: %w", progID, filepath.Base(src), err)
	}
	doc := openRes.ToIDispatch()
	if doc == nil {
		return "", fmt.Errorf("%s returned no document", progID)
	}
	defer func() {
		oleutil.CallMethod(doc, "Close", false)
		doc.Release()
	}()

	// 导出 PDF：优先 ExportAsFixedFormat（Word 与新版 WPS 都支持），回退 SaveAs2/SaveAs 格式 17
	if _, err := oleutil.CallMethod(doc, "ExportAsFixedFormat", absDst, 17); err == nil {
		return absDst, nil
	}
	if _, err := oleutil.CallMethod(doc, "SaveAs2", absDst, 17); err == nil {
		return absDst, nil
	}
	if _, err := oleutil.CallMethod(doc, "SaveAs", absDst, 17); err == nil {
		return absDst, nil
	}
	return "", fmt.Errorf("%s: no PDF export method available", progID)
}
