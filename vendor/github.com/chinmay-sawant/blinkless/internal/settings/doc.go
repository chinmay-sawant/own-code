// Package settings implements the wkhtmltopdf-compatible settings model and
// its dotted-name Set/Get surface.
//
// Engine callers use the typed structs directly: css.Apply builds a
// DefaultPdfGlobal for sheet collection and maps its media option onto
// MediaType; load reads LoadGlobal and LoadPage; convert.prepare reads the
// page and web fields; bindings/wasm reads the load policy for inline data:
// URLs. ClonePdfGlobal, ClonePdfObject, and CloneImageGlobal return
// independent snapshots when a caller needs one.
//
// # Policy A (settings honesty)
//
// Only options with a live engine consumer (convert, load, or css) get typed
// fields and dedicated setters. Inert wkhtml keys (dpi, javascript, plugins,
// log-level, js-delay, user-style-sheet, produce-forms, …) may be accepted
// into Ignored map[string]string for script compatibility, but must not
// reappear as typed stubs without a live consumer.
//
// Dual storage is collapsed where possible: Grayscale is the sole color bit
// (Set("colormode") and Set("grayscale") both write it); page geometry is
// PageSize name + Size width/height (mm); DumpOutline / DumpDefaultTOCXSL
// live on PdfGlobal.
package settings
