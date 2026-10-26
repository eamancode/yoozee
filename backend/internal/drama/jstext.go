package drama

import (
	"strings"
	"unicode/utf16"
)

// 服务端组装提示词沿用前端的字数上限与截断规则（原先在浏览器里拼），长度按 JS 字符串（UTF-16 码元）算，
// 空白按 JS 正则 \s 认，换到服务端后同一份输入拼出的文字不变。

// jsSpace 是 JS 正则 \s 匹配的字符。
func jsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsCompact 等于 value.replace(/\s+/g, " ").trim()。
func jsCompact(s string) string {
	return strings.Join(strings.FieldsFunc(s, jsSpace), " ")
}

// jsTrim 等于 String.prototype.trim。
func jsTrim(s string) string { return strings.TrimFunc(s, jsSpace) }

// JSTrim 是 JS 的 String.prototype.trim（服务端拼提示词时与前端同一套空白）。
func JSTrim(s string) string { return jsTrim(s) }

// JSLen 是 JS 字符串长度（UTF-16 码元数），提示词预算和字数上限都按它算。
func JSLen(s string) int { return jsLen(s) }

// jsLen 是 JS 的 string.length。
func jsLen(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// jsSlice 等于 s.slice(0, n)。
func jsSlice(s string, n int) string {
	if n <= 0 {
		return ""
	}
	units := utf16.Encode([]rune(s))
	if n >= len(units) {
		return s
	}
	return string(utf16.Decode(units[:n]))
}

// jsTruncate 是前端 truncateText：超长时截到 max-1 再加省略号。
func jsTruncate(s string, max int) string {
	if jsLen(s) > max {
		return jsSlice(s, max-1) + "…"
	}
	return s
}
