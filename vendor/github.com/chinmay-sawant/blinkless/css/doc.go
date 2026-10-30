// Package css parses stylesheets and applies them to an html.Document.
//
// Apply collects style elements from that document, plus any sheets passed
// in Options.Extra. Linked and imported sheets are routed through the load
// package; images are not fetched. The layout package places the resulting
// document.
//
// This package does not write a PDF.
package css
