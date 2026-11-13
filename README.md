# pdftoolbox

跨平台（**Windows / macOS / Linux**）PDF 工具箱：**合并**（PDF + Word + 图片）、**拆分**、**旋转**、**页码**、**水印**（文字/图片、可平铺）、**删空白页**、**信息**。命令行 + 内置图形界面（本地 Web UI）。

> Go 重写版：**单文件、零依赖**（约 14 MB，内嵌 CJK 字体），不需要 Python 或任何运行库。
> 命令行参数与原版 Python 实现完全一致，习惯可直接迁移。

## 特性

- **合并**：PDF、Word（.doc/.docx/.odt/.rtf/.wps，经 LibreOffice）、图片（png/jpg/bmp/tif/gif/webp）任意混合；图片按 `--image-dpi` 定尺寸；手机照片 EXIF 方向自动纠正；目录输入自然排序（img2 在 img10 前）
- **拆分**：每页一个文件，或按范围 `"1-3,5,8-"` 每段一个；重复段自动加后缀不互相覆盖
- **旋转**：90/180/270（可为负），与页面已有旋转**叠加**
- **页码**：起始页/起始编号任意、格式模板 `{n}`/`{t}`（支持中文，如 `第 {n} 页 共 {t} 页`）、九锚点位置、字号/颜色/不透明度；**输出为可搜索的真实文字**（内嵌 Noto Sans SC 子集）
- **水印**：文字（旋转或平铺整页，中文可用）或图片（保留透明通道）；可指定页码范围
- **删空白页**：纯 Go 内容流分析（无文字、无图像、无非白绘制即空白）；**含文字的页面永不删除**；`--dry-run` 仅检测
- **信息**：页数、页面尺寸分布、旋转、文本页统计、元数据（`--json` 机器可读）
- **诚实的错误处理**：原子写盘（临时文件 + rename，支持原地覆盖）、可读错误、退出码语义清晰、任何 panic 都被兜底成一行错误而不是崩溃栈

## 下载安装

