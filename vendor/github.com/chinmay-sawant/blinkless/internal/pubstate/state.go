// Package pubstate lets the public html, css, and layout packages share
// one parsed tree without putting internal types in their exported signatures.
// Each public package registers a reader while it initializes. The other
// packages call the reader. There is no map, so a document can be
// garbage-collected.
package pubstate

import (
	"github.com/chinmay-sawant/blinkless/internal/css"
	pdf "github.com/chinmay-sawant/blinkless/internal/fonts"
	"github.com/chinmay-sawant/blinkless/internal/html"
)

// Styled is the cascade result the layout package reads.
type Styled struct {
	Root     *html.Node
	Mode     html.DocumentMode
	Sheets   []*css.Stylesheet
	Registry *pdf.Registry
	Media    string
	WidthPx  int
	HeightPx int
	State    css.MatchState
}

// The readers are package state because html, css, and layout cannot share
// unexported fields without an import cycle, and an exported field would
// leak the internal node type. The html and css packages install them while
// their packages initialize, before any caller can parse, apply, or read, so
// Root and StyledOf are safe to call from several goroutines without a lock.
//
//nolint:gochecknoglobals // registration point for the public engine packages
var (
	readRoot   func(any) *html.Node
	readStyled func(any) (Styled, bool)
)

// RegisterRoot stores the reader for a public HTML document. The html package
// calls it from a package-level initializer, which runs before any caller can
// parse. Calling it later can race with a concurrent Root.
// The return value is a token so the call can initialize a blank variable.
func RegisterRoot(fn func(any) *html.Node) struct{} {
	readRoot = fn

	return struct{}{}
}

// Root returns the parsed tree for a public HTML document.
// A nil document or a missing reader returns nil.
func Root(doc any) *html.Node {
	if doc == nil || readRoot == nil {
		return nil
	}

	return readRoot(doc)
}

// RegisterStyled stores the reader for a public CSS document. The css package
// calls it from a package-level initializer, which runs before any caller can
// apply. Calling it later can race with a concurrent StyledOf.
// The return value is a token so the call can initialize a blank variable.
func RegisterStyled(fn func(any) (Styled, bool)) struct{} {
	readStyled = fn

	return struct{}{}
}

// StyledOf returns the cascade result for a public CSS document.
func StyledOf(doc any) (Styled, bool) {
	if doc == nil || readStyled == nil {
		return Styled{}, false //nolint:exhaustruct // no styled document is registered
	}

	return readStyled(doc)
}
