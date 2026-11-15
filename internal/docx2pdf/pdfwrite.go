// Package docx2pdf 是一个零依赖的 DOCX→PDF 兜底渲染器。
//
// 定位：当机器上既没有 Word/WPS（COM）、也没有 LibreOffice 时，
// 保证 .docx 仍然能转成"可读、可搜索"的 PDF。只覆盖常见排版子集：
// 段落（字号/粗斜体/颜色/对齐/缩进/间距）、标题样式、表格（网格+边框）、
// 内嵌图片、分页符、简单项目符号。不覆盖：浮动图形、文本框、页眉页脚、
// 脚注、复杂多级编号。
package docx2pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// ---------------------------------------------------------------- 字体

// fontPack 持有内嵌 TTF 及其字形映射。
type fontPack struct {
	mu   sync.Mutex
	sf   *sfnt.Font
	gid  map[rune]uint16
	toU  map[uint16]rune
	data []byte
	upem float64
	// 宽度表（1/1000 em）
	adv map[uint16]int
}

func newFontPack(ttf []byte) (*fontPack, error) {
	sf, err := sfnt.Parse(ttf)
	if err != nil {
		return nil, err
	}
	return &fontPack{
		sf:   sf,
		gid:  map[rune]uint16{},
		toU:  map[uint16]rune{},
		data: ttf,
		upem: float64(sf.UnitsPerEm()),
		adv:  map[uint16]int{},
	}, nil
}

func (f *fontPack) gidOf(r rune) uint16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if g, ok := f.gid[r]; ok {
		return g
	}
	idx, err := f.sf.GlyphIndex(nil, r)
	if err != nil {
		idx = 0
	}
	g := uint16(idx)
	f.gid[r] = g
	if g != 0 {
		f.toU[g] = r
	}
	return g
}

// widthPt 返回字符串在给定字号下的宽度（point）。
func (f *fontPack) widthPt(s string, size float64) float64 {
	w := 0.0
	for _, r := range s {
		g := f.gidOf(r)
		a, ok := f.adv[g]
		if !ok {
			adv, err := f.sf.GlyphAdvance(nil, sfnt.GlyphIndex(g), fixed.I(1000), font.HintingNone)
			if err != nil {
				a = estimateAdv(r)
			} else {
				a = int(adv) >> 6
				if a <= 0 {
					a = estimateAdv(r)
				}
			}
			f.adv[g] = a
		}
		w += float64(a)
	}
	return w / 1000 * size
}

// estimateAdv 缺字形时的宽度估计：CJK 全角，其余半角。
func estimateAdv(r rune) int {
	if r > 0x2E7F && !(r >= 0x20 && r < 0x7F) {
		return 1000
	}
	if r >= 0x20 && r < 0x7F {
		return 500
	}
	return 1000
}

// ---------------------------------------------------------------- 页面模型

type textRun struct {
	text   string
	size   float64
	bold   bool
	italic bool
	color  [3]float64
}

type line struct {
	x, y float64 // 基线
	run  textRun
}

type rect struct {
	x, y, w, h float64
	rgb        [3]float64
	fill       bool
	lw         float64
}

type imgPlace struct {
	x, y, w, h float64
	data       []byte // JPEG 字节
}

type page struct {
	lines []line
	rects []rect
	imgs  []imgPlace
}

// Doc 是待输出的文档。
type Doc struct {
	pages []*page
	cur   *page
	font  *fontPack
	w, h  float64 // 页面尺寸 pt
}

// New 创建文档（A4）。
func New(ttf []byte) (*Doc, error) {
	fp, err := newFontPack(ttf)
	if err != nil {
		return nil, err
	}
	d := &Doc{font: fp, w: 595.28, h: 841.89}
	d.newPage()
	return d, nil
}

func (d *Doc) newPage() {
	p := &page{}
	d.pages = append(d.pages, p)
	d.cur = p
}

// PageCount 返回页数。
func (d *Doc) PageCount() int { return len(d.pages) }

// ---------------------------------------------------------------- 输出

