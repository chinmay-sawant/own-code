package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Tab width and indentation. The viewer historically expanded every tab to
// four spaces; the file-backed editor paths (load, commit, typing, display
// columns) now honor a per-file resolved width, while transient multi-file
// surfaces (diff hunks, merge rows, git output, content search) keep the
// four-space default because their text is not one file.

const (
	defaultTabSize    = 4
	indentOverrideCap = sessionMaxPaths
	ecMaxDepth        = 24
)

// indentOverride pins one file's indentation. Size 0 means auto and Spaces
// nil means auto; sanitize drops fully-auto entries.
type indentOverride struct {
	Size   int   `json:"size,omitempty"`
	Spaces *bool `json:"spaces,omitempty"`
}

// validTabSize sanitizes a width: 2, 4, or 8 pass, anything else is 4.
func validTabSize(n int) int {
	switch n {
	case 2, 4, 8:
		return n
	default:
		return defaultTabSize
	}
}

// uiTabSize is the global width: 0 means auto (per-file detection), a valid
// size is fixed, anything else falls back to 4.
func (a *App) uiTabSize() int {
	if a.tabSize == 0 {
		return 0
	}

	return validTabSize(a.tabSize)
}

// uiInsertSpaces is the global style, defaulting to spaces.
func (a *App) uiInsertSpaces() bool {
	if !a.insertSet {
		return true
	}

	return a.insertSpaces
}

// uiUseEditorConfig reports whether .editorconfig files are honored,
// defaulting to on.
func (a *App) uiUseEditorConfig() bool {
	if !a.ecSet {
		return true
	}

	return a.useEditorConfig
}

// expandTabsSized replaces tabs with size spaces and strips a trailing CR.
// Tabs make column alignment impossible, so the viewer never prints one.
func expandTabsSized(text string, size int) string {
	if size <= 0 {
		size = defaultTabSize
	}

	text = strings.TrimSuffix(text, "\r")

	if !strings.ContainsRune(text, '\t') {
		return text
	}

	pad := strings.Repeat(" ", size)

	var b strings.Builder
	b.Grow(len(text) + size)

	for _, r := range text {
		if r == '\t' {
			b.WriteString(pad)

			continue
		}

		b.WriteRune(r)
	}

	return b.String()
}

// rawColumnAtSize turns a display column, with tabs expanded to size, back
// into a raw rune column in the buffer line.
func rawColumnAtSize(line string, displayCol, size int) int {
	if size <= 0 {
		size = defaultTabSize
	}

	if displayCol <= 0 {
		return 0
	}

	cells := 0
	for i, r := range []rune(line) {
		width := 1
		if r == '\t' {
			width = size
		}

		if cells+width > displayCol {
			return i
		}

		cells += width
		if cells == displayCol {
			return i + 1
		}
	}

	return len([]rune(line))
}

// displayColumnSize expands the prefix of one buffer line with size.
func displayColumnSize(text string, line, col, size int) int {
	raw := []rune(lineTextAt(text, line))
	if col > len(raw) {
		col = len(raw)
	}

	return len([]rune(expandTabsSized(string(raw[:col]), size)))
}

// resolveIndent returns the width, style, and source for one file.
// Precedence: explicit per-file override, .editorconfig, the global
// default, then improved detectIndent. Source is one of pin, ec, global,
// detect, or default.
func (a *App) resolveIndent(path string, lines []string) (size int, spaces bool, source string) {
	styleSet := false

	if ov, ok := a.indentByFile[path]; ok {
		if ov.Size == 2 || ov.Size == 4 || ov.Size == 8 {
			size = ov.Size
		}

		if ov.Spaces != nil {
			spaces, styleSet = *ov.Spaces, true
		}

		if size != 0 || styleSet {
			source = "pin"
		}
	}

	if a.uiUseEditorConfig() {
		if ec := a.editorConfigFor(path); ec.found {
			if size == 0 && ec.size > 0 {
				size = ec.size
			}

			if !styleSet && ec.hasStyle {
				spaces, styleSet = ec.spaces, true
			}

			if (size != 0 || styleSet) && source == "" {
				source = "ec"
			}
		}
	}

	if size == 0 || !styleSet {
		if global := a.uiTabSize(); global != 0 && size == 0 {
			size = global
			if source == "" {
				source = "global"
			}
		}

		if a.insertSet && !styleSet {
			spaces, styleSet = a.uiInsertSpaces(), true
			if source == "" {
				source = "global"
			}
		}
	}

	if size == 0 || !styleSet {
		detected := detectIndent(lines)

		if size == 0 {
			if n, ok := parseSpacesLabel(detected); ok {
				size = n
			} else {
				size = defaultTabSize
			}
		}

		if !styleSet {
			spaces = detected != "Tabs"
		}

		if source == "" {
			if detected == "None" {
				source = "default"
			} else {
				source = "detect"
			}
		}
	}

	return validTabSize(size), spaces, source
}

