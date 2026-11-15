# 第三方许可

## pdfcpu
Apache License 2.0 — https://github.com/pdfcpu/pdfcpu
PDF 引擎：合并/拆分/旋转/水印/页内容提取。

## golang.org/x/image
BSD-3-Clause — https://go.googlesource.com/image/
webp / tiff / bmp 解码。

## golang.org/x/text
BSD-3-Clause — https://go.googlesource.com/text/
pdfcpu 的传递依赖（文本规范化）。

## Noto Sans SC（子集，内嵌）
SIL Open Font License 1.1 — https://fonts.google.com/noto/specimen/Noto+Sans+SC
内嵌于 internal/cjkfont/assets/notosanssc.ttf，含 ASCII + GB2312 一二级汉字 + 工程常用符号，
由 fontTools.subset 从 Noto Sans SC 可变字体生成。仅用于页码与水印文字渲染。
