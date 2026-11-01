package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
)

const (
	sessionMaxPaths   = 32
	sessionHashBytes  = 16
	sessionDirMode    = 0o700
	sessionFileMode   = 0o600
	sessionSplitMin   = 0.2
	sessionSplitMax   = 0.8
	sessionGroupCount = 2
	sessionHistoryMin = historyMinRatio
	sessionHistoryMax = historyMaxRatio
)

// sessionState is the layout and open-document state one workspace restores
// after a restart. Paths are workspace-relative, the two tab lists belong to
// the two editor groups, and the ratios are fractions of the editor body.
type sessionState struct {
	Root       string   `json:"root"`
	Mode       string   `json:"mode"`
	Theme      string   `json:"theme,omitempty"`
	Recents    []string `json:"recents"`
	Tabs       []string `json:"tabs"`
	Active     int      `json:"active"`
	Tabs2      []string `json:"tabs2"`
	Active2    int      `json:"active2"`
	FocusGroup int      `json:"focusGroup"`
	Split      bool     `json:"split"`
	SplitRatio float64  `json:"splitRatio"`
	Details    bool     `json:"details"`
	DevTools   bool     `json:"devTools"`
	Wrap       bool     `json:"wrap"`
	Preview    bool     `json:"preview"`
	DiffSplit  bool     `json:"diffSplit"`
	// NoSticky persists the sticky-scroll toggle. Missing (old sessions)
	// means enabled, matching the fresh-app default.
	NoSticky      bool    `json:"noSticky,omitempty"`
	HistoryRatio  float64 `json:"historyRatio"`
	HistoryManual bool    `json:"historyManual"`
	// Match toggles for the find/replace bar and the workspace search.
	// Queries are never persisted; only the sticky toggles survive.
	FindCase        bool    `json:"findCase,omitempty"`
	FindRegex       bool    `json:"findRegex,omitempty"`
	FindWholeWord   bool    `json:"findWholeWord,omitempty"`
	SearchCase      bool    `json:"searchCase,omitempty"`
	SearchRegex     bool    `json:"searchRegex,omitempty"`
	SearchWholeWord bool    `json:"searchWholeWord,omitempty"`
	DrawerExtra     int     `json:"drawerExtra,omitempty"`
	Zoom            float64 `json:"zoom,omitempty"`
	// FontSize is the per-workspace base font in px. Zero means missing:
	// restore falls back to settings, then the 12px builtin. Keeping zero
	// as the missing marker preserves old sessions (which load as today).
	FontSize      float64              `json:"fontSize,omitempty"`
	BranchLogs    map[string]commitLog `json:"branchLogs,omitempty"`
	Nav           []string             `json:"nav,omitempty"`
	NavAt         int                  `json:"navAt,omitempty"`
	AutoSave      string               `json:"autoSave,omitempty"`
	AutoSaveDelay int                  `json:"autoSaveDelay,omitempty"`
	// Pinned/Pinned2 persist the per-group pin sets; PreviewTab/PreviewTab2
	// persist the single-click preview slots. The closed-tab stack (and any
	// snapshot text) is never persisted.
	Pinned      []string `json:"pinned,omitempty"`
	Pinned2     []string `json:"pinned2,omitempty"`
	PreviewTab  string   `json:"previewTab,omitempty"`
	PreviewTab2 string   `json:"previewTab2,omitempty"`
}

// defaultSessionPath returns the session file for root under the user config
// directory. The absolute root is hashed so two checkouts keep separate
// sessions while one checkout keeps the same name across runs. It does not
// create anything; saveSession makes the directory with owner-only mode.
func defaultSessionPath(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("session: absolute root: %w", err)
	}

	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("session: config dir: %w", err)
	}

	sum := sha256.Sum256([]byte(filepath.Clean(abs)))
	name := "session-" + hex.EncodeToString(sum[:sessionHashBytes]) + ".json"

	return filepath.Join(base, "ownframe", "code-view", name), nil
}

