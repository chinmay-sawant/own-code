package app

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/wsl"
)

// blockedProjectRoot reports whether root is a drive or filesystem root that
// must not open as a project. Drive roots scan too much and hide the real
// project name, so the picker redirects to a folder inside instead.
func blockedProjectRoot(root string) bool {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return false
	}

	if trimmed == "/" {
		return true
	}

	// The WSL distro list and a bare distro root (\\wsl$\<distro> -> /)
	// would scan an entire guest filesystem, so they need a folder inside.
	if wsl.IsWSLDistrosRoot(trimmed) || wsl.IsWSLDistroRoot(trimmed) {
		return true
	}

	// Windows drive roots like C:, C:\, or C:/.
	if len(trimmed) == 2 && wsl.IsDriveLetter(trimmed[0]) && trimmed[1] == ':' {
		return true
	}

	if len(trimmed) == 3 && wsl.IsDriveLetter(trimmed[0]) && trimmed[1] == ':' &&
		(trimmed[2] == '\\' || trimmed[2] == '/') {
		return true
	}

	// WSL drive mounts like /mnt/c.
	if len(trimmed) == len("/mnt/c") && strings.HasPrefix(trimmed, "/mnt/") &&
		wsl.IsDriveLetter(trimmed[len(trimmed)-1]) {
		return true
	}

	return false
}

// browseStartRoot is the directory the folder browser opens on: the current
// project root when it is still on disk, else the home directory, else /.
func (a *App) browseStartRoot() string {
	if a.ws != nil && !a.ws.Sample && isDir(a.ws.Root) {
		return a.ws.Root
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" && isDir(home) {
		return home
	}

	return "/"
}

// startBrowse opens the folder browser on the last known directory.
func (a *App) startBrowse() {
	a.browseOpen = true
	a.browseRoot = a.browseStartRoot()
	a.browseFilter = ""
	a.browseDrag = false
	a.browseDragScale = 0
	if a.page != nil {
		a.page.Form().SetFormValue("browse-filter", "")
	}
	a.browseList.reset()
	a.fileAlert = ""
}

// setBrowseRoot moves the browser to root, clearing the folder filter and
// parking the list at the first row. Every Up/enter/WSL navigation path
// funnels here so a stale query can never hide the new directory's rows.
func (a *App) setBrowseRoot(root string) {
	a.browseRoot = root
	if a.browseFilter != "" {
		a.browseFilter = ""
		if a.page != nil {
			a.page.Form().SetFormValue("browse-filter", "")
		}
	}
	a.browseList.reset()
	a.fileAlert = ""
}

// browseMaxEntries caps the folder browser list. The browser shows immediate
// subdirectories only, so a deep root cannot flood the picker.
const browseMaxEntries = 100

// listWSLDistros lists installed distros. It is a variable so tests can stub
// the wsl.exe call on machines without WSL.
var listWSLDistros = wsl.ListDistros //nolint:gochecknoglobals // stubbed in tests

// browseEntries lists up to browseMaxEntries immediate subdirectories of the
// root, sorted by name. Unreadable entries are skipped silently. An empty
// root is the virtual drives level (This PC) and lists every existing
// Windows drive instead of reading a directory.
func (a *App) browseEntries() []recentRootRow {
	rows := a.browseAllEntries()
	if len(rows) > browseMaxEntries {
		rows = rows[:browseMaxEntries]
	}

	return rows
}

