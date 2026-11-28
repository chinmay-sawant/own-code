// Package agentreview is the pure model behind reviewing agent proposals:
// line hunks, per-hunk accept/reject/revert that touches only agent-owned
// lines, and per-session per-file hunk state. It does no UI.
package agentreview

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

// ErrConflict means the target text no longer contains the lines a hunk needs.
var ErrConflict = errors.New("agentreview: hunk no longer matches the current text")

// ErrAmbiguous means the hunk matches several places equally well.
var ErrAmbiguous = errors.New("agentreview: hunk location is ambiguous")

const contextLines = 3

// maxLCSCells bounds the quadratic diff; beyond it the changed middle becomes one hunk.
const maxLCSCells = 4 << 20

// Hunk is one contiguous agent change. Lines keep their terminators, so a
// missing final newline is part of the change like any other text.
type Hunk struct {
	ID       string
	OldStart int // index of the first Old line in the baseline
	Old      []string
	New      []string
	Before   []string // up to contextLines baseline lines preceding the hunk
	After    []string // up to contextLines baseline lines following the hunk
}

// SplitLines splits s after each newline, keeping the terminators.
func SplitLines(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

// Diff returns the hunks that turn base into proposed. Hunks are maximal runs
// of change with no shared context, so each can be applied independently.
func Diff(base, proposed string) []Hunk {
	a, b := SplitLines(base), SplitLines(proposed)
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	sa, sb := len(a), len(b)
	for sa > pre && sb > pre && a[sa-1] == b[sb-1] {
		sa--
		sb--
	}
	am, bm := a[pre:sa], b[pre:sb]
	var hunks []Hunk
	if len(am) == 0 && len(bm) == 0 {
		return nil
	}
	if len(am) == 0 || len(bm) == 0 || len(am)*len(bm) > maxLCSCells {
		hunks = append(hunks, Hunk{OldStart: pre, Old: am, New: bm})
	} else {
		hunks = lcsHunks(am, bm, pre)
	}
	for i := range hunks {
		h := &hunks[i]
		lo := max(0, h.OldStart-contextLines)
		h.Before = append([]string(nil), a[lo:h.OldStart]...)
		end := h.OldStart + len(h.Old)
		h.After = append([]string(nil), a[end:min(len(a), end+contextLines)]...)
		h.Old = append([]string(nil), h.Old...)
		h.New = append([]string(nil), h.New...)
		h.ID = hunkID(*h)
	}
	return hunks
}

func lcsHunks(a, b []string, off int) []Hunk {
	n, m := len(a), len(b)
	t := make([][]int32, n+1)
	for i := range t {
		t[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	var hunks []Hunk
	var cur *Hunk
	flush := func() {
		if cur != nil {
			hunks = append(hunks, *cur)
			cur = nil
		}
	}
	open := func(i int) {
		if cur == nil {
			cur = &Hunk{OldStart: off + i}
		}
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			flush()
			i++
			j++
		case i < n && (j >= m || t[i+1][j] >= t[i][j+1]):
			open(i)
			cur.Old = append(cur.Old, a[i])
			i++
		default:
			open(i)
			cur.New = append(cur.New, b[j])
			j++
		}
	}
	flush()
	return hunks
}

func hunkID(h Hunk) string {
	s := sha256.New()
	s.Write([]byte(strconv.Itoa(h.OldStart)))
	for _, l := range h.Old {
		s.Write([]byte("-" + l + "\x00"))
	}
	for _, l := range h.New {
		s.Write([]byte("+" + l + "\x00"))
	}
	return "h-" + hex.EncodeToString(s.Sum(nil)[:6])
}

// Apply replaces the hunk's Old lines in current with its New lines. Only the
// hunk's own lines change; everything else, including user edits next to it,
// is kept byte for byte.
func Apply(current string, h Hunk) (string, error) {
	return splice(current, h, h.Old, h.New)
}

// Revert undoes an applied hunk: its New lines in current go back to Old.
func Revert(current string, h Hunk) (string, error) {
	return splice(current, h, h.New, h.Old)
}

func splice(current string, h Hunk, match, repl []string) (string, error) {
	lines := SplitLines(current)
	pos, err := locate(lines, h, match)
	if err != nil {
		return "", err
	}
	out := make([]string, 0, len(lines)-len(match)+len(repl))
	out = append(out, lines[:pos]...)
	out = append(out, repl...)
	out = append(out, lines[pos+len(match):]...)
	return strings.Join(out, ""), nil
}

func hasAt(lines []string, pos int, want []string) bool {
	if pos < 0 || pos+len(want) > len(lines) {
		return false
	}
	for i, w := range want {
		if lines[pos+i] != w {
			return false
		}
	}
	return true
}

// locate finds where match sits in lines, scoring candidates by how much of
// the hunk's recorded context surrounds them and then by distance from the
// baseline position. A bare insertion needs surrounding context to anchor.
func locate(lines []string, h Hunk, match []string) (int, error) {
	best, bestScore, bestDist, tie := -1, -1, 0, false
	for pos := 0; pos+len(match) <= len(lines); pos++ {
		if !hasAt(lines, pos, match) {
			continue
		}
		score := 0
		for k := 1; k <= len(h.Before) && hasAt(lines, pos-k, h.Before[len(h.Before)-k:]); k++ {
			score++
		}
		for k := 1; k <= len(h.After) && hasAt(lines, pos+len(match), h.After[:k]); k++ {
			score++
		}
		if len(match) == 0 && score == 0 && (len(h.Before) > 0 || len(h.After) > 0) {
			continue
		}
		if len(match) == 0 && len(h.Before) == 0 && len(h.After) == 0 && len(lines) != 0 {
			continue
		}
		dist := pos - h.OldStart
		if dist < 0 {
			dist = -dist
		}
		switch {
		case score > bestScore || (score == bestScore && dist < bestDist):
			best, bestScore, bestDist, tie = pos, score, dist, false
		case score == bestScore && dist == bestDist:
			tie = true
		}
	}
	if best < 0 {
		return 0, ErrConflict
	}
	if tie {
		return 0, ErrAmbiguous
	}
	return best, nil
}
