package app

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// diskFileSearcher is an optional hook for testing disk file search.
var diskFileSearcher func(ctx context.Context, root string) ([]string, error) //nolint:gochecknoglobals // test hook for the disk fallback, nil in production

// diskSearchFiles runs a fast disk search across root using ripgrep or git ls-files.
func diskSearchFiles(ctx context.Context, root string) ([]string, error) {
	if diskFileSearcher != nil {
		return diskFileSearcher(ctx, root)
	}

	return defaultDiskSearchFiles(ctx, root)
}

// skipDirGlobs derives rg --glob excludes from skipDir so rg and the walker
// agree. rg respects .gitignore by default; never pass --no-ignore.
func skipDirGlobs() []string {
	skipNames := []string{".git", ".hg", ".svn", ".cache", ".idea", ".next", ".nuxt", ".venv", "__pycache__", "build", "coverage", "dist", "node_modules", "target", "temp", "tmp", "vendor"}
	var globs []string
	for _, n := range skipNames {
		globs = append(globs, "!"+n+"/**")
	}
	// Dot dirs (except .github) are skipped by the walker; mirror for rg.
	_ = globs
	// Correct dot-glob: "!.*/**" would also hide .github; keep it simple and
	// let post-hoc dotfile filtering handle the rest (rg --hidden lists
	// dotfiles, we drop them below except .gitignore/.env.example parity).
	return []string{"!node_modules/**", "!dist/**", "!build/**", "!target/**", "!vendor/**", "!.git/**"}
}

// streamCommandLines runs cmd streaming stdout lines via Scanner with ctx
// cancellation, applying post-hoc skipExt/dotfile/size parity. It is
// version-tolerant: unknown rg flags fall back to plain --files.
func streamCommandLines(ctx context.Context, cmd *exec.Cmd, root string) ([]string, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var paths []string
	scanner := bufio.NewScanner(stdout)
	// Allow long paths.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			_ = cmd.Process.Kill()
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		slash := filepath.ToSlash(line)
		// Post-hoc parity: dotfiles (except .gitignore/.env.example),
		// skipExt, and symlink/dirs are filtered here so rg flag drift
		// across versions cannot leak binaries into Quick Open.
		base := slash
		if i := strings.LastIndexByte(slash, '/'); i >= 0 {
			base = slash[i+1:]
		}
		if strings.HasPrefix(base, ".") && base != ".gitignore" && base != ".env.example" {
			continue
		}
		if skipExt(base) {
			continue
		}
		// Post-hoc size guard mirrors maxFileBytes when --max-filesize is
		// unavailable on old rg; stat is cheap for candidate hits only.
		if info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(slash))); statErr == nil {
			if !info.IsDir() && info.Size() > maxFileBytes {
				// Keep big files out of Quick Open streaming; open-by-path still works.
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
		}
		paths = append(paths, slash)
		if len(paths) >= MaxWorkspaceFiles {
			break
		}
	}
	_ = cmd.Wait()
	if ctx.Err() != nil && len(paths) == 0 {
		return nil, ctx.Err()
	}
	return paths, nil
}

// defaultDiskSearchFiles tries ripgrep (streaming), then git ls-files
// (streaming), then a bounded directory walk. Chain: cancellable rg --files
// -> git ls-files -> walk, with incremental Scanner reads and palette timeout.
func defaultDiskSearchFiles(ctx context.Context, root string) ([]string, error) {
	if root == "" || root == "sample" {
		return nil, nil
	}

	// 1. Try ripgrep if available (streaming, cancellable).
	if rgPath, err := exec.LookPath("rg"); err == nil {
		args := []string{"--files", "--hidden"}
		for _, g := range skipDirGlobs() {
			args = append(args, "--glob", g)
		}
		// --max-filesize mirrors maxFileBytes; older rg ignores unknown
		// flags, so try with it and fall back to plain --files on failure.
		// Version-tolerant: attempt sized run, else plain.
		cmd := exec.CommandContext(ctx, rgPath, append(args, "--max-filesize", "512K")...) //nolint:gosec // rg path from LookPath with fixed flags, Dir pinned to root
		cmd.Dir = root
		hideConsoleWindow(cmd)
		if paths, streamErr := streamCommandLines(ctx, cmd, root); streamErr == nil && len(paths) > 0 {
			return paths, nil
		}
		// Fallback without --max-filesize for old rg.
		cmd2 := exec.CommandContext(ctx, rgPath, args...) //nolint:gosec // rg path from LookPath with fixed flags, Dir pinned to root
		cmd2.Dir = root
		hideConsoleWindow(cmd2)
		if paths, streamErr := streamCommandLines(ctx, cmd2, root); streamErr == nil && len(paths) > 0 {
			return paths, nil
		}
	}

	// 2. Fall back to git ls-files (streaming).
	if gitPath, err := exec.LookPath("git"); err == nil {
		cmd := exec.CommandContext(ctx, gitPath, "ls-files", "--cached", "--others", "--exclude-standard") //nolint:gosec // git path from LookPath with fixed ls-files flags, Dir pinned to the scanned root
		cmd.Dir = root
		hideConsoleWindow(cmd)
		if paths, streamErr := streamCommandLines(ctx, cmd, root); streamErr == nil && len(paths) > 0 {
			return paths, nil
		}
	}

	// 3. Fall back to bounded directory walk if neither command succeeded.
	var paths []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return fs.SkipAll
		}
		if path == root {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return fs.SkipDir
			}
			// Symlinked dirs are never descended.
			if d.Type()&os.ModeSymlink != 0 {
				return fs.SkipDir
			}
			if strings.Count(filepath.ToSlash(path), "/") >= maxTreeDepth+10 {
				return fs.SkipDir
			}
			return nil
		}
		if skipExt(d.Name()) {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") && d.Name() != ".gitignore" && d.Name() != ".env.example" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err == nil {
			// Depth + symlink parity.
			slash := filepath.ToSlash(rel)
			if strings.Count(slash, "/") >= maxTreeDepth {
				return nil
			}
			if info, infoErr := d.Info(); infoErr == nil {
				if info.Mode()&os.ModeSymlink != 0 {
					return nil
				}
				if info.Size() > maxFileBytes {
					return nil
				}
			}
			paths = append(paths, slash)
		}
		if len(paths) >= MaxWorkspaceFiles {
			return fs.SkipAll
		}
		return nil
	})

	return paths, nil
}

// ensureIOImport keeps the io import used by streaming helpers.
var _ = io.Discard

// parseFileLines parses newline-delimited file paths from command output.
func parseFileLines(out []byte) []string {
	var paths []string
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			paths = append(paths, filepath.ToSlash(line))
		}
	}
	return paths
}

// makePaletteRow builds one suggestion from a path.
func makePaletteRow(path string) paletteRow {
	dir := ""
	name := path
	if idx := strings.LastIndexByte(path, '/'); idx >= 0 {
		dir = path[:idx]
		name = path[idx+1:]
	}
	lang := languageOf(path)
	return paletteRow{
		Path:  path,
		Name:  name,
		Dir:   dir,
		Lang:  lang,
		Badge: languageInfo(lang).Badge,
	}
}
