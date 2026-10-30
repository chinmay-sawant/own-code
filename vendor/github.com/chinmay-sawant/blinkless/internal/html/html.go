// Package html implements a tokenizer and tree builder for the HTML subset
// blinkless accepts: tags, attributes, text, comments, doctype, self-closing
// and void elements. The tokenizer follows the WHATWG tokenizer's recovery
// rules for unfinished comments, tags, declarations, and quoted attribute
// values. Script/style contents are kept as raw text and stripped at the
// layout stage.
//
// ponytail: custom Node tree (Parent/Attrs/void); migrate to x/net/html only if layout/css rewritten, not free delete.
//
//nolint:all
package html

import (
	"bytes"
	"errors"
	"strings"
	"unicode/utf8"
)

// NodeType classifies a DOM node.
type NodeType int

const (
	// NodeUnknown is the zero value of NodeType. Parsed trees never produce
	// it; a zero-constructed Node must not classify as an ElementNode.
	NodeUnknown NodeType = iota
	ElementNode
	TextNode
	CommentNode
	DoctypeNode
)

// Node is one DOM node.
type Node struct {
	Type      NodeType
	Name      string // element name (adjusted case for foreign elements)
	Namespace Namespace
	Attrs     map[string]string
	AttrList  []Attr
	Text      string // text/comment/doctype content
	Doctype   Doctype
	Mode      DocumentMode // document mode; only meaningful on the root
	Children  []*Node
	// Contents holds a template element's content, mirroring the DOM's
	// separate DocumentFragment for template.content. It stays out of
	// Children, Walk, TextContent, and FindFirst so template content is
	// never rendered or collected as active style; consumers that need it
	// read Contents directly.
	Contents []*Node
	Parent   *Node
}

// Attribute returns an attribute value, or "". Attribute keys are stored
// lowercased for HTML elements and adjusted for foreign elements, so lookups
// try the exact name first and fall back to the lowercased key for HTML-style
// callers.
func (n *Node) Attribute(name string) string {
	if value, ok := n.Attrs[name]; ok {
		return value
	}

	for i := range len(name) {
		if name[i] >= 'A' && name[i] <= 'Z' {
			return n.Attrs[strings.ToLower(name)]
		}
	}

	return n.Attrs[name]
}

// FirstChild returns the first element child with name, or nil.
func (n *Node) FirstChild(name string) *Node {
	for _, c := range n.Children {
		if c.Type == ElementNode && c.Name == name {
			return c
		}
	}

	return nil
}

// TextContent concatenates all descendant text. Template contents are a
// separate fragment and contribute nothing, matching DOM textContent.
func (n *Node) TextContent() string {
	var b strings.Builder

	n.appendText(&b)

	return b.String()
}

// Walk visits n and every descendant in pre-order (document order). Template
// contents are not visited: they live in Node.Contents, not Children, and are
// inert until a consumer instantiates them. Deep adversarial trees stop at
// maxElementDepth.
func (n *Node) Walk(f func(*Node)) {
	n.WalkUntil(func(node *Node) bool {
		f(node)

		return true
	})
}

// WalkUntil visits n and every descendant in pre-order, stopping when f returns false.
// It reports whether the full tree was visited. Deep adversarial trees stop at
// maxElementDepth.
func (n *Node) WalkUntil(f func(*Node) bool) bool {
	return walkUntil(n, f, 0)
}

func walkUntil(n *Node, f func(*Node) bool, depth int) bool {
	if depthExceeded(depth) {
		return false
	}

	if !f(n) {
		return false
	}

	for _, child := range n.Children {
		if !walkUntil(child, f, depth+1) {
			return false
		}
	}

	return true
}

// FindFirst returns the first node in pre-order for which pred returns true, or nil.
// Template contents are not searched; see Walk.
func (n *Node) FindFirst(pred func(*Node) bool) *Node {
	var found *Node

	n.WalkUntil(func(node *Node) bool {
		if pred(node) {
			found = node

			return false
		}

		return true
	})

	return found
}

// TextContentOf returns the text content of the first element descendant
// named name, or "" when there is none.
func (n *Node) TextContentOf(name string) string {
	if found := n.FindFirst(func(c *Node) bool { return c.Type == ElementNode && c.Name == name }); found != nil {
		return found.TextContent()
	}

	return ""
}

func (n *Node) appendText(buf *strings.Builder) {
	n.appendTextDepth(buf, 0)
}

func (n *Node) appendTextDepth(buf *strings.Builder, depth int) {
	if depthExceeded(depth) {
		return
	}

	switch n.Type {
	case TextNode:
		buf.WriteString(n.Text)
	case ElementNode:
		for _, c := range n.Children {
			c.appendTextDepth(buf, depth+1)
		}
	case CommentNode, DoctypeNode:
		// comments and doctypes contribute no text
		return
	}
}

