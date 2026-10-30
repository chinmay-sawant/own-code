// Package edit is the text buffer and selection model behind the editor:
// the unsaved text of one open file and the anchor/caret selection over it.
package edit

import "strings"

// Buffer is the unsaved text of one open file. Lines are the editable
// form; Text serializes them with one trailing newline so a normal save keeps
// the file's last newline.
type Buffer struct {
	Path  string
	Lines []string
	Dirty bool
	// Original is the Text value the buffer opened or last saved with.
	Original string
}

// NewBuffer parses a file into lines. CRLF becomes LF, one trailing
// newline is dropped before splitting, and an empty file has zero lines.
func NewBuffer(path string, raw []byte) *Buffer {
	lines := splitEditLines(string(raw))

	return &Buffer{
		Path:     path,
		Lines:    lines,
		Original: JoinLines(lines),
	}
}

// Text returns the text a save would write.
func (b *Buffer) Text() string {
	return JoinLines(b.Lines)
}

// SetText replaces the lines from editor text. The buffer is dirty only when
// the new text differs from the text it opened or last saved with.
func (b *Buffer) SetText(text string) {
	b.Lines = splitEditLines(text)
	b.Dirty = b.Text() != b.Original
}

// LineCount returns the number of editable lines.
func (b *Buffer) LineCount() int {
	return len(b.Lines)
}

// Save serializes the buffer for the workspace writer. It does not touch
// disk.
func (b *Buffer) Save() []byte {
	return []byte(b.Text())
}

// MarkSaved records the current text as the saved text.
func (b *Buffer) MarkSaved() {
	b.Original = b.Text()
	b.Dirty = false
}

// splitEditLines normalizes CRLF and splits the trailing newline sentinel off
// the text. An empty file is zero lines, not one empty line; a file holding
// only a newline is one empty line.
func splitEditLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if text == "" {
		return nil
	}

	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// JoinLines writes lines as text with one trailing newline. A zero-line
// buffer writes nothing.
func JoinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}

	return strings.Join(lines, "\n") + "\n"
}

// ClampPos bounds a buffer position to [0, n].
func ClampPos(pos, n int) int {
	if pos < 0 {
		return 0
	}

	if pos > n {
		return n
	}

	return pos
}
