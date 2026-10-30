// Package html parses one HTML document for the CSS and layout packages.
//
// Parse keeps the engine's own tree. css.Apply reads that tree, and
// layout.DisplayList places it. markup.Parse returns a detached copy for inspection
// and is not the document those packages accept.
//
// Document exposes no doctype or document-mode accessor. css.Apply reads the
// mode off the engine root and carries it in the internal pubstate handoff
// (internal/pubstate/state.go, Styled.Mode). No public caller consumes a
// document mode today, so there is no accessor to keep in sync;
// markup.Node.Mode covers detached inspection.
//
// This package does not write a PDF.
package html
