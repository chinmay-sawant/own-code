package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Settings persist roaming defaults beside the per-workspace sessions.
// Precedence is session > settings > builtin: EnableSession loads the
// global file first, then lets a restored session override theme, zoom,
// and font size. Gestures write both the session (document wins per
// workspace) and the global file (roams as the next default).

const (
	settingsVersion      = 1
	settingsFileName     = "settings.json"
	defaultFontSize      = 12.0
	fontMinSize          = 8.0
	fontMaxSize          = 24.0
	maxGlobPerList       = 100
	maxGlobLen           = 200
	settingsFileModeNote = "0700/0600 atomic via temp+rename, like session.go"
)

// excludeSettings holds user glob lists plus the gitignore interplay.
// useGitignore=true keeps dimmed/expandable gitignored rows; false skips
// git calls (offline/fast). searchIgnored=true makes gitignored files
// searchable; false (default) skips them. Custom scan excludes hide
// entirely (distinct from dimmed); custom search excludes are counted in
// the search Note.
type excludeSettings struct {
	Scan          []string `json:"scan"`
	Search        []string `json:"search"`
	UseGitignore  bool     `json:"useGitignore"`
	SearchIgnored bool     `json:"searchIgnored"`
}

// UnmarshalJSON defaults UseGitignore to true when the key is absent, so
// old files without the excludes block keep gitignore integration while
// an explicit false is honored.
func (e *excludeSettings) UnmarshalJSON(data []byte) error {
	type plain excludeSettings
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var p plain
	p.UseGitignore = true
	for key, value := range raw {
		switch key {
		case "scan":
			if err := json.Unmarshal(value, &p.Scan); err != nil {
				return err
			}
		case "search":
			if err := json.Unmarshal(value, &p.Search); err != nil {
				return err
			}
		case "useGitignore":
			if err := json.Unmarshal(value, &p.UseGitignore); err != nil {
				return err
			}
		case "searchIgnored":
			if err := json.Unmarshal(value, &p.SearchIgnored); err != nil {
				return err
			}
		}
	}
	*e = excludeSettings(p)
	return nil
}

// globalSettings is the versioned settings.json schema.
type globalSettings struct {
	Version     int                `json:"version"`
	Theme       string             `json:"theme,omitempty"`
	FontSize    float64            `json:"fontSize,omitempty"`
	Zoom        float64            `json:"zoom,omitempty"`
	ZoomPerView map[string]float64 `json:"zoomPerView,omitempty"`
	Keymap      map[string]string  `json:"keymap,omitempty"`
	Excludes    excludeSettings    `json:"excludes,omitempty"`
}

// defaultSettingsPath returns the global file beside the per-workspace
// sessions: <UserConfigDir>/ownframe/code-view/settings.json.
func defaultSettingsPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("settings: config dir: %w", err)
	}
	return filepath.Join(base, "ownframe", "code-view", settingsFileName), nil
}

// loadSettings reads one settings file. A missing file is a first run and
// returns the zero settings without an error. A damaged file returns zero
// settings with the decode error wrapped so the caller can toast it, like
// loadSession. Unknown fields are ignored by encoding/json by default,
// which keeps version forward-compat.
func loadSettings(path string) (globalSettings, error) {
	var s globalSettings
	if strings.TrimSpace(path) == "" {
		return s, errors.New("settings: empty path")
	}
	data, err := os.ReadFile(path) //nolint:gosec // caller names the settings file under the user config dir or a test temp dir
	if errors.Is(err, os.ErrNotExist) {
		s = zeroSettings()
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("settings: read %s: %w", path, err)
	}
	// Decode through a generic map first so unknown top-level fields are
	// dropped explicitly and a forward-compatible version survives.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return zeroSettings(), fmt.Errorf("settings: decode %s: %w", path, err)
	}
	s = zeroSettings()
	for key, value := range raw {
		switch key {
		case "version":
			_ = json.Unmarshal(value, &s.Version)
		case "theme":
			_ = json.Unmarshal(value, &s.Theme)
		case "fontSize":
			_ = json.Unmarshal(value, &s.FontSize)
		case "zoom":
			_ = json.Unmarshal(value, &s.Zoom)
		case "zoomPerView":
			_ = json.Unmarshal(value, &s.ZoomPerView)
		case "keymap":
			_ = json.Unmarshal(value, &s.Keymap)
		case "excludes":
			_ = json.Unmarshal(value, &s.Excludes)
		}
	}
	s = s.sanitize()
	return s, nil
}

// saveSettings writes one settings file atomically through a temp file in
// the same directory. Parent and file are owner-only (0700/0600).
func saveSettings(path string, s globalSettings) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("settings: empty path")
	}
	s = s.sanitize()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("settings: encode %s: %w", path, err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, sessionDirMode); err != nil {
		return fmt.Errorf("settings: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("settings: temp in %s: %w", dir, err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("settings: write %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("settings: close %s: %w", name, err)
	}
	if err := os.Chmod(name, sessionFileMode); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("settings: chmod %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("settings: replace %s: %w", path, err)
	}
	return nil
}

// zeroSettings returns the builtin defaults: midnight, 12px, 100%,
// no per-view overrides, no keymap diffs, gitignore on.
func zeroSettings() globalSettings {
	return globalSettings{
		Version:  settingsVersion,
		Theme:    "midnight",
		FontSize: defaultFontSize,
		Zoom:     1,
		Excludes: excludeSettings{UseGitignore: true},
	}
}