// maxParseInputBytes is the default upper bound for a single Parse call's input.
// Parse itself does not enforce it: callers are expected to enforce
// MaxBodySize at the load seam (internal/load) before bytes reach ParseDocument.
// ParseWithLimit enforces it for callers that parse untrusted strings directly.
const maxParseInputBytes = 64 * 1024 * 1024

// Parse turns HTML source into a tree with a synthetic root. The source is
// preprocessed before tokenizing: CRLF and CR become LF, and invalid UTF-8
// bytes become U+FFFD. Charset detection happens at the load seam
// (internal/load), and input size is the caller's responsibility there. Use
// ParseDocument for the bytes-to-tree path (it strips the BOM), or
// ParseWithLimit when an explicit bound is needed.
func Parse(source string) (*Node, error) {
	builder := newTreeBuilder()

	scanTokens(source, builder.appendToken, builder.foreignCDATAAllowed)
	builder.finish()
	applySelectedContent(builder.root)

	return builder.root, nil
}

// ParseWithLimit turns HTML source into a tree, rejecting input larger than
// maxBytes. A non-positive maxBytes uses maxParseInputBytes. The underlying
// Parse does not cap input size.
func ParseWithLimit(source string, maxBytes int) (*Node, error) {
	if maxBytes <= 0 {
		maxBytes = maxParseInputBytes
	}

	if len(source) > maxBytes {
		return nil, errors.New("html: source exceeds byte limit")
	}

	return Parse(source)
}

// ParseDocument turns raw document bytes into a tree with a synthetic root,
// stripping a leading UTF-8 BOM (mirroring load.IsHTML). Only UTF-8/ASCII
// sources are supported; the charset rule is enforced at the load seam. Size
// enforcement follows the caller; see ParseWithLimit.
func ParseDocument(body []byte) (*Node, error) {
	s := strings.TrimPrefix(string(body), "\ufeff") // BOM, mirroring load.IsHTML

	return Parse(s)
}

// ParseDocumentWithLimit turns raw document bytes into a tree with an explicit
// byte bound on the source after BOM stripping.
func ParseDocumentWithLimit(body []byte, maxBytes int) (*Node, error) {
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	if maxBytes <= 0 {
		maxBytes = maxParseInputBytes
	}
	if len(body) > maxBytes {
		return nil, errors.New("html: source exceeds byte limit")
	}
	return Parse(string(body))
}

// tokenKind discriminates token types.
type tokenKind int

const (
	tokDoctype tokenKind = iota
	tokStart
	tokEnd
	tokText
	tokComment
)

type token struct {
	kind        tokenKind
	data        string
	attrs       []string // interleaved name, value
	selfClosing bool
	doctype     Doctype
}

// tokenSink consumes one scanned HTML token.
type tokenSink func(token)

// tokenize collects tokens for tokenizer-level tests. Parse uses scanTokens
// directly so a document does not retain a whole token slice before tree build.
func tokenize(src string) ([]token, error) {
	toks := make([]token, 0, strings.Count(src, "<")+1)

	scanTokens(src, func(tok token) {
		toks = append(toks, tok)
	}, nil)

	return toks, nil
}

// preprocessInput applies the input stream preprocessing rules: CRLF and CR
// become LF, and invalid UTF-8 byte sequences become U+FFFD. NUL bytes stay
// in the stream; the tokenizer handles them per state.
func preprocessInput(src string) string {
	if strings.IndexByte(src, '\r') < 0 && utf8.ValidString(src) {
		return src
	}

	var b strings.Builder

	b.Grow(len(src))

	for i := 0; i < len(src); {
		c := src[i]

		switch {
		case c == '\r':
			b.WriteByte('\n')

			i++
			if i < len(src) && src[i] == '\n' {
				i++
			}
		case c < utf8.RuneSelf:
			b.WriteByte(c)

			i++
		default:
			r, size := utf8.DecodeRuneInString(src[i:])
			if r == utf8.RuneError && size == 1 {
				b.WriteRune(utf8.RuneError)

				i++

				continue
			}

			b.WriteString(src[i : i+size])

			i += size
		}
	}

	return b.String()
}

// nulReplacement is U+FFFD, the substitute for a literal NUL byte in token
// states that replace it (comments, tag and attribute names and values, raw
// text).
const nulReplacement = "\uFFFD"

// replaceNUL maps every literal NUL byte in s to U+FFFD.
func replaceNUL(s string) string {
	if strings.IndexByte(s, 0) < 0 {
		return s
	}

	return strings.ReplaceAll(s, "\x00", nulReplacement)
}

// cdataPolicy reports whether the tokenizer may enter the CDATA section state
// at the current position. The tree builder answers from the adjusted current
// node: CDATA is allowed only when it is a non-HTML element. A nil policy
// disables CDATA handling, which is the tokenizer-level default.
type cdataPolicy func() bool

