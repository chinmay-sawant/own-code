package app

import (
	"path/filepath"
	"strings"
)

// crumbSegment is one clickable breadcrumb part: a folder's Path is its dir
// path, the leaf's Path is the file path. Index is the display position.
// Ellipsis marks the collapsed leading "…" that opens the root menu.
type crumbSegment struct {
	Name     string
	Path     string
	IsDir    bool
	Index    int
	Ellipsis bool
}

// siblingRow is one entry in a segment's dropdown: a peer file or folder.
type siblingRow struct {
	Name  string
	Path  string
	IsDir bool
}

// crumbSegments splits an active path into folder segments plus the file
// leaf. External absolute files split their full path; folder clicks on
// them reveal nothing (openCrumb guards).
func crumbSegments(active string) []crumbSegment {
	clean := filepath.ToSlash(active)
	parts := strings.Split(clean, "/")

	segs := make([]crumbSegment, 0, len(parts))

	for i, part := range parts {
		if part == "" {
			continue
		}

		last := i == len(parts)-1
		path := strings.Join(parts[:i+1], "/")

		segs = append(segs, crumbSegment{
			Name:  part,
			Path:  path,
			IsDir: !last,
			Index: len(segs),
		})
	}

	return segs
}

// crumbSegmentsView returns the display segments using the current frame
// geometry.
func (a *App) crumbSegmentsView() []crumbSegment {
	return a.crumbSegmentsFor(a.view.PaneWidth, a.view.ActiveNameShort)
}

// crumbSegmentsFor returns the display segments for one pane width,
// collapsing leading folders into one "…" button when the joined names
// exceed the file-crumb rune budget. The wrapper keeps its #crumb-path id
// and the flex bar keeps everything on one line.
func (a *App) crumbSegmentsFor(paneWidth int, clippedLeaf string) []crumbSegment {
	doc := a.groupDoc(0)
	if doc == nil || doc.Path == "" {
		return nil
	}

	full := crumbSegments(doc.Path)
	if len(full) == 0 {
		return nil
	}

	winW, _ := a.page.Size()
	budget := fileCrumbBudget(paneWidth, winW)

	total := 0
	for i, seg := range full {
		total += len([]rune(seg.Name))
		if i > 0 {
			total++
		}
	}

	if total <= budget {
		return full
	}

	// Narrow: the leaf keeps the clipped name the header already shows,
	// trailing folders fill the remainder, and the rest collapse.
	leaf := full[len(full)-1]
	leaf.Name = clippedLeaf
	if leaf.Name == "" {
		leaf.Name = leafNameFallback(full)
	}

	avail := budget - len([]rune(leaf.Name))
	kept := []crumbSegment{leaf}

	for i := len(full) - 2; i >= 0; i-- {
		need := len([]rune(full[i].Name)) + 1
		if avail < need {
			break
		}

		avail -= need
		kept = append([]crumbSegment{full[i]}, kept...)
	}

	if len(kept) < len(full) {
		kept = append([]crumbSegment{{Name: "…", Path: "", IsDir: true, Ellipsis: true}}, kept...)
	}

	for i := range kept {
		kept[i].Index = i
	}

	return kept
}

// leafNameFallback keeps a display name when clipping left nothing.
func leafNameFallback(full []crumbSegment) string {
	if len(full) == 0 {
		return ""
	}

	return full[len(full)-1].Name
}

// crumbSiblingsForMenu lists the dropdown rows for the open menu segment:
// the children of its parent directory, dirs first in tree order.
func (a *App) crumbSiblingsForMenu() []siblingRow {
	if !a.crumbMenuOpen {
		return nil
	}

	return a.siblingRowsForSegment(a.crumbSegmentsView(), a.crumbMenu)
}

// siblingRowsForSegment lists the dropdown rows for one display segment.
func (a *App) siblingRowsForSegment(segs []crumbSegment, index int) []siblingRow {
	if index < 0 || index >= len(segs) {
		return nil
	}

	seg := segs[index]

	parent := ""
	if !seg.Ellipsis {
		parent = dirName(seg.Path)
	}

	return a.siblingRows(parent)
}

// siblingRows lists the direct children of parent ("" for the root).
func (a *App) siblingRows(parent string) []siblingRow {
	if a.ws == nil {
		return nil
	}

	var kids []*fileNode

	if parent == "" {
		kids = a.ws.Tree
	} else {
		node := findTreeNode(a.ws.Tree, parent)
		if node == nil || !node.Dir {
			return nil
		}

		if !node.Loaded {
			_ = a.ws.loadDir(parent)
			node = findTreeNode(a.ws.Tree, parent)
		}

		if node == nil {
			return nil
		}

		kids = node.Children
	}

	rows := make([]siblingRow, 0, len(kids))

	for _, kid := range kids {
		rows = append(rows, siblingRow{Name: kid.Name, Path: kid.Path, IsDir: kid.Dir})
	}

	return rows
}

// openCrumb reveals a folder segment in the explorer without navigating
// away from the file. The leaf is a no-op (copy-path/copy-file stay
// canonical); external absolute paths never reveal.
func (a *App) openCrumb(path string) {
	a.crumbMenuOpen = false
	a.crumbMenu = 0

	if path == "" || filepath.IsAbs(filepath.FromSlash(path)) {
		a.refresh()

		return
	}

	if a.ws == nil || (a.ws.ByPath[path] == nil && findTreeNode(a.ws.Tree, path) == nil) {
		a.refresh()

		return
	}

	if !a.folders[path] {
		a.onToggleDir(path)
	} else {
		a.revealTreeFolder(path)
		a.refresh()
	}
}

// toggleCrumbMenu opens or closes the sibling dropdown for one display
// segment. Only one menu is open at a time; Esc or any non-crumb click
// closes it.
func (a *App) toggleCrumbMenu(index int) {
	segs := a.crumbSegmentsView()
	if index < 0 || index >= len(segs) {
		a.crumbMenuOpen = false
		a.refresh()

		return
	}

	if a.crumbMenuOpen && a.crumbMenu == index {
		a.crumbMenuOpen = false
		a.refresh()

		return
	}

	a.crumbMenu = index
	a.crumbMenuOpen = true
	a.refresh()
}

// openCrumbSibling opens a sibling file or reveals a sibling folder, then
// closes the menu. Like every open path it records MRU and nav history
// through openFile.
func (a *App) openCrumbSibling(path string) {
	a.crumbMenuOpen = false
	a.crumbMenu = 0

	if path == "" {
		a.refresh()

		return
	}

	if node := findTreeNode(a.ws.Tree, path); node != nil && node.Dir {
		a.openCrumb(path)

		return
	}

	a.openFile(path)
}

func fileCrumbBudget(paneWidth, winW int) int { return 80 }
