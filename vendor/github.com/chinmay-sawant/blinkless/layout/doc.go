// Package layout places a css.Document and returns a drawing list.
// DisplayList uses the HTML tree and the stylesheets from css.Apply. It does
// not paint a page bitmap and it does not write a PDF. A print layout is
// fitted to the canvas width when the content draws past it, mirroring the
// print renderers' smart shrink.
//
// Operation coordinates are canvas points, y down. Text Y is the baseline.
// Boxes are CSS pixels, y down, origin at the top left. An image operation
// keeps its encoded bytes. When orientation or a clip cannot stay in those
// bytes, that one operation is re-encoded as a PNG. That is the bitmap
// fallback. The page itself is still the drawing list.
package layout