// Write 生成 PDF 字节。
func (d *Doc) Write(w io.Writer) error {
	// 丢弃完全空白的尾页（除非只有一页）
	for len(d.pages) > 1 {
		last := d.pages[len(d.pages)-1]
		if len(last.lines) == 0 && len(last.rects) == 0 && len(last.imgs) == 0 {
			d.pages = d.pages[:len(d.pages)-1]
			continue
		}
		break
	}

	// 收集全部用到的 GID→Unicode 映射（ToUnicode CMap 用）
	gids := map[uint16]rune{}
	for _, pg := range d.pages {
		for _, ln := range pg.lines {
			for _, r := range ln.run.text {
				g := d.font.gidOf(r)
				gids[g] = r
			}
		}
	}

	// 组装对象
	objs := map[int][]byte{}
	next := 1
	alloc := func() int { n := next; next++; return n }

	catalog := alloc()
	pagesObj := alloc()
	fontObj := alloc()
	cidObj := alloc()
	descObj := alloc()
	fileObj := alloc()
	touniObj := alloc()

	pageObjs := make([]int, len(d.pages))
	contentObjs := make([]int, len(d.pages))
	imgObjs := map[int]map[int]int{} // page idx -> img index -> obj

	for i, pg := range d.pages {
		pageObjs[i] = alloc()
		contentObjs[i] = alloc()
		if len(pg.imgs) > 0 {
			imgObjs[i] = map[int]int{}
			for j := range pg.imgs {
				imgObjs[i][j] = alloc()
			}
		}
	}

	// 内容流（整份文档一个流不够：图片 XObject 按页资源；这里每页单独流）
	// 重新按页生成内容流
	var pageStreams [][]byte
	gidsPerDoc := gids
	_ = gidsPerDoc
	for _, pg := range d.pages {
		var b bytes.Buffer
		for _, r := range pg.rects {
			if r.fill {
				fmt.Fprintf(&b, "%.3f %.3f %.3f rg %.3f %.3f %.3f %.3f re f\n",
					r.rgb[0], r.rgb[1], r.rgb[2], r.x, r.y, r.w, r.h)
			} else {
				fmt.Fprintf(&b, "%.3f %.3f %.3f RG %.3f w %.3f %.3f %.3f %.3f re S\n",
					r.rgb[0], r.rgb[1], r.rgb[2], r.lw, r.x, r.y, r.w, r.h)
			}
		}
		for j, im := range pg.imgs {
			fmt.Fprintf(&b, "q %.3f 0 0 %.3f %.3f %.3f cm /Im%d Do Q\n", im.w, im.h, im.x, im.y, j)
		}
		for _, ln := range pg.lines {
			var hex strings.Builder
			for _, r := range ln.run.text {
				fmt.Fprintf(&hex, "%04X", d.font.gidOf(r))
			}
			fmt.Fprintf(&b, "BT %.3f %.3f %.3f rg /F0 %.2f Tf 1 0 0 1 %.3f %.3f Tm <%s> Tj ET\n",
				ln.run.color[0], ln.run.color[1], ln.run.color[2],
				ln.run.size, ln.x, ln.y, hex.String())
		}
		pageStreams = append(pageStreams, b.Bytes())
	}

	// 字体文件流
	var fcomp bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&fcomp, zlib.BestCompression)
	zw.Write(d.font.data)
	zw.Close()

	objs[fileObj] = streamObj(fcomp.Bytes(), fmt.Sprintf("/Length1 %d /Filter /FlateDecode", len(d.font.data)))
	objs[descObj] = []byte(fmt.Sprintf(
		"<< /Type /FontDescriptor /FontName /F0 /Flags 4 /FontBBox [0 -200 1000 900] /ItalicAngle 0 /Ascent 900 /Descent -200 /CapHeight 700 /StemV 80 /FontFile2 %d 0 R >>", fileObj))
	// /W 宽度表：没有它阅读器会按 /DW 1000 推断字距，ASCII 提取时会被插空格。
	var warr strings.Builder
	wids := make([]uint16, 0, len(d.font.adv))
	for g := range d.font.adv {
		wids = append(wids, g)
	}
	sort.Slice(wids, func(i, j int) bool { return wids[i] < wids[j] })
	for _, g := range wids {
		fmt.Fprintf(&warr, " %d %d %d", g, g, d.font.adv[g])
	}
	objs[cidObj] = []byte(fmt.Sprintf(
		"<< /Type /Font /Subtype /CIDFontType2 /BaseFont /F0 /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /FontDescriptor %d 0 R /DW 1000 /W [%s] /CIDToGIDMap /Identity >>", descObj, warr.String()))
	objs[fontObj] = []byte(fmt.Sprintf(
		"<< /Type /Font /Subtype /Type0 /BaseFont /F0 /Encoding /Identity-H /DescendantFonts [%d 0 R] /ToUnicode %d 0 R >>", cidObj, touniObj))
	objs[touniObj] = []byte(streamRaw(buildToUnicode(gids)))
	objs[catalog] = []byte(fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pagesObj))

	var kids strings.Builder
	for _, p := range pageObjs {
		fmt.Fprintf(&kids, "%d 0 R ", p)
	}
	objs[pagesObj] = []byte(fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", len(pageObjs), kids.String()))

	for i, pg := range d.pages {
		var comp bytes.Buffer
		zw2, _ := zlib.NewWriterLevel(&comp, zlib.BestSpeed)
		zw2.Write(pageStreams[i])
		zw2.Close()
		objs[contentObjs[i]] = streamObj(comp.Bytes(), "/Filter /FlateDecode")

		var res strings.Builder
		fmt.Fprintf(&res, "<< /Font << /F0 %d 0 R >>", fontObj)
		if len(pg.imgs) > 0 {
			res.WriteString(" /XObject << ")
			for j := range pg.imgs {
				fmt.Fprintf(&res, "/Im%d %d 0 R ", j, imgObjs[i][j])
			}
			res.WriteString(">> ")
		}
		res.WriteString(">>")
		objs[pageObjs[i]] = []byte(fmt.Sprintf(
			"<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %.2f %.2f] /Contents %d 0 R /Resources %s >>",
			pagesObj, d.w, d.h, contentObjs[i], res.String()))

		for j, im := range pg.imgs {
			objs[imgObjs[i][j]] = streamObj(im.data, fmt.Sprintf(
				"/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode",
				imWidth(im), imHeight(im)))
		}
	}

	// 写文件
	bw := newCountingWriter(w)
	bw.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	offsets := map[int]int64{}
	maxObj := 0
	for n := range objs {
		if n > maxObj {
			maxObj = n
		}
	}
	for n := 1; n <= maxObj; n++ {
		body, ok := objs[n]
		if !ok {
			continue
		}
		offsets[n] = bw.pos
		fmt.Fprintf(bw, "%d 0 obj\n", n)
		bw.Write(body)
		bw.WriteString("\nendobj\n")
	}
	xrefPos := bw.pos
	fmt.Fprintf(bw, "xref\n0 %d\n", maxObj+1)
	bw.WriteString("0000000000 65535 f \n")
	for n := 1; n <= maxObj; n++ {
		fmt.Fprintf(bw, "%010d 00000 n \n", offsets[n])
	}
	fmt.Fprintf(bw, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", maxObj+1, catalog, xrefPos)
	return bw.err
}

// imWidth/imHeight 从 JPEG SOF 段读尺寸。
func imWidth(im imgPlace) int  { w, _ := jpegDim(im.data); return w }
func imHeight(im imgPlace) int { _, h := jpegDim(im.data); return h }

func jpegDim(b []byte) (int, int) {
	i := 2
	for i+9 < len(b) {
		if b[i] != 0xFF {
			i++
			continue
		}
		m := b[i+1]
		if m >= 0xC0 && m <= 0xCF && m != 0xC4 && m != 0xC8 && m != 0xCC {
			h := int(b[i+5])<<8 | int(b[i+6])
			w := int(b[i+7])<<8 | int(b[i+8])
			return w, h
		}
		seg := int(b[i+2])<<8 | int(b[i+3])
		i += 2 + seg
	}
	return 1, 1
}

func streamObj(data []byte, extra string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "<< /Length %d %s >>\nstream\n", len(data), extra)
	b.Write(data)
	b.WriteString("\nendstream")
	return b.Bytes()
}

