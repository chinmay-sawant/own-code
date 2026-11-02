package app

import (
	"strings"

	"github.com/chinmay-sawant/own-code/internal/edit"
)

// Trailing-whitespace trim on save. Modes are off (byte-identical), all
// (every line), and touched (only lines the buffer changed since the last
// save). Merge saves and diff-revert patches never pass through here: they
// write through their own paths so conflict markers and reverts stay exact.

// validTrimMode sanitizes a mode: all and touched pass, anything else is off.
func validTrimMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "all", "touched":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return ""
	}
}

// effectiveTrimMode is the trim mode for one file. An explicit setting wins;
// when unset, an .editorconfig trim_trailing_whitespace bridges in.
func (a *App) effectiveTrimMode(path string) string {
	if mode := validTrimMode(a.trimMode); mode != "" {
		return mode
	}

	if a.uiUseEditorConfig() {
		if ec := a.editorConfigFor(path); ec.found && ec.trim != nil && *ec.trim {
			return "all"
		}
	}

	return ""
}

// applyTrimForSave trims lines for one save. savedLines is the last-saved
// text split into lines (nil skips touched detection and trims nothing in
// touched mode). Binary, oversized, and truncated documents are skipped by
// the caller through trimSkippable. It returns the lines to write and the
// trimmed count.
func applyTrimForSave(mode string, savedLines, current []string) ([]string, int) {
	switch mode {
	case "all":
		return edit.TrimLines(current)
	case "touched":
		if savedLines == nil {
			return current, 0
		}

		return edit.TrimTouched(current, edit.TouchedLines(savedLines, current))
	default:
		return current, 0
	}
}

// trimSkippable reports whether one document must skip trimming: binary,
// oversized, or truncated files are never rewritten for whitespace.
func trimSkippable(doc *fileDoc, byteLen int) bool {
	if doc == nil {
		return true
	}

	if doc.Binary || doc.Err != "" {
		return true
	}

	if doc.Truncated || len(doc.Lines) > maxFileLines || byteLen > maxFileBytes {
		return true
	}

	return false
}

// trimSavedLines splits the last-saved buffer text for touched detection.
func trimSavedLines(original string) []string {
	if original == "" {
		return []string{}
	}

	return strings.Split(strings.TrimSuffix(original, "\n"), "\n")
}

// cycleTrimMode steps off to touched to all and back to off.
func (a *App) cycleTrimMode() {
	switch validTrimMode(a.trimMode) {
	case "":
		a.trimMode = "touched"
	case "touched":
		a.trimMode = "all"
	default:
		a.trimMode = ""
	}

	a.refresh()
}