// browseAllEntries lists every immediate subdirectory of the root, sorted by
// name, without the browseMaxEntries cap. The filtered list uses it so a
// query can reach folders past the first capped page; the cap is reapplied
// after filtering.
func (a *App) browseAllEntries() []recentRootRow {
	if a.browseRoot == "" {
		return availableDrives(isDir)
	}

	// The virtual WSL level lists installed distros instead of reading a
	// directory, so Windows users can reach \\wsl$\<distro> from the picker.
	if wsl.IsWSLDistrosRoot(a.browseRoot) {
		return browseWSLDistroRows()
	}

	// WSL UNC paths join with backslashes to keep the \\wsl$\ form that
	// Windows hands out; filepath.Join would keep forward slashes on the
	// Linux test host and break the round trip.
	if wsl.IsWSLPath(a.browseRoot) {
		ents, err := os.ReadDir(a.browseRoot)
		if err != nil {
			return nil
		}
		rows := make([]recentRootRow, 0, len(ents))
		for _, ent := range ents {
			full := joinWSLChild(a.browseRoot, ent.Name())
			if !isDir(full) {
				continue
			}
			rows = append(rows, recentRootRow{Path: full, Name: ent.Name()})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		return rows
	}

	ents, err := os.ReadDir(a.browseRoot)
	if err != nil {
		return nil
	}

	rows := make([]recentRootRow, 0, len(ents))

	for _, ent := range ents {
		full := filepath.Join(a.browseRoot, ent.Name())
		if !isDir(full) {
			continue
		}

		rows = append(rows, recentRootRow{Path: full, Name: ent.Name()})
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })

	return rows
}

// browseFilteredEntries lists the current root's subdirectories matching the
// folder filter. An empty filter returns the capped sorted list; otherwise
// the full directory is searched case-insensitively so a query reaches past
// the first capped page, and the cap is reapplied to the matches.
func (a *App) browseFilteredEntries() []recentRootRow {
	q := strings.TrimSpace(a.browseFilter)
	if q == "" {
		return a.browseEntries()
	}
	rows := a.browseAllEntries()
	lower := strings.ToLower(q)
	kept := make([]recentRootRow, 0, len(rows))
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.Name), lower) {
			kept = append(kept, r)
		}
		if len(kept) >= browseMaxEntries {
			break
		}
	}
	return kept
}

// browseDisplayName is the base name of the browser root, or the root
// itself when it has no base name (for example /). The virtual drives
// level (empty root) shows as This PC and the virtual WSL level as WSL.
func (a *App) browseDisplayName() string {
	if a.browseRoot == "" {
		return "This PC"
	}

	if wsl.IsWSLDistrosRoot(a.browseRoot) {
		return "WSL"
	}

	if wsl.IsWSLPath(a.browseRoot) || isWindowsDrivePath(a.browseRoot) {
		if name := browseBaseName(a.browseRoot); name != "" {
			return name
		}
		return a.browseRoot
	}

	if name := baseName(a.browseRoot); name != "" {
		return name
	}

	return a.browseRoot
}

