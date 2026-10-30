package fonts

// maxAdvUnitsCacheEntries bounds the per-Font advance-units cache. A trade
// price table reuses a dozen distinct runes; a large CJK document can exceed
// the cap, in which case new runes compute without storing so the map cannot
// grow with the document.
const maxAdvUnitsCacheEntries = 2048

// initialAdvUnitsCacheEntries sizes a fresh per-Font advance-units map. A
// trade price table reuses a dozen distinct runes; the map grows no further
// than maxAdvUnitsCacheEntries.
const initialAdvUnitsCacheEntries = 64

// cachedAdvanceUnits returns the cached advance width in font units for r.
func (f *Font) cachedAdvanceUnits(runeValue rune) (float64, bool) {
	f.advMu.RLock()
	defer f.advMu.RUnlock()

	if len(f.advUnits) == 0 {
		return 0, false
	}

	units, ok := f.advUnits[runeValue]

	return units, ok
}

// storeAdvanceUnits records the advance width in font units for r unless the
// cache is full.
func (f *Font) storeAdvanceUnits(runeValue rune, units float64) {
	f.advMu.Lock()
	defer f.advMu.Unlock()

	if f.advUnits == nil {
		f.advUnits = make(map[rune]float64, initialAdvUnitsCacheEntries)
	}

	if len(f.advUnits) >= maxAdvUnitsCacheEntries {
		return
	}

	f.advUnits[runeValue] = units
}

// advanceUnitsUncached computes the advance width in font units for r with a
// single cmap lookup, matching Advance.
func (f *Font) advanceUnitsUncached(runeValue rune) float64 {
	if len(f.advance) == 0 {
		return 0
	}

	g := f.GlyphID(runeValue)
	if int(g) >= len(f.advance) {
		return float64(f.advance[0])
	}

	return float64(f.advance[g])
}

// advanceUnits returns the advance width in font units for r, serving
// repeated runes from the per-Font cache.
func (f *Font) advanceUnits(runeValue rune) float64 {
	if units, ok := f.cachedAdvanceUnits(runeValue); ok {
		return units
	}

	units := f.advanceUnitsUncached(runeValue)
	f.storeAdvanceUnits(runeValue, units)

	return units
}
