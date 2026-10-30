package prepare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
	pdf "github.com/chinmay-sawant/blinkless/internal/fonts"
	"github.com/chinmay-sawant/blinkless/internal/html"
	"github.com/chinmay-sawant/blinkless/internal/line"
	"github.com/chinmay-sawant/blinkless/internal/load"
)

const (
	maxStylesheetRules    = 1_000_000
	maxImportDepth        = 8
	maxRemoteAssetFetches = 64
)

var (
	errStylesheetLimit      = errors.New("convert: stylesheet rule limit exceeded")
	errStylesheetFetchLimit = errors.New("convert: stylesheet fetch limit exceeded")
)

// SheetOptions configures stylesheet viewport/media gating and warning labels.
type SheetOptions struct {
	ViewportW, ViewportH float64
	MediaType            string
	ObjectIndex          int
	// PageBoxViewport, when set, replaces the gating viewport after inline
	// <style> sheets are parsed. Callers use it to apply inline @page geometry
	// before linked and imported media queries are gated, so size features
	// see the final page box the cascade will use. nil keeps ViewportW/H.
	PageBoxViewport func(inline []*css.Stylesheet) (width, height float64)
	// Cache, when non-nil, reuses sheets parsed by an earlier collection of
	// the same tree. Nil parses and fetches everything again.
	Cache *SheetCache `exhaustruct:"optional"`
}

type sheetCollector struct {
	resources   load.ResourceContext
	opts        SheetOptions
	log         io.Writer
	sheets      []*css.Stylesheet
	seen        map[string]struct{}
	rules       int
	visits      uint32
	fetches     int
	fetchCapped bool
	err         error
	// inlineSheets / inlineByNode cache the <style> parses from the gating
	// pre-pass so the main walk does not parse inline styles twice.
	inlineSheets []*css.Stylesheet
	inlineByNode map[*html.Node]*css.Stylesheet
}

//nolint:wsl,lll // stylesheet collection flow
func collectSheets(ctx context.Context, resources load.ResourceContext, root *html.Node, opts SheetOptions, log io.Writer) ([]*css.Stylesheet, error) {
	collector := sheetCollector{ //nolint:exhaustruct // empty result state
		resources: resources, opts: opts, log: log, seen: make(map[string]struct{}),
	}
	if root != nil {
		if collector.opts.PageBoxViewport != nil {
			collector.preparseInlineStyles(ctx, root)

			if collector.err == nil {
				collector.opts.ViewportW, collector.opts.ViewportH = collector.opts.PageBoxViewport(collector.inlineSheets)
			}
		}

		root.Walk(func(node *html.Node) { collector.visit(ctx, node) })
	}

	const softRuleWarn = 25_000

	if collector.rules >= softRuleWarn {
		collector.warn("large stylesheet volume (%d rules); print may be slow", collector.rules)
	}
	if collector.err != nil {
		return collector.sheets, collector.err
	}

	return collector.sheets, nil
}

func (collector *sheetCollector) visit(ctx context.Context, node *html.Node) {
	if collector.err != nil || node == nil || node.Type != html.ElementNode {
		return
	}

	collector.visits++
	if collector.visits&63 == 0 {
		if err := ctx.Err(); err != nil {
			collector.err = err

			return
		}
	}

	switch node.Name {
	case "style":
		collector.collectStyle(ctx, node)
	case "link":
		collector.collectLink(ctx, node)
	}
}

// preparseInlineStyles parses inline <style> sheets once so the caller can
// derive the stylesheet-gating viewport from inline @page geometry before any
// link or @import media query is evaluated. Invalid sheets are skipped here
// and reported once by the main walk.
func (collector *sheetCollector) preparseInlineStyles(ctx context.Context, root *html.Node) {
	if root == nil {
		return
	}

	collector.inlineByNode = make(map[*html.Node]*css.Stylesheet)

	var visits uint32

	root.Walk(func(node *html.Node) {
		if collector.err != nil || node == nil || node.Type != html.ElementNode || node.Name != "style" {
			return
		}

		visits++
		if visits&63 == 0 {
			if err := ctx.Err(); err != nil {
				collector.err = err

				return
			}
		}

		sheet := collector.opts.Cache.inlineSheet(node)
		if sheet == nil {
			parsed, err := css.Parse(styleText(node))
			if err != nil {
				return
			}

			sheet = parsed
			collector.opts.Cache.noteInline(node, sheet)
		}

		collector.inlineByNode[node] = sheet
		collector.inlineSheets = append(collector.inlineSheets, sheet)
	})
}