// indentSizeFor is the resolved width for one file, used by the editor
// paths that need a number without the full triple.
func (a *App) indentSizeFor(path string) int {
	var lines []string
	if doc := a.docs[path]; doc != nil {
		lines = doc.Lines
	}

	size, _, _ := a.resolveIndent(path, lines)

	return size
}

// indentLabelFor is the status-bar and details label for one file, with a
// source marker only for pinned and editorconfig results so plain detection
// keeps its historic bare form.
func (a *App) indentLabelFor(path string) string {
	var lines []string
	if doc := a.docs[path]; doc != nil {
		lines = doc.Lines
	}

	size, spaces, source := a.resolveIndent(path, lines)

	var base string
	if !spaces {
		base = "Tabs"
		if size != defaultTabSize {
			base = fmt.Sprintf("Tabs: %d", size)
		}
	} else {
		base = fmt.Sprintf("Spaces: %d", size)
	}

	switch source {
	case "pin":
		return base + " · pin"
	case "ec":
		return base + " · ec"
	default:
		return base
	}
}

// parseSpacesLabel reads the width out of a "Spaces: N" label.
func parseSpacesLabel(label string) (int, bool) {
	rest, ok := strings.CutPrefix(label, "Spaces: ")
	if !ok {
		return 0, false
	}

	n, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil || n <= 0 {
		return 0, false
	}

	return n, true
}

