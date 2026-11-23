package app

import (
	"strconv"
	"strings"
)

// popupRowView is one painted list row.
type popupRowView struct {
	Index    int
	Label    string
	Detail   string
	Kind     string
	Selected bool
	Name     string
}

// popupLineView is one painted content line: Markdown segments, or plain
// code inside a fence.
type popupLineView struct {
	Segments []segment
	Code     bool
	Blank    bool
}

// popupView is the template data for the popup.
type popupView struct {
	Show     bool
	List     bool
	Trap     bool
	Above    bool
	Label    string
	Left     int
	Top      int
	Width    int
	Height   int
	Filter   string
	Empty    bool
	Count    string
	ActiveID string
	Rows     []popupRowView
	Lines    []popupLineView
}

// popupView builds the popup's template data for the current window size.
func (a *App) popupView() popupView {
	p := &a.popup
	if !p.open {
		return popupView{}
	}

	winW, winH := popupDefaultWin, popupDefaultWin
	if a.page != nil {
		winW, winH = a.page.Size()
	}

	box := p.box(winW, winH)
	label := strings.TrimSpace(p.spec.Label)

	if label == "" {
		label = "Suggestions"
		if !p.isList() {
			label = "Details"
		}
	}

	v := popupView{
		Show:   true,
		List:   p.isList(),
		Trap:   p.spec.Trap,
		Above:  box.Above,
		Label:  label,
		Left:   box.Left,
		Top:    box.Top,
		Width:  box.Width,
		Height: box.Height,
		Filter: strings.TrimSpace(p.spec.Filter),
	}

	if !v.List {
		v.Lines = popupContentLines(p.spec.Content)

		return v
	}

	v.Empty = len(p.matches) == 0
	v.Count = strconv.Itoa(len(p.matches)) + " of " + strconv.Itoa(len(p.spec.Items))

	end := min(len(p.matches), p.offset+popupMaxRows)
	for i := p.offset; i < end; i++ {
		item := p.spec.Items[p.matches[i]]
		name := item.Label
		if item.Detail != "" {
			name += ", " + item.Detail
		}

		v.Rows = append(v.Rows, popupRowView{
			Index:    i,
			Label:    item.Label,
			Detail:   item.Detail,
			Kind:     item.Kind,
			Selected: i == p.index,
			Name:     name,
		})

		if i == p.index {
			v.ActiveID = "popup-item-" + strconv.Itoa(i)
		}
	}

	return v
}

// popupContentLines renders Markdown text for the read-only card. It reuses
// the editor's Markdown highlighter for headings, emphasis, and inline code,
// and shows fenced code as plain code lines.
func popupContentLines(content string) []popupLineView {
	var lines []popupLineView

	fence := false

	for _, raw := range strings.Split(strings.TrimRight(strings.ReplaceAll(content, "\r\n", "\n"), "\n"), "\n") {
		raw = strings.ReplaceAll(raw, "\t", "  ")

		if strings.HasPrefix(strings.TrimSpace(raw), "```") {
			fence = !fence

			continue
		}

		switch {
		case fence:
			lines = append(lines, popupLineView{Code: true, Segments: []segment{{Text: raw, Class: classPlain}}})
		case strings.TrimSpace(raw) == "":
			lines = append(lines, popupLineView{Blank: true})
		default:
			lines = append(lines, popupLineView{Segments: popupMarkdownSegments(raw)})
		}
	}

	return lines
}

// popupMarkdownSegments styles one Markdown line for the hover card. A heading
// shows its text in the heading style without the leading "#" markers, which
// read as noise in a card; every other line uses the editor's highlighter.
func popupMarkdownSegments(raw string) []segment {
	r := []rune(raw)
	if level, marker := headingLevel(r); level > 0 {
		return []segment{{Text: strings.TrimSpace(string(r[marker:])), Class: headingClass(level)}}
	}

	return markdownLine(raw)
}
