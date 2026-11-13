//go:build !windows

package ingest

import "time"

// comConverter 在非 Windows 平台不存在。
type comConverter struct{ ProgID string }

func (c comConverter) DisplayName() string { return "COM (unavailable)" }

func detectCOMConverter() *comConverter { return nil }

func convertWordViaCOM(src, outDir string, conv *comConverter, timeout time.Duration) (string, error) {
	return "", errNoCOM
}
