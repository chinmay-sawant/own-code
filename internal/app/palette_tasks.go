package app

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

const (
	taskMaxPerSource = 30
	taskFileLimit    = 512 << 10
)

// taskDef is one runnable task. ID is stable (kind:name) and is what the
// palette round-trips; Line is the exact text typed into the shell and is
// rebuilt from validated parts, never from user-typed palette text.
type taskDef struct {
	ID    string
	Label string
	Line  string
}

var (
	taskNameRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,63}$`)
	makeTargetRE = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_.-]*)\s*:($|[^=])`)
)

// detectTasks finds Makefile targets, package.json scripts, go test and
// cargo tasks in root. Only names that pass taskNameRE are offered, so a
// hostile Makefile or package.json cannot smuggle shell syntax into the
// line typed in the terminal.
func detectTasks(root string) []taskDef {
	var out []taskDef

	out = append(out, makeTasks(root)...)
	out = append(out, npmTasks(root)...)

	if fileExists(filepath.Join(root, "go.mod")) {
		out = append(out,
			taskDef{ID: "go:test", Label: "go test ./...", Line: "go test ./..."},
			taskDef{ID: "go:build", Label: "go build ./...", Line: "go build ./..."},
			taskDef{ID: "go:vet", Label: "go vet ./...", Line: "go vet ./..."},
		)
	}

	if fileExists(filepath.Join(root, "Cargo.toml")) {
		out = append(out,
			taskDef{ID: "cargo:build", Label: "cargo build", Line: "cargo build"},
			taskDef{ID: "cargo:test", Label: "cargo test", Line: "cargo test"},
		)
	}

	return out
}

func fileExists(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}

// makeTasks lists plain targets of the first Makefile found. Pattern rules,
// special targets (.PHONY) and variable assignments are skipped.
func makeTasks(root string) []taskDef {
	var file string

	for _, name := range []string{"Makefile", "makefile", "GNUmakefile"} {
		if fileExists(filepath.Join(root, name)) {
			file = filepath.Join(root, name)

			break
		}
	}

	if file == "" {
		return nil
	}

	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	var out []taskDef

	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 256<<10)

	for sc.Scan() && len(out) < taskMaxPerSource {
		line := sc.Text()
		if line == "" || line[0] == '\t' || line[0] == ' ' || line[0] == '#' {
			continue
		}

		m := makeTargetRE.FindStringSubmatch(line)
		if m == nil || !taskNameRE.MatchString(m[1]) || seen[m[1]] {
			continue
		}

		seen[m[1]] = true
		out = append(out, taskDef{ID: "make:" + m[1], Label: "make " + m[1], Line: "make " + m[1]})
	}

	return out
}

// npmTasks lists package.json scripts, run with the package manager the
// lockfile implies.
func npmTasks(root string) []taskDef {
	path := filepath.Join(root, "package.json")

	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > taskFileLimit {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}

	if json.Unmarshal(data, &pkg) != nil {
		return nil
	}

	runner := "npm run"
	kind := "npm"

	switch {
	case fileExists(filepath.Join(root, "pnpm-lock.yaml")):
		runner, kind = "pnpm run", "pnpm"
	case fileExists(filepath.Join(root, "yarn.lock")):
		runner, kind = "yarn run", "yarn"
	}

	names := make([]string, 0, len(pkg.Scripts))
	for name := range pkg.Scripts {
		if taskNameRE.MatchString(name) {
			names = append(names, name)
		}
	}

	sort.Strings(names)

	if len(names) > taskMaxPerSource {
		names = names[:taskMaxPerSource]
	}

	out := make([]taskDef, 0, len(names))
	for _, name := range names {
		out = append(out, taskDef{ID: kind + ":" + name, Label: runner + " " + name, Line: runner + " " + name})
	}

	return out
}

// runTask types one detected task into the integrated terminal, opening it
// when needed. The task is re-resolved by id from a fresh detection.
func (a *App) runTask(ctx context.Context, id string) {
	var task *taskDef

	if a.ws != nil && !a.ws.Sample && a.ws.Root != "" {
		for _, t := range detectTasks(a.ws.Root) {
			if t.ID == id {
				t := t
				task = &t

				break
			}
		}
	}

	if task == nil {
		a.showToast("info", "Task not found", "That task is no longer defined in this workspace.")

		return
	}

	if !a.termOpen || a.term == nil {
		a.toggleTerminal(ctx)
	} else {
		a.focusTerminal(ctx)
	}

	a.writeTerminal([]byte(task.Line + "\r"))
	a.showToast("info", "Task started", task.Line)
}
