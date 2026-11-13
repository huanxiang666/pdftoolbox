package docx2pdf

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"image"
	_ "image/gif" // gif 首帧
	"image/jpeg"
	_ "image/png" // png
	"io"
	"regexp"
	"strconv"
	"strings"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// 页面几何（point）
const (
	pageW      = 595.28
	pageH      = 841.89
	marginX    = 56.7 // 2cm
	marginTop  = 56.7
	marginBot  = 56.7
	lineFactor = 1.35
)

// styleInfo 是 styles.xml 里一个样式的关键属性。
type styleInfo struct {
	name   string
	size   float64
	bold   bool
	italic bool
	align  string
	bullet bool
	before float64
	after  float64
	parent string
}

// paraProps 段落级属性。
type paraProps struct {
	align  string
	size   float64
	bold   bool
	italic bool
	color  [3]float64
	before float64
	after  float64
	indent float64
	bullet bool
}

// runProps 行内属性。
type runProps struct {
	size     float64
	bold     bool
	italic   bool
	color    [3]float64
	colorSet bool
	has      bool
}

// Render 把 .docx 渲染成 PDF 字节。ttf 为内嵌字体数据。
func Render(docxPath string, ttf []byte) ([]byte, error) {
	zr, err := zip.OpenReader(docxPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open docx: %w", err)
	}
	defer zr.Close()
	return RenderZip(&zr.Reader, ttf)
}

// RenderZip 从已打开的 zip 渲染。
func RenderZip(zr *zip.Reader, ttf []byte) ([]byte, error) {
	docXML, err := readZipFile(zr, "word/document.xml")
	if err != nil {
		return nil, fmt.Errorf("bad docx (no word/document.xml): %w", err)
	}
	styles := parseStyles(readZipFileOrEmpty(zr, "word/styles.xml"))
	rels := parseRels(readZipFileOrEmpty(zr, "word/_rels/document.xml.rels"))
	media := map[string][]byte{}
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "word/media/") {
			if b, err := readZipEntry(f); err == nil {
				media[strings.TrimPrefix(f.Name, "word/")] = b
			}
		}
	}

	doc, err := New(ttf)
	if err != nil {
		return nil, err
	}
	l := &layout{doc: doc, styles: styles, rels: rels, media: media}
	l.flow(docXML)

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func readZipFile(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			return readZipEntry(f)
		}
	}
	return nil, fmt.Errorf("missing %s", name)
}

func readZipFileOrEmpty(zr *zip.Reader, name string) []byte {
	b, _ := readZipFile(zr, name)
	return b
}

func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// ---------------------------------------------------------------- styles

var headingRe = regexp.MustCompile(`(?i)^heading\s*(\d)$|^标题\s*(\d)$`)

func parseStyles(data []byte) map[string]styleInfo {
	out := map[string]styleInfo{}
	defSize := 10.5
	if len(data) == 0 {
		return out
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	var cur *styleInfo
	var curID string
	inDocDefaults := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "docDefaults":
				inDocDefaults = true
			case "style":
				curID = attr(t, "styleId")
				cur = &styleInfo{}
			case "name":
				if cur != nil {
					cur.name = attr(t, "val")
				}
			case "basedOn":
				if cur != nil {
					cur.parent = attr(t, "val")
				}
			case "jc":
				if cur != nil {
					cur.align = attr(t, "val")
				}
			case "sz":
				v, _ := strconv.ParseFloat(attr(t, "val"), 64)
				if v > 0 {
					switch {
					case cur != nil:
						cur.size = v / 2
					case inDocDefaults:
						defSize = v / 2
					}
				}
			case "b":
				if cur != nil && attr(t, "val") != "0" && attr(t, "val") != "false" {
					cur.bold = true
				}
			case "i":
				if cur != nil && attr(t, "val") != "0" && attr(t, "val") != "false" {
					cur.italic = true
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "docDefaults":
				inDocDefaults = false
			case "style":
				if cur != nil {
					out[curID] = *cur
					cur = nil
				}
			}
		}
	}
	for id, s := range out {
		if s.parent != "" {
			if p, ok := out[s.parent]; ok {
				if s.size == 0 {
					s.size = p.size
				}
				if s.align == "" {
					s.align = p.align
				}
				s.bold = s.bold || p.bold
				s.italic = s.italic || p.italic
			}
		}
		if m := headingRe.FindStringSubmatch(s.name); m != nil {
			lvl := m[1]
			if lvl == "" {
				lvl = m[2]
			}
			n, _ := strconv.Atoi(lvl)
			if n < 1 {
				n = 1
			}
			sizes := []float64{22, 18, 15, 13, 12, 11, 11, 11, 11}
			if n <= len(sizes) {
				s.size = sizes[n-1]
			}
			s.bold = true
			s.before = 10
			s.after = 6
		}
		if s.size == 0 {
			s.size = defSize
		}
		out[id] = s
	}
	return out
}

