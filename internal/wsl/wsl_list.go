package wsl

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// ParseDistroListText turns the text form of `wsl.exe -l -q` into distro
// names: one per non-empty line, trimmed, with a leading "*" (verbose mark)
// stripped. It is pure so the Windows lister and unit tests share it.
func ParseDistroListText(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		name := strings.TrimSpace(strings.Trim(line, "\r\ufeff "))
		name = strings.TrimPrefix(name, "*")
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		// Skip the header line older wsl.exe prints without -q.
		lowered := strings.ToLower(name)
		if strings.HasPrefix(lowered, "windows subsystem") || strings.HasPrefix(lowered, "name ") {
			continue
		}
		out = append(out, name)
	}
	return out
}

// DecodeWSLListOutput decodes `wsl.exe -l -q` bytes. The launcher writes
// UTF-16LE with a BOM; older builds may emit ANSI/UTF-8. A BOM forces LE,
// a NUL-heavy buffer without a BOM is treated as LE, otherwise the bytes
// pass through as UTF-8 with invalid sequences replaced.
func DecodeWSLListOutput(data []byte) string {
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		return decodeUTF16LE(data[2:])
	}
	if looksLikeUTF16LE(data) {
		return decodeUTF16LE(data)
	}
	if utf8.Valid(data) {
		return string(data)
	}
	return strings.ToValidUTF8(string(data), "")
}

// looksLikeUTF16LE reports ASCII-heavy UTF-16LE: even positions carry text
// and odd positions are mostly NUL. Empty or NUL-free buffers are not LE.
func looksLikeUTF16LE(data []byte) bool {
	if len(data) < 2 {
		return false
	}
	even := len(data) / 2
	if even == 0 {
		return false
	}
	nulOdd := 0
	for i := 1; i < len(data); i += 2 {
		if data[i] == 0 {
			nulOdd++
		}
	}
	// ASCII text in LE has every odd byte NUL; require a strong majority.
	return nulOdd*10 >= even*7
}

// decodeUTF16LE converts little-endian UTF-16 bytes to a Go string,
// handling surrogate pairs and replacing malformed units.
func decodeUTF16LE(data []byte) string {
	n := len(data) / 2
	u16 := make([]uint16, 0, n)
	for i := 0; i+1 < len(data); i += 2 {
		u16 = append(u16, uint16(data[i])|uint16(data[i+1])<<8)
	}
	return string(utf16.Decode(u16))
}