// scanTokens preprocesses raw HTML and emits each token as soon as it is
// recognized. Malformed input follows the spec's recovery rules: unfinished
// comments, tags, declarations, and quoted attribute values produce tokens
// or drop cleanly instead of failing the parse.
func scanTokens(src string, emit tokenSink, allowCDATA cdataPolicy) {
	src = preprocessInput(src)

	pos := 0
	srcLen := len(src)

	for pos < srcLen {
		if src[pos] != '<' {
			span := strings.IndexByte(src[pos:], '<')
			if span < 0 {
				span = srcLen - pos
			}

			emit(token{kind: tokText, data: UnescapeEntities(src[pos : pos+span])}) //nolint:exhaustruct
			pos += span

			continue
		}

		if pos+1 >= srcLen {
			emit(token{kind: tokText, data: "<"}) //nolint:exhaustruct

			break
		}

		var next int

		switch {
		case src[pos+1] == '!':
			next = scanBang(src, pos, emit, allowCDATA)
		case src[pos+1] == '/':
			next = scanEndTag(src, pos, emit)
		case src[pos+1] == '?':
			next = scanBogusComment(src, pos+2, "?", emit)
		case isASCIILetter(src[pos+1]):
			next = scanStartTag(src, pos, emit)
		default:
			emit(token{kind: tokText, data: "<"}) //nolint:exhaustruct

			next = pos + 1
		}

		pos = next
	}
}

// cdataOpen is the exact, case-sensitive opener of a CDATA section.
const cdataOpen = "<![CDATA["

// scanBang tokenizes a '!' construct at pos: comment, CDATA section in
// foreign content, doctype, or a bogus declaration that becomes a comment.
func scanBang(src string, pos int, emit tokenSink, allowCDATA cdataPolicy) int {
	if strings.HasPrefix(src[pos:], "<!--") {
		return scanComment(src, pos, emit)
	}

	if strings.HasPrefix(src[pos:], cdataOpen) && allowCDATA != nil && allowCDATA() {
		return scanCDATA(src, pos, emit)
	}

	if len(src)-pos >= len("<!doctype") && strings.EqualFold(src[pos:pos+len("<!doctype")], "<!doctype") {
		return scanDoctype(src, pos, emit)
	}

	return scanBogusComment(src, pos+2, "", emit)
}

// scanCDATA consumes a CDATA section starting at the '<![CDATA[' opener and
// emits its content as one text token. A run of ']' characters shorter than
// the ']]>' terminator stays in the content; at EOF the gathered content is
// emitted (eof-in-cdata recovery). NUL bytes stay in the stream; the tree
// builder substitutes U+FFFD in foreign text.
func scanCDATA(src string, pos int, emit tokenSink) int {
	start := pos + len(cdataOpen)

	for i := start; i < len(src); {
		if src[i] != ']' {
			i++

			continue
		}

		j := i
		for j < len(src) && src[j] == ']' {
			j++
		}

		if j < len(src) && src[j] == '>' && j-i >= 2 {
			if text := src[start : j-2]; text != "" {
				emit(token{kind: tokText, data: text}) //nolint:exhaustruct
			}

			return j + 1
		}

		i = j
	}

	if text := src[start:]; text != "" {
		emit(token{kind: tokText, data: text}) //nolint:exhaustruct
	}

	return len(src)
}

// scanBogusComment consumes a bogus comment from src[from:] up to '>' or EOF,
// prefixing the initial data that the caller already consumed.
func scanBogusComment(src string, from int, initial string, emit tokenSink) int {
	end := strings.IndexByte(src[from:], '>')
	if end < 0 {
		emit(token{kind: tokComment, data: replaceNUL(initial + src[from:])}) //nolint:exhaustruct

		return len(src)
	}

	emit(token{kind: tokComment, data: replaceNUL(initial + src[from:from+end])}) //nolint:exhaustruct

	return from + end + 1
}

// Comment states, in the order the WHATWG tokenizer defines them.
type commentState int

const (
	commentStartState commentState = iota
	commentStartDashState
	commentStateData
	commentEndDashState
	commentEndState
	commentEndBangState
)