// loadSession reads one state file. A missing file is a first run, so it
// returns the zero state without an error. A damaged file also returns the
// zero state, with the decode error wrapped for the caller to report.
func loadSession(path string) (sessionState, error) {
	var state sessionState

	if strings.TrimSpace(path) == "" {
		return state, errors.New("session: empty path")
	}

	data, err := os.ReadFile(path) //nolint:gosec // the caller names the session file, either under the user config dir or in a test temp dir.
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}

	if err != nil {
		return state, fmt.Errorf("session: read %s: %w", path, err)
	}

	if err := json.Unmarshal(data, &state); err != nil {
		return sessionState{}, fmt.Errorf("session: decode %s: %w", path, err)
	}

	state.Recents = capSessionPaths(state.Recents)
	// Tab lists are unlimited: never truncate open tabs, pins, or preview
	// slots here. Recents and nav history stay budgeted.
	state.Nav = capNavPaths(state.Nav)

	return state, nil
}

// saveSession writes one state file through a temporary file in the same
// directory, then renames it into place so a crash cannot leave half a file.
// The parent directory and the file are owner-only.
func saveSession(path string, state sessionState) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("session: empty path")
	}

	state.Recents = capSessionPaths(state.Recents)
	// Tab lists are unlimited: persist every open tab. Recents and nav
	// history stay budgeted.
	state.Nav = capNavPaths(state.Nav)

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("session: encode %s: %w", path, err)
	}

	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, sessionDirMode); err != nil {
		return fmt.Errorf("session: create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return fmt.Errorf("session: temp in %s: %w", dir, err)
	}

	name := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)

		return fmt.Errorf("session: write %s: %w", name, err)
	}

	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)

		return fmt.Errorf("session: close %s: %w", name, err)
	}

	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)

		return fmt.Errorf("session: replace %s: %w", path, err)
	}

	return nil
}

// sanitize returns a state that is safe to apply: relative unique paths only,
// indices inside their tab lists, a valid focus group, ratios inside the pane
// limits, and the current root when the saved state named none.
func (s sessionState) sanitize(root string) sessionState {
	s.Root = strings.TrimSpace(s.Root)
	if s.Root == "" {
		s.Root = strings.TrimSpace(root)
	}

	s.Recents = cleanSessionPaths(s.Recents)
	// Tab lists are unlimited: clean without the 32-path budget.
	s.Tabs = cleanTabPaths(s.Tabs)
	s.Tabs2 = cleanTabPaths(s.Tabs2)
	s.Pinned = cleanTabPaths(s.Pinned)
	s.Pinned2 = cleanTabPaths(s.Pinned2)
	s.Nav = cleanNavPaths(s.Nav)

	// Pins and preview slots only survive for tabs that survived: drop pins
	// naming closed tabs and slots pointing outside their group.
	s.Pinned = keepSessionPins(s.Pinned, s.Tabs)
	s.Pinned2 = keepSessionPins(s.Pinned2, s.Tabs2)
	s.PreviewTab = keepSessionSlot(s.PreviewTab, s.Tabs, s.Pinned)
	s.PreviewTab2 = keepSessionSlot(s.PreviewTab2, s.Tabs2, s.Pinned2)

	s.Active = clampSessionIndex(s.Active, len(s.Tabs))
	s.Active2 = clampSessionIndex(s.Active2, len(s.Tabs2))
	s.FocusGroup = clampSessionIndex(s.FocusGroup, sessionGroupCount)
	s.SplitRatio = clampSessionRatio(s.SplitRatio, sessionSplitMin, sessionSplitMax)
	s.HistoryRatio = clampSessionRatio(s.HistoryRatio, sessionHistoryMin, sessionHistoryMax)
	s.DrawerExtra = clampDrawerExtra(s.DrawerExtra)
	s.Zoom = clampSessionZoom(s.Zoom)
	// FontSize stays zero when missing so restore can fall back to
	// settings → builtin. Non-zero values clamp to 8..24.
	if s.FontSize != 0 {
		s.FontSize = clampFontSize(s.FontSize)
	}
	s.NavAt = clampHistoryIndex(s.NavAt, len(s.Nav))
	// Auto-save keeps the zero value untouched so older sessions compare
	// equal; restoreState applies the off/1000 ms defaults. Unknown modes
	// fall back to off and wild delays clamp into range.
	if s.AutoSave != "" {
		s.AutoSave = validAutoSave(strings.TrimSpace(s.AutoSave))
	}
	if s.AutoSaveDelay != 0 {
		s.AutoSaveDelay = clampAutoSaveDelay(s.AutoSaveDelay)
	}

	return s
}

// keepSessionPins drops pin entries that name tabs no longer open.
func keepSessionPins(pins, tabs []string) []string {
	if len(pins) == 0 || len(tabs) == 0 {
		return nil
	}

	open := make(map[string]bool, len(tabs))
	for _, path := range tabs {
		open[path] = true
	}

	var out []string

	for _, path := range pins {
		if open[path] {
			out = append(out, path)
		}
	}

	return out
}

