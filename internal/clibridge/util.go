package clibridge

// util.go —— clibridge 包内小工具。

import (
	"encoding/base64"
	"regexp"
	"unicode/utf16"
)

// regexpMatch 简易正则匹配（config.toml 行解析用，量小可接受）。
func regexpMatch(pattern, s string) bool {
	return regexp.MustCompile(pattern).MatchString(s)
}

// itoa 非负整数转字符串（避免重复引 strconv）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// encodeBase64 UTF-16LE 编码后 base64（对齐 TS Buffer.from(script,'utf16le').toString('base64')），
// 供 Windows PowerShell -EncodedCommand 使用。
func encodeBase64(s string) string {
	u16 := utf16.Encode([]rune(s))
	bytes := make([]byte, len(u16)*2)
	for i, v := range u16 {
		bytes[i*2] = byte(v)
		bytes[i*2+1] = byte(v >> 8)
	}
	return base64.StdEncoding.EncodeToString(bytes)
}
