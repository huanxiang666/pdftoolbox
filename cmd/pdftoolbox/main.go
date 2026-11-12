// Command pdftoolbox 是 PDF 工具箱的命令行入口。
//
// 子命令与参数和 Python 版完全一致，习惯可以直接迁移：
//
//	merge / split / rotate / number / watermark / clean / info / check / gui
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/huanxiang666/pdftoolbox/internal/common"
	"github.com/huanxiang666/pdftoolbox/internal/ingest"
	"github.com/huanxiang666/pdftoolbox/internal/ops"
)

// Version 是版本号，构建时用 -ldflags 覆盖。
var Version = "1.0.0"

func main() {
	// 控制台中文：Windows 下把输出代码页切到 UTF-8，避免乱码。
	setConsoleUTF8()

	// 防崩溃兜底：任何未预期 panic 都变成一行可读错误，退出码 1。
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "error: unexpected failure: %v\n", r)
			if os.Getenv("PDFTOOLBOX_DEBUG") != "" {
				debug.PrintStack()
			}
			os.Exit(1)
		}
	}()

	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	if len(argv) == 0 {
		printUsage()
		return 2
	}
	switch argv[0] {
	case "--version", "-version", "version":
		fmt.Printf("pdftoolbox %s (%s/%s, go %s)\n", Version, goos(), goarch(), goversion())
		return 0
	case "--help", "-h", "help":
		printUsage()
		return 0
	case "check":
		return runCheck()
	case "gui":
		return runGUI(argv[1:])
	case "merge":
		return runMerge(argv[1:])
	case "split":
		return runSplit(argv[1:])
	case "rotate":
		return runRotate(argv[1:])
	case "number":
		return runNumber(argv[1:])
	case "watermark":
		return runWatermark(argv[1:])
	case "clean":
		return runClean(argv[1:])
	case "info":
		return runInfo(argv[1:])
	}
	fmt.Fprintf(os.Stderr, "error: unknown command %q\n\n", argv[0])
	printUsage()
	return 2
}

func printUsage() {
	fmt.Print(`pdftoolbox — PDF 工具箱 / PDF toolbox

用法 / usage:
  pdftoolbox merge     <inputs...> -o out.pdf [--recursive] [--image-dpi N] [--word-timeout N]
  pdftoolbox split     in.pdf -o dir (--per-page | --ranges "1-3,5,8-") [--prefix P]
  pdftoolbox rotate    in.pdf [-o out.pdf] --angle 90|180|270 [--pages "1-3"]
  pdftoolbox number    in.pdf [-o out.pdf] [--start-page N] [--first-number N]
                       [--format "第 {n} 页 共 {t} 页"] [--position POS] [--margin N]
                       [--fontsize N] [--color C] [--opacity F]
  pdftoolbox watermark in.pdf [-o out.pdf] (--text T | --image img.png) [--pages SPEC]
                       [--opacity F] [--angle DEG] [--fontsize N] [--color C]
                       [--position POS] [--tile] [--image-scale F]
  pdftoolbox clean     in.pdf [-o out.pdf] [--threshold F] [--dpi N] [--dry-run]
  pdftoolbox info      in.pdf [--json]
  pdftoolbox check
  pdftoolbox gui
  pdftoolbox --version | --help

页码范围 / page spec: "3"  "2-7"  "5-"  "-3"  "1-3,5,8-"  "all"
位置 / position: top-left top-center top-right middle-left center middle-right
                 bottom-left bottom-center bottom-right
颜色 / color: black white red green blue gray yellow orange purple cyan magenta
              #rrggbb #rgb 或 "r,g,b"(0..1)
退出码 / exit codes: 0 成功 · 1 操作失败 · 2 用法错误 · 130 用户中断
`)
}

// ------------------------------------------------------------------ flags

// flagSet 是极简 flag 解析器：支持 --name value、--name=value、布尔开关。
type flagSet struct {
	args   []string
	vals   map[string]string
	bools  map[string]bool
	errMsg string
}

func newFlags(args []string, boolNames []string) *flagSet {
	fs := &flagSet{vals: map[string]string{}, bools: map[string]bool{}}
	isBool := map[string]bool{}
	for _, b := range boolNames {
		isBool[b] = true
	}
	i := 0
	for i < len(args) {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			fs.args = append(fs.args, a)
			i++
			continue
		}
		name := strings.TrimLeft(a, "-")
		if eq := strings.Index(name, "="); eq >= 0 {
			fs.vals[name[:eq]] = name[eq+1:]
			i++
			continue
		}
		if isBool[name] {
			fs.bools[name] = true
			i++
			continue
		}
		if i+1 >= len(args) {
			fs.errMsg = fmt.Sprintf("flag --%s needs a value", name)
			return fs
		}
		fs.vals[name] = args[i+1]
		i += 2
	}
	return fs
}