// scanComment tokenizes a comment starting at '<!--', including the spec's
// unfinished-comment recovery: at EOF the comment token is emitted with the
// data gathered so far.
func scanComment(src string, pos int, emit tokenSink) int {
	var b strings.Builder

	i := pos + len("<!--")
	state := commentStartState

	for i < len(src) {
		c := src[i]

		switch state {
		case commentStartState:
			switch c {
			case '-':
				state = commentStartDashState

				i++
			case '>':
				emit(token{kind: tokComment, data: b.String()}) //nolint:exhaustruct

				return i + 1
			default:
				state = commentStateData
			}
		case commentStartDashState:
			switch c {
			case '-':
				state = commentEndState

				i++
			case '>':
				emit(token{kind: tokComment, data: b.String()}) //nolint:exhaustruct

				return i + 1
			default:
				b.WriteByte('-')

				state = commentStateData
			}
		case commentStateData:
			switch c {
			case '-':
				state = commentEndDashState

				i++
			case 0:
				b.WriteString(nulReplacement)

				i++
			default:
				b.WriteByte(c)

				i++
			}
		case commentEndDashState:
			if c == '-' {
				state = commentEndState

				i++
			} else {
				b.WriteByte('-')

				state = commentStateData
			}
		case commentEndState:
			switch c {
			case '>':
				emit(token{kind: tokComment, data: b.String()}) //nolint:exhaustruct

				return i + 1
			case '!':
				state = commentEndBangState

				i++
			case '-':
				b.WriteByte('-')

				i++
			default:
				b.WriteString("--")

				state = commentStateData
			}
		case commentEndBangState:
			switch c {
			case '-':
				b.WriteString("--!")

				state = commentEndDashState

				i++
			case '>':
				emit(token{kind: tokComment, data: b.String()}) //nolint:exhaustruct

				return i + 1
			default:
				b.WriteString("--!")

				state = commentStateData
			}
		}
	}

	emit(token{kind: tokComment, data: b.String()}) //nolint:exhaustruct

	return len(src)
}

// scanEndTag tokenizes a closing tag at pos. Malformed input follows the
// spec's recovery: a non-letter name becomes a bogus comment, EOF inside the
// tag drops the token, and a stray "</" stays text.
func scanEndTag(src string, pos int, emit tokenSink) int {
	i := pos + 2
	if i >= len(src) {
		emit(token{kind: tokText, data: "</"}) //nolint:exhaustruct

		return len(src)
	}

	if !isASCIILetter(src[i]) {
		if src[i] == '>' {
			return i + 1 // missing end tag name: no token
		}

		return scanBogusComment(src, i, "", emit)
	}

	j := i

	for j < len(src) && !isWhitespace(src[j]) && src[j] != '/' && src[j] != '>' {
		j++
	}

	name := strings.ToLower(replaceNUL(src[i:j]))
	if j >= len(src) {
		return len(src) // EOF in tag name: drop the token
	}

	if src[j] == '>' {
		emit(token{kind: tokEnd, data: name}) //nolint:exhaustruct

		return j + 1
	}

	if src[j] == '/' && j+1 < len(src) && src[j+1] == '>' {
		emit(token{kind: tokEnd, data: name}) //nolint:exhaustruct

		return j + 2 // end tag with trailing solidus
	}

	// Attributes on end tags are parsed and ignored.
	_, _, next, ok := scanTagAttributes(src, j)
	if !ok {
		return len(src)
	}

	emit(token{kind: tokEnd, data: name}) //nolint:exhaustruct

	return next
}

// Tag attribute states, in the order the WHATWG tokenizer defines them.
type tagState int

const (
	tagStateBeforeAttrName tagState = iota
	tagStateAttrName
	tagStateAfterAttrName
	tagStateBeforeAttrValue
	tagStateAttrValueDouble
	tagStateAttrValueSingle
	tagStateAttrValueUnquoted
	tagStateAfterAttrValueQuoted
	tagStateSelfClosing
)

