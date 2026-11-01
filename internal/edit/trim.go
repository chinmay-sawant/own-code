package edit

import "strings"

// TrimLines strips trailing spaces and tabs from every line. An empty line
// stays empty, and the newline sentinel is the caller's business: this works
// on split lines, not serialized text. It returns the stripped lines and the
// number of lines that changed.
func TrimLines(lines []string) ([]string, int) {
	out := make([]string, len(lines))
	changed := 0

	for i, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		out[i] = trimmed

		if trimmed != line {
			changed++
		}
	}

	return out, changed
}

// TouchedLines marks the current lines that differ from the saved lines, so
// an on-save trim can leave untouched lines byte-identical and keep diffs
// minimal. Lines are compared positionally; appended lines past the saved
// length count as touched, and lines past the current length are dropped
// with the deletion. It returns one flag per current line.
func TouchedLines(saved, current []string) []bool {
	touched := make([]bool, len(current))

	for i, line := range current {
		if i >= len(saved) || saved[i] != line {
			touched[i] = true
		}
	}

	return touched
}

// TrimTouched strips trailing whitespace only from touched lines, using the
// flags from TouchedLines. It returns the stripped lines and the number of
// lines that changed.
func TrimTouched(current []string, touched []bool) ([]string, int) {
	out := make([]string, len(current))
	changed := 0

	for i, line := range current {
		if i < len(touched) && touched[i] {
			if trimmed := strings.TrimRight(line, " \t"); trimmed != line {
				out[i] = trimmed
				changed++

				continue
			}
		}
		out[i] = line
	}

	return out, changed
}
