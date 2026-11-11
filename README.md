# pdftoolbox — PDF 工具箱

[![CI](https://github.com/huanxiang666/pdftoolbox/actions/workflows/ci.yml/badge.svg)](https://github.com/huanxiang666/pdftoolbox/actions/workflows/ci.yml)
[![Release](https://github.com/huanxiang666/pdftoolbox/actions/workflows/release.yml/badge.svg)](https://github.com/huanxiang666/pdftoolbox/actions/workflows/release.yml)
[![License: AGPL-3.0](https://img.shields.io/badge/license-AGPL--3.0-blue.svg)](LICENSE)

跨平台（**Windows / Linux**）PDF 工具箱：**合并**（PDF + Word + 图片）、**拆分**、**旋转**、**自定义起始页加页码**、**加水印**（文字/图片、可平铺）、**删空白页**。提供命令行（CLI）和图形界面（GUI）两种用法。

English: [README_EN.md](README_EN.md)
姊妹项目：[dwg2pdf — DWG/DXF 批量转 PDF](https://github.com/huanxiang666/dwg2pdf)

## 特性

- **合并**：PDF、Word（.doc/.docx/.odt/.rtf/.wps）、图片（png/jpg/bmp/tif/gif/webp）任意混合，按给定顺序合成一个 PDF；图片按指定 DPI 定尺寸，手机照片 EXIF 方向自动纠正
- **拆分**：每页一个文件，或按页码范围（`"1-3,5,8-"`）每段一个文件
- **旋转**：任意页顺时针 90/180/270（支持负角度），与页面已有旋转叠加
- **页码**：从第 N 页开始、起始编号任意、格式模板 `{n}`/`{t}`（支持中文，如 `第 {n} 页 共 {t} 页`）、9 个锚点位置、字号/颜色/透明度；输出为**可搜索的真实文字**（内置 CJK 字体）
- **水印**：文字（45° 斜排或平铺整页，中文可用）或图片（PNG 透明通道保留，透明度预乘）；可指定页码范围
- **删空白页**：低分辨率栅格化 + 墨迹占比阈值判定；**含文字的页面永不删除**（即使白字），支持 `--dry-run` 仅检测
- **诚实的错误处理**：所有操作原子写盘（临时文件 + rename），报错信息可读，退出码语义清晰
- **信息**：页数、页面尺寸分布、旋转、文本页统计、元数据

## 下载安装

### 方式一：下载可执行包（推荐，无需 Python）

到 [Releases](https://github.com/huanxiang666/pdftoolbox/releases) 下载：

| 平台 | 文件 | 用法 |
|---|---|---|
| Windows x64 | `pdftoolbox-x.y.z-windows-x64.zip` | 解压后双击 `pdftoolbox-gui.exe`（图形界面）或用 `pdftoolbox.exe`（命令行） |
| Linux x64 | `pdftoolbox-x.y.z-linux-x64.tar.gz` | 解压后运行 `pdftoolbox/pdftoolbox-gui`（图形界面）或 `pdftoolbox/pdftoolbox`（命令行） |

> Windows 包未做代码签名，SmartScreen 提示时选择「仍要运行」。

### 方式二：pip 安装（需要 Python 3.10+）

```bash
pip install git+https://github.com/huanxiang666/pdftoolbox.git
pdftoolbox check      # 环境自检
pdftoolbox-gui        # 图形界面
```

## Word 文档支持：需要一个免费转换器

**PDF 和图片开箱即用。**合并 Word 文档需要以下**任一**外部转换器（自动检测，也可用 `PDFTOOLBOX_WORD_CONVERTER=/path/to/soffice` 指定）：

1. **LibreOffice**（免费，全平台）：<https://www.libreoffice.org/download/>
   - Debian/Ubuntu：`sudo apt install libreoffice-writer`
2. **Windows + Microsoft Word**：`pip install "pdftoolbox[word]"`（docx2pdf 桥接）

没有转换器时，Word 输入会报出带安装指引的明确错误，PDF/图片功能不受影响。

## 快速上手（CLI）

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

# 水印：斜排平铺 / 图片水印
pdftoolbox watermark in.pdf -o out.pdf --text "机密" --tile --opacity 0.18
pdftoolbox watermark in.pdf -o out.pdf --image logo.png --position bottom-right --image-scale 0.2

# 删空白页（先 dry-run 看看删哪些）
pdftoolbox clean in.pdf --dry-run
pdftoolbox clean in.pdf -o out.pdf

# 信息 / 环境自检
pdftoolbox info in.pdf --json
pdftoolbox check
```

**退出码**：`0` 成功 · `1` 操作失败（错误信息在 stderr） · `2` 用法错误 · `130` 用户中断。

**页码范围语法**（`--pages`/`--ranges` 通用）：`"3"`、`"2-7"`、`"5-"`（到末页）、`"-3"`（从首页）、`"1-3,5,8-"` 组合；`all` 或留空 = 全部页。

## 图形界面

Windows 双击 `pdftoolbox-gui.exe`；Linux 运行 `pdftoolbox-gui` 或 `pdftoolbox gui`。
七个标签页对应七个功能：合并 / 拆分 / 旋转 / 页码 / 水印 / 删空白页 / 信息。合并页支持调整文件顺序（上移/下移），所有操作在后台线程执行、界面不卡。

## 行为说明（工程细节）

- **原子写盘**：所有输出先写 `*.part<pid>` 临时文件再 rename，中途失败不会留下半个 PDF；**支持原地覆盖**（输出路径 = 输入路径是安全的，输入先整体读入内存）。
- **空白页判定**：无任何可提取文字的页面，按 48 DPI 栅格化后统计非白像素占比，低于阈值（默认 0.002）判为空白。有文字的页面永不被删——白字、隐形文字也是内容。
- **页码/水印文字**使用内置 CJK 字体渲染，中文正常显示且可搜索复制。
- **图片合并尺寸**：默认按 96 DPI 把像素换算为 PDF 点（`--image-dpi` 可调；300 DPI 适合扫描件归档）。
- **加密 PDF**：明确报错提示先解密（v0.1 不支持带密码输入）。

## 从源码开发

```bash
git clone https://github.com/huanxiang666/pdftoolbox.git && cd pdftoolbox
python -m venv .venv && source .venv/bin/activate   # Windows: .venv\Scripts\activate
pip install -e ".[dev]"
pytest -q                                            # 62+ 项测试
```

## 打包发布

```bash
pip install . pyinstaller
pyinstaller packaging/pdftoolbox.spec --noconfirm    # 产物在 dist/pdftoolbox/
```

推送 `v*` 标签后 GitHub Actions 自动构建 Windows / Linux 可执行包并发布 Release（含冻结二进制的多步冒烟测试）。

## 技术栈与许可证

| 组件 | 用途 | 许可证 |
|---|---|---|
| [PyMuPDF](https://pymupdf.org/) | PDF 引擎（合并/拆分/旋转/文字/图像） | AGPL-3.0 |
| [Pillow](https://python-pillow.org/) | 图片尺寸/EXIF 方向/透明度处理 | HPND |
| [LibreOffice](https://www.libreoffice.org/)（可选，外部进程） | Word → PDF | MPL-2.0 |
| [docx2pdf](https://pypi.org/project/docx2pdf/)（可选） | Windows Word COM 桥接 | MIT |

本软件整体以 **AGPL-3.0** 发布（因捆绑 PyMuPDF），详见 [LICENSE](LICENSE) 与 [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md)。

## 已知限制

- 加密（带密码）PDF 不支持输入。
- 多帧 GIF / 多页 TIFF 仅取第一帧/页。
- Word 转换质量取决于 LibreOffice/MS Word 本身；复杂排版的 .doc 老格式偶有走样。
- 水印平铺的角度/间距为固定算法，暂不支持自定义间距。