// scanTagAttributes parses the attribute part of a tag starting at i, which
// must point at whitespace, '/', or '>'. It returns the interleaved
// name/value pairs, the self-closing flag, and the index after the closing
// '>'. ok is false when the tag runs into EOF before closing.
func scanTagAttributes(src string, i int) (attrs []string, selfClosing bool, next int, ok bool) {
	var (
		nameBuf  strings.Builder
		valueBuf strings.Builder
	)

	state := tagStateBeforeAttrName

	for i < len(src) {
		c := src[i]

		switch state {
		case tagStateBeforeAttrName:
			switch {
			case isWhitespace(c):
				i++
			case c == '/':
				state = tagStateSelfClosing

				i++
			case c == '>':
				return attrs, selfClosing, i + 1, true
			case c == '=':
				nameBuf.WriteByte('=')

				state = tagStateAttrName

				i++
			default:
				nameBuf.Reset()

				state = tagStateAttrName
			}
		case tagStateAttrName:
			switch {
			case isWhitespace(c):
				state = tagStateAfterAttrName

				i++
			case c == '/' || c == '>':
				state = tagStateAfterAttrName
			case c == '=':
				state = tagStateBeforeAttrValue

				i++
			default:
				if nameBuf.Len() < maxAttributeNameBytes {
					appendNULReplaced(&nameBuf, c)
				}

				i++
			}
		case tagStateAfterAttrName:
			switch {
			case isWhitespace(c):
				i++
			case c == '/':
				attrs = appendCappedAttr(attrs, strings.ToLower(nameBuf.String()), "")
				nameBuf.Reset()

				state = tagStateSelfClosing

				i++
			case c == '=':
				state = tagStateBeforeAttrValue

				i++
			case c == '>':
				attrs = appendCappedAttr(attrs, strings.ToLower(nameBuf.String()), "")

				return attrs, selfClosing, i + 1, true
			default:
				attrs = appendCappedAttr(attrs, strings.ToLower(nameBuf.String()), "")
				nameBuf.Reset()

				state = tagStateAttrName
			}
		case tagStateBeforeAttrValue:
			switch {
			case isWhitespace(c):
				i++
			case c == '"':
				state = tagStateAttrValueDouble

				i++
			case c == '\'':
				state = tagStateAttrValueSingle

				i++
			case c == '>':
				attrs = appendCappedAttr(attrs, strings.ToLower(nameBuf.String()), "")

				return attrs, selfClosing, i + 1, true
			default:
				state = tagStateAttrValueUnquoted
			}
		case tagStateAttrValueDouble, tagStateAttrValueSingle:
			quote := byte('"')
			if state == tagStateAttrValueSingle {
				quote = '\''
			}

			switch {
			case c == quote:
				state = tagStateAfterAttrValueQuoted

				i++
			case c == 0:
				if valueBuf.Len()+len(nulReplacement) <= maxAttributeValueBytes {
					valueBuf.WriteString(nulReplacement)
				}

				i++
			case c == '&':
				decoded, consumed := decodeAttributeReference(src, i)
				if valueBuf.Len()+len(decoded) <= maxAttributeValueBytes {
					valueBuf.WriteString(decoded)
				}

				i += consumed
			default:
				if valueBuf.Len() < maxAttributeValueBytes {
					valueBuf.WriteByte(c)
				}

				i++
			}
		case tagStateAttrValueUnquoted:
			switch {
			case isWhitespace(c):
				attrs = appendCappedAttr(attrs, strings.ToLower(nameBuf.String()), valueBuf.String())
				nameBuf.Reset()
				valueBuf.Reset()

				state = tagStateBeforeAttrName

				i++
			case c == '>':
				attrs = appendCappedAttr(attrs, strings.ToLower(nameBuf.String()), valueBuf.String())

				return attrs, selfClosing, i + 1, true
			case c == 0:
				if valueBuf.Len()+len(nulReplacement) <= maxAttributeValueBytes {
					valueBuf.WriteString(nulReplacement)
				}

				i++
			case c == '&':
				decoded, consumed := decodeAttributeReference(src, i)
				if valueBuf.Len()+len(decoded) <= maxAttributeValueBytes {
					valueBuf.WriteString(decoded)
				}

				i += consumed
			default:
				if valueBuf.Len() < maxAttributeValueBytes {
					valueBuf.WriteByte(c)
				}

				i++
			}
		case tagStateAfterAttrValueQuoted:
			switch {
			case isWhitespace(c):
				attrs = appendCappedAttr(attrs, strings.ToLower(nameBuf.String()), valueBuf.String())
				nameBuf.Reset()
				valueBuf.Reset()

				state = tagStateBeforeAttrName

				i++
			case c == '/':
				attrs = appendCappedAttr(attrs, strings.ToLower(nameBuf.String()), valueBuf.String())
				nameBuf.Reset()
				valueBuf.Reset()

				state = tagStateSelfClosing

				i++
			case c == '>':
				attrs = appendCappedAttr(attrs, strings.ToLower(nameBuf.String()), valueBuf.String())

				return attrs, selfClosing, i + 1, true
			default:
				attrs = appendCappedAttr(attrs, strings.ToLower(nameBuf.String()), valueBuf.String())
				nameBuf.Reset()
				valueBuf.Reset()

				state = tagStateBeforeAttrName
			}
		case tagStateSelfClosing:
			if c == '>' {
				return attrs, true, i + 1, true
			}

			state = tagStateBeforeAttrName
		}
	}

	return nil, false, len(src), false
}

// appendNULReplaced appends c to buf, mapping a literal NUL byte to U+FFFD.
func appendNULReplaced(buf *strings.Builder, c byte) {
	if c == 0 {
		buf.WriteString(nulReplacement)

		return
	}

	buf.WriteByte(c)
}

// decodeAttributeReference decodes the character reference at the '&' at
// src[i] with the attribute-context rules. It returns the decoded text and
// the number of source bytes consumed; an ambiguous or historically flushed
// reference decodes to a bare "&".
func decodeAttributeReference(src string, i int) (string, int) {
	if decoded, consumed, ok := decodeCharRefAt(src, i, true); ok {
		return decoded, consumed
	}

	return "&", 1
}