func parseRels(data []byte) map[string]string {
	out := map[string]string{}
	if len(data) == 0 {
		return out
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if t, ok := tok.(xml.StartElement); ok && t.Name.Local == "Relationship" {
			out[attr(t, "Id")] = attr(t, "Target")
		}
	}
	return out
}

func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// ---------------------------------------------------------------- layout

type layout struct {
	doc        *Doc
	styles     map[string]styleInfo
	rels       map[string]string
	media      map[string][]byte
	y          float64
	pendingImg [2]float64
}

// flowState 是 flow 的局部状态（避免闭包变量过多）。
type flowState struct {
	pp    *paraProps
	text  strings.Builder
	rp    runProps
	tbl   *tableBuilder
	depth int
}

func (l *layout) flow(docXML []byte) {
	l.y = marginTop
	dec := xml.NewDecoder(bytes.NewReader(docXML))
	st := &flowState{}

	inCell := func() bool { return st.tbl != nil && st.depth > 0 && st.tbl.cur != nil && st.tbl.cur.cur != nil }
	// 当前 run 属性的写入目标
	rpTarget := func() *runProps {
		if inCell() {
			return &st.tbl.cur.cur.rp
		}
		return &st.rp
	}
	textTarget := func() *strings.Builder {
		if inCell() {
			return &st.tbl.cur.cur.text
		}
		return &st.text
	}
	flushRun := func() {
		// run 结束时把累积文本变成一个 run
		if inCell() {
			c := st.tbl.cur.cur
			if c.text.Len() > 0 {
				c.runs = append(c.runs, runPropsText{c.rp, c.text.String()})
				c.text.Reset()
			}
			c.rp = runProps{}
			return
		}
		if st.pp != nil && st.text.Len() > 0 {
			l.emitParagraph(*st.pp, []runPropsText{{st.rp, st.text.String()}})
		}
		st.text.Reset()
		st.rp = runProps{}
	}
	endPara := func() {
		if inCell() {
			flushRun()
			st.tbl.endPara()
			st.pp = nil
			return
		}
		flushRun()
		st.pp = nil
	}

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				if inCell() {
					st.tbl.startPara()
					st.pp = &paraProps{size: 9, color: [3]float64{0, 0, 0}}
					continue
				}
				st.pp = &paraProps{size: 10.5, color: [3]float64{0, 0, 0}}
			case "pStyle":
				if st.pp != nil {
					if s, ok := l.styles[attr(t, "val")]; ok {
						st.pp.size = s.size
						st.pp.bold = s.bold
						st.pp.italic = s.italic
						st.pp.align = s.align
						st.pp.before = s.before
						st.pp.after = s.after
						st.pp.bullet = s.bullet
					}
				}
			case "jc":
				if st.pp != nil {
					st.pp.align = attr(t, "val")
				}
			case "spacing":
				if st.pp != nil {
					if v, err := strconv.ParseFloat(attr(t, "before"), 64); err == nil {
						st.pp.before = v / 20
					}
					if v, err := strconv.ParseFloat(attr(t, "after"), 64); err == nil {
						st.pp.after = v / 20
					}
				}
			case "ind":
				if st.pp != nil {
					if v, err := strconv.ParseFloat(attr(t, "left"), 64); err == nil {
						st.pp.indent = v / 20
					}
				}
			case "numPr":
				if st.pp != nil {
					st.pp.bullet = true
					st.pp.indent = 18
				}
			case "r":
				if inCell() {
					st.tbl.cur.cur.rp = runProps{}
				} else {
					st.rp = runProps{}
				}
			case "sz":
				if v, err := strconv.ParseFloat(attr(t, "val"), 64); err == nil && v > 0 {
					rp := rpTarget()
					rp.size = v / 2
					rp.has = true
				}
			case "b":
				if attr(t, "val") != "0" && attr(t, "val") != "false" {
					rp := rpTarget()
					rp.bold = true
					rp.has = true
				}
			case "i":
				if attr(t, "val") != "0" && attr(t, "val") != "false" {
					rp := rpTarget()
					rp.italic = true
					rp.has = true
				}
			case "color":
				if c, ok := parseHexColor(attr(t, "val")); ok {
					rp := rpTarget()
					rp.color = c
					rp.colorSet = true
					rp.has = true
				}
			case "br":
				if attr(t, "type") == "page" && !inCell() {
					flushRun()
					l.doc.newPage()
					l.y = marginTop
				} else {
					textTarget().WriteString("\n")
				}
			case "tbl":
				if st.pp != nil {
					endPara()
				}
				if st.tbl == nil {
					st.tbl = &tableBuilder{}
					st.depth = 1
				} else {
					st.depth++
				}
			case "gridCol":
				if st.tbl != nil && st.depth == 1 {
					if w, err := strconv.ParseFloat(attr(t, "w"), 64); err == nil {
						st.tbl.cols = append(st.tbl.cols, w/20)
					}
				}
			case "tr":
				if st.tbl != nil && st.depth == 1 {
					st.tbl.newRow()
				}
			case "tc":
				if st.tbl != nil && st.depth == 1 {
					st.tbl.newCell()
				}
			case "extent":
				if st.pp != nil && !inCell() {
					cx, _ := strconv.ParseFloat(attr(t, "cx"), 64)
					cy, _ := strconv.ParseFloat(attr(t, "cy"), 64)
					if cx > 0 && cy > 0 {
						l.pendingImg = [2]float64{cx / 12700, cy / 12700}
					}
				}
			case "blip":
				if st.pp != nil && !inCell() && l.pendingImg[0] > 0 {
					rid := ""
					for _, a := range t.Attr {
						if a.Name.Local == "embed" {
							rid = a.Value
						}
					}
					if target, ok := l.rels[rid]; ok {
						if data, ok2 := l.media[target]; ok2 {
							flushRun()
							l.placeImage(data, l.pendingImg[0], l.pendingImg[1])
						}
					}
					l.pendingImg = [2]float64{0, 0}
				}
			}
		case xml.CharData:
			if st.pp != nil || inCell() {
				textTarget().Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "p":
				endPara()
			case "r":
				flushRun()
			case "tbl":
				if st.tbl != nil {
					st.depth--
					if st.depth == 0 {
						l.emitTable(st.tbl)
						st.tbl = nil
					}
				}
			}
		}
	}
	endPara()
	if st.tbl != nil {
		l.emitTable(st.tbl)
	}
}

