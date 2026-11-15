// Package common 提供与 Python 版 pdftoolbox 完全一致的公共语义：
// 错误类型、页码范围解析、位置锚点、颜色解析、原子写盘。
package common

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// OpError 面向用户的操作错误（参数错误、不支持的输入等）。
type OpError struct{ msg string }

func (e *OpError) Error() string { return e.msg }

// Err 构造 OpError。
func Err(format string, a ...any) error { return &OpError{fmt.Sprintf(format, a...)} }

// IsOpError 判断是否为面向用户的错误。
func IsOpError(err error) bool {
	_, ok := err.(*OpError)
	return ok
}

// ParsePages 把 1-based 页码描述解析为排序去重后的 0-based 索引。
//
// 接受：nil/""/"all"（全部）、单页 "3"、区间 "2-7"、开区间 "5-"、
// 前开区间 "-3"、逗号组合 "1-3,5,8-"。非法或越界返回 OpError。
func ParsePages(spec string, total int) ([]int, error) {
	if total <= 0 {
		return nil, Err("document has no pages")
	}
	s := strings.TrimSpace(strings.ToLower(spec))
	if spec == "" || s == "all" {
		out := make([]int, total)
		for i := range out {
			out[i] = i
		}
		return out, nil
	}
	set := map[int]bool{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "-") {
			loS, hiS, found := cut(part, "-")
			if !found {
				return nil, Err("invalid page range: %q", part)
			}
			loS, hiS = strings.TrimSpace(loS), strings.TrimSpace(hiS)
			lo, hi := 1, total
			var err error
			if loS != "" {
				if lo, err = strconv.Atoi(loS); err != nil {
					return nil, Err("invalid page range: %q", part)
				}
			}
			if hiS != "" {
				if hi, err = strconv.Atoi(hiS); err != nil {
					return nil, Err("invalid page range: %q", part)
				}
			}
			if lo < 1 || hi > total || lo > hi {
				return nil, Err("page range %q out of bounds (document has %d pages)", part, total)
			}
			for i := lo - 1; i < hi; i++ {
				set[i] = true
			}
		} else {
			n, err := strconv.Atoi(part)
			if err != nil {
				return nil, Err("invalid page number: %q", part)
			}
			if n < 1 || n > total {
				return nil, Err("page %d out of bounds (document has %d pages)", n, total)
			}
			set[n-1] = true
		}
	}
	if len(set) == 0 {
		return nil, Err("page selection %q is empty", spec)
	}
	out := make([]int, 0, len(set))
	for i := range set {
		out = append(out, i)
	}
	sort.Ints(out)
	return out, nil
}

// cut 是 strings.Cut 的兼容包装。
func cut(s, sep string) (string, string, bool) {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

// FormatPages 返回 1-based 的人类可读页码摘要。
func FormatPages(indices []int, total int) string {
	if len(indices) == total {
		return "all"
	}
	nums := make([]int, len(indices))
	for i, v := range indices {
		nums[i] = v + 1
	}
	if len(nums) <= 8 {
		parts := make([]string, len(nums))
		for i, n := range nums {
			parts[i] = strconv.Itoa(n)
		}
		return strings.Join(parts, ",")
	}
	return fmt.Sprintf("%d-%d (%d pages)", nums[0], nums[len(nums)-1], len(nums))
}

// Position 是 (fx, fy) 锚点比例。
type Position struct{ FX, FY float64 }

// Positions 九个锚点位置，键名与 Python 版一致。
var Positions = map[string]Position{
	"top-left":      {0.0, 0.0},
	"top-center":    {0.5, 0.0},
	"top-right":     {1.0, 0.0},
	"middle-left":   {0.0, 0.5},
	"center":        {0.5, 0.5},
	"middle-right":  {1.0, 0.5},
	"bottom-left":   {0.0, 1.0},
	"bottom-center": {0.5, 1.0},
	"bottom-right":  {1.0, 1.0},
}

// PositionNames 返回排序后的位置名列表（用于帮助文本）。
func PositionNames() []string {
	out := make([]string, 0, len(Positions))
	for k := range Positions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ResolvePosition 解析位置名。
func ResolvePosition(name, def string) (Position, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		key = strings.ToLower(def)
	}
	p, ok := Positions[key]
	if !ok {
		return Position{}, Err("unknown position %q (choose from: %s)", name, strings.Join(PositionNames(), ", "))
	}
	return p, nil
}

// RGB 颜色（0..1）。
type RGB struct{ R, G, B float64 }

var namedColors = map[string]RGB{
	"black":   {0, 0, 0},
	"white":   {1, 1, 1},
	"red":     {0.85, 0.1, 0.1},
	"green":   {0.1, 0.55, 0.15},
	"blue":    {0.1, 0.2, 0.8},
	"gray":    {0.55, 0.55, 0.55},
	"grey":    {0.55, 0.55, 0.55},
	"yellow":  {0.95, 0.8, 0.05},
	"orange":  {0.95, 0.55, 0.05},
	"purple":  {0.5, 0.2, 0.7},
	"cyan":    {0.1, 0.7, 0.8},
	"magenta": {0.8, 0.15, 0.7},
}

// ParseColor 解析 "red" / "#rrggbb" / "#rgb" / "0.5,0.2,0.9"。
func ParseColor(value string, def RGB) (RGB, error) {
	s := strings.ToLower(strings.TrimSpace(value))
	if value == "" || s == "" {
		return def, nil
	}
	if c, ok := namedColors[s]; ok {
		return c, nil
	}
	if strings.HasPrefix(s, "#") {
		h := s[1:]
		switch len(h) {
		case 3:
			r, err1 := hex1(h[0])
			g, err2 := hex1(h[1])
			b, err3 := hex1(h[2])
			if err1 != nil || err2 != nil || err3 != nil {
				return RGB{}, Err("invalid hex color: %q (expected #rgb or #rrggbb)", value)
			}
			return RGB{r, g, b}, nil
		case 6:
			r, err1 := hex2(h[0:2])
			g, err2 := hex2(h[2:4])
			b, err3 := hex2(h[4:6])
			if err1 != nil || err2 != nil || err3 != nil {
				return RGB{}, Err("invalid hex color: %q (expected #rgb or #rrggbb)", value)
			}
			return RGB{r, g, b}, nil
		}
		return RGB{}, Err("invalid hex color: %q (expected #rgb or #rrggbb)", value)
	}
	parts := strings.Split(s, ",")
	if len(parts) == 3 {
		var v [3]float64
		ok := true
		for i, p := range parts {
			f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
			if err != nil || f < 0 || f > 1 {
				ok = false
				break
			}
			v[i] = f
		}
		if ok {
			return RGB{v[0], v[1], v[2]}, nil
		}
	}
	return RGB{}, Err("invalid color: %q (use a name, #rrggbb, or 'r,g,b' with 0..1 values)", value)
}

func hex1(c byte) (float64, error) {
	v, err := strconv.ParseUint(string([]byte{c, c}), 16, 32)
	if err != nil {
		return 0, err
	}
	return float64(v) / 255, nil
}

func hex2(s string) (float64, error) {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, err
	}
	return float64(v) / 255, nil
}

// SaveAtomic 先写临时文件再 rename，保证失败不留半个文件。
// 输出路径等于输入路径也是安全的（调用方应已把输入读入内存）。
func SaveAtomic(write func(tmp string) error, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.part%d", path, os.Getpid())
	if err := write(tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