// scanStartTag tokenizes a start tag at pos, including the raw-text content
// of script/style/title/textarea and the other raw-text elements up to their
// closing tag.
func scanStartTag(src string, pos int, emit tokenSink) int {
	i := pos + 1
	nameStart := i

	for i < len(src) && !isWhitespace(src[i]) && src[i] != '/' && src[i] != '>' {
		i++
	}

	if i >= len(src) {
		return len(src) // EOF in tag name: drop the token
	}

	name := strings.ToLower(replaceNUL(src[nameStart:i]))

	attrs, selfClosing, next, ok := scanTagAttributes(src, i)
	if !ok {
		return len(src) // EOF inside the tag: drop the token
	}

	emit(token{kind: tokStart, data: name, attrs: attrs, selfClosing: selfClosing})

	// A raw-text element starts its raw content even with a self-closing
	// flag: the flag is a parse error that the tree builder ignores for HTML
	// elements.
	if mode, raw := rawTextMode(name); raw {
		var (
			text   string
			after  int
			closed bool
		)

		if mode == textScript {
			text, after, closed = scanScriptData(src, next, name)
		} else {
			text, after, closed = scanRawText(src, next, name)
		}

		if mode == textRCDATA {
			text = UnescapeEntities(replaceNUL(text))
		} else {
			text = replaceNUL(text)
		}

		if text != "" {
			emit(token{kind: tokText, data: text}) //nolint:exhaustruct
		}

		if closed {
			emit(token{kind: tokEnd, data: name}) //nolint:exhaustruct

			return after
		}

		return len(src)
	}

	if name == "plaintext" {
		if rest := replaceNUL(src[next:]); rest != "" {
			emit(token{kind: tokText, data: rest}) //nolint:exhaustruct
		}

		return len(src)
	}

	return next
}

// textMode distinguishes the tokenizer states an element's content is parsed
// in: RCDATA decodes character references, RAWTEXT and script data do not,
// and Data is the ordinary tokenizer.
type textMode int

const (
	textRCDATA textMode = iota
	textRAWTEXT
	textScript
	textData
)

// rawTextMode reports the raw-text content mode for name. noscript is
// deliberately absent: it is raw text only with scripting enabled, and this
// engine parses with scripting disabled. noembed is RAWTEXT, matching the
// corpus (upstream tests16 expects its content consumed as raw text).
func rawTextMode(name string) (textMode, bool) {
	switch name {
	case "title", "textarea":
		return textRCDATA, true
	case "style", "xmp", "iframe", "noframes", "noembed":
		return textRAWTEXT, true
	case "script":
		return textScript, true
	default:
		return 0, false
	}
}

// scanRawText consumes raw element content from src[from:] up to the
// appropriate end tag. It returns the raw text, the position after the
// closing tag, and whether the end tag was found. A partial end tag at EOF
// is dropped from the text (eof-in-tag), except when the name is still
// incomplete, which the spec re-emits as text.
func scanRawText(src string, from int, name string) (string, int, bool) {
	var text strings.Builder

	pos := from

	for pos < len(src) {
		lt := strings.IndexByte(src[pos:], '<')
		if lt < 0 {
			break
		}

		lt += pos
		text.WriteString(src[pos:lt])

		if lt+1 >= len(src) || src[lt+1] != '/' {
			text.WriteByte('<')

			pos = lt + 1

			continue
		}

		nameStart := lt + 2
		j := nameStart

		for j < len(src) && isASCIILetter(src[j]) {
			j++
		}

		candidate := src[nameStart:j]

		if candidate == "" || !strings.EqualFold(candidate, name) {
			text.WriteString("</")
			text.WriteString(candidate)

			pos = j

			continue
		}

		if j >= len(src) {
			// EOF in the end tag name: the partial tag becomes text.
			text.WriteString("</")
			text.WriteString(candidate)

			pos = j

			break
		}

		if src[j] == '>' {
			return text.String(), j + 1, true
		}

		if isWhitespace(src[j]) || src[j] == '/' {
			_, _, after, ok := scanTagAttributes(src, j)
			if !ok {
				return text.String(), len(src), false
			}

			return text.String(), after, true
		}

		// e.g. "</xmp<": not an end tag, reconsume at the terminator.
		text.WriteString("</")
		text.WriteString(candidate)

		pos = j
	}

	text.WriteString(src[pos:])

	return text.String(), len(src), false
}

// scriptDataState is one of the WHATWG tokenizer's script data states. The
// escaped and double-escaped states keep "<!--" comments and nested
// "<script>" wrappers inside the script text instead of ending the element
// at the first "</script>".
type scriptDataState int