type runPropsText struct {
	rp   runProps
	text string
}

// placeImage 把图片放到当前 y 位置。
func (l *layout) placeImage(data []byte, w, h float64) {
	maxW := pageW - 2*marginX
	if w > maxW {
		h = h * maxW / w
		w = maxW
	}
	if l.y+h > pageH-marginBot {
		l.doc.newPage()
		l.y = marginTop
	}
	jpg := toJPEG(data)
	if jpg == nil {
		return
	}
	l.doc.cur.imgs = append(l.doc.cur.imgs, imgPlace{
		x: marginX, y: pageH - l.y - h, w: w, h: h, data: jpg,
	})
	l.y += h + 6
}

// emitParagraph 排版一个段落。
func (l *layout) emitParagraph(pp paraProps, runs []runPropsText) {
	maxW := pageW - 2*marginX - pp.indent
	l.y += pp.before
	for _, rt := range runs {
		size := pp.size
		col := pp.color
		if rt.rp.has {
			if rt.rp.size > 0 {
				size = rt.rp.size
			}
			if rt.rp.colorSet {
				col = rt.rp.color
			}
		}
		text := strings.ReplaceAll(rt.text, "\r", "")
		for _, para := range strings.Split(text, "\n") {
			para = strings.TrimRight(para, " ")
			if para == "" {
				continue
			}
			prefix := ""
			if pp.bullet {
				prefix = "•  "
			}
			for _, ln := range l.wrap(prefix+para, size, maxW) {
				lh := size * lineFactor
				if l.y+lh > pageH-marginBot {
					l.doc.newPage()
					l.y = marginTop
				}
				x := marginX + pp.indent
				lw := l.doc.font.widthPt(ln, size)
				switch pp.align {
				case "center":
					x = marginX + (pageW-2*marginX-lw)/2
				case "right", "end":
					x = pageW - marginX - lw
				}
				l.doc.cur.lines = append(l.doc.cur.lines, line{
					x: x, y: pageH - l.y - lh + size*0.28,
					run: textRun{text: ln, size: size, color: col},
				})
				l.y += lh
			}
		}
	}
	l.y += pp.after + 2
}