// sanitize clamps and cleans one settings value: theme falls back to
// midnight, font 8..24, zoom 0.5..3.0, per-view editor/terminal only
// 0.5..3.0, globs capped (~100/list, 200 chars, invalid dropped, never
// aborts), chords validated (invalid dropped). A zero FontSize/Zoom is a
// missing marker and resolves to the builtin default.
func (s globalSettings) sanitize() globalSettings {
	if s.Version <= 0 || s.Version > settingsVersion {
		if s.Version <= 0 {
			s.Version = settingsVersion
		}
		// Forward-compat: unknown future versions keep their known
		// fields; unknown fields were already ignored at decode.
	}
	s.Theme = validTheme(s.Theme)
	if s.FontSize == 0 {
		s.FontSize = defaultFontSize
	} else {
		s.FontSize = clampFontSize(s.FontSize)
	}
	if s.Zoom == 0 {
		s.Zoom = 1
	} else {
		s.Zoom = clampSettingsZoom(s.Zoom)
	}
	if len(s.ZoomPerView) > 0 {
		clean := map[string]float64{}
		for _, view := range []string{"editor", "terminal"} {
			if v, ok := s.ZoomPerView[view]; ok && v != 0 {
				clean[view] = clampSettingsZoom(v)
			}
		}
		s.ZoomPerView = clean
		if len(clean) == 0 {
			s.ZoomPerView = nil
		}
	}
	if len(s.Keymap) > 0 {
		clean := map[string]string{}
		for action, chord := range s.Keymap {
			norm, ok := normalizeChord(chord)
			if !ok {
				continue
			}
			if !keymapActionKnown(action) {
				continue
			}
			// Stored diffs only: a chord equal to the default is dropped.
			if def, known := defaultChordFor(action); known {
				if defNorm, valid := normalizeChord(def); valid && norm == defNorm {
					continue
				}
			}
			clean[action] = norm
		}
		s.Keymap = clean
		if len(clean) == 0 {
			s.Keymap = nil
		}
	}
	s.Excludes.Scan = cleanGlobList(s.Excludes.Scan)
	s.Excludes.Search = cleanGlobList(s.Excludes.Search)
	return s
}

// clampFontSize keeps a base font size in 8..24 px.
func clampFontSize(v float64) float64 {
	if v < fontMinSize {
		return fontMinSize
	}
	if v > fontMaxSize {
		return fontMaxSize
	}
	return v
}

// clampSettingsZoom keeps a settings zoom in the layout range,
// defaulting a missing value to 100 percent.
func clampSettingsZoom(v float64) float64 {
	if v < zoomMin || v > zoomMax {
		if v == 0 {
			return 1
		}
		// Clamp out-of-range to the nearest bound instead of resetting,
		// so a hand-edited 5.0 lands at 3.0 rather than 1.0.
		if v < zoomMin {
			return zoomMin
		}
		return zoomMax
	}
	return v
}

