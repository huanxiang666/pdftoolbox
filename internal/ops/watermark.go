package ops

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"

	"github.com/huanxiang666/pdftoolbox/internal/common"
)

// maxTileCells 平铺水印每页的戳记上限，防止极端参数爆量。
const maxTileCells = 4000

// Watermark 给选定页加文字或图片水印。
//
// 文字模式：单个旋转戳记，或 tile=true 时斜向网格平铺整页。
// 图片模式：按 imageScale（占页宽比例）缩放，锚定在 position。
func Watermark(
	src, output string,
	text, imagePath, pages string,
	opacity, angle, fontsize float64,
	colorSpec, position string,
	tile bool,
	imageScale float64,
) (*Result, error) {
	if (text == "") == (imagePath == "") {
		return nil, common.Err("watermark needs exactly one of --text or --image")
	}
	if !(opacity > 0 && opacity <= 1) {
		return nil, common.Err("opacity must be in (0, 1], got %v", opacity)
	}
	if text != "" && fontsize <= 0 {
		return nil, common.Err("fontsize must be > 0, got %v", fontsize)
	}
	if text == "" && !(imageScale > 0 && imageScale <= 1) {
		return nil, common.Err("image-scale must be in (0, 1], got %v", imageScale)
	}
	data, total, err := Open(src)
	if err != nil {
		return nil, err
	}
	if output == "" {
		output = defaultOutput(src, "watermark")
	}
	indices, err := common.ParsePages(pages, total)
	if err != nil {
		return nil, err
	}
	pos, err := common.ResolvePosition(position, "center")
	if err != nil {
		return nil, err
	}
	rgb, err := common.ParseColor(colorSpec, common.RGB{R: 0.6, G: 0.6, B: 0.6})
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	var buf bytes.Buffer

	if text != "" {
		if tile {
			m, _, err := tileWatermarks(data, indices, text, fontsize, rgb, opacity, angle)
			if err != nil {
				return nil, err
			}
			if err := api.AddWatermarksSliceMap(ctx, bytes.NewReader(data), &buf, m, conf()); err != nil {
				return nil, common.Err("watermark failed: %v", err)
			}
		} else {
			wm, err := mkTextWM(text, fontsize, rgb, opacity, pos, 0, angle)
			if err != nil {
				return nil, err
			}
			if err := api.AddWatermarks(ctx, bytes.NewReader(data), &buf, indicesToSpec(indices), wm, conf()); err != nil {
				return nil, common.Err("watermark failed: %v", err)
			}
		}
	} else {
		if _, err := os.Stat(imagePath); err != nil {
			return nil, common.Err("watermark image not found: %s", imagePath)
		}
		desc := fmt.Sprintf("scale:%.4f, opacity:%.4f, pos:%s", imageScale, opacity, pdfcpuPosName(pos))
		wm, err := api.ImageWatermark(ctx, imagePath, desc, true, false, types.POINTS, conf())
		if err != nil {
			return nil, common.Err("watermark failed: %v", err)
		}
		if err := api.AddWatermarks(ctx, bytes.NewReader(data), &buf, indicesToSpec(indices), wm, conf()); err != nil {
			return nil, common.Err("watermark failed: %v", err)
		}
	}

	if err := saveAtomic(buf.Bytes(), output); err != nil {
		return nil, common.Err("cannot write %s: %v", output, err)
	}
	kind := fmt.Sprintf("text %q", text)
	if text == "" {
		kind = fmt.Sprintf("image %s", filepath.Base(imagePath))
	}
	return &Result{
		Output: output,
		Pages:  len(indices),
		Messages: []string{fmt.Sprintf("watermark (%s, opacity %v, angle %v°) on %s",
			kind, opacity, angle, common.FormatPages(indices, total))},
	}, nil
}

// pdfcpuPosName 把九锚点映射为 pdfcpu 的位置名。
func pdfcpuPosName(p common.Position) string {
	switch {
	case p.FY == 0 && p.FX == 0:
		return "tl"
	case p.FY == 0 && p.FX == 1:
		return "tr"
	case p.FY == 0:
		return "tc"
	case p.FY == 1 && p.FX == 0:
		return "bl"
	case p.FY == 1 && p.FX == 1:
		return "br"
	case p.FY == 1:
		return "bc"
	case p.FX == 0:
		return "l"
	case p.FX == 1:
		return "r"
	default:
		return "c"
	}
}

// tileWatermarks 生成平铺网格的每页戳记列表。
//
// 网格算法与 Python 版一致：行距 = fontsize*4，列距按文字宽度与旋转角估算，
// 奇数行错开半列，形成斜向覆盖。
func tileWatermarks(data []byte, indices []int, text string, fontsize float64, rgb common.RGB, opacity, angle float64) (map[int][]*model.Watermark, int, error) {
	tw := 0.0
	for _, r := range text {
		if r > 127 {
			tw += fontsize
		} else {
			tw += fontsize * 0.55
		}
	}
	rad := angle * math.Pi / 180
	stepX := tw*math.Abs(math.Cos(rad)) + math.Abs(math.Sin(rad))*fontsize + fontsize*3
	stepY := fontsize * 4
	if stepX <= 0 || stepY <= 0 {
		return nil, 0, common.Err("watermark tile geometry invalid")
	}

	dims, err := pageDimensions(data)
	if err != nil {
		return nil, 0, err
	}
	m := map[int][]*model.Watermark{}
	cells := 0
	tl := common.Position{FX: 0, FY: 0}
	for _, i := range indices {
		pageNr := i + 1
		w, h := dims[i][0], dims[i][1]
		var list []*model.Watermark
		y := 0.0
		row := 0
		for y < h+stepY && len(list) < maxTileCells {
			x := 0.0
			if row%2 == 1 {
				x = stepX / 2
			}
			for x < w+stepX && len(list) < maxTileCells {
				wm, err := mkTextWM(text, fontsize, rgb, opacity, tl, 0, angle)
				if err != nil {
					return nil, 0, err
				}
				wm.Pos = types.TopLeft
				wm.Dx = x
				// 校准结论：pdfcpu 的 TopLeft 锚点下 bbox 顶边 = 1.2*fontsize - Dy，
				// 即 Dy 向上为正。要让第 row 行落在页内 y 处，取 Dy = 1.06*fontsize - y。
				wm.Dy = 1.06*fontsize - y
				list = append(list, wm)
				x += stepX
			}
			y += stepY
			row++
		}
		m[pageNr] = list
		cells += len(list)
	}
	return m, cells, nil
}

// pageDimensions 返回每页 [宽, 高]（point）。
func pageDimensions(data []byte) ([][2]float64, error) {
	info, err := api.PDFInfo(context.Background(), bytes.NewReader(data), "", nil, false, conf())
	if err != nil {
		return nil, common.Err("cannot read PDF: %v", err)
	}
	out := make([][2]float64, len(info.PageBoundaries))
	for i, pb := range info.PageBoundaries {
		r := pb.Crop
		if r == nil || r.Rect == nil {
			r = pb.Media
		}
		if r == nil || r.Rect == nil {
			out[i] = [2]float64{595, 842}
			continue
		}
		out[i] = [2]float64{r.Rect.Width(), r.Rect.Height()}
	}
	return out, nil
}