//nolint:nlreturn // collector traversal flow
func (collector *sheetCollector) collectStyle(ctx context.Context, node *html.Node) {
	if collector.err != nil {
		return
	}

	sheet := collector.inlineByNode[node]
	if sheet == nil {
		sheet = collector.opts.Cache.inlineSheet(node)
	}

	if sheet == nil {
		parsed, err := css.Parse(styleText(node))
		if err != nil {
			collector.warn("skipping <style>: %v", err)
			return
		}

		sheet = parsed
		collector.opts.Cache.noteInline(node, sheet)
	}

	collector.addWithImports(ctx, sheet, collector.resources.Base(), 0)
}

//nolint:wsl,nlreturn // collector traversal flow
func (collector *sheetCollector) collectLink(ctx context.Context, node *html.Node) {
	if collector.err != nil {
		return
	}
	if err := ctx.Err(); err != nil {
		collector.err = err

		return
	}

	if !linkStylesheet(node, collector.opts.ViewportW, collector.opts.ViewportH, collector.opts.MediaType) {
		return
	}

	if cached := collector.opts.Cache.linkSheet(node); cached.sheet != nil {
		collector.noteSeen(cached.seen)
		collector.addWithImports(ctx, cached.sheet, cached.base, 0)

		return
	}

	// Fetch is bounded per request by the loader's timeout policy
	// (LoadPage.Timeout, or load.DefaultResponseTimeout when unset); ctx
	// carries the caller's overall deadline and cancellation.
	// The shared fetch cap stops a wide set of link and import URLs.
	if !collector.allowStylesheetFetch() {
		return
	}

	resource, err := collector.resources.Fetch(ctx, node.Attribute("href"))
	if err != nil {
		collector.warn("skipping <link href=%q>: %v", node.Attribute("href"), err)
		return
	}

	sheet, err := css.ParseBytes(resource.Body)
	if err != nil {
		collector.warn("skipping <link href=%q>: %v", node.Attribute("href"), err)
		return
	}
	collector.noteSeen(resource.URL)
	collector.opts.Cache.noteLink(node, cachedSheet{
		sheet: sheet,
		base:  resourceBase(resource),
		seen:  resource.URL,
	})
	collector.addWithImports(ctx, sheet, resourceBase(resource), 0)
}

// addWithImports appends imported sheets (recursively) before sheet so @import
// rules precede the importer, matching CSS cascade order.
//
//nolint:wsl // collector import flow
func (collector *sheetCollector) addWithImports(ctx context.Context, sheet *css.Stylesheet, base string, depth int) {
	if sheet == nil || collector.err != nil {
		return
	}
	collector.fetchImports(ctx, sheet, base, depth)
	if collector.err != nil {
		return
	}
	collector.add(sheet)
}

//nolint:wsl,nlreturn // collector import flow
func (collector *sheetCollector) fetchImports(ctx context.Context, sheet *css.Stylesheet, base string, depth int) {
	if collector.err != nil || sheet == nil || len(sheet.Imports) == 0 {
		return
	}
	if depth >= maxImportDepth {
		collector.warn("skipping nested @import: depth exceeds %d", maxImportDepth)
		return
	}
	for index := range sheet.Imports {
		collector.fetchOneImport(ctx, sheet, index, base, depth)
	}
}