到 [Releases](https://github.com/huanxiang666/pdftoolbox/releases) 下载对应平台包，解压即用：

| 平台 | 文件 |
|---|---|
| Windows x64 | `pdftoolbox-x.y.z-windows-amd64.zip` |
| Windows arm64 | （CI 构建） |
| Linux x64 / arm64 | `pdftoolbox-x.y.z-linux-*.tar.gz` |
| macOS Intel / Apple Silicon | `pdftoolbox-x.y.z-darwin-*.tar.gz` |

Windows 包未做代码签名，SmartScreen 提示时选择「仍要运行」。

### 从源码构建

```bash
go build -o pdftoolbox ./cmd/pdftoolbox
```

## 图形界面

```bash
pdftoolbox gui            # 启动本地 Web 界面并自动打开浏览器
pdftoolbox gui --port 9000   # 指定端口
pdftoolbox gui --no-browser  # 不自动开浏览器
```

界面把七个功能做成标签页：**拖文件进去 → 点开始 → 下载结果**。所有处理都在本机完成，文件不经过任何外部服务器。关闭终端窗口即停止服务。

## 命令行用法

```bash
# 合并：pdf + word + 图片，顺序即参数顺序
pdftoolbox merge 报告.docx 附图.png 附件.pdf -o 合并结果.pdf

# 拆分：每页一个文件 / 按范围每段一个文件
pdftoolbox split 大文件.pdf -o parts --per-page
pdftoolbox split 大文件.pdf -o parts --ranges "1-3,5,8-"

# 旋转：第 1-3 页顺时针 90°
pdftoolbox rotate in.pdf -o out.pdf --angle 90 --pages 1-3

# 页码：从第 2 页开始标"第 10 页"，右下角，中文格式
pdftoolbox number in.pdf -o out.pdf --start-page 2 --first-number 10 \
    --format "第 {n} 页 共 {t} 页" --position bottom-right

# 水印：斜排 / 平铺 / 图片
pdftoolbox watermark in.pdf -o out.pdf --text "机密" --opacity 0.18
pdftoolbox watermark in.pdf -o out.pdf --text "机密" --tile --opacity 0.18
pdftoolbox watermark in.pdf -o out.pdf --image logo.png --position bottom-right --image-scale 0.2

# 删空白页（先 dry-run 看看删哪些）
pdftoolbox clean in.pdf --dry-run
pdftoolbox clean in.pdf -o out.pdf

# 信息 / 环境自检
pdftoolbox info in.pdf --json
pdftoolbox check
```

**退出码**：`0` 成功 · `1` 操作失败 · `2` 用法错误 · `130` 用户中断。

**页码范围语法**：`"3"`、`"2-7"`、`"5-"`（到末页）、`"-3"`（从首页）、`"1-3,5,8-"` 组合；`all` 或留空 = 全部页。

**位置**：`top-left top-center top-right middle-left center middle-right bottom-left bottom-center bottom-right`

**颜色**：`black white red green blue gray yellow orange purple cyan magenta`、`#rrggbb`、`#rgb`、`"r,g,b"`（0..1）

## Word 支持（三级回退，永远可用）

合并 Word 文档按以下顺序自动选择转换器，**任何一级失败自动尝试下一级**：

1. **Microsoft Word / WPS Office 的 COM 自动化**（仅 Windows，装了任一即可，保真度最高）
   - 探测顺序 Word → WPS；可用 `PDFTOOLBOX_COM_PROGID=KWPS.Application` 强制指定
2. **LibreOffice**（全平台，`soffice` 自动探测；`PDFTOOLBOX_WORD_CONVERTER` 可强制指定）
3. **内嵌纯 Go DOCX 渲染器**（零依赖兜底，任何平台都可用）
   - 覆盖：段落（字号/粗斜体/颜色/对齐/缩进/间距）、标题样式、表格网格、内嵌图片、分页符、项目符号
   - 不覆盖：浮动图形、文本框、页眉页脚、脚注、复杂多级编号（这类文档请装 Word/WPS/LibreOffice）

因此**即使一台机器上什么 Office 都没装，.docx 也能转**；.doc/.odt/.rtf/.wps 老格式仍需 1 或 2。

## 行为说明

- **原子写盘**：所有输出先写 `*.part<pid>` 再 rename；输出路径 = 输入路径是安全的
- **空白页判定**：无文字操作、无图像/XObject、无着色、无非白绘制的页面判为空白；含文字的页面永不被删
- **页码/水印中文**：内嵌 Noto Sans SC 子集（SIL OFL），渲染为嵌入字体，**可搜索可复制**
- **图片尺寸**：默认 96 DPI 把像素换算为 PDF 点（`--image-dpi 300` 适合扫描件归档）
- **加密 PDF**：明确报错提示先解密
- **多帧 GIF / 多页 TIFF**：仅取第一帧/页

## 与 Python 原版的差异

| 项 | Python 原版 | 本 Go 版 |
|---|---|---|
| 分发 | PyInstaller 包 100+ MB，启动慢 | 单文件 14 MB，毫秒级启动 |
| 依赖 | Python 运行时 + PyMuPDF + Pillow | 无 |
| GUI | tkinter 桌面窗口 | 本地 Web UI（浏览器） |
| 空白页判定 | 48 DPI 栅格 + 墨迹占比 | 内容流静态分析（更快，方向更安全） |
| 平铺水印网格 | 相同算法 | 相同算法（行距 fontsize×4，奇数行错半列） |

## 从源码开发

```bash
go test ./...        # 单元测试（common / ingest / ops）
go vet ./...
gofmt -l .
```

构建发布包：

```bash
powershell -File build.ps1 -Version 1.0.0     # Windows
# 或交叉编译：
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o pdftoolbox-linux ./cmd/pdftoolbox
```

## 技术栈与许可

| 组件 | 用途 | 许可证 |
|---|---|---|
| [pdfcpu](https://github.com/pdfcpu/pdfcpu) | PDF 引擎 | Apache-2.0 |
| [golang.org/x/image](https://pkg.go.dev/golang.org/x/image) | webp/tiff/bmp 解码 | BSD-3-Clause |
| Noto Sans SC 子集（内嵌） | 中文页码/水印 | SIL OFL 1.1 |

本软件整体以 **AGPL-3.0** 发布，与原版一致，详见 [LICENSE](LICENSE) 与 [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md)。

## 已知限制

- 加密（带密码）PDF 不支持输入
- 多帧 GIF / 多页 TIFF 仅取第一帧/页
- Word 转换质量取决于 LibreOffice 本身
- 平铺水印的角度/间距为固定算法