// sanitizeIndentOverrides drops bad keys, invalid entries, and caps the map.
func sanitizeIndentOverrides(overrides map[string]indentOverride) map[string]indentOverride {
	if len(overrides) == 0 {
		return nil
	}

	out := make(map[string]indentOverride, min(len(overrides), indentOverrideCap))

	for _, path := range sortedKeys(overrides) {
		if len(out) >= indentOverrideCap {
			break
		}

		clean, ok := cleanSessionPath(path)
		if !ok {
			continue
		}

		ov := overrides[path]
		if ov.Spaces != nil {
			size := 0
			if ov.Size == 2 || ov.Size == 4 || ov.Size == 8 {
				size = ov.Size
			}
			out[clean] = indentOverride{Size: size, Spaces: ov.Spaces}
			continue
		}

		if ov.Size == 2 || ov.Size == 4 || ov.Size == 8 {
			out[clean] = indentOverride{Size: ov.Size}
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// setIndentOverride pins one file. A nil spaces with size 0 clears the pin.
func (a *App) setIndentOverride(path string, ov indentOverride) bool {
	clean, ok := cleanSessionPath(path)
	if !ok {
		return false
	}

	if ov.Size == 0 && ov.Spaces == nil {
		delete(a.indentByFile, clean)
		if len(a.indentByFile) == 0 {
			a.indentByFile = nil
		}

		return true
	}

	if a.indentByFile == nil {
		a.indentByFile = map[string]indentOverride{}
	}

	a.indentByFile[clean] = ov

	for len(a.indentByFile) > indentOverrideCap {
		for _, path := range a.groupPaths() {
			delete(a.indentByFile, path)
			break
		}
	}

	return true
}

// reexpandDoc re-reads every open, unmodified document with its resolved tab
// width and retokenizes it. Dirty files and the live edit buffer keep their
// text.
func (a *App) reexpandDoc() {
	for path, doc := range a.docs {
		if doc == nil || a.isDirty(path) {
			continue
		}

		if a.editing && a.edit != nil && a.edit.Path == path {
			continue
		}

		fresh := a.ws.openDoc(path)
		if fresh.Err != "" || fresh.Binary {
			continue
		}

		a.docs[path] = fresh
	}

	a.refresh()
}

// detectIndent reports the indentation the file actually uses. It scores
// tab-indented lines against space-indented lines: tabs win only when they
// lead the majority of indented lines, so a stray tab no longer flips a
// spaces file. The width is the mode of the leading-space deltas across
// consecutive indented lines, falling back to the smallest run. A file with
// no indented lines reports None.
func detectIndent(lines []string) string {
	tabs, spaces := 0, 0
	deltas := map[int]int{}
	smallest := 0
	prev := -1

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		n := 0
		tabbed := false

		for _, r := range line {
			if r == '\t' {
				tabbed = true
				break
			}

			if r != ' ' {
				break
			}

			n++
		}

		if tabbed {
			tabs++
			prev = -1

			continue
		}

		if n == 0 {
			prev = -1

			continue
		}

		spaces++

		if n > 0 && (smallest == 0 || n < smallest) {
			smallest = n
		}

		if prev >= 0 && n != prev {
			if d := n - prev; d > 0 {
				deltas[d]++
			} else if d = prev - n; d > 0 && d < n {
				deltas[d]++
			}
		}

		prev = n
	}

	if tabs > 0 && tabs >= spaces {
		return "Tabs"
	}

	if spaces > 0 {
		width, best := 0, 0
		keys := make([]int, 0, len(deltas))
		for d := range deltas {
			keys = append(keys, d)
		}
		sort.Ints(keys)

		for _, d := range keys {
			if deltas[d] > best {
				width, best = d, deltas[d]
			}
		}

		if width > 0 {
			return fmt.Sprintf("Spaces: %d", width)
		}

		if smallest > 0 {
			return fmt.Sprintf("Spaces: %d", smallest)
		}
	}

	if tabs > 0 {
		return "Tabs"
	}

	return "None"
}

// editorConfig is the resolved .editorconfig result for one file.
type editorConfig struct {
	found    bool
	spaces   bool
	hasStyle bool
	size     int
	trim     *bool
}

// ecFile is one parsed .editorconfig with its mtime, so unchanged files are
// never re-read.
type ecFile struct {
	mtime    int64
	root     bool
	sections []ecSection
}

// ecSection is one glob section with its lowercase keys.
type ecSection struct {
	pattern string
	keys    map[string]string
}

// editorConfigFor resolves the .editorconfig chain for one workspace file,
// from the workspace root down to the file's directory. The sample
// workspace has no disk and resolves empty. Results are mtime-cached per
// config file.
func (a *App) editorConfigFor(path string) editorConfig {
	var out editorConfig
	if a.ws == nil || a.ws.Sample || a.ws.Root == "" {
		return out
	}

	rel := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
		return out
	}

	dir := filepath.ToSlash(filepath.Dir(rel))
	var chain []string
	depth := 0
	for {
		chain = append(chain, dir)
		if dir == "." || dir == "/" || dir == "" || depth >= ecMaxDepth {
			break
		}
		dir = filepath.ToSlash(filepath.Dir(dir))
		depth++
	}

	// Walk root-most first so closer files win per key.
	for i := len(chain) - 1; i >= 0; i-- {
		cfgPath := filepath.Join(a.ws.Root, filepath.FromSlash(chain[i]), ".editorconfig")
		parsed, ok := a.loadEditorConfig(cfgPath)
		if !ok {
			continue
		}

		out.found = true

		for _, sec := range parsed.sections {
			if !ecMatch(sec.pattern, chain[i], rel) {
				continue
			}

			if style, ok := sec.keys["indent_style"]; ok {
				switch style {
				case "space":
					out.spaces, out.hasStyle = true, true
				case "tab":
					out.spaces, out.hasStyle = false, true
				}
			}

			if raw, ok := sec.keys["indent_size"]; ok {
				if raw == "tab" {
					if w, ok := sec.keys["tab_width"]; ok {
						if n, err := strconv.Atoi(w); err == nil && n > 0 {
							out.size = validTabSize(n)
						}
					}
				} else if n, err := strconv.Atoi(raw); err == nil && n > 0 {
					out.size = validTabSize(n)
				}
			} else if raw, ok := sec.keys["tab_width"]; ok {
				if out.size == 0 {
					if n, err := strconv.Atoi(raw); err == nil && n > 0 {
						out.size = validTabSize(n)
					}
				}
			}

			if raw, ok := sec.keys["trim_trailing_whitespace"]; ok {
				switch raw {
				case "true":
					yes := true
					out.trim = &yes
				case "false":
					no := false
					out.trim = &no
				}
			}
		}

		if parsed.root {
			break
		}
	}

	return out
}

// loadEditorConfig parses one .editorconfig, reusing the cache when the
// mtime matches. Missing or unreadable files report false without caching
// an error, so a later save is picked up.
func (a *App) loadEditorConfig(path string) (*ecFile, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil, false
	}

	mtime := info.ModTime().UnixNano()

	if a.ecCache != nil {
		if cached, ok := a.ecCache[path]; ok && cached.mtime == mtime {
			return &cached, true
		}
	}

	raw, err := os.ReadFile(path) //nolint:gosec // the path is always a .editorconfig under the workspace root
	if err != nil {
		return nil, false
	}

	parsed := &ecFile{mtime: mtime}
	parsed.sections, parsed.root = parseEditorConfig(string(raw))

	if a.ecCache == nil {
		a.ecCache = map[string]ecFile{}
	}
	a.ecCache[path] = *parsed

	return parsed, true
}