func (collector *sheetCollector) fetchOneImport(
	ctx context.Context, sheet *css.Stylesheet, index int, base string, depth int,
) {
	if collector.err != nil {
		return
	}

	if err := ctx.Err(); err != nil {
		collector.err = err

		return
	}

	ref := collector.prepareImportRef(sheet.Imports[index], base)
	if ref == "" {
		return
	}

	key := importKey{sheet: sheet, index: index}
	if cached := collector.opts.Cache.importSheet(key); cached.sheet != nil {
		collector.noteSeen(cached.seen)
		collector.addWithImports(ctx, cached.sheet, cached.base, depth+1)

		return
	}

	imported, sheetBase, seen := collector.loadImportedSheet(ctx, base, ref)
	if imported == nil {
		return
	}

	collector.opts.Cache.noteImport(key, cachedSheet{
		sheet: imported,
		base:  sheetBase,
		seen:  seen,
	})
	collector.addWithImports(ctx, imported, sheetBase, depth+1)
}

func (collector *sheetCollector) prepareImportRef(rule css.ImportRule, base string) string {
	ref := importRef(rule.URL)
	if ref == "" {
		return ""
	}

	if rule.Media != "" && !css.MediaMatches(
		rule.Media,
		collector.opts.MediaType,
		collector.opts.ViewportW,
		collector.opts.ViewportH,
	) {
		return ""
	}

	resolved := resolvedRef(base, ref)
	if collector.seenURL(resolved) {
		return ""
	}

	collector.noteSeen(resolved)

	return ref
}

func (collector *sheetCollector) loadImportedSheet(
	ctx context.Context, base, ref string,
) (*css.Stylesheet, string, string) {
	resource, err := collector.fetchRef(ctx, base, ref)
	if err != nil {
		if !errors.Is(err, errStylesheetFetchLimit) {
			collector.warn("skipping @import %q: %v", ref, err)
		}

		return nil, "", ""
	}

	if resource == nil || resource.Skip {
		collector.warn("skipping @import %q: resource skipped", ref)

		return nil, "", ""
	}

	// A redirect or alias can land on an already-seen stylesheet even when the
	// original ref did not match; treat a different final URL as the cycle
	// key. The resolved ref itself was just marked seen above, so only a
	// different final URL counts here.
	if resolved := resolvedRef(base, ref); resource.URL != resolved && collector.seenURL(resource.URL) {
		collector.warn("skipping @import %q: stylesheet cycle detected via %q", ref, resource.URL)

		return nil, "", ""
	}

	collector.noteSeen(resource.URL)

	sheet, err := css.ParseBytes(resource.Body)
	if err != nil {
		collector.warn("skipping @import %q: %v", ref, err)

		return nil, "", ""
	}

	return sheet, resourceBase(resource), resource.URL
}

// fetchRef loads ref with the same ACL as <link rel=stylesheet>. Relative
// imports resolve against the current sheet base, not the document base.
func (collector *sheetCollector) fetchRef(ctx context.Context, base, ref string) (*load.Resource, error) {
	if !collector.allowStylesheetFetch() {
		return nil, errStylesheetFetchLimit
	}

	resources := collector.resources
	if base != "" && base != resources.Base() {
		if loader := resources.Loader(); loader != nil {
			resources = loader.ForResource(&load.Resource{Base: base}, resources.PageLoad()) //nolint:exhaustruct,lll // base-only resource reference
		}
	}

	resource, err := resources.Fetch(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("fetch imported stylesheet %q: %w", ref, err)
	}

	return resource, nil
}

// allowStylesheetFetch counts one link or import fetch against the cap.
// Further fetches are skipped after the cap, with a single warning.
func (collector *sheetCollector) allowStylesheetFetch() bool {
	if collector.fetches >= maxRemoteAssetFetches {
		if !collector.fetchCapped {
			collector.warn("skipping further stylesheet fetches: limit %d", maxRemoteAssetFetches)
			collector.fetchCapped = true
		}

		return false
	}

	collector.fetches++

	return true
}

func (collector *sheetCollector) seenURL(raw string) bool {
	if raw == "" || collector.seen == nil {
		return false
	}

	_, ok := collector.seen[raw]

	return ok
}

