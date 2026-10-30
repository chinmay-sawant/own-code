// tree rules; same conventions as html.go
//
//nolint:all // SVG/MathML namespace, adjustment tables, and foreign-content
package html

import "strings"

// Namespace identifies the DOM namespace an element belongs to. The zero
// value is the HTML namespace, so zero-constructed nodes keep working.
type Namespace uint8

const (
	// NamespaceHTML is the namespace of ordinary HTML elements.
	NamespaceHTML Namespace = iota
	// NamespaceSVG is the SVG namespace.
	NamespaceSVG
	// NamespaceMathML is the MathML namespace.
	NamespaceMathML
)

// Attr is one attribute with its namespace. Namespace is empty for ordinary
// attributes and "xlink", "xml", or "xmlns" for attributes adjusted under the
// standard's foreign-attribute rules.
type Attr struct {
	Namespace string
	Name      string
	Value     string
}

// svgElementNames maps lowercased SVG tag names to their adjusted DOM names
// (the standard's "adjust SVG tag names" table).
var svgElementNames = map[string]string{
	"altglyph":            "altGlyph",
	"altglyphdef":         "altGlyphDef",
	"altglyphitem":        "altGlyphItem",
	"animatecolor":        "animateColor",
	"animatemotion":       "animateMotion",
	"animatetransform":    "animateTransform",
	"clippath":            "clipPath",
	"feblend":             "feBlend",
	"fecolormatrix":       "feColorMatrix",
	"fecomponenttransfer": "feComponentTransfer",
	"fecomposite":         "feComposite",
	"feconvolvematrix":    "feConvolveMatrix",
	"fediffuselighting":   "feDiffuseLighting",
	"fedisplacementmap":   "feDisplacementMap",
	"fedistantlight":      "feDistantLight",
	"fedropshadow":        "feDropShadow",
	"feflood":             "feFlood",
	"fefunca":             "feFuncA",
	"fefuncb":             "feFuncB",
	"fefuncg":             "feFuncG",
	"fefuncr":             "feFuncR",
	"fegaussianblur":      "feGaussianBlur",
	"feimage":             "feImage",
	"femerge":             "feMerge",
	"femergenode":         "feMergeNode",
	"femorphology":        "feMorphology",
	"feoffset":            "feOffset",
	"fepointlight":        "fePointLight",
	"fespecularlighting":  "feSpecularLighting",
	"fespotlight":         "feSpotLight",
	"fetile":              "feTile",
	"feturbulence":        "feTurbulence",
	"foreignobject":       "foreignObject",
	"glyphref":            "glyphRef",
	"lineargradient":      "linearGradient",
	"radialgradient":      "radialGradient",
	"textpath":            "textPath",
}

// svgAttributeNames maps lowercased SVG attribute names to their adjusted
// names (the standard's "adjust SVG attributes" table).
var svgAttributeNames = map[string]string{
	"attributename":       "attributeName",
	"attributetype":       "attributeType",
	"basefrequency":       "baseFrequency",
	"baseprofile":         "baseProfile",
	"calcmode":            "calcMode",
	"clippathunits":       "clipPathUnits",
	"diffuseconstant":     "diffuseConstant",
	"edgemode":            "edgeMode",
	"filterunits":         "filterUnits",
	"glyphref":            "glyphRef",
	"gradienttransform":   "gradientTransform",
	"gradientunits":       "gradientUnits",
	"kernelmatrix":        "kernelMatrix",
	"kernelunitlength":    "kernelUnitLength",
	"keypoints":           "keyPoints",
	"keysplines":          "keySplines",
	"keytimes":            "keyTimes",
	"lengthadjust":        "lengthAdjust",
	"limitingconeangle":   "limitingConeAngle",
	"markerheight":        "markerHeight",
	"markerunits":         "markerUnits",
	"markerwidth":         "markerWidth",
	"maskcontentunits":    "maskContentUnits",
	"maskunits":           "maskUnits",
	"numoctaves":          "numOctaves",
	"pathlength":          "pathLength",
	"patterncontentunits": "patternContentUnits",
	"patterntransform":    "patternTransform",
	"patternunits":        "patternUnits",
	"pointsatx":           "pointsAtX",
	"pointsaty":           "pointsAtY",
	"pointsatz":           "pointsAtZ",
	"preservealpha":       "preserveAlpha",
	"preserveaspectratio": "preserveAspectRatio",
	"primitiveunits":      "primitiveUnits",
	"refx":                "refX",
	"refy":                "refY",
	"repeatcount":         "repeatCount",
	"repeatdur":           "repeatDur",
	"requiredextensions":  "requiredExtensions",
	"requiredfeatures":    "requiredFeatures",
	"specularconstant":    "specularConstant",
	"specularexponent":    "specularExponent",
	"spreadmethod":        "spreadMethod",
	"startoffset":         "startOffset",
	"stddeviation":        "stdDeviation",
	"stitchtiles":         "stitchTiles",
	"surfacescale":        "surfaceScale",
	"systemlanguage":      "systemLanguage",
	"tablevalues":         "tableValues",
	"targetx":             "targetX",
	"targety":             "targetY",
	"textlength":          "textLength",
	"viewbox":             "viewBox",
	"viewtarget":          "viewTarget",
	"xchannelselector":    "xChannelSelector",
	"ychannelselector":    "yChannelSelector",
	"zoomandpan":          "zoomAndPan",
}