// parseEditorConfig splits raw text into sections with lowercase keys.
func parseEditorConfig(raw string) ([]ecSection, bool) {
	var (
		sections []ecSection
		current  *ecSection
		root     bool
		preamble = map[string]string{}
	)

	flush := func() {
		if current != nil {
			sections = append(sections, *current)
			current = nil
		}
	}

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			pattern := strings.TrimSpace(line[1 : len(line)-1])
			current = &ecSection{pattern: pattern, keys: map[string]string{}}
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.ToLower(strings.TrimSpace(value))

		if current != nil {
			current.keys[key] = value
		} else if key == "root" {
			preamble[key] = value
		}
	}

	flush()

	if v, ok := preamble["root"]; ok && v == "true" {
		root = true
	}

	for _, sec := range sections {
		if v, ok := sec.keys["root"]; ok && v == "true" {
			root = true
		}
	}

	return sections, root
}

// ecMatch reports whether pattern from config dir covers rel. A pattern
// without a slash matches the base name; otherwise * ? ** and {a,b} match
// the slash-relative path.
func ecMatch(pattern, dir, rel string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}

	if !strings.ContainsRune(pattern, '/') {
		return ecGlob(pattern, baseName(rel))
	}

	var target string
	if dir == "." || dir == "" {
		target = rel
	} else if rel == dir || strings.HasPrefix(rel, dir+"/") {
		target = strings.TrimPrefix(rel, dir+"/")
	} else {
		return false
	}

	return ecGlob(pattern, target)
}

// ecGlob matches s against an editorconfig glob with *, ?, **, and {a,b}.
func ecGlob(pattern, s string) bool {
	for _, expanded := range ecExpandBraces(pattern) {
		if ecGlobSingle(expanded, s) {
			return true
		}
	}

	return false
}

// ecExpandBraces splits the first {...} group into its alternatives.
func ecExpandBraces(pattern string) []string {
	start := strings.IndexByte(pattern, '{')
	if start < 0 {
		return []string{pattern}
	}

	depth := 0
	for i := start; i < len(pattern); i++ {
		switch pattern[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				var out []string
				for _, alt := range strings.Split(pattern[start+1:i], ",") {
					out = append(out, ecExpandBraces(pattern[:start]+alt+pattern[i+1:])...)
				}

				return out
			}
		}
	}

	return []string{pattern}
}

// ecGlobSingle matches s against a brace-free glob.
func ecGlobSingle(pattern, s string) bool {
	px, sx := []rune(pattern), []rune(s)

	var match func(pi, si int) bool
	match = func(pi, si int) bool {
		for pi < len(px) {
			switch px[pi] {
			case '*':
				if pi+1 < len(px) && px[pi+1] == '*' {
					// ** eats slashes; a following slash is optional.
					ni := pi + 2
					if ni < len(px) && px[ni] == '/' {
						ni++
					}
					for k := si; k <= len(sx); k++ {
						if match(ni, k) {
							return true
						}
					}

					return false
				}

				for k := si; k <= len(sx); k++ {
					if k < len(sx) && sx[k] == '/' {
						break
					}
					if match(pi+1, k) {
						return true
					}
				}

				return false
			case '?':
				if si >= len(sx) || sx[si] == '/' {
					return false
				}
				pi++
				si++
			case '/':
				if si >= len(sx) || sx[si] != '/' {
					return false
				}
				pi++
				si++
			default:
				if si >= len(sx) || px[pi] != sx[si] {
					return false
				}
				pi++
				si++
			}
		}

		return si == len(sx)
	}

	return match(0, 0)
}
