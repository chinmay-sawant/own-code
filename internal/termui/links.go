package termui

import (
	"sort"
	"strings"
)

// Link is one clickable file location in a terminal row. Path is the raw
// path text as printed, Line is 1-based, Col is 1-based when present and 0
// when the row only carries a line. Start and End are rune offsets in the
// row's plain text; the half-open span covers the link for rendering.
type Link struct {
	Path  string
	Line  int
	Col   int
	Start int
	End   int
}

// ParseTerminalLinks extracts clickable file locations from one visible
// terminal row. It handles path:line, path:line:col, absolute paths, and
// Python traceback File "x", line N forms. It rejects timestamps, bare
// numbers, and URLs. The scan is regex-free and bounded by the row length;
// callers must only feed visible rows.
//
// A path is file-like when it holds a dot or slash and at least one ASCII
// letter, which keeps timestamps (12:34), decimals (12.34:56), and bare
// words out while keeping main.go, ./rel/path.go, and /abs/path.go.
func ParseTerminalLinks(text string) []Link {
	if text == "" {
		return nil
	}
	if !strings.Contains(text, ":") && !strings.Contains(text, "File ") {
		return nil
	}
	rs := []rune(text)
	if len(rs) > 4096 {
		rs = rs[:4096]
	}
	urls := findURLRanges(rs)
	py := parsePythonLinks(rs)
	gen := parseGenericLinks(rs, urls)

	all := make([]Link, 0, len(py)+len(gen))
	all = append(all, py...)
	// Drop generic links that overlap a Python link; the Python span wins
	// because it already carries the line number.
	for _, g := range gen {
		overlap := false
		for _, p := range py {
			if g.Start < p.End && p.Start < g.End {
				overlap = true
				break
			}
		}
		if !overlap {
			all = append(all, g)
		}
	}
	if len(all) == 0 {
		return nil
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Start != all[j].Start {
			return all[i].Start < all[j].Start
		}
		return all[i].End > all[j].End
	})
	out := all[:0]
	lastEnd := -1
	for _, l := range all {
		if l.Start < lastEnd {
			continue
		}
		out = append(out, l)
		lastEnd = l.End
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func isASCIILetter(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

func isPathChar(r rune) bool {
	if isASCIILetter(r) || isDigit(r) {
		return true
	}
	switch r {
	case '_', '-', '.', '/', '+', '~':
		return true
	}
	return false
}

func isSchemeChar(r rune) bool {
	if isASCIILetter(r) || isDigit(r) {
		return true
	}
	switch r {
	case '+', '-', '.':
		return true
	}
	return false
}

func isURLTerminator(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '"', '\'', '<', '>', '`':
		return true
	}
	return false
}

func isTrailingURLPunct(r rune) bool {
	switch r {
	case '.', ',', ';', '!', '?', ')', ']', '}':
		return true
	}
	return false
}

// isFileLike reports whether a candidate path looks like a file reference.
func isFileLike(path string) bool {
	if path == "" || len(path) > 256 {
		return false
	}
	if strings.Contains(path, "://") {
		return false
	}
	if strings.HasPrefix(path, "<") || strings.HasSuffix(path, "/") {
		return false
	}
	if path == "." || path == ".." || path == "/" {
		return false
	}
	hasDot := strings.Contains(path, ".")
	hasSlash := strings.Contains(path, "/")
	if !hasDot && !hasSlash {
		return false
	}
	for _, r := range path {
		if isASCIILetter(r) {
			return true
		}
	}
	return false
}

// findURLRanges marks http:// style spans so file detection skips ports and
// paths inside URLs. Ranges are half-open rune offsets.
func findURLRanges(rs []rune) [][2]int {
	var out [][2]int
	n := len(rs)
	for i := 0; i+3 <= n; i++ {
		if rs[i] != ':' || rs[i+1] != '/' || rs[i+2] != '/' {
			continue
		}
		s := i - 1
		for s >= 0 && isSchemeChar(rs[s]) {
			s--
		}
		s++
		e := i + 3
		for e < n && !isURLTerminator(rs[e]) {
			e++
		}
		for e > s && isTrailingURLPunct(rs[e-1]) {
			e--
		}
		if e > s {
			out = append(out, [2]int{s, e})
			i = e
		} else {
			i = e
		}
	}
	return out
}

func overlapsURL(start, end int, urls [][2]int) bool {
	for _, u := range urls {
		if start < u[1] && u[0] < end {
			return true
		}
	}
	return false
}

// parsePythonLinks finds File "path", line N forms (double or single quotes).
// The span runs from the path start through the line digits so the whole
// traceback location underlines as one link.
func parsePythonLinks(rs []rune) []Link {
	var out []Link
	n := len(rs)
	for i := 0; i+6 < n; i++ {
		if !(rs[i] == 'F' && rs[i+1] == 'i' && rs[i+2] == 'l' && rs[i+3] == 'e' && rs[i+4] == ' ') {
			continue
		}
		quote := rs[i+5]
		if quote != '"' && quote != '\'' {
			continue
		}
		j := i + 6
		for j < n && rs[j] != quote {
			j++
		}
		if j >= n {
			continue
		}
		path := string(rs[i+6 : j])
		if path == "" || strings.Contains(path, "<") || strings.Contains(path, ">") {
			i = j
			continue
		}
		if !isFileLike(path) {
			i = j
			continue
		}
		k := j + 1
		if k >= n || rs[k] != ',' {
			i = j
			continue
		}
		k++
		for k < n && (rs[k] == ' ' || rs[k] == '\t') {
			k++
		}
		if k+4 > n || !(rs[k] == 'l' && rs[k+1] == 'i' && rs[k+2] == 'n' && rs[k+3] == 'e') {
			i = j
			continue
		}
		k += 4
		for k < n && (rs[k] == ' ' || rs[k] == '\t') {
			k++
		}
		dstart := k
		line := 0
		for k < n && isDigit(rs[k]) {
			line = line*10 + int(rs[k]-'0')
			if line > 1000000 {
				break
			}
			k++
		}
		if k == dstart || line <= 0 || line > 1000000 {
			i = j
			continue
		}
		out = append(out, Link{Path: path, Line: line, Start: i + 6, End: k})
		i = k - 1
	}
	return out
}

// parseGenericLinks finds path:line and path:line:col forms via a colon scan.
// Each colon with a digit after it opens a candidate; the path runs back over
// path chars and the line/col run forward over digits.
func parseGenericLinks(rs []rune, urls [][2]int) []Link {
	var out []Link
	n := len(rs)
	i := 0
	for i < n {
		if rs[i] != ':' {
			i++
			continue
		}
		if i+1 >= n || !isDigit(rs[i+1]) {
			i++
			continue
		}
		j := i + 1
		line := 0
		for j < n && isDigit(rs[j]) {
			line = line*10 + int(rs[j]-'0')
			if line > 1000000 {
				break
			}
			j++
		}
		if line <= 0 || line > 1000000 {
			i++
			continue
		}
		col := 0
		end := j
		if j < n && rs[j] == ':' && j+1 < n && isDigit(rs[j+1]) {
			k := j + 1
			c := 0
			for k < n && isDigit(rs[k]) {
				c = c*10 + int(rs[k]-'0')
				if c > 1000000 {
					break
				}
				k++
			}
			if c > 0 && c <= 1000000 {
				col = c
				end = k
			}
		}
		k := i - 1
		for k >= 0 && isPathChar(rs[k]) {
			k--
		}
		pathStart := k + 1
		// Windows drive: C:/path keeps its drive letter.
		if k >= 0 && rs[k] == ':' && k-1 >= 0 && isASCIILetter(rs[k-1]) {
			before := k - 2
			if before < 0 || (!isPathChar(rs[before]) && rs[before] != ':') {
				pathStart = k - 1
			}
		}
		if pathStart >= i {
			i++
			continue
		}
		path := string(rs[pathStart:i])
		if !isFileLike(path) {
			i++
			continue
		}
		if overlapsURL(pathStart, end, urls) {
			i = end
			continue
		}
		out = append(out, Link{Path: path, Line: line, Col: col, Start: pathStart, End: end})
		i = end
	}
	return out
}