// foreignAttr is one entry of the standard's "adjust foreign attributes"
// table: the namespace prefix and the local name.
type foreignAttr struct {
	prefix string
	local  string
}

// foreignAttributeNames maps lowercased qualified attribute names to their
// namespace prefix and local name.
var foreignAttributeNames = map[string]foreignAttr{
	"xlink:actuate": {"xlink", "actuate"},
	"xlink:arcrole": {"xlink", "arcrole"},
	"xlink:href":    {"xlink", "href"},
	"xlink:role":    {"xlink", "role"},
	"xlink:show":    {"xlink", "show"},
	"xlink:title":   {"xlink", "title"},
	"xlink:type":    {"xlink", "type"},
	"xml:lang":      {"xml", "lang"},
	"xml:space":     {"xml", "space"},
	"xmlns":         {"xmlns", "xmlns"},
	"xmlns:xlink":   {"xmlns", "xlink"},
}

// adjustForeignElementName applies the namespace-specific element-name
// adjustment for a foreign element.
func adjustForeignElementName(ns Namespace, name string) string {
	if ns == NamespaceSVG {
		if adjusted, ok := svgElementNames[name]; ok {
			return adjusted
		}
	}

	return name
}

// adjustAttributeName resolves one lowercased token attribute into the map
// key and structured Attr stored on the node. HTML attributes keep their
// name; SVG and MathML attributes are case-adjusted; foreign attributes gain
// a namespace prefix.
func adjustAttributeName(ns Namespace, name, value string) (string, Attr) {
	switch ns {
	case NamespaceSVG:
		if adjusted, ok := svgAttributeNames[name]; ok {
			name = adjusted
		}
	case NamespaceMathML:
		if name == "definitionurl" {
			name = "definitionURL"
		}
	case NamespaceHTML:
	}

	if ns == NamespaceHTML {
		return name, Attr{Name: name, Value: value}
	}

	if foreign, ok := foreignAttributeNames[name]; ok {
		return foreign.prefix + ":" + foreign.local, Attr{
			Namespace: foreign.prefix,
			Name:      foreign.local,
			Value:     value,
		}
	}

	return name, Attr{Name: name, Value: value}
}

// isMathMLTextIntegrationPoint reports whether n is a MathML text integration
// point: mi, mo, mn, ms, or mtext.
func isMathMLTextIntegrationPoint(n *Node) bool {
	if n == nil || n.Namespace != NamespaceMathML {
		return false
	}

	switch n.Name {
	case "mi", "mo", "mn", "ms", "mtext":
		return true
	}

	return false
}

// isHTMLIntegrationPoint reports whether n is an HTML integration point:
// SVG foreignObject, desc, or title, or MathML annotation-xml with a text/html
// or application/xhtml+xml encoding attribute.
func isHTMLIntegrationPoint(n *Node) bool {
	if n == nil {
		return false
	}

	switch n.Namespace {
	case NamespaceSVG:
		switch n.Name {
		case "foreignObject", "desc", "title":
			return true
		}
	case NamespaceMathML:
		if n.Name == "annotation-xml" {
			// The encoding value must match exactly (ASCII case-insensitive):
			// " text/html " with spaces is not an HTML integration point.
			encoding := strings.ToLower(n.Attribute("encoding"))

			return encoding == "text/html" || encoding == "application/xhtml+xml"
		}
	case NamespaceHTML:
	}

	return false
}

// foreignTextUnsetsFrameset reports whether a foreign-content text run holds
// a character that clears the frameset-ok flag: NUL bytes and whitespace do
// not, any other character does.
func foreignTextUnsetsFrameset(data string) bool {
	for i := 0; i < len(data); i++ {
		c := data[i]
		if c != 0 && !isWhitespace(c) {
			return true
		}
	}

	return false
}

