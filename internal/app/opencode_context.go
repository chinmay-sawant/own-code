package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/chinmay-sawant/own-code/internal/sensitive"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	maxOpenCodeContextBytes = 32 << 10
	openCodeContextLines    = 3
)

var (
	errInvalidAgentContextPath = errors.New("agent context path must name a regular file inside the workspace")
	errSensitiveAgentContext   = errors.New("agent context excludes sensitive files by default")
	errAgentContextTooLarge    = errors.New("agent context selection exceeds the size limit")
	errInvalidAgentSelection   = errors.New("agent context selection is outside the document")
)

// agentContextSnapshot is an immutable view of one editor document at the
// time a prompt is prepared. Text is a bounded excerpt that always contains
// the selected text (or caret line); Version identifies the complete buffer.
// Selection offsets are rune offsets within Text, while Document offsets are
// rune offsets within the complete editor buffer.
type agentContextSnapshot struct {
	WorkspaceRoot string
	RelativePath  string
	Version       string
	Text          string
	SelectionText string
	SelectionFrom int
	SelectionTo   int
	DocumentFrom  int
	DocumentTo    int
	StartLine     int
	EndLine       int
	Truncated     bool
}

// captureAgentContext builds a bounded selection snapshot without reading
// arbitrary files. The caller passes the active editor buffer so unsaved text
// is captured; the path check confirms that the source is an ordinary file
// inside the selected workspace and excludes common credential files.
func captureAgentContext(workspaceRoot, relativePath, documentText string, selectionFrom, selectionTo int) (agentContextSnapshot, error) {
	root, relative, err := validateAgentContextPath(workspaceRoot, relativePath)
	if err != nil {
		return agentContextSnapshot{}, err
	}
	if isSensitiveAgentContextPath(relative) {
		return agentContextSnapshot{}, errSensitiveAgentContext
	}
	if !utf8.ValidString(documentText) {
		return agentContextSnapshot{}, errors.New("agent context document is not valid UTF-8")
	}
	runes := []rune(documentText)
	if selectionFrom > selectionTo {
		selectionFrom, selectionTo = selectionTo, selectionFrom
	}
	if selectionFrom < 0 || selectionTo > len(runes) {
		return agentContextSnapshot{}, errInvalidAgentSelection
	}
	selected := string(runes[selectionFrom:selectionTo])
	if len(selected) > maxOpenCodeContextBytes {
		return agentContextSnapshot{}, errAgentContextTooLarge
	}
	startLine, endLine := runeLineRange(runes, selectionFrom, selectionTo)
	firstLine := max(0, startLine-openCodeContextLines)
	lastLine := min(lineCount(runes)-1, endLine+openCodeContextLines)

	// Expand context by whole lines while retaining the chosen selection. A
	// very long selected line remains available up to the hard byte limit.
	for {
		startOffset := lineStartOffset(runes, firstLine)
		endOffset := lineEndOffset(runes, lastLine)
		candidate := string(runes[startOffset:endOffset])
		if len(candidate) <= maxOpenCodeContextBytes {
			digest := sha256.Sum256([]byte(documentText))
			return agentContextSnapshot{
				WorkspaceRoot: root,
				RelativePath:  relative,
				Version:       hex.EncodeToString(digest[:]),
				Text:          candidate,
				SelectionText: selected,
				SelectionFrom: selectionFrom - startOffset,
				SelectionTo:   selectionTo - startOffset,
				DocumentFrom:  selectionFrom,
				DocumentTo:    selectionTo,
				StartLine:     firstLine + 1,
				EndLine:       lastLine + 1,
				Truncated:     firstLine > 0 || lastLine < lineCount(runes)-1,
			}, nil
		}
		if firstLine == startLine && lastLine == endLine {
			startOffset, endOffset = boundedAgentContextWindow(runes, selectionFrom, selectionTo)
			candidate = string(runes[startOffset:endOffset])
			digest := sha256.Sum256([]byte(documentText))
			return agentContextSnapshot{
				WorkspaceRoot: root,
				RelativePath:  relative,
				Version:       hex.EncodeToString(digest[:]),
				Text:          candidate,
				SelectionText: selected,
				SelectionFrom: selectionFrom - startOffset,
				SelectionTo:   selectionTo - startOffset,
				DocumentFrom:  selectionFrom,
				DocumentTo:    selectionTo,
				StartLine:     lineIndexAt(runes, startOffset) + 1,
				EndLine:       lineIndexAt(runes, max(startOffset, endOffset-1)) + 1,
				Truncated:     true,
			}, nil
		}
		if startLine-firstLine >= lastLine-endLine && firstLine < startLine {
			firstLine++
			continue
		}
		if lastLine > endLine {
			lastLine--
			continue
		}
		if firstLine < startLine {
			firstLine++
		}
	}
}