// keepSessionSlot keeps a preview slot only when it names an open, unpinned
// tab in its group.
func keepSessionSlot(slot string, tabs, pins []string) string {
	if slot == "" || len(tabs) == 0 {
		return ""
	}

	open := false
	for _, path := range tabs {
		if path == slot {
			open = true

			break
		}
	}

	if !open {
		return ""
	}

	for _, path := range pins {
		if path == slot {
			return ""
		}
	}

	return slot
}

// cleanTabPaths trims, normalizes, and de-duplicates tab paths, drops
// entries that are absolute or climb out of the workspace, and keeps every
// entry: tabs are unlimited, so no budget applies.
func cleanTabPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}

	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))

	for _, raw := range paths {
		path, ok := cleanSessionPath(raw)
		if !ok {
			continue
		}

		if _, dup := seen[path]; dup {
			continue
		}

		seen[path] = struct{}{}
		out = append(out, path)
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// cleanSessionPaths trims, normalizes, and de-duplicates paths, drops entries
// that are absolute or climb out of the workspace, and keeps the first 32.
func cleanSessionPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}

	out := make([]string, 0, min(len(paths), sessionMaxPaths))
	seen := make(map[string]struct{}, min(len(paths), sessionMaxPaths))

	for _, raw := range paths {
		path, ok := cleanSessionPath(raw)
		if !ok {
			continue
		}

		if _, dup := seen[path]; dup {
			continue
		}

		seen[path] = struct{}{}
		out = append(out, path)

		if len(out) == sessionMaxPaths {
			break
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// cleanSessionPath turns one saved path into a workspace-relative slash path.
// It rejects absolute paths, Windows volume paths, and any parent segment.
func cleanSessionPath(raw string) (string, bool) {
	path := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if path == "" || strings.ContainsRune(path, '\x00') {
		return "", false
	}

	if pathpkg.IsAbs(path) || (len(path) >= 2 && path[1] == ':') {
		return "", false
	}

	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return "", false
		}
	}

	path = pathpkg.Clean(path)
	if path == "." || path == ".." || strings.HasPrefix(path, "../") {
		return "", false
	}

	return path, true
}

// capSessionPaths keeps a loaded or saved list inside the persistence budget
// without touching the caller's entries.
func capSessionPaths(paths []string) []string {
	if len(paths) > sessionMaxPaths {
		return paths[:sessionMaxPaths]
	}

	return paths
}

// clampSessionIndex keeps an index inside a list of length items, with zero
// as the empty-list index.
func clampSessionIndex(index, length int) int {
	if index < 0 || length <= 0 {
		return 0
	}

	if index >= length {
		return length - 1
	}

	return index
}

// clampSessionRatio keeps a pane fraction inside its usable range.
func clampSessionRatio(value, low, high float64) float64 {
	if value < low {
		return low
	}

	if value > high {
		return high
	}

	return value
}

// clampSessionZoom keeps a restored layout zoom inside the usable range,
// defaulting a missing or wild value to 100 percent.
func clampSessionZoom(value float64) float64 {
	if value < zoomMin || value > zoomMax {
		return 1
	}

	return value
}

// capNavPaths keeps a navigation history inside its budget without touching
// the caller's entries.
func capNavPaths(paths []string) []string {
	if len(paths) > navMaxEntries {
		return paths[len(paths)-navMaxEntries:]
	}

	return paths
}

// cleanNavPaths trims and normalizes navigation entries, drops absolute or
// escaping paths, collapses consecutive duplicates, and keeps the newest
// entries inside the navigation budget.
func cleanNavPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}

	out := make([]string, 0, min(len(paths), navMaxEntries))

	for _, raw := range paths {
		path, ok := cleanSessionPath(raw)
		if !ok {
			continue
		}

		if len(out) > 0 && out[len(out)-1] == path {
			continue
		}

		out = append(out, path)
	}

	if len(out) > navMaxEntries {
		out = append([]string(nil), out[len(out)-navMaxEntries:]...)
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// clampHistoryIndex keeps a navigation index inside its history, with zero
// as the empty-history index.
func clampHistoryIndex(index, length int) int {
	if length <= 0 {
		return 0
	}

	if index < 0 {
		return 0
	}

	if index >= length {
		return length - 1
	}

	return index
}