// foreignBreakoutTags are the start tags that pop foreign content and are
// reprocessed under the HTML rules.
var foreignBreakoutTags = map[string]bool{
	"b": true, "big": true, "blockquote": true, "body": true, "br": true,
	"center": true, "code": true, "dd": true, "div": true, "dl": true,
	"dt": true, "em": true, "embed": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "head": true, "hr": true, "i": true,
	"img": true, "li": true, "listing": true, "menu": true, "meta": true,
	"nobr": true, "ol": true, "p": true, "pre": true, "ruby": true, "s": true,
	"small": true, "span": true, "strong": true, "strike": true, "sub": true,
	"sup": true, "table": true, "tt": true, "u": true, "ul": true, "var": true,
}

// isForeignBreakout reports whether a start tag under foreign content must be
// reprocessed with the HTML rules. font is a breakout only with a color, face,
// or size attribute.
func isForeignBreakout(tokItem *token) bool {
	if foreignBreakoutTags[tokItem.data] {
		return true
	}

	if tokItem.data == "font" {
		for i := 0; i+1 < len(tokItem.attrs); i += 2 {
			switch tokItem.attrs[i] {
			case "color", "face", "size":
				return true
			}
		}
	}

	return false
}

// --- foreign content dispatch ---

// foreignToken applies the foreign-content rules when the current node is in
// a foreign namespace. It reports whether the token was consumed. A breakout
// token is popped back to HTML or integration-point content and reprocessed
// under the current HTML mode.
func (b *treeBuilder) foreignToken(tokItem *token) bool {
	switch tokItem.kind {
	case tokText:
		if !b.currentIsForeignText() {
			return false
		}

		b.appendTextToken(tokItem.data)

		// The foreign-content character rule clears frameset-ok for any
		// non-whitespace character; NUL is replaced with U+FFFD and does not.
		if foreignTextUnsetsFrameset(tokItem.data) {
			b.framesetOK = false
		}

		return true
	case tokDoctype:
		return b.currentIsForeign()
	case tokStart:
		if !b.inForeignStartContext(tokItem) {
			return false
		}

		if isForeignBreakout(tokItem) {
			b.popForeignBreakout()

			return false
		}

		b.insertForeignElement(tokItem.data, b.adjustedCurrent().Namespace, tokItem)

		// The tokenizer consumes raw-text content by tag name. In foreign
		// content these elements are ordinary, so the captured run must be
		// re-tokenized in the Data state.
		if _, raw := rawTextMode(tokItem.data); raw || tokItem.data == "plaintext" {
			b.reparseRawText = true
		}

		return true
	case tokEnd:
		if !b.currentIsForeign() {
			return false
		}

		if tokItem.data == "br" || tokItem.data == "p" {
			b.popForeignBreakout()

			return false
		}

		return b.closeForeignElement(tokItem.data)
	case tokComment:
		return false
	}

	return false
}

func (b *treeBuilder) inForeignStartContext(tokItem *token) bool {
	top := b.adjustedCurrent()
	if top.Namespace == NamespaceHTML {
		return false
	}

	if isMathMLTextIntegrationPoint(top) && tokItem.data != "mglyph" && tokItem.data != "malignmark" {
		return false
	}

	if top.Namespace == NamespaceMathML && top.Name == "annotation-xml" && tokItem.data == "svg" {
		return false
	}

	return !isHTMLIntegrationPoint(top)
}

func (b *treeBuilder) currentIsForeign() bool {
	return b.adjustedCurrent().Namespace != NamespaceHTML
}

// foreignCDATAAllowed reports whether the tokenizer may treat "<![CDATA[" as
// a CDATA section at the current point: the standard requires an adjusted
// current node that is not an element in the HTML namespace. The tokenizer
// consults this policy synchronously, after every earlier token has been
// processed, so the tree state is the same one the standard's tokenizer sees.
func (b *treeBuilder) foreignCDATAAllowed() bool {
	node := b.adjustedCurrent()

	return node != nil && node.Namespace != NamespaceHTML
}

func (b *treeBuilder) currentIsForeignText() bool {
	top := b.adjustedCurrent()
	if top.Namespace == NamespaceHTML {
		return false
	}

	return !isMathMLTextIntegrationPoint(top) && !isHTMLIntegrationPoint(top)
}

func (b *treeBuilder) popForeignBreakout() {
	for len(b.stack) > 1 {
		top := b.top()
		if top.Namespace == NamespaceHTML || isMathMLTextIntegrationPoint(top) || isHTMLIntegrationPoint(top) {
			return
		}

		b.stack = b.stack[:len(b.stack)-1]
	}
}