const (
	scriptDataData scriptDataState = iota
	scriptDataLessThan
	scriptDataEndTagOpen
	scriptDataEndTagName
	scriptDataEscapeStart
	scriptDataEscapeStartDash
	scriptDataEscaped
	scriptDataEscapedDash
	scriptDataEscapedDashDash
	scriptDataEscapedLessThan
	scriptDataEscapedEndTagOpen
	scriptDataEscapedEndTagName
	scriptDataDoubleEscapeStart
	scriptDataDoubleEscaped
	scriptDataDoubleEscapedDash
	scriptDataDoubleEscapedDashDash
	scriptDataDoubleEscapedLessThan
	scriptDataDoubleEscapeEnd
)

// scanScriptData consumes script element content from src[from:] using the
// WHATWG script data states, including the escaped and double-escaped
// variants. It returns the script text, the position after the closing end
// tag, and whether that end tag was found. Only an appropriate end tag for
// name closes the element; every other "</" construct becomes text. NUL
// bytes become U+FFFD. At EOF a partially scanned end tag is flushed as text
// (eof-in-script-html-comment-like-text recovery).
func scanScriptData(src string, from int, name string) (string, int, bool) {
	var (
		text strings.Builder
		buf  strings.Builder // temporary buffer for double-escape detection
		tag  strings.Builder // end tag name being accumulated
	)

	pos := from
	state := scriptDataData

	// flushEndTag emits the withheld "</" and the end-tag name so far.
	flushEndTag := func() {
		text.WriteString("</")
		text.WriteString(tag.String())
	}

	// endTagHandled checks an appropriate end tag at pos: '>' completes it,
	// whitespace or '/' consumes the remaining attributes. It reports
	// whether the script element ends here.
	endTagHandled := func() (int, bool, bool) {
		if !strings.EqualFold(tag.String(), name) {
			return 0, false, false
		}

		switch {
		case src[pos] == '>':
			return pos + 1, true, true
		case isWhitespace(src[pos]) || src[pos] == '/':
			_, _, after, ok := scanTagAttributes(src, pos)
			if !ok {
				// EOF inside the end tag: the partial tag is dropped and the
				// script element stays open (eof-in-tag recovery).
				return len(src), false, true
			}

			return after, true, true
		default:
			return 0, false, false
		}
	}

	for pos < len(src) {
		c := src[pos]

		switch state {
		case scriptDataData:
			switch {
			case c == '<':
				state = scriptDataLessThan
				pos++
			case c == 0:
				text.WriteString(nulReplacement)

				pos++
			default:
				text.WriteByte(c)

				pos++
			}
		case scriptDataLessThan:
			switch c {
			case '/':
				tag.Reset()
				state = scriptDataEndTagOpen

				pos++
			case '!':
				text.WriteString("<!")
				state = scriptDataEscapeStart

				pos++
			default:
				text.WriteByte('<')
				state = scriptDataData // reconsume
			}
		case scriptDataEndTagOpen:
			if isASCIILetter(c) {
				tag.Reset()
				state = scriptDataEndTagName
			} else {
				text.WriteString("</")
				state = scriptDataData // reconsume
			}
		case scriptDataEndTagName:
			if isASCIILetter(c) {
				// The buffer keeps the source case: a rejected or unfinished
				// end tag is flushed back into the text verbatim. The
				// appropriate-end-tag check folds case.
				tag.WriteByte(c)

				pos++
			} else if next, closed, handled := endTagHandled(); handled {
				return text.String(), next, closed
			} else {
				flushEndTag()
				state = scriptDataData // reconsume
			}
		case scriptDataEscapeStart:
			if c == '-' {
				text.WriteByte('-')
				state = scriptDataEscapeStartDash

				pos++
			} else {
				state = scriptDataData // reconsume
			}
		case scriptDataEscapeStartDash:
			if c == '-' {
				text.WriteByte('-')
				state = scriptDataEscapedDashDash

				pos++
			} else {
				state = scriptDataData // reconsume
			}
		case scriptDataEscaped:
			switch {
			case c == '-':
				text.WriteByte('-')
				state = scriptDataEscapedDash

				pos++
			case c == '<':
				state = scriptDataEscapedLessThan

				pos++
			case c == 0:
				text.WriteString(nulReplacement)

				pos++
			default:
				text.WriteByte(c)

				pos++
			}
		case scriptDataEscapedDash:
			switch {
			case c == '-':
				text.WriteByte('-')
				state = scriptDataEscapedDashDash

				pos++
			case c == '<':
				state = scriptDataEscapedLessThan

				pos++
			case c == 0:
				text.WriteString(nulReplacement)

				state = scriptDataEscaped

				pos++
			default:
				text.WriteByte(c)
				state = scriptDataEscaped

				pos++
			}
		case scriptDataEscapedDashDash:
			switch {
			case c == '-':
				text.WriteByte('-')

				pos++
			case c == '<':
				state = scriptDataEscapedLessThan

				pos++
			case c == '>':
				text.WriteByte('>')
				state = scriptDataData

				pos++
			case c == 0:
				text.WriteString(nulReplacement)

				state = scriptDataEscaped

				pos++
			default:
				text.WriteByte(c)
				state = scriptDataEscaped

				pos++
			}
		case scriptDataEscapedLessThan:
			switch {
			case c == '/':
				tag.Reset()
				state = scriptDataEscapedEndTagOpen

				pos++
			case isASCIILetter(c):
				buf.Reset()

				text.WriteByte('<')
				state = scriptDataDoubleEscapeStart // reconsume
			default:
				text.WriteByte('<')
				state = scriptDataEscaped // reconsume
			}
		case scriptDataEscapedEndTagOpen:
			if isASCIILetter(c) {
				tag.Reset()
				state = scriptDataEscapedEndTagName
			} else {
				text.WriteString("</")
				state = scriptDataEscaped // reconsume
			}
		case scriptDataEscapedEndTagName:
			if isASCIILetter(c) {
				tag.WriteByte(c)

				pos++
			} else if next, closed, handled := endTagHandled(); handled {
				return text.String(), next, closed
			} else {
				flushEndTag()
				state = scriptDataEscaped // reconsume
			}
		case scriptDataDoubleEscapeStart:
			switch {
			case isWhitespace(c) || c == '/' || c == '>':
				if buf.String() == "script" {
					state = scriptDataDoubleEscaped
				} else {
					state = scriptDataEscaped
				}

				text.WriteByte(c)

				pos++
			case isASCIILetter(c):
				buf.WriteByte(lowerASCII(c))
				text.WriteByte(c)

				pos++
			default:
				state = scriptDataEscaped // reconsume
			}
		case scriptDataDoubleEscaped:
			switch {
			case c == '-':
				text.WriteByte('-')
				state = scriptDataDoubleEscapedDash

				pos++
			case c == '<':
				text.WriteByte('<')
				state = scriptDataDoubleEscapedLessThan

				pos++
			case c == 0:
				text.WriteString(nulReplacement)

				pos++
			default:
				text.WriteByte(c)

				pos++
			}
		case scriptDataDoubleEscapedDash:
			switch {
			case c == '-':
				text.WriteByte('-')
				state = scriptDataDoubleEscapedDashDash

				pos++
			case c == '<':
				text.WriteByte('<')
				state = scriptDataDoubleEscapedLessThan

				pos++
			case c == 0:
				text.WriteString(nulReplacement)

				state = scriptDataDoubleEscaped

				pos++
			default:
				text.WriteByte(c)
				state = scriptDataDoubleEscaped

				pos++
			}
		case scriptDataDoubleEscapedDashDash:
			switch {
			case c == '-':
				text.WriteByte('-')

				pos++
			case c == '<':
				text.WriteByte('<')
				state = scriptDataDoubleEscapedLessThan

				pos++
			case c == '>':
				text.WriteByte('>')
				state = scriptDataData

				pos++
			case c == 0:
				text.WriteString(nulReplacement)

				state = scriptDataDoubleEscaped

				pos++
			default:
				text.WriteByte(c)
				state = scriptDataDoubleEscaped

				pos++
			}
		case scriptDataDoubleEscapedLessThan:
			if c == '/' {
				buf.Reset()

				text.WriteByte('/')
				state = scriptDataDoubleEscapeEnd

				pos++
			} else {
				state = scriptDataDoubleEscaped // reconsume
			}
		case scriptDataDoubleEscapeEnd:
			switch {
			case isWhitespace(c) || c == '/' || c == '>':
				if buf.String() == "script" {
					state = scriptDataEscaped
				} else {
					state = scriptDataDoubleEscaped
				}

				text.WriteByte(c)

				pos++
			case isASCIILetter(c):
				buf.WriteByte(lowerASCII(c))
				text.WriteByte(c)

				pos++
			default:
				state = scriptDataDoubleEscaped // reconsume
			}
		}
	}

	// EOF recovery: a withheld "<" is emitted, and a partially scanned end
	// tag flushes its "<", "/", and name characters as text.
	switch state {
	case scriptDataLessThan, scriptDataEscapedLessThan:
		text.WriteByte('<')
	case scriptDataEndTagOpen, scriptDataEndTagName,
		scriptDataEscapedEndTagOpen, scriptDataEscapedEndTagName:
		flushEndTag()
	}

	return text.String(), len(src), false
}

// appendCappedAttr appends one attribute unless the per-element count or total
// byte cap has already been reached; otherwise it returns attrs unchanged.
func appendCappedAttr(attrs []string, name, value string) []string {
	if len(attrs)/2 >= maxAttributesPerElement {
		return attrs
	}

	total := 0
	for _, entry := range attrs {
		total += len(entry)
	}

	if total+len(name)+len(value) > maxAttributeBytesPerElement {
		return attrs
	}

	return append(attrs, name, value)
}

// lowerASCII lowercases one ASCII letter byte.
func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}

	return c
}

func isWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
