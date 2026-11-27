package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

// Format on save runs an external formatter over the buffer text through
// stdin/stdout before the file is written. It is configured by the "format"
// block of settings.json:
//
//	"format": {
//	  "onSave": true,
//	  "formatters": {
//	    ".go": { "command": "gofmt" },
//	    ".ts": { "command": "prettier", "args": ["--stdin-filepath", "{file}"] },
//	    ".py": { "command": "black", "args": ["-q", "-"] }
//	  }
//	}
//
// Keys are file extensions. "{file}" in an argument expands to the file's
// absolute path. Defaults: gofmt for .go and prettier for common web
// extensions. A missing tool, a non-zero exit (for example a syntax error),
// a timeout, or empty output all skip formatting silently; the save always
// proceeds with the unformatted text.

const (
	formatTimeout    = 5 * time.Second
	formatMaxBytes   = 2 << 20
	formatMaxEntries = 32
	formatMaxArgs    = 16
)

// formatSettings is the "format" block of settings.json.
type formatSettings struct {
	// OnSave is nil (default on) or an explicit true/false.
	OnSave     *bool                    `json:"onSave,omitempty"`
	Formatters map[string]formatterSpec `json:"formatters,omitempty"`
}

// formatterSpec is one external formatter. An empty Command disables the
// extension.
type formatterSpec struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// sanitize bounds the block: bad keys, control characters, and oversize
// argument lists drop.
func (s *formatSettings) sanitize() *formatSettings {
	if s == nil {
		return nil
	}

	out := &formatSettings{OnSave: s.OnSave}

	for ext, spec := range s.Formatters {
		if len(out.Formatters) >= formatMaxEntries {
			break
		}

		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext == "" || len(ext) > 32 || strings.ContainsAny(ext, "/\\\x00\r\n") {
			continue
		}

		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}

		spec.Command = strings.TrimSpace(spec.Command)
		if strings.ContainsAny(spec.Command, "\x00\r\n") {
			continue
		}

		if len(spec.Args) > formatMaxArgs {
			spec.Args = spec.Args[:formatMaxArgs]
		}

		args := make([]string, 0, len(spec.Args))

		for _, arg := range spec.Args {
			if len(arg) <= 512 && !strings.ContainsRune(arg, 0) {
				args = append(args, arg)
			}
		}

		spec.Args = args

		if out.Formatters == nil {
			out.Formatters = map[string]formatterSpec{}
		}

		out.Formatters[ext] = spec
	}

	return out
}

var prettierExts = []string{".js", ".jsx", ".ts", ".tsx", ".json", ".css", ".scss", ".html", ".md", ".yaml", ".yml"}

// formatOnSaveEnabled reports whether save runs formatters (default on).
func (a *App) formatOnSaveEnabled() bool {
	if f := a.settings.Format; f != nil && f.OnSave != nil {
		return *f.OnSave
	}

	return true
}

// formatterFor resolves the formatter for a path: a settings entry first,
// then the built-in defaults. ok is false when none applies.
func (a *App) formatterFor(path string) (formatterSpec, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return formatterSpec{}, false
	}

	if f := a.settings.Format; f != nil {
		if spec, found := f.Formatters[ext]; found {
			return spec, spec.Command != ""
		}
	}

	if ext == ".go" {
		return formatterSpec{Command: "gofmt"}, true
	}

	for _, e := range prettierExts {
		if e == ext {
			return formatterSpec{Command: "prettier", Args: []string{"--stdin-filepath", "{file}"}}, true
		}
	}

	return formatterSpec{}, false
}

// runFormatter pipes text through spec and returns the formatted text.
// ok is false when the tool is missing, fails, times out, or returns
// nothing.
func runFormatter(ctx context.Context, spec formatterSpec, dir, file, text string) (string, bool) {
	if spec.Command == "" || len(text) > formatMaxBytes {
		return "", false
	}

	bin, err := exec.LookPath(spec.Command)
	if err != nil {
		return "", false
	}

	args := make([]string, len(spec.Args))
	for i, arg := range spec.Args {
		args[i] = strings.ReplaceAll(arg, "{file}", file)
	}

	ctx, cancel := context.WithTimeout(ctx, formatTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // command comes from the user's own settings or the built-in defaults
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		cmd.Dir = dir
	}
	cmd.Stdin = strings.NewReader(text)

	var stdout bytes.Buffer

	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return "", false
	}

	out := stdout.String()
	if strings.TrimSpace(out) == "" || !utf8.ValidString(out) {
		return "", false
	}

	return out, true
}

// formatEditForSave formats the live buffer before a save. The caret keeps
// its line and column, clamped to the new text.
func (a *App) formatEditForSave(path string) {
	if a.edit == nil || !a.formatOnSaveEnabled() {
		return
	}

	spec, ok := a.formatterFor(path)
	if !ok {
		return
	}

	if trimSkippable(a.docs[path], len(a.edit.Text())) {
		return
	}

	dir, file := "", path

	if a.ws != nil {
		dir = a.ws.Root
		file = filepath.Join(a.ws.Root, filepath.FromSlash(path))
	}

	text := a.edit.Text()

	out, ok := runFormatter(context.Background(), spec, dir, file, text)
	if !ok {
		return
	}

	out = strings.TrimSuffix(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	if out == strings.TrimSuffix(text, "\n") {
		return
	}

	aLine, aCol := offsetLineCol(text, a.editAnchor)
	cLine, cCol := offsetLineCol(text, a.editCaret)
	lines := strings.Split(out, "\n")

	a.edit.SetText(edit.JoinLines(lines))
	a.editAnchor = clampedLineOffset(lines, aLine, aCol)
	a.editCaret = clampedLineOffset(lines, cLine, cCol)
	a.commitEditBuffer()
}
