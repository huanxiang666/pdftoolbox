package ops

import (
	"bytes"
	"context"
	"io"
	"strconv"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// pageScan 是一页内容流的扫描结论。
type pageScan struct {
	HasText    bool
	HasImage   bool // 内联图像或 XObject（含表单）
	HasShading bool
	HasInk     bool // 存在非白色的描边/填充
}

// scanContent 解析一页内容流，判断是否含可见内容。
//
// 这是纯 Go 的空白页判定：不需要栅格化。
// 语义与 Python 版"48DPI 栅格 + 墨迹占比"近似，但方向更安全——
// 任何文字/图像/非白绘制都会判为"有内容"，不会误删。
func scanContent(r io.Reader) pageScan {
	var sc pageScan
	buf, err := io.ReadAll(r)
	if err != nil {
		return sc
	}

	var fillWhite, strokeWhite = true, true // 默认黑色 = 有墨；先置 true 表示"当前色是白"？不：默认黑
	fillWhite, strokeWhite = false, false
	patternFill, patternStroke := false, false

	var nums []float64
	var names []string

	i, n := 0, len(buf)
	for i < n {
		c := buf[i]
		switch {
		case c == '%': // 注释到行尾
			for i < n && buf[i] != '\n' && buf[i] != '\r' {
				i++
			}
		case c == '(':
			depth := 1
			i++
			for i < n && depth > 0 {
				switch buf[i] {
				case '\\':
					i++
				case '(':
					depth++
				case ')':
					depth--
				}
				i++
			}
			nums, names = nums[:0], names[:0]
			// 字符串是操作数，但对我们无意义；清空即可
		case c == '<' && i+1 < n && buf[i+1] != '<':
			i += 2
			for i < n && !(buf[i] == '>' && i+1 < n && buf[i+1] == '>') {
				if buf[i] == '>' {
					i++
					break
				}
				i++
			}
			if i < n {
				i++
			}
			nums, names = nums[:0], names[:0]
		case c == '<' && i+1 < n && buf[i+1] == '<':
			i += 2
			skipDict(buf, &i)
			nums, names = nums[:0], names[:0]
		case c == '[':
			i++
			// 数组：TJ 用；扫到 ] 为止，内部字符串跳过
			for i < n && buf[i] != ']' {
				if buf[i] == '(' {
					depth := 1
					i++
					for i < n && depth > 0 {
						switch buf[i] {
						case '\\':
							i++
						case '(':
							depth++
						case ')':
							depth--
						}
						i++
					}
					continue
				}
				i++
			}
			if i < n {
				i++
			}
		case c == '/':
			i++
			start := i
			for i < n && !isDelim(buf[i]) {
				i++
			}
			names = append(names, string(buf[start:i]))
		case c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9'):
			start := i
			i++
			for i < n && (buf[i] == '.' || (buf[i] >= '0' && buf[i] <= '9')) {
				i++
			}
			if v, err := parseFloat(string(buf[start:i])); err == nil {
				nums = append(nums, v)
			}
		case isAlpha(c):
			start := i
			for i < n && isAlpha(buf[i]) || (i < n && buf[i] == '*') {
				i++
			}
			op := string(buf[start:i])
			sc.applyOp(op, &nums, &names, &fillWhite, &strokeWhite, &patternFill, &patternStroke)
			nums, names = nums[:0], names[:0]
		default:
			i++
		}
	}
	return sc
}

func (sc *pageScan) applyOp(op string, nums *[]float64, names *[]string,
	fillWhite, strokeWhite *bool, patternFill, patternStroke *bool) {
	switch op {
	case "rg":
		if len(*nums) >= 3 {
			*fillWhite = nearWhiteRGB((*nums)[len(*nums)-3], (*nums)[len(*nums)-2], (*nums)[len(*nums)-1])
			*patternFill = false
		}
	case "g":
		if len(*nums) >= 1 {
			*fillWhite = nearWhiteGray((*nums)[len(*nums)-1])
			*patternFill = false
		}
	case "k":
		if len(*nums) >= 4 {
			*fillWhite = nearWhiteCMYK((*nums)[len(*nums)-4], (*nums)[len(*nums)-3], (*nums)[len(*nums)-2], (*nums)[len(*nums)-1])
			*patternFill = false
		}
	case "RG":
		if len(*nums) >= 3 {
			*strokeWhite = nearWhiteRGB((*nums)[len(*nums)-3], (*nums)[len(*nums)-2], (*nums)[len(*nums)-1])
			*patternStroke = false
		}
	case "G":
		if len(*nums) >= 1 {
			*strokeWhite = nearWhiteGray((*nums)[len(*nums)-1])
			*patternStroke = false
		}
	case "K":
		if len(*nums) >= 4 {
			*strokeWhite = nearWhiteCMYK((*nums)[len(*nums)-4], (*nums)[len(*nums)-3], (*nums)[len(*nums)-2], (*nums)[len(*nums)-1])
			*patternStroke = false
		}
	case "cs":
		if len(*names) > 0 {
			*patternFill = eqFold(names_last(*names), "Pattern")
		}
	case "CS":
		if len(*names) > 0 {
			*patternStroke = eqFold(names_last(*names), "Pattern")
		}
	case "sc", "scn":
		if *patternFill {
			*fillWhite = false
		} else if len(*nums) >= 1 {
			*fillWhite = allNearWhite(*nums)
		}
	case "SC", "SCN":
		if *patternStroke {
			*strokeWhite = false
		} else if len(*nums) >= 1 {
			*strokeWhite = allNearWhite(*nums)
		}
	case "S", "s":
		if !*strokeWhite {
			sc.HasInk = true
		}
	case "f", "F", "f*":
		if !*fillWhite {
			sc.HasInk = true
		}
	case "B", "B*", "b", "b*":
		if !*fillWhite || !*strokeWhite {
			sc.HasInk = true
		}
	case "sh":
		sc.HasShading = true
	case "BI":
		sc.HasImage = true
	case "Do":
		sc.HasImage = true
	case "Tj", "TJ", "'", "\"":
		sc.HasText = true
	}
}

func names_last(ns []string) string {
	if len(ns) == 0 {
		return ""
	}
	return ns[len(ns)-1]
}

func eqFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'a' && ca <= 'z' {
			ca -= 32
		}
		if cb >= 'a' && cb <= 'z' {
			cb -= 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func allNearWhite(nums []float64) bool {
	for _, v := range nums {
		if v < 0.98 {
			return false
		}
	}
	return true
}

func nearWhiteRGB(r, g, b float64) bool { return r >= 0.98 && g >= 0.98 && b >= 0.98 }
func nearWhiteGray(v float64) bool      { return v >= 0.98 }
func nearWhiteCMYK(c, m, y, k float64) bool {
	return c <= 0.02 && m <= 0.02 && y <= 0.02 && k <= 0.02
}

func isDelim(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', '\f', '\v', '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '\'' || c == '"'
}

func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

// skipDict 跳过 << ... >>（含嵌套）。
func skipDict(buf []byte, i *int) {
	depth := 1
	n := len(buf)
	for *i < n && depth > 0 {
		if buf[*i] == '<' && *i+1 < n && buf[*i+1] == '<' {
			depth++
			*i += 2
			continue
		}
		if buf[*i] == '>' && *i+1 < n && buf[*i+1] == '>' {
			depth--
			*i += 2
			continue
		}
		if buf[*i] == '(' {
			d := 1
			*i++
			for *i < n && d > 0 {
				switch buf[*i] {
				case '\\':
					*i++
				case '(':
					d++
				case ')':
					d--
				}
				*i++
			}
			continue
		}
		*i++
	}
}

// scanPages 扫描全部页的内容流。
func scanPages(data []byte) ([]pageScan, error) {
	out := make([]pageScan, 0, 64)
	err := api.ExtractContent(context.Background(), bytes.NewReader(data), nil,
		func(r io.Reader, pageNr int) error {
			out = append(out, scanContent(r))
			return nil
		}, conf())
	if err != nil {
		return nil, err
	}
	return out, nil
}