// cleanGlobList trims, caps (~100 entries, 200 chars each), and drops
// invalid patterns. An invalid glob never aborts a scan or search; it is
// rejected at the editor and dropped here.
func cleanGlobList(patterns []string) []string {
	if len(patterns) == 0 {
		return nil
	}
	out := make([]string, 0, min(len(patterns), maxGlobPerList))
	for _, raw := range patterns {
		p := strings.TrimSpace(raw)
		if p == "" || strings.HasPrefix(p, "!") {
			continue // v1 has no negation
		}
		if len(p) > maxGlobLen {
			continue
		}
		if len(out) >= maxGlobPerList {
			break
		}
		if _, err := globToRegexp(p); err != nil {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// globToRegexp compiles one user glob. Supported: *, ?, **, character
// classes, trailing / for dir-only. Case-sensitive v1. Choice note: a
// small custom compiler instead of doublestar, so settings add no new
// dependency; semantics are documented on the Files & Search panel.
func globToRegexp(pattern string) (*regexp.Regexp, error) {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return nil, errors.New("empty pattern")
	}
	if strings.HasPrefix(p, "!") {
		return nil, errors.New("negation is not supported in v1")
	}
	if strings.ContainsRune(p, '\x00') {
		return nil, errors.New("NUL in pattern")
	}
	dirOnly := strings.HasSuffix(p, "/")
	p = strings.TrimSuffix(p, "/")
	// foo/ means foo/** in matching.
	suffix := ""
	if dirOnly {
		suffix = "(?:/.*)?"
	}
	var b strings.Builder
	b.WriteString("(?s)^")
	i := 0
	for i < len(p) {
		c := p[i]
		switch c {
		case '*':
			if i+1 < len(p) && p[i+1] == '*' {
				// ** matches across separators; a trailing /** also
				// matches the bare prefix.
				rest := p[i+2:]
				if strings.HasPrefix(rest, "/") {
					b.WriteString("(?:.*)?")
					i += 2
				} else {
					b.WriteString(".*")
					i += 2
				}
			} else {
				b.WriteString("[^/]*")
				i++
			}
		case '?':
			b.WriteString("[^/]")
			i++
		case '[':
			j := i + 1
			if j < len(p) && (p[j] == '!' || p[j] == '^') {
				j++
			}
			if j < len(p) && p[j] == ']' {
				j++
			}
			for j < len(p) && p[j] != ']' {
				j++
			}
			if j >= len(p) {
				return nil, fmt.Errorf("unbalanced [ in %q", pattern)
			}
			cls := p[i : j+1]
			cls = strings.Replace(cls, "[!", "[^", 1)
			b.WriteString(cls)
			i = j + 1
		case '\\':
			if i+1 < len(p) {
				b.WriteString(regexp.QuoteMeta(string(p[i+1])))
				i += 2
			} else {
				b.WriteString("\\\\")
				i++
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
			i++
		}
	}
	b.WriteString(suffix)
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, err
	}
	return re, nil
}

// excludeMatcher is one precompiled glob set. Compile once per
// settings/rescan; match on every candidate with short-circuit.
type excludeMatcher struct {
	patterns []string
	res      []*regexp.Regexp
	dirOnly  []bool
}

// compileExcludeMatcher precompiles a cleaned glob list. Invalid entries
// are skipped, never fatal.
func compileExcludeMatcher(patterns []string) *excludeMatcher {
	m := &excludeMatcher{}
	for _, p := range cleanGlobList(patterns) {
		re, err := globToRegexp(p)
		if err != nil {
			continue
		}
		m.patterns = append(m.patterns, p)
		m.res = append(m.res, re)
		m.dirOnly = append(m.dirOnly, strings.HasSuffix(strings.TrimSpace(p), "/"))
	}
	if len(m.patterns) == 0 {
		return nil
	}
	return m
}

// matchSlash reports whether slash-relative path matches. Both the full
// relative path and the basename are tried, so "*.log" matches at any
// depth while "build/**" stays anchored.
func (m *excludeMatcher) matchSlash(rel string, isDir bool) bool {
	if m == nil || rel == "" {
		return false
	}
	rel = strings.TrimPrefix(rel, "./")
	for i, re := range m.res {
		_ = i
		if re.MatchString(rel) {
			return true
		}
		// Basename fallback for patterns without a slash.
		if !strings.Contains(m.patterns[i], "/") {
			if base := baseName(rel); base != rel && re.MatchString(base) {
				return true
			}
		}
		_ = isDir
	}
	return false
}

// scanExcludes bundles the compiled scan/search sets plus the gitignore
// toggles for one settings snapshot.
type scanExcludes struct {
	scan          *excludeMatcher
	search        *excludeMatcher
	useGitignore  bool
	searchIgnored bool
	scanCount     int
	searchCount   int
}

// compileScanExcludes precompiles one excludes block.
func compileScanExcludes(e excludeSettings) *scanExcludes {
	return &scanExcludes{
		scan:          compileExcludeMatcher(e.Scan),
		search:        compileExcludeMatcher(e.Search),
		useGitignore:  e.UseGitignore,
		searchIgnored: e.SearchIgnored,
		scanCount:     len(cleanGlobList(e.Scan)),
		searchCount:   len(cleanGlobList(e.Search)),
	}
}

// excludedByScan reports whether a slash-relative path is hidden by custom
// scan excludes. Hard skips (skipDir, symlinks, depth, binary) run first
// in the walker and always win; this only adds the user layer.
func (s *scanExcludes) excludedByScan(rel string, isDir bool) bool {
	if s == nil || s.scan == nil {
		return false
	}
	return s.scan.matchSlash(rel, isDir)
}

// excludedBySearch reports whether a slash-relative path is skipped at
// search time. Default inherits scan patterns plus gitignored files (when
// searchIgnored=false); custom search patterns always apply.
func (s *scanExcludes) excludedBySearch(rel string, ignored bool) bool {
	if s == nil {
		return false
	}
	if s.scan != nil && s.scan.matchSlash(rel, false) {
		return true
	}
	if s.search != nil && s.search.matchSlash(rel, false) {
		return true
	}
	if ignored && !s.searchIgnored {
		return true
	}
	return false
}

// normalizeChord parses and normalizes one keymap chord. Grammar:
// Ctrl/Alt/Shift/Meta + key, case-insensitive, modifiers sorted in
// Ctrl+Alt+Shift+Meta order. A bare key normalizes to itself lowercased
// except single letters which uppercase. Returns false for empty or
// malformed chords.
func normalizeChord(raw string) (string, bool) {
	chord, ok := parseChord(raw)
	if !ok {
		return "", false
	}
	return chord, true
}

// parseChord implements normalizeChord.
func parseChord(raw string) (string, bool) {
	parts := strings.Split(raw, "+")
	if len(parts) == 0 {
		return "", false
	}
	var mods []string
	var keys []string
	seenMod := map[string]bool{}
	for _, part := range parts {
		p := strings.TrimSpace(part)
		if p == "" {
			return "", false
		}
		lower := strings.ToLower(p)
		switch lower {
		case "ctrl", "control":
			if !seenMod["ctrl"] {
				mods = append(mods, "Ctrl")
				seenMod["ctrl"] = true
			}
		case "alt", "opt", "option":
			if !seenMod["alt"] {
				mods = append(mods, "Alt")
				seenMod["alt"] = true
			}
		case "shift":
			if !seenMod["shift"] {
				mods = append(mods, "Shift")
				seenMod["shift"] = true
			}
		case "meta", "cmd", "command", "win", "super":
			if !seenMod["meta"] {
				mods = append(mods, "Meta")
				seenMod["meta"] = true
			}
		default:
			keys = append(keys, normalizeKeyName(p))
		}
	}
	if len(keys) != 1 {
		return "", false
	}
	order := map[string]int{"Ctrl": 0, "Alt": 1, "Shift": 2, "Meta": 3}
	sort.Slice(mods, func(i, j int) bool { return order[mods[i]] < order[mods[j]] })
	if len(mods) == 0 {
		return keys[0], true
	}
	return strings.Join(mods, "+") + "+" + keys[0], true
}

// normalizeKeyName lowercases most key names but uppercases single
// letters and keeps function keys canonical (F12).
func normalizeKeyName(key string) string {
	k := strings.TrimSpace(key)
	if k == "" {
		return k
	}
	if len([]rune(k)) == 1 {
		return strings.ToUpper(k)
	}
	lower := strings.ToLower(k)
	if strings.HasPrefix(lower, "f") && len(lower) <= 3 {
		return strings.ToUpper(lower)
	}
	// Window key names arrive lowercase (arrowleft, backquote, ...).
	return lower
}

// keymapEntry is one rebindable row in Settings → Keyboard.
type keymapEntry struct {
	Action  string
	Label   string
	Default string
	Fixed   bool
}

// keymapCatalog inventories the rebindable subset of onKeyDown. Engine
// precedence (terminal/overlays/editing) is unchanged; non-rebindable
// chords are marked Fixed in the UI.
func keymapCatalog() []keymapEntry {
	return []keymapEntry{
		{"zoom-in", "Zoom in", "Ctrl+=", false},
		{"zoom-out", "Zoom out", "Ctrl+-", false},
		{"zoom-reset", "Reset zoom", "Ctrl+0", false},
		{"toggle-terminal", "Toggle terminal", "Ctrl+`", false},
		{"quick-open", "Quick open", "Ctrl+P", false},
		{"find-in-file", "Find in file", "Ctrl+F", false},
		{"replace-in-file", "Replace in file", "Ctrl+H", false},
		{"workspace-search", "Search in workspace", "Ctrl+Shift+F", false},
		{"workspace-replace", "Replace in workspace", "Ctrl+Shift+H", false},
		{"save-file", "Save file", "Ctrl+S", false},
		{"toggle-edit", "Toggle edit mode", "Ctrl+E", false},
		{"toggle-split", "Split editor", "Ctrl+\\", false},
		{"focus-left", "Focus left editor", "Ctrl+1", false},
		{"focus-right", "Focus right editor", "Ctrl+2", false},
		{"cycle-pane", "Cycle editor pane", "F6", false},
		{"goto-definition", "Go to definition", "Ctrl+D", false},
		{"toggle-preview", "Toggle preview", "Ctrl+Shift+V", false},
		{"paste-explorer", "Paste in explorer", "Ctrl+V", false},
		{"copy-path", "Copy path / file", "Ctrl+C", false},
		{"cut-path", "Cut path / file", "Ctrl+X", false},
		{"select-all-files", "Select all files", "Ctrl+A", false},
		{"line-card", "Open line card", "Ctrl+I", false},
		{"mode-explorer", "Go to Explorer", "1", false},
		{"mode-changes", "Go to Source Control", "2", false},
		{"toggle-details", "Toggle details", "3", false},
		{"focus-filter", "Focus explorer filter", "F", false},
		{"close-tab", "Close tab", "W", false},
		{"toggle-wrap", "Toggle word wrap", "Z", false},
		{"toggle-blame", "Toggle blame", "B", false},
		{"toggle-preview-key", "Toggle preview (M)", "M", false},
		{"reload-file", "Reload file", "R", false},
		{"hunk-next", "Next hunk/change", "N", false},
		{"hunk-prev", "Previous hunk/change", "P", false},
		{"history-back", "Navigate back", "Alt+ArrowLeft", false},
		{"history-forward", "Navigate forward", "Alt+ArrowRight", false},
		// Window-owned and always fixed: listed read-only, never rebindable.
		{"devtools", "Toggle developer tools (F12)", "F12", true},
		{"devtools-alt", "Toggle developer tools (Ctrl+Shift+I)", "Ctrl+Shift+I", true},
	}
}

// defaultChordFor returns the catalog default for one action.
func defaultChordFor(action string) (string, bool) {
	for _, e := range keymapCatalog() {
		if e.Action == action {
			return e.Default, true
		}
	}
	return "", false
}

// keymapActionKnown reports whether an action is in the catalog and rebindable.
func keymapActionKnown(action string) bool {
	for _, e := range keymapCatalog() {
		if e.Action == action {
			return !e.Fixed
		}
	}
	return false
}

// resolveChord returns the actions one normalized chord currently maps to,
// including defaults shadowed by a custom bind.
func resolveChord(keymap map[string]string, chord string) []string {
	norm, ok := normalizeChord(chord)
	if !ok {
		return nil
	}
	var out []string
	for _, e := range keymapCatalog() {
		if e.Fixed {
			continue
		}
		current, ok := normalizeChord(e.Default)
		if !ok {
			continue
		}
		if custom, has := keymap[e.Action]; has {
			if norm, valid := normalizeChord(custom); valid {
				current = norm
			}
		}
		if current == norm {
			out = append(out, e.Action)
		}
	}
	sort.Strings(out)
	return out
}

// isReservedChord reports chords that can never be rebound: F12 and
// Ctrl+Shift+I are window-owned for the devtools dock.
func isReservedChord(chord string) bool {
	norm, ok := normalizeChord(chord)
	if !ok {
		return false
	}
	return norm == "F12" || norm == "Ctrl+Shift+I"
}

// isTerminalRiskyChord reports chords the terminal or editor owns while
// focused (bare Ctrl+C/V/X/Z/Y and single letters). Rebinding them needs
// an explicit confirm and a warning in the UI.
func isTerminalRiskyChord(chord string) bool {
	norm, ok := normalizeChord(chord)
	if !ok {
		return false
	}
	switch norm {
	case "Ctrl+C", "Ctrl+V", "Ctrl+X", "Ctrl+Z", "Ctrl+Y":
		return true
	}
	// Single-letter binds without a modifier only fire when not editing,
	// terminal-blurred, and no overlay is open; require confirm.
	if !strings.Contains(norm, "+") && len([]rune(norm)) == 1 {
		return true
	}
	return false
}

// isSingleLetterChord reports bare A-Z binds that need confirm.
func isSingleLetterChord(chord string) bool {
	norm, ok := normalizeChord(chord)
	if !ok {
		return false
	}
	return !strings.Contains(norm, "+") && len([]rune(norm)) == 1
}

// settingsSnapshot builds the global file from live state. Keymap stores
// diffs only: a chord equal to the catalog default is omitted, so Reset is
// deleting the entry.
func (a *App) settingsSnapshot() globalSettings {
	s := globalSettings{
		Version:  settingsVersion,
		Theme:    validTheme(a.theme),
		FontSize: a.baseFontSize(),
		Zoom:     a.uiZoom(),
		Excludes: excludeSettings{UseGitignore: true},
	}
	if len(a.zoomPerView) > 0 {
		s.ZoomPerView = map[string]float64{}
		for _, view := range []string{"editor", "terminal"} {
			if v, ok := a.zoomPerView[view]; ok && v > 0 && v != 1 {
				s.ZoomPerView[view] = clampSettingsZoom(v)
			}
		}
		if len(s.ZoomPerView) == 0 {
			s.ZoomPerView = nil
		}
	}
	if a.scanFilter != nil {
		s.Excludes.UseGitignore = a.scanFilter.useGitignore
		s.Excludes.SearchIgnored = a.scanFilter.searchIgnored
	} else if a.settings.Excludes.UseGitignore || a.settings.Version == 0 {
		s.Excludes.UseGitignore = true
	}
	if a.settings.Excludes.Scan != nil {
		s.Excludes.Scan = append([]string(nil), a.settings.Excludes.Scan...)
	}
	if a.settings.Excludes.Search != nil {
		s.Excludes.Search = append([]string(nil), a.settings.Excludes.Search...)
	}
	if keymap := a.keymapDiffs(); len(keymap) > 0 {
		s.Keymap = keymap
	}
	return s.sanitize()
}

// keymapDiffs returns the stored custom binds (diffs only).
func (a *App) keymapDiffs() map[string]string {
	if a.settings.Keymap == nil {
		return nil
	}
	out := map[string]string{}
	for action, chord := range a.settings.Keymap {
		if !keymapActionKnown(action) {
			continue
		}
		if norm, ok := normalizeChord(chord); ok {
			if def, known := defaultChordFor(action); known {
				if defNorm, valid := normalizeChord(def); valid && norm == defNorm {
					continue
				}
			}
			out[action] = norm
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// persistSettings writes the global file when it differs from the last
// snapshot. Gestures call this alongside persistSession. Like sessions, it
// waits for sessionReady so tests that call New directly never touch the
// user config directory.
func (a *App) persistSettings() {
	if !a.sessionReady || a.settingsPath == "" {
		return
	}
	s := a.settingsSnapshot()
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	if string(data) == a.settingsLast {
		return
	}
	if err := saveSettings(a.settingsPath, s); err != nil {
		return
	}
	a.settings = s
	a.settingsLast = string(data)
}

// applySettings installs one loaded global snapshot as the roaming
// defaults: theme, font, zoom, per-view, keymap, and compiled excludes.
func (a *App) applySettings(s globalSettings) {
	s = s.sanitize()
	a.settings = s
	a.theme = validTheme(s.Theme)
	a.fontSize = s.FontSize
	if s.Zoom != 0 {
		a.zoom = s.Zoom
	}
	if len(s.ZoomPerView) > 0 {
		a.zoomPerView = map[string]float64{}
		for k, v := range s.ZoomPerView {
			a.zoomPerView[k] = v
		}
	} else {
		a.zoomPerView = nil
	}
	a.scanFilter = compileScanExcludes(s.Excludes)
	if data, err := json.Marshal(s); err == nil {
		a.settingsLast = string(data)
	}
}

// keymapChord returns the live chord for one action: the custom bind when
// present, else the catalog default. Fixed actions return their default.
// Both sides normalize, so "Alt+ArrowLeft" matches the window's
// "alt+arrowleft" event.
func (a *App) keymapChord(action string) string {
	if a.settings.Keymap != nil {
		if custom, ok := a.settings.Keymap[action]; ok {
			if norm, valid := normalizeChord(custom); valid {
				return norm
			}
		}
	}
	if def, known := defaultChordFor(action); known {
		if norm, valid := normalizeChord(def); valid {
			return norm
		}
		return def
	}
	return ""
}

// keymapMatches reports whether the current key event (ctrl/shift held,
// window key name) equals the live chord for one action.
func (a *App) keymapMatches(action string, key string) bool {
	want := a.keymapChord(action)
	if want == "" {
		return false
	}
	got := chordFromEvent(a.ctrl, a.shift, key)
	return got == want
}

// keymapCustomized reports whether one action has a stored rebind that
// differs from its catalog default.
func (a *App) keymapCustomized(action string) bool {
	if a.settings.Keymap == nil {
		return false
	}
	custom, ok := a.settings.Keymap[action]
	if !ok {
		return false
	}
	norm, valid := normalizeChord(custom)
	if !valid {
		return false
	}
	def, known := defaultChordFor(action)
	if !known {
		return true
	}
	defNorm, valid := normalizeChord(def)
	return !valid || norm != defNorm
}

// keymapAllows gates an old chord: true when the action is uncustomized
// (behavior byte-identical, including shift-insensitive Ctrl chords) or
// when the event still matches the live chord.
func (a *App) keymapAllows(action string, key string) bool {
	return !a.keymapCustomized(action) || a.keymapMatches(action, key)
}

// dispatchCustomKeymap fires rebound chords that no home branch names. It
// runs late (after terminal/overlay/editing guards), so precedence for
// defaults is unchanged; customs resolve at the same priority as the
// plain-key switch. Returns true when a custom chord fired.
func (a *App) dispatchCustomKeymap(ctx context.Context, key string) (bool, error) {
	for _, e := range keymapCatalog() {
		if e.Fixed || !a.keymapCustomized(e.Action) {
			continue
		}
		if !a.keymapMatches(e.Action, key) {
			continue
		}
		return true, a.runKeymapAction(ctx, e.Action)
	}
	return false, nil
}

// runKeymapAction runs one catalog action from the dispatcher. Guards (focus,
// mode, readiness) mirror the home branches.
func (a *App) runKeymapAction(ctx context.Context, action string) error {
	switch action {
	case "zoom-in":
		return a.zoomIn(ctx)
	case "zoom-out":
		return a.zoomOut(ctx)
	case "zoom-reset":
		return a.resetZoom(ctx)
	case "toggle-terminal":
		a.toggleTerminal(ctx)
		return a.page.Redraw(ctx)
	case "quick-open":
		return a.openPalette(ctx)
	case "find-in-file":
		return a.openFind(ctx)
	case "replace-in-file":
		return a.openReplace(ctx)
	case "workspace-search":
		return a.openWorkspaceSearch(ctx)
	case "workspace-replace":
		return a.openWorkspaceReplace(ctx)
	case "save-file":
		a.saveDirtyActive()
		return a.page.Redraw(ctx)
	case "toggle-edit":
		a.toggleEdit()
		return a.page.Redraw(ctx)
	case "toggle-split":
		a.toggleSplit()
		return a.page.Redraw(ctx)
	case "focus-left":
		a.setFocusGroup(0)
		return a.page.Redraw(ctx)
	case "focus-right":
		a.setFocusGroup(1)
		return a.page.Redraw(ctx)
	case "cycle-pane":
		if a.split && a.focusGroup == 1 {
			a.setFocusGroup(0)
		} else {
			a.setFocusGroup(1)
		}
		return a.page.Redraw(ctx)
	case "goto-definition":
		return a.gotoDefinitionAtCaret(ctx)
	case "toggle-preview", "toggle-preview-key":
		a.togglePreview()
		return a.page.Redraw(ctx)
	case "paste-explorer":
		if a.pasteTargetReady() {
			return a.pasteFromClipboard(ctx)
		}
		return a.page.Redraw(ctx)
	case "copy-path":
		if a.explorerCopyReady() {
			if done, err := a.copyExplorerSelection(ctx, false); done {
				return err
			}
		}
		return a.page.Redraw(ctx)
	case "cut-path":
		if a.explorerCopyReady() {
			if done, err := a.copyExplorerSelection(ctx, true); done {
				return err
			}
		}
		return a.page.Redraw(ctx)
	case "select-all-files":
		if a.mode == "changes" {
			a.selectAllFiles()
			a.refresh()
		}
		return a.page.Redraw(ctx)
	case "line-card":
		a.showInlineCardAtCurrent()
		return a.page.Redraw(ctx)
	case "mode-explorer":
		a.setMode(ctx, "explorer")
		return a.page.Redraw(ctx)
	case "mode-changes":
		a.setMode(ctx, "changes")
		return a.page.Redraw(ctx)
	case "toggle-details":
		a.toggleDetails()
		return a.page.Redraw(ctx)
	case "focus-filter":
		return a.focusFilter(ctx)
	case "close-tab":
		if a.mode == "changes" {
			a.closeWorkingTree(ctx)
		} else {
			group := a.activeGroup()
			a.closeTabIn(group, a.groupActive(group))
		}
		return a.page.Redraw(ctx)
	case "toggle-wrap":
		a.toggleWrap()
		return a.page.Redraw(ctx)
	case "toggle-blame":
		a.toggleBlame()
		return a.page.Redraw(ctx)
	case "reload-file":
		if a.mode == "changes" {
			a.ReloadChanges(ctx, true)
		} else {
			a.reloadActive(ctx)
		}
		return a.page.Redraw(ctx)
	case "hunk-next":
		if a.mode == "changes" {
			a.jumpHunk(1)
		}
		return a.page.Redraw(ctx)
	case "hunk-prev":
		if a.mode == "changes" {
			a.jumpHunk(-1)
		}
		return a.page.Redraw(ctx)
	case "history-back":
		a.goNavBack()
		return a.page.Redraw(ctx)
	case "history-forward":
		a.goNavForward()
		return a.page.Redraw(ctx)
	default:
		return a.page.Redraw(ctx)
	}
}

// chordFromEvent builds a normalized chord from modifier state plus the
// window key name. The window sends Alt as "alt+<key>" and Meta chords via
// a.ctrl; single names cover the rest.
func chordFromEvent(ctrl, shift bool, key string) string {
	k := strings.TrimSpace(key)
	if strings.HasPrefix(strings.ToLower(k), "alt+") {
		rest := strings.TrimPrefix(k, "alt+")
		rest = strings.TrimPrefix(rest, "Alt+")
		rest = strings.TrimPrefix(rest, "ALT+")
		if shift {
			return "Alt+Shift+" + normalizeKeyName(rest)
		}
		return "Alt+" + normalizeKeyName(rest)
	}
	mods := []string{}
	if ctrl {
		mods = append(mods, "Ctrl")
	}
	if shift {
		mods = append(mods, "Shift")
	}
	name := normalizeKeyName(k)
	// Map window punctuation names to the chord glyph the catalog uses.
	switch name {
	case "equal":
		name = "="
	case "minus":
		name = "-"
	case "backslash":
		name = "\\"
	case "backquote", "grave", "`":
		name = "`"
	case "kpadd":
		name = "="
	case "kpsubtract":
		name = "-"
	case "kp0":
		name = "0"
	}
	if len(mods) == 0 {
		return name
	}
	return strings.Join(mods, "+") + "+" + name
}

// settingsTabName returns the selected Settings section, defaulting to
// appearance for unknown or empty values.
func (a *App) settingsTabName() string {
	switch a.settingsTab {
	case "keyboard", "files":
		return a.settingsTab
	default:
		return "appearance"
	}
}

// zoomPerViewValue returns one per-view override or 0 when the view follows
// the global zoom.
func (a *App) zoomPerViewValue(view string) float64 {
	if a.zoomPerView == nil {
		return 0
	}
	return a.zoomPerView[view]
}

// zoomPerViewLabel renders one override as "110%" or "—" when following.
func (a *App) zoomPerViewLabel(view string) string {
	if v := a.zoomPerViewValue(view); v > 0 {
		return fmt.Sprintf("%.0f%%", math.Round(v*zoomPercent))
	}
	return "—"
}

// keymapPendingMessage describes the parked keymap conflict for the banner.
func (a *App) keymapPendingMessage() string {
	if a.keymapPendingAction == "" || len(a.keymapPendingOthers) == 0 {
		return ""
	}
	return a.keymapPendingChord + " is already used by " + strings.Join(a.keymapPendingOthers, ", ") + " — Swap, Replace, or Cancel."
}

// keymapRows builds the Settings → Keyboard rows: label, live chord,
// default, custom/fixed flags, capture highlight, and conflict highlight.
func (a *App) keymapRows() []keymapRow {
	rows := make([]keymapRow, 0, len(keymapCatalog()))
	pendingSet := map[string]bool{}
	for _, other := range a.keymapPendingOthers {
		pendingSet[other] = true
	}
	for _, e := range keymapCatalog() {
		live := e.Default
		if norm, ok := normalizeChord(e.Default); ok {
			live = norm
		}
		custom := false
		if a.settings.Keymap != nil {
			if c, ok := a.settings.Keymap[e.Action]; ok {
				if norm, valid := normalizeChord(c); valid {
					live = norm
					if def, known := defaultChordFor(e.Action); known {
						if defNorm, valid := normalizeChord(def); !valid || norm != defNorm {
							custom = true
						}
					} else {
						custom = true
					}
				}
			}
		}
		rows = append(rows, keymapRow{
			Action:    e.Action,
			Label:     e.Label,
			Chord:     live,
			Default:   e.Default,
			Custom:    custom,
			Fixed:     e.Fixed,
			Capturing: a.keymapCapture == e.Action,
			Conflict:  pendingSet[e.Action],
		})
	}
	return rows
}

// scanFilterAllowsGitignore reports the live useGitignore toggle.
func (a *App) scanFilterAllowsGitignore() bool {
	if a.scanFilter == nil {
		return true
	}
	return a.scanFilter.useGitignore
}

// scanFilterAllowsSearchIgnored reports the live searchIgnored toggle.
func (a *App) scanFilterAllowsSearchIgnored() bool {
	if a.scanFilter == nil {
		return false
	}
	return a.scanFilter.searchIgnored
}

// scanExcludeMatchCount reports the parked hidden count for the Files &
// Search live count.
func (a *App) scanExcludeMatchCount() int {
	return a.scanHidden
}

// searchExcludeSkipCount counts files the next search would skip.
func (a *App) searchExcludeSkipCount() int {
	if a.scanFilter == nil || a.ws == nil {
		return 0
	}
	n := 0
	for _, meta := range a.ws.Files {
		if meta != nil && a.scanFilter.excludedBySearch(meta.Path, meta.Ignored) {
			n++
		}
	}
	return n
}

// customExcludedBySearch reports a match against the user's own scan/search
// patterns, ignoring the gitignored default. Search uses it to count custom
// excludes for the Note separately from the shared Skipped tally.
func (s *scanExcludes) customExcludedBySearch(rel string) bool {
	if s == nil {
		return false
	}
	if s.scan != nil && s.scan.matchSlash(rel, false) {
		return true
	}
	if s.search != nil && s.search.matchSlash(rel, false) {
		return true
	}
	return false
}

// countCustomSearchExcluded counts workspace files the custom scan/search
// patterns hide from content search.
func countCustomSearchExcluded(ws *workspace, filter *scanExcludes) int {
	if ws == nil || filter == nil {
		return 0
	}
	n := 0
	for _, meta := range ws.Files {
		if meta != nil && filter.customExcludedBySearch(meta.Path) {
			n++
		}
	}
	return n
}

// excludesSkippedNote renders the custom-excludes count for the search Note.
func excludesSkippedNote(n int) string {
	if n == 1 {
		return "1 file skipped by excludes."
	}
	return fmt.Sprintf("%d files skipped by excludes.", n)
}

// scanDraftText returns the raw scan textarea: the live draft while the
// panel is open, else the saved globs joined by newline.
func (a *App) scanDraftText() string {
	if a.scanTouched {
		return a.scanDraft
	}
	return strings.Join(a.settings.Excludes.Scan, "\n")
}

// searchDraftText returns the raw search textarea likewise.
func (a *App) searchDraftText() string {
	if a.searchTouched {
		return a.searchDraft
	}
	return strings.Join(a.settings.Excludes.Search, "\n")
}

// parseGlobLines splits textarea content into cleaned, valid globs plus
// the 1-based lines that failed validation. Invalid lines never save and
// never abort a scan or search; the panel names them in red.
func parseGlobLines(text string) (valid []string, bad []int) {
	for i, line := range strings.Split(text, "\n") {
		p := strings.TrimSpace(line)
		if p == "" {
			continue
		}
		if len(p) > maxGlobLen || strings.HasPrefix(p, "!") {
			bad = append(bad, i+1)
			continue
		}
		if _, err := globToRegexp(p); err != nil {
			bad = append(bad, i+1)
			continue
		}
		if len(valid) < maxGlobPerList {
			valid = append(valid, p)
		}
	}
	return valid, bad
}

// invalidGlobLines returns the 1-based invalid line numbers of one textarea.
func invalidGlobLines(text string) []int {
	_, bad := parseGlobLines(text)
	return bad
}

// applyKeymapChord stores one rebind. Diffs only: a chord equal to the
// default deletes the entry, so Reset is just forgetting the diff.
func (a *App) applyKeymapChord(action, chord string) {
	norm, ok := normalizeChord(chord)
	if !ok || !keymapActionKnown(action) {
		return
	}
	if a.settings.Keymap == nil {
		a.settings.Keymap = map[string]string{}
	}
	if def, known := defaultChordFor(action); known {
		if defNorm, valid := normalizeChord(def); valid && norm == defNorm {
			delete(a.settings.Keymap, action)
			if len(a.settings.Keymap) == 0 {
				a.settings.Keymap = nil
			}
			a.persistSettings()
			return
		}
	}
	a.settings.Keymap[action] = norm
	a.persistSettings()
}

// processKeymapCapture handles one key event while a Rebind capture is
// armed. Reserved chords (F12/Ctrl+Shift+I) are disallowed with a toast.
// Collisions park in the pending banner (Swap/Replace/Cancel); terminal-
// risky and single-letter chords need an explicit confirm.
func (a *App) processKeymapCapture(chord string) {
	action := a.keymapCapture
	a.keymapCapture = ""
	if action == "" || !keymapActionKnown(action) {
		a.refresh()
		return
	}
	norm, ok := normalizeChord(chord)
	if !ok {
		a.settingsNote = "That chord was empty — press keys or Esc to cancel."
		a.refresh()
		return
	}
	if isReservedChord(norm) {
		a.settingsNote = norm + " is window-owned (developer dock) and cannot be rebound."
		a.showToast("info", "Reserved shortcut", norm+" opens the developer dock and stays fixed.")
		a.refresh()
		return
	}
	others := resolveChord(a.settings.Keymap, norm)
	kept := others[:0]
	for _, other := range others {
		if other != action {
			kept = append(kept, other)
		}
	}
	others = kept
	if len(others) > 0 {
		a.keymapPendingAction = action
		a.keymapPendingChord = norm
		a.keymapPendingOthers = others
		a.settingsNote = norm + " is already used — Swap, Replace, or Cancel."
		a.refresh()
		return
	}
	if isTerminalRiskyChord(norm) || isSingleLetterChord(norm) {
		a.keymapPendingAction = action
		a.keymapPendingChord = norm
		a.keymapPendingOthers = nil
		a.confirmOpen = true
		a.confirmKind = "keymap-risky"
		a.confirmTitle = "Rebind to " + norm + "?"
		if isSingleLetterChord(norm) {
			a.confirmMsg = "Single-letter shortcuts only fire when not editing with the terminal blurred. Apply anyway?"
		} else {
			a.confirmMsg = "The terminal or editor owns " + norm + " while focused, so the shortcut will not fire there. Apply anyway?"
		}
		a.refresh()
		return
	}
	a.applyKeymapChord(action, norm)
	a.settingsNote = ""
	a.showToast("success", "Shortcut updated", action+" is now "+norm+".")
	a.refresh()
}

// applyKeymapPending stores the pending rebind after its confirm.
func (a *App) applyKeymapPending() {
	action, chord := a.keymapPendingAction, a.keymapPendingChord
	a.keymapPendingAction, a.keymapPendingChord, a.keymapPendingOthers = "", "", nil
	if action == "" || chord == "" {
		return
	}
	a.applyKeymapChord(action, chord)
	a.showToast("success", "Shortcut updated", action+" is now "+chord+".")
}

// swapKeymapPending exchanges the pending chord with the conflicting action.
func (a *App) swapKeymapPending() {
	action, chord := a.keymapPendingAction, a.keymapPendingChord
	others := append([]string(nil), a.keymapPendingOthers...)
	a.keymapPendingAction, a.keymapPendingChord, a.keymapPendingOthers = "", "", nil
	if action == "" || chord == "" || len(others) == 0 {
		return
	}
	oldLive := a.keymapChord(action)
	a.applyKeymapChord(action, chord)
	// The displaced action takes the pending action's old chord; when the
	// old chord was itself a default elsewhere it simply resets.
	for _, other := range others {
		a.applyKeymapChord(other, oldLive)
	}
	a.showToast("success", "Shortcuts swapped", action+" is now "+chord+".")
}

// replaceKeymapPending steals the pending chord and resets the displaced
// actions to their defaults.
func (a *App) replaceKeymapPending() {
	action, chord := a.keymapPendingAction, a.keymapPendingChord
	others := append([]string(nil), a.keymapPendingOthers...)
	a.keymapPendingAction, a.keymapPendingChord, a.keymapPendingOthers = "", "", nil
	if action == "" || chord == "" {
		return
	}
	if a.settings.Keymap != nil {
		for _, other := range others {
			delete(a.settings.Keymap, other)
		}
		if len(a.settings.Keymap) == 0 {
			a.settings.Keymap = nil
		}
	}
	a.applyKeymapChord(action, chord)
	a.showToast("success", "Shortcut updated", action+" is now "+chord+".")
}

// cancelKeymapPending drops a parked conflict or capture.
func (a *App) cancelKeymapPending() {
	a.keymapCapture = ""
	a.keymapPendingAction, a.keymapPendingChord, a.keymapPendingOthers = "", "", nil
	a.settingsNote = ""
}

// resetKeymapAction forgets one custom bind, restoring its default.
func (a *App) resetKeymapAction(action string) {
	if a.settings.Keymap != nil {
		delete(a.settings.Keymap, action)
		if len(a.settings.Keymap) == 0 {
			a.settings.Keymap = nil
		}
	}
	a.persistSettings()
}

// resetKeymapAll forgets every custom bind.
func (a *App) resetKeymapAll() {
	a.settings.Keymap = nil
	a.cancelKeymapPending()
	a.persistSettings()
}

// countScanHidden counts the workspace files one scan list would hide.
func (a *App) countScanHidden(scan []string) int {
	m := compileExcludeMatcher(scan)
	if m == nil || a.ws == nil {
		return 0
	}
	n := 0
	for _, meta := range a.ws.Files {
		if meta != nil && m.matchSlash(meta.Path, false) {
			n++
		}
	}
	return n
}

// overbroadExcludes reports whether applying scan globs would hide too
// much: the tree would empty or lose over half its files. Such edits need
// an explicit confirm so a stray "**" cannot blank the explorer.
func (a *App) overbroadExcludes(scan []string) (bool, string) {
	if a.ws == nil || len(a.ws.Files) == 0 {
		return false, ""
	}
	if compileExcludeMatcher(scan) == nil {
		return false, ""
	}
	hidden := a.countScanHidden(scan)
	total := len(a.ws.Files)
	if hidden >= total && total > 0 {
		return true, "These patterns would hide every file in the explorer."
	}
	if hidden*2 > total {
		return true, fmt.Sprintf("These patterns would hide %d of %d files.", hidden, total)
	}
	return false, ""
}

// applyPendingExcludes installs the parked overbroad scan/search edit and
// rescans with it.
func (a *App) applyPendingExcludes() {
	scan := append([]string(nil), a.pendingExcludesScan...)
	search := append([]string(nil), a.pendingExcludesSearch...)
	a.pendingExcludesScan, a.pendingExcludesSearch = nil, nil
	e := a.settings.Excludes
	e.Scan = cleanGlobList(scan)
	e.Search = cleanGlobList(search)
	a.scanHidden = a.countScanHidden(e.Scan)
	a.setScanExcludes(e)
	a.scanDraft, a.scanTouched = "", false
	a.searchDraft, a.searchTouched = "", false
	a.settingsNote = ""
	if !a.ws.Sample && a.ws.Root != "" {
		if next, delta, err := rescanDiskWorkspaceExcludes(a.ws.Root, a.ws, a.scanLimit, a.scanFilter); err == nil {
			a.installRescan(next, delta)
		}
	}
	a.showToast("success", "Excludes applied", "The explorer rescanned with the new patterns.")
	a.refresh()
}

// noteInvalidGlobs refreshes the Files & Search validation note from the
// live drafts. Invalid lines are named, never saved, and never abort.
func (a *App) noteInvalidGlobs() {
	_, scanBad := parseGlobLines(a.scanDraftText())
	_, searchBad := parseGlobLines(a.searchDraftText())
	switch {
	case len(scanBad) > 0 && len(searchBad) > 0:
		a.settingsNote = fmt.Sprintf("Invalid globs are not saved: scan line %s, search line %s.", intsString(scanBad), intsString(searchBad))
	case len(scanBad) > 0:
		a.settingsNote = fmt.Sprintf("Invalid scan globs are not saved: line %s.", intsString(scanBad))
	case len(searchBad) > 0:
		a.settingsNote = fmt.Sprintf("Invalid search globs are not saved: line %s.", intsString(searchBad))
	default:
		a.settingsNote = ""
	}
}

// intsString joins 1-based line numbers for the validation note.
func intsString(ns []int) string {
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, fmt.Sprintf("%d", n))
	}
	return strings.Join(parts, ", ")
}

// applyExcludesRescan validates the drafts, confirms overbroad scan edits,
// then saves both lists and rescans with them.
func (a *App) applyExcludesRescan(_ context.Context) error {
	scan, scanBad := parseGlobLines(a.scanDraftText())
	search, searchBad := parseGlobLines(a.searchDraftText())
	if len(scanBad) > 0 || len(searchBad) > 0 {
		a.noteInvalidGlobs()
		a.refresh()
		return nil
	}
	if over, msg := a.overbroadExcludes(scan); over {
		a.pendingExcludesScan = append([]string(nil), scan...)
		a.pendingExcludesSearch = append([]string(nil), search...)
		a.confirmOpen = true
		a.confirmKind = "excludes-overbroad"
		a.confirmTitle = "Hide this much?"
		a.confirmMsg = msg + " Apply anyway? Clear the patterns and rescan to undo."
		a.refresh()
		return nil
	}
	e := a.settings.Excludes
	e.Scan = scan
	e.Search = search
	a.scanHidden = a.countScanHidden(scan)
	a.setScanExcludes(e)
	a.scanDraft, a.scanTouched = "", false
	a.searchDraft, a.searchTouched = "", false
	a.settingsNote = ""
	if !a.ws.Sample && a.ws.Root != "" {
		if next, delta, err := rescanDiskWorkspaceExcludes(a.ws.Root, a.ws, a.scanLimit, a.scanFilter); err == nil {
			a.installRescan(next, delta)
		}
	}
	a.showToast("success", "Excludes applied", "The explorer rescanned with the new patterns.")
	a.refresh()
	return nil
}
