package svg

import (
	"bytes"
	"strings"
)

const (
	svgTagOther = iota
	svgTagOpen
	svgTagClose
	svgTagSelfClose
)

// stripSVGTextElements removes text, tspan, and textPath elements, including
// their contents, before canvas.ParseSVG. Those elements call getFontFace,
// which loads a system font with no size cap and panics when the family is
// missing. Other bytes stay in place so geometry still rasterizes. Text
// glyphs disappear.
func stripSVGTextElements(data []byte) []byte {
	if !bytesContainsFold(data, []byte("<text")) && !bytesContainsFold(data, []byte("<tspan")) {
		return data
	}

	out, changed := stripSVGTextScan(data)
	if !changed {
		return data
	}

	return out
}

func stripSVGTextScan(data []byte) ([]byte, bool) {
	var out bytes.Buffer

	changed := false
	flush := 0
	pos := 0
	depth := 0

	for pos < len(data) {
		rel := bytes.IndexByte(data[pos:], '<')
		if rel < 0 {
			break
		}

		start := pos + rel
		end, kind, local, ok := svgScanMarkup(data, start)

		if !ok {
			break
		}

		if depth > 0 {
			depth, flush = svgSkipText(depth, flush, end, kind, local)
			pos = end + 1

			continue
		}

		if !isSVGTextElement(local) || kind == svgTagOther {
			pos = end + 1

			continue
		}

		changed = true

		out.Write(data[flush:start])

		if kind == svgTagSelfClose || kind == svgTagClose {
			flush = end + 1
			pos = flush

			continue
		}

		depth = 1
		flush = end + 1
		pos = flush
	}

	if depth == 0 {
		out.Write(data[flush:])
	}

	return out.Bytes(), changed
}

// svgSkipText updates the skip depth for one markup span inside a removed
// text element. A matching close that returns to zero sets flush just past
// that span so the caller copies the following bytes.
func svgSkipText(depth, flush, end, kind int, local string) (int, int) {
	if !isSVGTextElement(local) {
		return depth, flush
	}

	if kind == svgTagOpen {
		return depth + 1, flush
	}

	if kind == svgTagClose && depth > 0 {
		depth--
		if depth == 0 {
			flush = end + 1
		}
	}

	return depth, flush
}

func isSVGTextElement(local string) bool {
	switch local {
	case "text", "tspan", "textpath":
		return true
	default:
		return false
	}
}

// svgScanMarkup returns the index of the closing '>' plus the tag kind.
// Comments and CDATA are opaque so a fake end tag inside them cannot end
// a skipped text element early.
func svgScanMarkup(data []byte, start int) (int, int, string, bool) {
	if start >= len(data) || data[start] != '<' {
		return 0, svgTagOther, "", false
	}

	if end, ok := svgOpaqueMarkupEnd(data, start); ok || svgOpaqueMarkupOpen(data, start) {
		if !ok {
			return 0, svgTagOther, "", false
		}

		return end, svgTagOther, "", true
	}

	end, ok := emulateTagEnd(data, start)
	if !ok {
		return 0, svgTagOther, "", false
	}

	local, kind := svgTagLocalName(data, start, end)

	return end, kind, local, true
}

func svgOpaqueMarkupOpen(data []byte, start int) bool {
	return bytes.HasPrefix(data[start:], []byte("<!--")) ||
		bytes.HasPrefix(data[start:], []byte("<![CDATA["))
}

func svgOpaqueMarkupEnd(data []byte, start int) (int, bool) {
	if end, ok := svgMarkupCloser(data, start, "<!--", "-->"); ok {
		return end, true
	}

	return svgMarkupCloser(data, start, "<![CDATA[", "]]>")
}

func svgMarkupCloser(data []byte, start int, open, endMark string) (int, bool) {
	if !bytes.HasPrefix(data[start:], []byte(open)) {
		return 0, false
	}

	rel := bytes.Index(data[start+len(open):], []byte(endMark))
	if rel < 0 {
		return 0, false
	}

	return start + len(open) + rel + len(endMark) - 1, true
}

func svgTagKind(inner []byte) (int, []byte) {
	switch inner[0] {
	case '!', '?':
		return svgTagOther, inner
	case '/':
		return svgTagClose, inner[1:]
	default:
		return svgTagOpen, inner
	}
}

//nolint:cyclop // close slash and self-close share one name scan
func svgTagLocalName(data []byte, start, end int) (string, int) {
	inner := data[start+1 : end]
	if len(inner) == 0 {
		return "", svgTagOther
	}

	kind, inner := svgTagKind(inner)
	if kind == svgTagOther {
		return "", svgTagOther
	}

	inner = bytes.TrimLeft(inner, " \t\r\n")
	if len(inner) == 0 {
		return "", svgTagOther
	}

	if kind == svgTagOpen {
		trimmed := bytes.TrimRight(inner, " \t\r\n")
		if len(trimmed) > 0 && trimmed[len(trimmed)-1] == '/' {
			kind = svgTagSelfClose
		}
	}

	nameLen := 0
	for nameLen < len(inner) && isEmulateNameChar(inner[nameLen]) {
		nameLen++
	}

	if nameLen == 0 {
		return "", svgTagOther
	}

	name := strings.ToLower(string(inner[:nameLen]))
	if colon := strings.LastIndexByte(name, ':'); colon >= 0 {
		name = name[colon+1:]
	}

	return name, kind
}