func (collector *sheetCollector) noteSeen(raw string) {
	if raw == "" {
		return
	}

	if collector.seen == nil {
		collector.seen = make(map[string]struct{})
	}

	collector.seen[raw] = struct{}{}
}

func resourceBase(resource *load.Resource) string {
	if resource == nil {
		return ""
	}

	if resource.Base != "" {
		return resource.Base
	}

	return resource.URL
}

// hasFontExtension reports whether uri's path ends with ext, ignoring any
// query string or fragment so a cache-busting suffix cannot hide the type.
func hasFontExtension(uri, ext string) bool {
	path := uri
	if parsed, err := url.Parse(uri); err == nil && parsed.Path != "" {
		path = parsed.Path
	}

	return strings.HasSuffix(strings.ToLower(path), ext)
}

func importRef(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 4 && strings.EqualFold(raw[:4], "url(") && strings.HasSuffix(raw, ")") {
		raw = strings.TrimSpace(raw[4 : len(raw)-1])
	}

	return strings.Trim(raw, `"' `)
}

func resolvedRef(base, ref string) string {
	parsed, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return strings.TrimSpace(ref)
	}

	if parsed.IsAbs() || strings.TrimSpace(base) == "" {
		return parsed.String()
	}

	baseURL, err := url.Parse(base)
	if err != nil {
		return parsed.String()
	}

	return baseURL.ResolveReference(parsed).String()
}

//nolint:wsl,nlreturn // collector accumulation flow
func (collector *sheetCollector) add(sheet *css.Stylesheet) {
	if sheet == nil {
		return
	}

	collector.rules += len(sheet.Rules)
	if collector.rules > maxStylesheetRules {
		collector.err = fmt.Errorf("%w: got %d, limit %d", errStylesheetLimit, collector.rules, maxStylesheetRules)
		return
	}
	collector.sheets = append(collector.sheets, sheet)
}

//nolint:wsl,nlreturn // warning routing flow
func (collector *sheetCollector) warn(format string, args ...any) {
	if collector.log == nil {
		return
	}
	if collector.opts.ObjectIndex > 0 {
		line.Emit(collector.log, line.Warn, "object %d: "+format, append([]any{collector.opts.ObjectIndex}, args...)...)
		return
	}
	line.Emit(collector.log, line.Warn, format, args...)
}

//nolint:wsl,nlreturn // text extraction flow
func styleText(node *html.Node) string {
	var out strings.Builder
	for _, child := range node.Children {
		if child.Type == html.TextNode {
			out.WriteString(child.Text)
		}
	}
	return out.String()
}

//nolint:wsl,nlreturn // media predicate
func linkStylesheet(node *html.Node, viewportW, viewportH float64, mediaType string) bool {
	if node.Name != "link" || node.Attribute("href") == "" {
		return false
	}
	if !strings.Contains(strings.ToLower(node.Attribute("rel")), "stylesheet") {
		return false
	}
	media := node.Attribute("media")
	return media == "" || css.MediaMatches(media, mediaType, viewportW, viewportH)
}

//nolint:wsl,nlreturn // font-face collection flow
func mergeFontFaces(
	ctx context.Context,
	resources load.ResourceContext,
	registry *pdf.Registry,
	sheets []*css.Stylesheet,
	idx int,
	log io.Writer,
) *pdf.Registry {
	fetches := 0
	fetchCapped := false

	for _, sheet := range sheets {
		if ctx != nil && ctx.Err() != nil {
			return registry
		}
		if sheet == nil {
			continue
		}
		for _, face := range sheet.FontFaces {
			if ctx != nil && ctx.Err() != nil {
				return registry
			}
			registry = mergeFontFace(ctx, resources, registry, face, idx, log, &fetches, &fetchCapped)
		}
	}
	return registry
}