func (f *flagSet) str(names ...string) string {
	for _, n := range names {
		if v, ok := f.vals[n]; ok {
			return v
		}
	}
	return ""
}

func (f *flagSet) bool(names ...string) bool {
	for _, n := range names {
		if f.bools[n] {
			return true
		}
	}
	return false
}

func (f *flagSet) int(names ...string) (int, bool) {
	s := f.str(names...)
	if s == "" {
		return 0, false
	}
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		f.errMsg = fmt.Sprintf("invalid integer for --%s: %q", names[0], s)
		return 0, false
	}
	return v, true
}

func (f *flagSet) float(names ...string) (float64, bool) {
	s := f.str(names...)
	if s == "" {
		return 0, false
	}
	var v float64
	if _, err := fmt.Sscanf(s, "%g", &v); err != nil {
		f.errMsg = fmt.Sprintf("invalid number for --%s: %q", names[0], s)
		return 0, false
	}
	return v, true
}

// ------------------------------------------------------------------ commands

func report(r *ops.Result, asJSON bool) {
	if asJSON {
		payload := map[string]any{
			"output":   r.Output,
			"pages":    r.Pages,
			"messages": r.Messages,
			"details":  r.Details,
		}
		b, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Println(string(b))
		return
	}
	for _, line := range r.Messages {
		fmt.Println(line)
	}
	if r.Output != "" {
		abs, err := filepath.Abs(r.Output)
		if err != nil {
			abs = r.Output
		}
		fmt.Printf("output: %s\n", abs)
	}
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	if common.IsOpError(err) {
		return 1
	}
	return 1
}

func runMerge(args []string) int {
	fs := newFlags(args, []string{"r", "recursive"})
	if fs.errMsg != "" {
		fmt.Fprintln(os.Stderr, "error:", fs.errMsg)
		return 2
	}
	out := fs.str("o", "output")
	if out == "" || len(fs.args) < 1 {
		fmt.Fprintln(os.Stderr, "error: merge needs inputs and -o output.pdf")
		return 2
	}
	dpi := 96
	if v, ok := fs.int("image-dpi"); ok {
		dpi = v
	}
	wt := 300
	if v, ok := fs.int("word-timeout"); ok {
		wt = v
	}
	files, ignored := ingest.CollectInputs(fs.args, fs.bool("r", "recursive"))
	for _, p := range ignored {
		fmt.Fprintf(os.Stderr, "warning: ignoring unsupported input: %s\n", p)
	}
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "error: no supported input files found (%s)\n",
			strings.Join(ingest.SupportedInputExtensions(), ", "))
		return 2
	}
	res, err := ops.Merge(files, out, dpi, time.Duration(wt)*time.Second)
	if err != nil {
		return fail(err)
	}
	report(res, false)
	return 0
}

func runSplit(args []string) int {
	fs := newFlags(args, []string{"per-page"})
	if fs.errMsg != "" {
		fmt.Fprintln(os.Stderr, "error:", fs.errMsg)
		return 2
	}
	if len(fs.args) != 1 {
		fmt.Fprintln(os.Stderr, "error: split needs exactly one input PDF")
		return 2
	}
	outDir := fs.str("o", "output-dir")
	if outDir == "" {
		fmt.Fprintln(os.Stderr, "error: split needs -o output-dir")
		return 2
	}
	perPage := fs.bool("per-page")
	ranges := fs.str("ranges")
	if perPage == (ranges != "") {
		fmt.Fprintln(os.Stderr, "error: split needs exactly one of --per-page or --ranges")
		return 2
	}
	res, err := ops.Split(fs.args[0], outDir, perPage, ranges, fs.str("prefix"))
	if err != nil {
		return fail(err)
	}
	report(res, false)
	return 0
}

func runRotate(args []string) int {
	fs := newFlags(args, nil)
	if fs.errMsg != "" {
		fmt.Fprintln(os.Stderr, "error:", fs.errMsg)
		return 2
	}
	if len(fs.args) != 1 {
		fmt.Fprintln(os.Stderr, "error: rotate needs exactly one input PDF")
		return 2
	}
	angleStr := fs.str("angle")
	if angleStr == "" {
		fmt.Fprintln(os.Stderr, "error: rotate needs --angle")
		return 2
	}
	var angle int
	if _, err := fmt.Sscanf(angleStr, "%d", &angle); err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid --angle %q\n", angleStr)
		return 2
	}
	res, err := ops.Rotate(fs.args[0], fs.str("o", "output"), angle, fs.str("pages"))
	if err != nil {
		return fail(err)
	}
	report(res, false)
	return 0
}

