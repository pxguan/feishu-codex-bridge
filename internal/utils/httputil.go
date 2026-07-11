package utils

import "io"

// httputil.go —— 共享 HTTP 小工具（feishuauth / eventdiagnosis 用）。

func readAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}
