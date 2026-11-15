package common

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParsePages(t *testing.T) {
	cases := []struct {
		spec  string
		total int
		want  []int
	}{
		{"", 3, []int{0, 1, 2}},
		{"all", 3, []int{0, 1, 2}},
		{"2", 3, []int{1}},
		{"1-3", 3, []int{0, 1, 2}},
		{"2-", 3, []int{1, 2}},
		{"-2", 3, []int{0, 1}},
		{"1-2,3", 5, []int{0, 1, 2}},
		{"3,1,3", 3, []int{0, 2}},
	}
	for _, c := range cases {
		got, err := ParsePages(c.spec, c.total)
		if err != nil {
			t.Errorf("ParsePages(%q,%d) err=%v", c.spec, c.total, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParsePages(%q,%d)=%v want %v", c.spec, c.total, got, c.want)
		}
	}
}

func TestParsePagesErrors(t *testing.T) {
	bad := []struct {
		spec  string
		total int
	}{
		{"4", 3},
		{"0", 3},
		{"3-1", 3},
		{"2-9", 3},
		{"x", 3},
		{"1-", 0},
	}
	for _, c := range bad {
		if _, err := ParsePages(c.spec, c.total); err == nil {
			t.Errorf("ParsePages(%q,%d) 应报错", c.spec, c.total)
		}
	}
}

func TestFormatPages(t *testing.T) {
	if got := FormatPages([]int{0, 1, 2}, 3); got != "all" {
		t.Errorf("got %q", got)
	}
	if got := FormatPages([]int{0, 4}, 9); got != "1,5" {
		t.Errorf("got %q", got)
	}
	got := FormatPages([]int{0, 1, 2, 3, 4, 5, 6, 7, 8}, 12)
	if got != "1-9 (9 pages)" {
		t.Errorf("got %q", got)
	}
}

func TestResolvePosition(t *testing.T) {
	p, err := ResolvePosition("bottom-right", "center")
	if err != nil || p.FX != 1 || p.FY != 1 {
		t.Errorf("got %v %v", p, err)
	}
	if _, err := ResolvePosition("nope", "center"); err == nil {
		t.Error("未知位置应报错")
	}
	p, err = ResolvePosition("", "top-left")
	if err != nil || p.FX != 0 || p.FY != 0 {
		t.Errorf("默认位置错误: %v", p)
	}
}

func TestParseColor(t *testing.T) {
	c, err := ParseColor("red", RGB{})
	if err != nil || math.Abs(c.R-0.85) > 1e-9 {
		t.Errorf("red: %v %v", c, err)
	}
	c, _ = ParseColor("#ff8000", RGB{})
	if math.Abs(c.R-1) > 1e-9 || math.Abs(c.G-128.0/255) > 1e-3 || c.B != 0 {
		t.Errorf("#ff8000: %v", c)
	}
	c, _ = ParseColor("#f80", RGB{})
	if math.Abs(c.G-136.0/255) > 1e-3 {
		t.Errorf("#f80: %v", c)
	}
	c, _ = ParseColor("0.1,0.2,0.3", RGB{})
	if math.Abs(c.B-0.3) > 1e-9 {
		t.Errorf("0.1,0.2,0.3: %v", c)
	}
	def := RGB{0.5, 0.5, 0.5}
	if c, _ = ParseColor("", def); c != def {
		t.Errorf("空值应返回默认")
	}
	for _, bad := range []string{"#xyz", "#12345", "1,2", "purpleish"} {
		if _, err := ParseColor(bad, def); err == nil {
			t.Errorf("%q 应报错", bad)
		}
	}
}

func TestSaveAtomic(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sub", "out.pdf")
	want := []byte("hello pdf")
	err := SaveAtomic(func(tmp string) error { return os.WriteFile(tmp, want, 0o644) }, target)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(want) {
		t.Fatalf("内容不符: %q %v", got, err)
	}
	// 不应留下临时文件
	ents, _ := os.ReadDir(filepath.Dir(target))
	if len(ents) != 1 {
		t.Errorf("留下多余文件: %d", len(ents))
	}
	// 写失败不应产生目标文件
	target2 := filepath.Join(dir, "fail.pdf")
	SaveAtomic(func(tmp string) error { return os.ErrPermission }, target2)
	if _, err := os.Stat(target2); err == nil {
		t.Error("失败时不应留下目标文件")
	}
}