func runNumber(args []string) int {
	fs := newFlags(args, nil)
	if fs.errMsg != "" {
		fmt.Fprintln(os.Stderr, "error:", fs.errMsg)
		return 2
	}
	if len(fs.args) != 1 {
		fmt.Fprintln(os.Stderr, "error: number needs exactly one input PDF")
		return 2
	}
	startPage, firstNumber := 1, 1
	if v, ok := fs.int("start-page"); ok {
		startPage = v
	}
	if v, ok := fs.int("first-number"); ok {
		firstNumber = v
	}
	format := fs.str("format")
	if format == "" {
		format = "{n}"
	}
	position := fs.str("position")
	if position == "" {
		position = "bottom-center"
	}
	margin, fontsize := 24.0, 11.0
	if v, ok := fs.float("margin"); ok {
		margin = v
	}
	if v, ok := fs.float("fontsize"); ok {
		fontsize = v
	}
	colorSpec := fs.str("color")
	if colorSpec == "" {
		colorSpec = "black"
	}
	opacity := 1.0
	if v, ok := fs.float("opacity"); ok {
		opacity = v
	}
	res, err := ops.NumberPages(fs.args[0], fs.str("o", "output"), startPage, firstNumber,
		format, position, margin, fontsize, colorSpec, opacity)
	if err != nil {
		return fail(err)
	}
	report(res, false)
	return 0
}

func runWatermark(args []string) int {
	fs := newFlags(args, []string{"tile"})
	if fs.errMsg != "" {
		fmt.Fprintln(os.Stderr, "error:", fs.errMsg)
		return 2
	}
	if len(fs.args) != 1 {
		fmt.Fprintln(os.Stderr, "error: watermark needs exactly one input PDF")
		return 2
	}
	text := fs.str("text")
	image := fs.str("image")
	if (text == "") == (image == "") {
		fmt.Fprintln(os.Stderr, "error: watermark needs exactly one of --text or --image")
		return 2
	}
	opacity, angle, fontsize := 0.25, 45.0, 36.0
	if v, ok := fs.float("opacity"); ok {
		opacity = v
	}
	if v, ok := fs.float("angle"); ok {
		angle = v
	}
	if v, ok := fs.float("fontsize"); ok {
		fontsize = v
	}
	colorSpec := fs.str("color")
	if colorSpec == "" {
		colorSpec = "gray"
	}
	position := fs.str("position")
	if position == "" {
		position = "center"
	}
	scale := 0.25
	if v, ok := fs.float("image-scale"); ok {
		scale = v
	}
	res, err := ops.Watermark(fs.args[0], fs.str("o", "output"), text, image, fs.str("pages"),
		opacity, angle, fontsize, colorSpec, position, fs.bool("tile"), scale)
	if err != nil {
		return fail(err)
	}
	report(res, false)
	return 0
}

func runClean(args []string) int {
	fs := newFlags(args, []string{"dry-run"})
	if fs.errMsg != "" {
		fmt.Fprintln(os.Stderr, "error:", fs.errMsg)
		return 2
	}
	if len(fs.args) != 1 {
		fmt.Fprintln(os.Stderr, "error: clean needs exactly one input PDF")
		return 2
	}
	threshold, dpi := 0.002, 48
	if v, ok := fs.float("threshold"); ok {
		threshold = v
	}
	if v, ok := fs.int("dpi"); ok {
		dpi = v
	}
	res, err := ops.CleanBlank(fs.args[0], fs.str("o", "output"), threshold, dpi, fs.bool("dry-run"))
	if err != nil {
		return fail(err)
	}
	report(res, false)
	return 0
}

func runInfo(args []string) int {
	fs := newFlags(args, []string{"json"})
	if fs.errMsg != "" {
		fmt.Fprintln(os.Stderr, "error:", fs.errMsg)
		return 2
	}
	if len(fs.args) != 1 {
		fmt.Fprintln(os.Stderr, "error: info needs exactly one input PDF")
		return 2
	}
	res, err := ops.PDFInfo(fs.args[0])
	if err != nil {
		return fail(err)
	}
	report(res, fs.bool("json"))
	return 0
}

func runCheck() int {
	fmt.Printf("pdftoolbox %s  |  %s/%s  |  pdf engine: pdfcpu (pure Go)\n", Version, goos(), goarch())
	fmt.Println("PDF operations: OK (built-in)")
	fmt.Println("CJK page numbers/watermarks: OK (embedded Noto Sans SC subset; text stays searchable)")
	if conv := ingest.DetectWordConverter(); conv != nil {
		fmt.Printf("Word converter: %s\n", conv.DisplayName())
		fmt.Println("Word input (.doc/.docx/.odt/.rtf/.wps): supported")
	} else {
		fmt.Println("Word converter: not found")
		fmt.Println(strings.ReplaceAll(ingest.WordInstallHint, "\n", "\n  "))
	}
	fmt.Println("GUI: built-in web interface (pdftoolbox gui)")
	return 0
}
