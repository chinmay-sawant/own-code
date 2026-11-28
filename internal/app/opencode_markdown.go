package app

import (
	"html"
	"html/template"
	"strings"
	"unicode/utf8"
)

// renderOpenCodeMarkdown converts assistant text into a small, safe HTML
// subset: fenced code, headings, bullet items, and paragraphs. Every byte of
// input is escaped; no input HTML or URL is ever emitted raw. An unterminated
// fence (a stream still in progress) renders as an open code block.
func renderOpenCodeMarkdown(text string) template.HTML {
	text = strings.ToValidUTF8(text, "�")
	var b strings.Builder
	var para []string
	inCode := false
	var code []string
	flushPara := func() {
		if len(para) > 0 {
			b.WriteString(`<p class="md-p">` + html.EscapeString(strings.Join(para, " ")) + `</p>`)
			para = nil
		}
	}
	flushCode := func() {
		b.WriteString(`<pre class="md-code">` + html.EscapeString(strings.Join(code, "\n")) + `</pre>`)
		code = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				flushCode()
				inCode = false
			} else {
				flushPara()
				inCode = true
			}
			continue
		}
		if inCode {
			code = append(code, line)
			continue
		}
		switch {
		case trimmed == "":
			flushPara()
		case strings.HasPrefix(trimmed, "#"):
			flushPara()
			level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
			if level <= 6 && strings.HasPrefix(trimmed[level:], " ") {
				b.WriteString(`<p class="md-h">` + html.EscapeString(strings.TrimSpace(trimmed[level:])) + `</p>`)
			} else {
				para = append(para, trimmed)
			}
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			flushPara()
			b.WriteString(`<p class="md-li">• ` + html.EscapeString(strings.TrimSpace(trimmed[2:])) + `</p>`)
		default:
			para = append(para, trimmed)
		}
	}
	if inCode {
		flushCode()
	}
	flushPara()
	return template.HTML(b.String())
}

// openCodePlainBody renders non-assistant text verbatim and escaped.
func openCodePlainBody(text string) template.HTML {
	return template.HTML(`<pre>` + html.EscapeString(strings.ToValidUTF8(text, "�")) + `</pre>`)
}

// splitIncompleteUTF8 returns the longest valid prefix of b and the trailing
// bytes that could still become a complete rune with more input. Invalid bytes
// inside the prefix are replaced by U+FFFD.
func splitIncompleteUTF8(b []byte) (string, []byte) {
	cut := len(b)
	for i := 1; i <= utf8.UTFMax-1 && i <= len(b); i++ {
		c := b[len(b)-i]
		if c&0xC0 == 0x80 {
			continue // continuation byte; keep looking for the lead
		}
		if c >= 0xC0 && !utf8.FullRune(b[len(b)-i:]) {
			cut = len(b) - i
		}
		break
	}
	return strings.ToValidUTF8(string(b[:cut]), "�"), append([]byte(nil), b[cut:]...)
}