func validateAgentContextPath(workspaceRoot, relativePath string) (string, string, error) {
	if strings.TrimSpace(workspaceRoot) == "" || strings.TrimSpace(relativePath) == "" {
		return "", "", errInvalidAgentContextPath
	}
	rootAbs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", "", errInvalidAgentContextPath
	}
	root, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", "", errInvalidAgentContextPath
	}
	rootInfo, err := os.Stat(root)
	if err != nil || !rootInfo.IsDir() {
		return "", "", errInvalidAgentContextPath
	}

	raw := filepath.FromSlash(relativePath)
	if filepath.IsAbs(raw) || filepath.VolumeName(raw) != "" {
		return "", "", errInvalidAgentContextPath
	}
	for _, part := range strings.Split(raw, string(filepath.Separator)) {
		if part == ".." {
			return "", "", errInvalidAgentContextPath
		}
	}
	clean := filepath.Clean(raw)
	if clean == "." || clean == ".." || !filepath.IsLocal(clean) {
		return "", "", errInvalidAgentContextPath
	}
	target := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return "", "", errInvalidAgentContextPath
	}

	// Reject symlinks in every existing path component. This keeps an
	// in-workspace lexical path from redirecting context outside the root.
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				break
			}
			return "", "", errInvalidAgentContextPath
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", errInvalidAgentContextPath
		}
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", errInvalidAgentContextPath
	}
	return root, filepath.ToSlash(rel), nil
}

func isSensitiveAgentContextPath(relativePath string) bool {
	return sensitive.Path(relativePath)
}

func runeLineRange(runes []rune, from, to int) (int, int) {
	start, end := lineIndexAt(runes, from), lineIndexAt(runes, to)
	if to > from && to <= len(runes) && runes[to-1] == '\n' {
		end--
	}
	if end < start {
		end = start
	}
	return start, end
}

func lineIndexAt(runes []rune, offset int) int {
	line := 0
	for i := 0; i < offset && i < len(runes); i++ {
		if runes[i] == '\n' {
			line++
		}
	}
	return line
}

func lineCount(runes []rune) int {
	count := 1
	for _, r := range runes {
		if r == '\n' {
			count++
		}
	}
	return count
}

func lineStartOffset(runes []rune, line int) int {
	if line <= 0 {
		return 0
	}
	seen := 0
	for i, r := range runes {
		if r == '\n' {
			seen++
			if seen == line {
				return i + 1
			}
		}
	}
	return len(runes)
}

func lineEndOffset(runes []rune, line int) int {
	start := lineStartOffset(runes, line)
	for i := start; i < len(runes); i++ {
		if runes[i] == '\n' {
			return i + 1
		}
	}
	return len(runes)
}

func boundedAgentContextWindow(runes []rune, selectionFrom, selectionTo int) (int, int) {
	start, end := selectionFrom, selectionTo
	used := len(string(runes[start:end]))
	leftBudget := (maxOpenCodeContextBytes - used) / 2
	rightBudget := maxOpenCodeContextBytes - used - leftBudget

	for start > 0 {
		width := utf8.RuneLen(runes[start-1])
		if width > leftBudget {
			break
		}
		start--
		leftBudget -= width
	}
	for end < len(runes) {
		width := utf8.RuneLen(runes[end])
		if width > rightBudget {
			break
		}
		end++
		rightBudget -= width
	}
	// Spend any unused bytes on the other side when one edge of the document
	// prevents the balanced first pass from using the full budget.
	for start > 0 {
		width := utf8.RuneLen(runes[start-1])
		if width > leftBudget+rightBudget {
			break
		}
		start--
		leftBudget -= width
	}
	for end < len(runes) {
		width := utf8.RuneLen(runes[end])
		if width > leftBudget+rightBudget {
			break
		}
		end++
		rightBudget -= width
	}
	return start, end
}

func (s agentContextSnapshot) String() string {
	return fmt.Sprintf("agentContextSnapshot{file:%q, version:%s, bytes:%d}", s.RelativePath, s.Version, len(s.Text))
}