func streamRaw(s string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "<< /Length %d >>\nstream\n%s\nendstream", len(s), s)
	return b.Bytes()
}

// buildToUnicode 生成 GID→Unicode 的 CMap，保证 PDF 文本可搜索复制。
func buildToUnicode(gids map[uint16]rune) string {
	keys := make([]uint16, 0, len(gids))
	for k := range gids {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var b strings.Builder
	b.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n")
	b.WriteString("/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n")
	b.WriteString("/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n")
	b.WriteString("1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")
	for i := 0; i < len(keys); i += 100 {
		end := i + 100
		if end > len(keys) {
			end = len(keys)
		}
		fmt.Fprintf(&b, "%d beginbfchar\n", end-i)
		for _, k := range keys[i:end] {
			r := gids[k]
			if r > 0xFFFF {
				r = '?'
			}
			fmt.Fprintf(&b, "<%04X> <%04X>\n", k, uint16(r))
		}
		b.WriteString("endbfchar\n")
	}
	b.WriteString("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")
	return b.String()
}

type countingWriter struct {
	w   io.Writer
	pos int64
	err error
}

func newCountingWriter(w io.Writer) *countingWriter { return &countingWriter{w: w} }

func (c *countingWriter) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	n, err := c.w.Write(p)
	c.pos += int64(n)
	c.err = err
	return n, err
}

func (c *countingWriter) WriteString(s string) {
	c.Write([]byte(s))
}

var _ = math.Abs