// wrap 按宽度折行（CJK 可在任意字后断行）。
func (l *layout) wrap(s string, size, maxW float64) []string {
	var out []string
	cur := ""
	curW := 0.0
	for _, r := range s {
		rw := l.doc.font.widthPt(string(r), size)
		if curW+rw > maxW && cur != "" {
			out = append(out, cur)
			cur = ""
			curW = 0
			if r == ' ' {
				continue
			}
		}
		cur += string(r)
		curW += rw
	}
	if cur != "" {
		out = append(out, cur)
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// ---------------------------------------------------------------- tables

type tableBuilder struct {
	cols []float64
	rows [][]*cellBuilder
	cur  *cellBuilder
}

type cellBuilder struct {
	paras []paraRuns
	cur   *paraRuns
}

type paraRuns struct {
	pp   paraProps
	runs []runPropsText
	text strings.Builder
	rp   runProps
}

func (t *tableBuilder) newRow() {
	t.rows = append(t.rows, nil)
}

func (t *tableBuilder) newCell() {
	c := &cellBuilder{}
	if len(t.rows) == 0 {
		t.newRow()
	}
	t.rows[len(t.rows)-1] = append(t.rows[len(t.rows)-1], c)
	t.cur = c
}

func (t *tableBuilder) startPara() {
	if t.cur == nil {
		return
	}
	t.cur.cur = &paraRuns{pp: paraProps{size: 9, color: [3]float64{0, 0, 0}}}
}

func (t *tableBuilder) endPara() {
	if t.cur == nil || t.cur.cur == nil {
		return
	}
	if t.cur.cur.text.Len() > 0 {
		t.cur.cur.runs = append(t.cur.cur.runs, runPropsText{t.cur.cur.rp, t.cur.cur.text.String()})
		t.cur.cur.text.Reset()
	}
	if len(t.cur.cur.runs) > 0 {
		t.cur.paras = append(t.cur.paras, *t.cur.cur)
	}
	t.cur.cur = nil
}

// emitTable 把表格画到页面。
func (l *layout) emitTable(t *tableBuilder) {
	maxW := pageW - 2*marginX
	cols := t.cols
	if len(cols) == 0 {
		nc := 1
		for _, r := range t.rows {
			if len(r) > nc {
				nc = len(r)
			}
		}
		cols = make([]float64, nc)
		for i := range cols {
			cols[i] = maxW / float64(nc)
		}
	}
	sum := 0.0
	for _, c := range cols {
		sum += c
	}
	if sum <= 0 {
		sum = 1
	}
	for i := range cols {
		cols[i] = cols[i] / sum * maxW
	}

	l.y += 4
	for _, row := range t.rows {
		rowH := 0.0
		cellLines := make([][][2]string, len(row))
		for ci, cell := range row {
			if cell == nil {
				continue
			}
			cw := cols[ci] - 8
			if cw < 20 {
				cw = 20
			}
			var lines [][2]string
			h := 0.0
			for _, pr := range cell.paras {
				size := pr.pp.size
				if size <= 0 {
					size = 9
				}
				for _, rt := range pr.runs {
					if rt.rp.size > 0 {
						size = rt.rp.size
					}
					for _, ln := range l.wrap(rt.text, size, cw) {
						lines = append(lines, [2]string{ln, fmt.Sprintf("%.2f", size)})
						h += size * lineFactor
					}
				}
			}
			if h == 0 {
				h = 9 * lineFactor
			}
			cellLines[ci] = lines
			if h > rowH {
				rowH = h
			}
		}
		rowH += 6
		if l.y+rowH > pageH-marginBot {
			l.doc.newPage()
			l.y = marginTop
		}
		x := marginX
		top := l.y
		for ci := range cols {
			l.doc.cur.rects = append(l.doc.cur.rects, rect{
				x: x, y: pageH - top - rowH, w: cols[ci], h: rowH,
				rgb: [3]float64{0.4, 0.4, 0.4}, lw: 0.5,
			})
			x += cols[ci]
		}
		x = marginX
		for ci, cell := range row {
			if cell != nil {
				yy := top + 4
				for _, ln := range cellLines[ci] {
					size, _ := strconv.ParseFloat(ln[1], 64)
					lh := size * lineFactor
					l.doc.cur.lines = append(l.doc.cur.lines, line{
						x: x + 4, y: pageH - yy - lh + size*0.28,
						run: textRun{text: ln[0], size: size, color: [3]float64{0, 0, 0}},
					})
					yy += lh
				}
			}
			x += cols[ci]
		}
		l.y += rowH
	}
	l.y += 6
}

// ---------------------------------------------------------------- 颜色/图像

func parseHexColor(v string) ([3]float64, bool) {
	v = strings.TrimPrefix(v, "#")
	if len(v) != 6 {
		return [3]float64{}, false
	}
	r, err1 := strconv.ParseInt(v[0:2], 16, 32)
	g, err2 := strconv.ParseInt(v[2:4], 16, 32)
	b, err3 := strconv.ParseInt(v[4:6], 16, 32)
	if err1 != nil || err2 != nil || err3 != nil {
		return [3]float64{}, false
	}
	return [3]float64{float64(r) / 255, float64(g) / 255, float64(b) / 255}, true
}

// toJPEG 把任意支持格式转成 JPEG 字节（PDF 用 DCTDecode 直接嵌入）。
func toJPEG(data []byte) []byte {
	if isJPEG(data) {
		return data
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil
	}
	return buf.Bytes()
}

func isJPEG(b []byte) bool {
	return len(b) > 3 && b[0] == 0xFF && b[1] == 0xD8
}
