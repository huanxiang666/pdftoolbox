package ingest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectInputsOrderAndNaturalSort(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"img10.png", "img2.png", "note.txt", "a.pdf"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)
	}
	// 显式文件保持给定顺序
	files, ignored := CollectInputs([]string{
		filepath.Join(dir, "a.pdf"),
		filepath.Join(dir, "img10.png"),
	}, false)
	if len(files) != 2 || filepath.Base(files[0]) != "a.pdf" || filepath.Base(files[1]) != "img10.png" {
		t.Errorf("显式顺序错误: %v", files)
	}
	if len(ignored) != 0 {
		t.Errorf("ignored=%v", ignored)
	}
	// 目录展开按自然排序：img2 在 img10 前
	files, ignored = CollectInputs([]string{dir}, false)
	if len(files) != 3 {
		t.Fatalf("应得 3 个文件，得到 %v", files)
	}
	if filepath.Base(files[0]) != "a.pdf" || filepath.Base(files[1]) != "img2.png" || filepath.Base(files[2]) != "img10.png" {
		t.Errorf("自然排序错误: %v", files)
	}
	if len(ignored) != 0 {
		t.Errorf("目录内不支持文件不应计入 ignored（与 Python 版一致）: %v", ignored)
	}
}

func TestCollectInputsMissing(t *testing.T) {
	files, ignored := CollectInputs([]string{filepath.Join(t.TempDir(), "nope.pdf")}, false)
	if len(files) != 0 || len(ignored) != 1 {
		t.Errorf("files=%v ignored=%v", files, ignored)
	}
}

func TestExifOrientation(t *testing.T) {
	// 构造最小 JPEG：SOI + APP1(Exif, little-endian, 一个 0x0112=6 的条目) + 任意后续
	app1 := []byte{
		'E', 'x', 'i', 'f', 0, 0,
		'I', 'I', 42, 0, 8, 0, 0, 0, // TIFF little endian, IFD0 at 8
		1, 0, // 1 个条目
		0x12, 0x01, // tag 0x0112
		3, 0, // SHORT
		1, 0, 0, 0, // count 1
		6, 0, 0, 0, // value 6
		0, 0, 0, 0, // next IFD = 0
	}
	seg := make([]byte, 0, len(app1)+4)
	seg = append(seg, 0xFF, 0xD8) // SOI
	seg = append(seg, 0xFF, 0xE1) // APP1
	seg = append(seg, byte((len(app1)+2)>>8), byte((len(app1)+2)&0xFF))
	seg = append(seg, app1...)
	seg = append(seg, 0xFF, 0xDA) // SOS：停止扫描
	if o := exifOrientation(seg); o != 6 {
		t.Errorf("orientation=%d want 6", o)
	}
	// 无 EXIF
	if o := exifOrientation([]byte{0xFF, 0xD8, 0xFF, 0xDA}); o != 1 {
		t.Errorf("无 EXIF 应为 1，得到 %d", o)
	}
	// 非 JPEG
	if o := exifOrientation([]byte{0x89, 'P', 'N', 'G'}); o != 1 {
		t.Errorf("非 JPEG 应为 1，得到 %d", o)
	}
}

func TestImageToPDFSize(t *testing.T) {
	dir := t.TempDir()
	pngPath := makeTestPNG(t, dir, 200, 100)
	data, err := ImageToPDF(pngPath, 96)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 100 {
		t.Fatal("产物过小")
	}
	if string(data[0:5]) != "%PDF-" {
		t.Fatal("不是 PDF")
	}
}

func TestImageToPDFBadDPI(t *testing.T) {
	if _, err := ImageToPDF("x.png", 0); err == nil {
		t.Error("dpi 0 应报错")
	}
}

func TestDetectWordConverterEnvBad(t *testing.T) {
	t.Setenv(EnvWordConverter, "/nonexistent/soffice-bin-xyz")
	if c := DetectWordConverter(); c != nil && c.Path == "/nonexistent/soffice-bin-xyz" {
		t.Error("无效环境变量不应被采用")
	}
}
