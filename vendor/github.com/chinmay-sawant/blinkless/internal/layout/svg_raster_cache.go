package layout

import "strconv"

// The restyle path serializes an inline SVG and rasterizes the result on every
// call. A paint animation that repeats a value (the endpoints, a paused state,
// two elements with the same icon) asks for the same document twice, so the
// encoded raster is kept keyed by the serialized SVG. The cache belongs to one
// Result: it is created on the first restyle and dropped with the layout, so it
// cannot outlive the document it describes.

const (
	// svgRasterCacheMaxEntries bounds how many rasters the cache keeps. A
	// paint animation moves through a handful of colors per element, so eight
	// covers the endpoints and the last few frames without pinning a whole
	// animation history.
	svgRasterCacheMaxEntries = 8
	// svgRasterCacheMaxPixels bounds the decoded pixels the cache keeps, not
	// the encoded bytes: a PNG of a flat icon is tiny while its decoded raster
	// is not. Two megapixels is about 8 MB at RGBA.
	svgRasterCacheMaxPixels = 2 << 20
)

// svgRasterEntry is one cached raster: the encoded PNG and its pixel bounds.
type svgRasterEntry struct {
	key  string
	png  []byte
	w, h int
}

// svgRasterCache remembers the raster of each serialized inline SVG, oldest
// entry first. The zero value is an empty cache.
type svgRasterCache struct {
	entries []svgRasterEntry
	pixels  int
	// hits and misses count the lookups. They are diagnostics for tests and a
	// future cost counter; nothing in the paint path reads them.
	hits   int
	misses int
}

// get returns the raster stored for one serialized document. The returned
// slice is shared with the cached entry and must be treated as read-only.
func (cache *svgRasterCache) get(key string) ([]byte, int, int, bool) {
	if cache == nil {
		return nil, 0, 0, false
	}

	for index := range cache.entries {
		if cache.entries[index].key != key {
			continue
		}

		cache.hits++

		return cache.entries[index].png, cache.entries[index].w, cache.entries[index].h, true
	}

	cache.misses++

	return nil, 0, 0, false
}

// put stores one raster. A raster larger than the whole pixel budget is not
// cached: it could never fit and would evict every useful entry first.
func (cache *svgRasterCache) put(key string, png []byte, width, height int) {
	if cache == nil || key == "" || len(png) == 0 || width <= 0 || height <= 0 {
		return
	}

	pixels := width * height
	if pixels > svgRasterCacheMaxPixels {
		return
	}

	if cache.replace(key, png, width, height, pixels) {
		return
	}

	cache.entries = append(cache.entries, svgRasterEntry{
		key: key,
		png: png,
		w:   width,
		h:   height,
	})
	cache.pixels += pixels

	for len(cache.entries) > svgRasterCacheMaxEntries ||
		cache.pixels > svgRasterCacheMaxPixels {
		cache.pixels -= cache.entries[0].w * cache.entries[0].h
		cache.entries[0] = svgRasterEntry{} //nolint:exhaustruct // release the payload
		cache.entries = cache.entries[1:]
	}
}

// replace updates the entry already stored under key, reporting whether one
// was found. Refreshing in place keeps the entry's age, so a repeated paint
// does not keep the entry alive ahead of newer frames.
func (cache *svgRasterCache) replace(key string, png []byte, width, height, pixels int) bool {
	for index := range cache.entries {
		if cache.entries[index].key != key {
			continue
		}

		cache.pixels += pixels - cache.entries[index].w*cache.entries[index].h
		cache.entries[index].png = png
		cache.entries[index].w = width
		cache.entries[index].h = height

		return true
	}

	return false
}

// svgInlineCacheKey keys one inline-SVG raster by the resolution it was
// rasterized at and its serialized document, so equal icons rasterized at
// different maxSide caps never share a raster.
func svgInlineCacheKey(data []byte, maxSide int) string {
	return strconv.Itoa(maxSide) + "\x00" + string(data)
}

// inlineSVGRasterCache returns the engine's inline-SVG raster cache, creating
// it on first use. It lives for one Layout run: repeated identical icons
// rasterize once per run instead of once per occurrence.
func (e *engine) inlineSVGRasterCache() *svgRasterCache {
	if e.svgInlineCache == nil {
		e.svgInlineCache = &svgRasterCache{} //nolint:exhaustruct // empty cache
	}

	return e.svgInlineCache
}

// svgRasterCache returns the SVG raster cache of this layout, creating it on
// first use. The cache dies with the Result.
func (r *Result) svgRasterCache() *svgRasterCache {
	if r == nil {
		return nil
	}

	if r.svgRasters == nil {
		r.svgRasters = &svgRasterCache{} //nolint:exhaustruct // empty cache
	}

	return r.svgRasters
}
