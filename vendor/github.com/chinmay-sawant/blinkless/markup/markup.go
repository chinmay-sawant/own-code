// Package markup parses UTF-8 HTML into a detached copy of the tree for
// inspection. css.Apply and layout work on the engine's own tree instead
// (package html); markup.Parse is the read-only view.
//
// The copy carries the parse results a caller can inspect: SVG and MathML
// namespaces, adjusted foreign attribute names, the structured doctype on
// TypeDoctype nodes, the document mode on the root, and a template element's
// content fragment on Contents.
//
// MathML subtrees are copied with NamespaceMathML and render downstream as
// generic elements. Nothing in the pipeline marks them unsupported; layout
// dispatches on element name only.
package markup

import (
	"fmt"

	"github.com/chinmay-sawant/blinkless/internal/html"
)

// Type classifies a parsed node.
type Type int

const (
	// TypeElement is a tag. Name is lowercase.
	TypeElement Type = iota + 1
	// TypeText is character data.
	TypeText
	// TypeComment is an HTML comment.
	TypeComment
	// TypeDoctype is the doctype declaration.
	TypeDoctype
)

// Namespace identifies the DOM namespace an element belongs to. The zero
// value is the HTML namespace.
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
// attributes and "xlink", "xml", or "xmlns" for adjusted foreign attributes.
type Attr struct {
	Namespace string
	Name      string
	Value     string
}

// Doctype is the structured content of one DOCTYPE declaration. The raw
// declaration text stays on Node.Text.
type Doctype struct {
	Name        string
	PublicID    string
	HasPublicID bool
	SystemID    string
	HasSystemID bool
	ForceQuirks bool
}

// DocumentMode is the quirks mode derived from the doctype. The zero value is
// NoQuirks.
type DocumentMode uint8

const (
	// NoQuirks is standards mode.
	NoQuirks DocumentMode = iota
	// LimitedQuirks is almost standards mode.
	LimitedQuirks
	// Quirks is quirks mode.
	Quirks
)

// String names the mode for tests and diagnostics.
func (m DocumentMode) String() string {
	switch m {
	case NoQuirks:
		return "no-quirks"
	case LimitedQuirks:
		return "limited-quirks"
	case Quirks:
		return "quirks"
	default:
		return "unknown"
	}
}

// Node is one parsed HTML node. Children are in document order.
// There is no parent pointer. Attribute keys are lowercased for HTML elements
// and adjusted (for example viewBox) for foreign elements.
//
// Doctype is set on TypeDoctype nodes and Mode on the root node. Contents
// holds a template element's content fragment, mirroring the engine tree:
// it stays out of Children.
type Node struct {
	Type      Type
	Name      string
	Namespace Namespace
	Attrs     map[string]string
	AttrList  []Attr
	Text      string
	Doctype   Doctype
	Mode      DocumentMode // document mode; only meaningful on the root
	Children  []*Node
	Contents  []*Node
}

// Parse parses UTF-8 HTML and returns an owned copy of the tree.
// A nil source is parsed as an empty document.
func Parse(source []byte) (*Node, error) {
	root, err := html.ParseDocument(source)
	if err != nil {
		return nil, fmt.Errorf("markup: parse: %w", err)
	}

	return copyNode(root), nil
}

func copyNode(node *html.Node) *Node {
	if node == nil {
		return nil
	}

	kind, ok := copyType(node.Type)
	if !ok {
		return nil
	}

	children := copyChildren(node.Children)
	contents := copyChildren(node.Contents)

	return &Node{
		Type:      kind,
		Name:      node.Name,
		Namespace: copyNamespace(node.Namespace),
		Attrs:     copyAttrs(node.Attrs),
		AttrList:  copyAttrList(node.AttrList),
		Text:      node.Text,
		Doctype:   copyDoctype(node.Doctype),
		Mode:      copyMode(node.Mode),
		Children:  children,
		Contents:  contents,
	}
}

func copyChildren(nodes []*html.Node) []*Node {
	out := make([]*Node, 0, len(nodes))

	for _, node := range nodes {
		copied := copyNode(node)
		if copied != nil {
			out = append(out, copied)
		}
	}

	return out
}

func copyDoctype(doctype html.Doctype) Doctype {
	return Doctype{
		Name:        doctype.Name,
		PublicID:    doctype.PublicID,
		HasPublicID: doctype.HasPublicID,
		SystemID:    doctype.SystemID,
		HasSystemID: doctype.HasSystemID,
		ForceQuirks: doctype.ForceQuirks,
	}
}

func copyMode(mode html.DocumentMode) DocumentMode {
	switch mode {
	case html.NoQuirks:
		return NoQuirks
	case html.LimitedQuirks:
		return LimitedQuirks
	case html.Quirks:
		return Quirks
	default:
		return NoQuirks
	}
}

func copyNamespace(ns html.Namespace) Namespace {
	switch ns {
	case html.NamespaceSVG:
		return NamespaceSVG
	case html.NamespaceMathML:
		return NamespaceMathML
	case html.NamespaceHTML:
		return NamespaceHTML
	default:
		return NamespaceHTML
	}
}

func copyAttrList(attrs []html.Attr) []Attr {
	if len(attrs) == 0 {
		return nil
	}

	out := make([]Attr, 0, len(attrs))
	for _, attr := range attrs {
		out = append(out, Attr{Namespace: attr.Namespace, Name: attr.Name, Value: attr.Value})
	}

	return out
}

func copyType(kind html.NodeType) (Type, bool) {
	switch kind {
	case html.ElementNode:
		return TypeElement, true
	case html.TextNode:
		return TypeText, true
	case html.CommentNode:
		return TypeComment, true
	case html.DoctypeNode:
		return TypeDoctype, true
	case html.NodeUnknown:
		return 0, false
	default:
		return 0, false
	}
}

func copyAttrs(attrs map[string]string) map[string]string {
	if len(attrs) == 0 {
		return nil
	}

	out := make(map[string]string, len(attrs))

	for key, value := range attrs {
		out[key] = value
	}

	return out
}
