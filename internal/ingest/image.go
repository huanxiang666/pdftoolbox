package ingest

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// pdfcpuImport 描述一次图像导入的页面几何。
type pdfcpuImport struct {
	DPI  int
	W, H float64 // 页面尺寸（point）
	Full bool    // 图像铺满整页
}

// importImageFile 用 pdfcpu 把内存中的图像写成单页 PDF 文件。
func importImageFile(data []byte, outFile string, imp *pdfcpuImport) error {
	tmp, err := writeTempImage(data)
	if err != nil {
		return err
	}
	defer removeTempImage(tmp)

	ic := pdfcpu.DefaultImportConfig()
	ic.PageDim = &types.Dim{Width: imp.W, Height: imp.H}
	ic.UserDim = true
	ic.PageSize = ""
	ic.DPI = imp.DPI
	if imp.Full {
		// 注意：pdfcpu 在 Pos=Full 时会把页面尺寸设为图像像素值（忽略 DPI），
		// 因此这里用 Center + 显式 PageDim，让页面 = 像素在 DPI 下的物理尺寸，
		// 图像按 DPI 换算后正好铺满整页。
		ic.Pos = types.Center
		ic.Scale = 1
		ic.ScaleAbs = true
	}
	return api.ImportImagesFile(context.Background(), []string{tmp}, outFile, ic, nil)
}

// decodeOriented 解码图像并按 EXIF 方向摆正。
func decodeOriented(raw []byte, orient int) (image.Image, error) {
	img, _, err := image.Decode(readerOf(raw))
	if err != nil {
		return nil, err
	}
	return applyOrientation(img, orient), nil
}

// applyOrientation 按 EXIF 0x0112 值变换图像。
func applyOrientation(img image.Image, o int) image.Image {
	switch o {
	case 1, 0:
		return img
	case 2:
		return flipH(img)
	case 3:
		return rotate180(img)
	case 4:
		return flipV(img)
	case 5:
		return flipH(rotate90(img))
	case 6:
		return rotate90(img)
	case 7:
		return flipH(rotate270(img))
	case 8:
		return rotate270(img)
	}
	return img
}

func bounds(img image.Image) (w, h int) {
	b := img.Bounds()
	return b.Dx(), b.Dy()
}

func rotate90(img image.Image) image.Image {
	w, h := bounds(img)
	out := image.NewRGBA(image.Rect(0, 0, h, w))
	b := img.Bounds()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.Set(h-1-y, x, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return out
}

func rotate270(img image.Image) image.Image {
	w, h := bounds(img)
	out := image.NewRGBA(image.Rect(0, 0, h, w))
	b := img.Bounds()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.Set(y, w-1-x, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return out
}

func rotate180(img image.Image) image.Image {
	w, h := bounds(img)
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	b := img.Bounds()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.Set(w-1-x, h-1-y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return out
}

func flipH(img image.Image) image.Image {
	w, h := bounds(img)
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	b := img.Bounds()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.Set(w-1-x, y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return out
}

func flipV(img image.Image) image.Image {
	w, h := bounds(img)
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	b := img.Bounds()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.Set(x, h-1-y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return out
}

// hasAlpha 判断图像是否含透明通道。
func hasAlpha(img image.Image) bool {
	switch img.ColorModel() {
	case color.AlphaModel, color.Alpha16Model, color.RGBAModel, color.RGBA64Model, color.NRGBAModel, color.NRGBA64Model:
		return true
	}
	if _, ok := img.(*image.Paletted); ok {
		// 调色板可能含透明色
		p := img.(*image.Paletted)
		for _, c := range p.Palette {
			if _, _, _, a := c.RGBA(); a < 0xffff {
				return true
			}
		}
	}
	return false
}

// toRGB 把图像画到不透明 RGB 上（JPEG 不支持 alpha）。
func toRGB(img image.Image) image.Image {
	if hasAlpha(img) {
		b := img.Bounds()
		out := image.NewRGBA(b)
		draw.Draw(out, b, image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.Draw(out, b, img, b.Min, draw.Over)
		return out
	}
	return img
}

func encodePNG(w io.Writer, img image.Image) error { return png.Encode(w, img) }