// browseWSLDistroRows lists installed distros as browse rows. The path
// keeps the canonical \\wsl$\<distro> form so entering it stays a WSL path.
func browseWSLDistroRows() []recentRootRow {
	distros := listWSLDistros()
	rows := make([]recentRootRow, 0, len(distros))
	for _, d := range distros {
		name := strings.TrimSpace(d)
		if name == "" {
			continue
		}
		rows = append(rows, recentRootRow{Path: wsl.WSLDistrosRoot + `\` + name, Name: name})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

// joinWSLChild appends a child name to a WSL UNC root with a backslash,
// preserving the \\wsl$\ form Windows hands out.
func joinWSLChild(root, name string) string {
	trimmed := strings.TrimRight(root, `/\`)
	if trimmed == "" {
		return name
	}
	return trimmed + `\` + name
}

// browseBaseName returns the last element splitting on both separators, so
// UNC and drive paths display correctly on every host OS.
func browseBaseName(path string) string {
	trimmed := strings.TrimRight(path, `/\`)
	if trimmed == "" {
		return ""
	}
	for i := len(trimmed) - 1; i >= 0; i-- {
		if trimmed[i] == '/' || trimmed[i] == '\\' {
			return trimmed[i+1:]
		}
	}
	return trimmed
}

// wslParentDir returns the UNC parent of a WSL path. A bare distro root
// climbs to the virtual distro list.
func wslParentDir(root string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(root), `/\`)
	idx := -1
	for i := len(trimmed) - 1; i >= 0; i-- {
		if trimmed[i] == '/' || trimmed[i] == '\\' {
			idx = i
			break
		}
	}
	if idx < 0 {
		return trimmed
	}
	parent := trimmed[:idx]
	if parent == "" {
		return wsl.WSLDistrosRoot
	}
	return parent
}

// isBrowseDriveRoot reports a Windows drive root such as C:, C:\, or C:/.
// Only drive roots step out to the virtual drives level on Up; the Unix
// root / keeps its existing behavior and never becomes drives.
func isBrowseDriveRoot(root string) bool {
	if len(root) == 2 && wsl.IsDriveLetter(root[0]) && root[1] == ':' {
		return true
	}

	if len(root) == 3 && wsl.IsDriveLetter(root[0]) && root[1] == ':' &&
		(root[2] == '\\' || root[2] == '/') {
		return true
	}

	return false
}

// isWindowsDrivePath reports a path on a Windows drive (D:\, D:/, D:foo).
// The check is textual so drive logic stays testable on every OS.
func isWindowsDrivePath(path string) bool {
	return len(path) >= 2 && wsl.IsDriveLetter(path[0]) && path[1] == ':'
}

// driveSepIndex is the separator position in a drive-rooted path: D:\x
// carries its first inner separator at index 2, and anything at or below
// that index is the root itself.
const driveSepIndex = 2

// browseParentDir returns the parent of root. WSL UNC paths climb with both
// separators so Up behaves the same on every OS; Windows drive paths compare
// both separators explicitly; anything else follows the host filepath rules.
func browseParentDir(root string) string {
	if wsl.IsWSLPath(root) {
		if wsl.IsWSLDistroRoot(root) {
			return wsl.WSLDistrosRoot
		}
		return wslParentDir(root)
	}
	if isWindowsDrivePath(root) {
		if isBrowseDriveRoot(root) {
			return root
		}

		end := len(root)
		for end > 3 && (root[end-1] == '/' || root[end-1] == '\\') {
			end--
		}

		trimmed := root[:end]
		for i := len(trimmed) - 1; i >= 2; i-- {
			if trimmed[i] == '/' || trimmed[i] == '\\' {
				if i == driveSepIndex {
					return trimmed[:2] + `\`
				}

				return trimmed[:i]
			}
		}

		return root[:2]
	}

	return filepath.Dir(root)
}

// browseUpTarget resolves one Up step from root. ok is false when Up goes
// nowhere: the drives level itself, or the Unix root /. A drive root steps
// out to the virtual drives level, reported as the empty sentinel. The WSL
// distro list steps back to This PC, and a bare distro root steps to that
// list, so Windows users can round-trip between drives and WSL.
func browseUpTarget(root string) (next string, ok bool) {
	if root == "" {
		return "", false
	}

	if wsl.IsWSLDistrosRoot(root) {
		return "", true
	}

	if _, sub, isWSL := wsl.ParseWSLRoot(root); isWSL {
		if sub == "/" {
			return wsl.WSLDistrosRoot, true
		}
		parent := browseParentDir(root)
		if parent == "" || parent == root {
			return root, false
		}
		return parent, true
	}

	if isBrowseDriveRoot(root) {
		return "", true
	}

	parent := browseParentDir(root)
	if parent == "" || parent == root {
		return root, false
	}

	return parent, true
}

// driveCapHint sizes the drive row slice for the common few-drive case;
// availableDrives grows it when a machine has more.
const driveCapHint = 4

// availableDrives probes A:\ through Z:\ and returns one row per existing
// drive. exists is isDir in the browser and a stub in tests, so the level
// stays deterministic on machines without drive letters. The 26 letters
// bound the rows; browseMaxEntries caps them further.
func availableDrives(exists func(string) bool) []recentRootRow {
	rows := make([]recentRootRow, 0, driveCapHint)

	for c := byte('A'); c <= byte('Z'); c++ {
		drive := string([]byte{c, ':', '\\'})
		if !exists(drive) {
			continue
		}

		rows = append(rows, recentRootRow{Path: drive, Name: drive})

		if len(rows) >= browseMaxEntries {
			break
		}
	}

	return rows
}