//nolint:wsl,nlreturn // font-face collection flow
func mergeFontFace(
	ctx context.Context,
	resources load.ResourceContext,
	registry *pdf.Registry,
	face css.FontFace,
	idx int,
	log io.Writer,
	fetches *int,
	fetchCapped *bool,
) *pdf.Registry {
	for _, uri := range css.FontFaceURLs(face.Src) {
		if fontSourceFetched(uri) && !allowFontFetch(fetches, fetchCapped, idx, log) {
			continue
		}
		font, ok := fetchFontFace(ctx, resources, uri, idx, log)
		if !ok {
			continue
		}
		if face.Family != "" {
			font.PostScriptName = strings.ReplaceAll(face.Family, " ", "")
		}
		if registry == nil {
			registry = pdf.NewRegistry()
		}
		registry.AddFont(font)
		if face.Family != "" {
			registry.AddFamilyAlias(face.Family, font)
		}
	}
	return registry
}

// fontBodyWithinCap applies the font size cap before Decode so a hostile
// payload cannot force decompression or table parsing before the size is
// checked. The cap mirrors internal/fonts.maxFontBytes, tightened by the
// loader's body limit when one is configured.
func fontBodyWithinCap(resources load.ResourceContext, body []byte) bool {
	const maxFontSrcBytes = int64(32 << 20)

	maxBytes := maxFontSrcBytes
	if loader := resources.Loader(); loader != nil && loader.MaxBodySize > 0 && loader.MaxBodySize < maxBytes {
		maxBytes = loader.MaxBodySize
	}

	return int64(len(body)) <= maxBytes
}

// fontSourceFetched reports whether uri is loaded with resources.Fetch.
// data:, WOFF2, and EOT sources are refused locally and do not count.
func fontSourceFetched(uri string) bool {
	lower := strings.ToLower(strings.TrimSpace(uri))
	if strings.HasPrefix(lower, "data:") {
		return false
	}

	return !hasFontExtension(lower, ".woff2") && !hasFontExtension(lower, ".eot")
}

// allowFontFetch counts one remote font fetch. Nil fetches means a direct
// single-URL call with no collection cap.
func allowFontFetch(fetches *int, capped *bool, idx int, log io.Writer) bool {
	if fetches == nil {
		return true
	}

	if *fetches >= maxRemoteAssetFetches {
		if capped != nil && !*capped {
			line.Emit(log, line.Warn,
				"object %d: skipping further font fetches after %d",
				idx, maxRemoteAssetFetches)

			*capped = true
		}

		return false
	}

	*fetches++

	return true
}

//nolint:wsl,nlreturn // font-face collection flow
func fetchFontFace(
	ctx context.Context,
	resources load.ResourceContext,
	uri string,
	idx int,
	log io.Writer,
) (*pdf.Font, bool) {
	// The remote-font policy excludes WOFF2/EOT and data: sources, including
	// the subset of WOFF2 supported by the standalone font decoder.
	if !fontSourceFetched(uri) {
		line.Emit(log, line.Warn,
			"object %d: @font-face src %q skipped (WOFF2/EOT/data: unsupported; WOFF1/TTF/OTF only)", idx, uri)
		return nil, false
	}

	resource, err := resources.Fetch(ctx, uri)
	if err != nil {
		line.Emit(log, line.Warn, "object %d: @font-face src %q: %v", idx, uri, err)
		return nil, false
	}

	if !fontBodyWithinCap(resources, resource.Body) {
		line.Emit(log, line.Warn,
			"object %d: @font-face src %q: %d bytes exceeds the font byte limit",
			idx, uri, len(resource.Body))
		return nil, false
	}

	if len(resource.Body) >= 4 && string(resource.Body[:4]) == "wOF2" {
		line.Emit(log, line.Warn, "object %d: @font-face src %q skipped (WOFF2 font source)", idx, uri)

		return nil, false
	}

	body, err := pdf.Decode(resource.Body)
	if err != nil {
		line.Emit(log, line.Warn, "object %d: @font-face src %q: %v", idx, uri, err)
		return nil, false
	}

	font, err := pdf.ParseFontBytes(body)
	if err != nil {
		line.Emit(log, line.Warn, "object %d: @font-face src %q: %v", idx, uri, err)
		return nil, false
	}
	return font, true
}
