package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// createDir makes one new directory under the root. It refuses the sample
// workspace, paths outside the root, and paths the workspace or the disk
// already holds. Registration (tree nodes) stays with the caller, like
// createFile.
func (w *workspace) createDir(path string) error {
	if !w.writable() {
		return errSampleReadOnly()
	}

	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes workspace: %s", path)
	}

	rel := filepath.ToSlash(clean)
	if w.ByPath[rel] != nil || findTreeNode(w.Tree, rel) != nil {
		return fmt.Errorf("file exists %s", path)
	}

	target := filepath.Join(w.Root, clean)
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("file exists %s", path)
	}

	if err := os.MkdirAll(target, workspaceDirMode); err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}

	return nil
}

// renamePath moves one workspace path to another name in the same parent
// directory. It refuses the sample workspace, paths outside the root, a
// missing source, and an existing destination: rename never overwrites.
// Tree and index bookkeeping stays with the caller.
func (w *workspace) renamePath(oldRel, newRel string) error {
	if !w.writable() {
		return errSampleReadOnly()
	}

	oldClean := filepath.Clean(filepath.FromSlash(oldRel))
	newClean := filepath.Clean(filepath.FromSlash(newRel))

	for _, clean := range []string{oldClean, newClean} {
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("path escapes workspace: %s", clean)
		}
	}

	if filepath.ToSlash(oldClean) == filepath.ToSlash(newClean) {
		return fmt.Errorf("rename %s: same name", oldRel)
	}

	oldTarget := filepath.Join(w.Root, oldClean)
	if _, err := os.Stat(oldTarget); err != nil {
		return fmt.Errorf("rename %s: %w", oldRel, err)
	}

	newTarget := filepath.Join(w.Root, newClean)
	if _, err := os.Stat(newTarget); err == nil {
		return fmt.Errorf("file exists %s", newRel)
	}

	if w.ByPath[filepath.ToSlash(newClean)] != nil || findTreeNode(w.Tree, filepath.ToSlash(newClean)) != nil {
		return fmt.Errorf("file exists %s", newRel)
	}

	if err := os.Rename(oldTarget, newTarget); err != nil {
		return fmt.Errorf("rename %s: %w", oldRel, err)
	}

	return nil
}

// removePath deletes one workspace file or directory tree. It refuses the
// sample workspace and paths outside the root. A missing path is an error so
// callers can report it. Tree and index bookkeeping stays with the caller.
func (w *workspace) removePath(rel string) error {
	if !w.writable() {
		return errSampleReadOnly()
	}

	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes workspace: %s", rel)
	}

	target := filepath.Join(w.Root, clean)
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("remove %s: %w", rel, err)
	}

	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove %s: %w", rel, err)
	}

	return nil
}

// addDiskDir registers a directory already created under the root: it
// inserts the tree node under its parent. A missing parent is an error;
// loading a folder stays with loadDir. Files never land here.
func (w *workspace) addDiskDir(candidate string) error {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(candidate)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
		return fmt.Errorf("path escapes workspace: %s", candidate)
	}

	info, err := os.Stat(filepath.Join(w.Root, filepath.FromSlash(clean)))
	if err != nil {
		return err
	}

	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", candidate)
	}

	dir := dirName(clean)

	if dir == "" {
		if findTreeNode(w.Tree, clean) != nil {
			return nil
		}

		w.Tree = append(w.Tree, &fileNode{Path: clean, Name: baseName(clean), Dir: true})
		sortTree(w.Tree)

		return nil
	}

	parent := findTreeNode(w.Tree, dir)
	if parent == nil || !parent.Dir {
		return fmt.Errorf("no folder %s", dir)
	}

	if findChild(parent.Children, baseName(clean)) == nil {
		parent.Children = append(parent.Children, &fileNode{Path: clean, Name: baseName(clean), Dir: true})
	}

	sortTree(parent.Children)

	return nil
}

// dropPath forgets one workspace path and, for directories, its whole
// subtree: ByPath entries, Files rows, and tree nodes. Missing paths are a
// no-op so delete and rename-source cleanup share it.
func (w *workspace) dropPath(rel string) {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if clean == "." || clean == "" {
		return
	}

	for path, meta := range w.ByPath {
		if path == clean || strings.HasPrefix(path, clean+"/") {
			if meta != nil {
				w.Bytes -= meta.Size
			}

			delete(w.ByPath, path)
		}
	}

	kept := w.Files[:0]
	for _, meta := range w.Files {
		if meta == nil || meta.Path == clean || strings.HasPrefix(meta.Path, clean+"/") {
			continue
		}

		kept = append(kept, meta)
	}
	w.Files = kept

	w.Tree = dropTreeNodes(w.Tree, clean)
}

// dropTreeNodes removes the node at rel and any node below it.
func dropTreeNodes(nodes []*fileNode, rel string) []*fileNode {
	kept := nodes[:0]
	for _, node := range nodes {
		if node.Path == rel || strings.HasPrefix(node.Path, rel+"/") {
			continue
		}

		node.Children = dropTreeNodes(node.Children, rel)
		kept = append(kept, node)
	}

	return kept
}
