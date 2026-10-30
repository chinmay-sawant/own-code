package html

import (
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/html"
)

// SetAttribute sets an attribute on the element with the given id and
// reports whether the element exists.
//
// The name is matched the way the parser stores attributes: an attribute the
// element already carries keeps its stored spelling, a new one is stored
// lowercased. An empty name changes nothing and reports false.
//
// The change lands on the parsed tree, so the next style cascade and the
// next layout see it.
func (d *Document) SetAttribute(elementID, name, value string) bool {
	node := d.elementByID(elementID)
	if node == nil || name == "" {
		return false
	}

	if node.Attrs == nil {
		node.Attrs = make(map[string]string, 1)
	}

	key := attributeKey(node.Attrs, name)
	node.Attrs[key] = value
	setAttrListValue(node, key, value)

	return true
}

// SetText replaces the element's text content and reports whether the
// element exists.
//
// The text becomes one text node and every child of the element is dropped,
// the way the DOM textContent setter works. An empty text leaves the element
// without children.
//
// The change lands on the parsed tree, so the next style cascade and the
// next layout see it.
func (d *Document) SetText(elementID, text string) bool {
	node := d.elementByID(elementID)
	if node == nil {
		return false
	}

	node.Children = nil

	if text == "" {
		return true
	}

	node.Children = []*html.Node{{
		Type:   html.TextNode,
		Text:   text,
		Parent: node,
	}}

	return true
}

// elementByID returns the element node with the given id, or nil.
func (d *Document) elementByID(elementID string) *html.Node {
	if d == nil || d.root == nil || elementID == "" {
		return nil
	}

	return d.root.FindFirst(func(candidate *html.Node) bool {
		return candidate.Type == html.ElementNode && candidate.Attribute("id") == elementID
	})
}

// attributeKey returns the map key for name: the key the element already
// stores for that attribute, or the lowercased name for a new attribute.
func attributeKey(attrs map[string]string, name string) string {
	for key := range attrs {
		if strings.EqualFold(key, name) {
			return key
		}
	}

	return strings.ToLower(name)
}

// setAttrListValue records the value in the ordered attribute list, so the
// list and the map carry the same value.
func setAttrListValue(node *html.Node, key, value string) {
	for i := range node.AttrList {
		if strings.EqualFold(node.AttrList[i].Name, key) {
			node.AttrList[i].Value = value

			return
		}
	}

	node.AttrList = append(node.AttrList, html.Attr{
		Namespace: "",
		Name:      key,
		Value:     value,
	})
}
