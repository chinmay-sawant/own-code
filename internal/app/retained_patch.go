package app

import (
	"context"
	"html"
	"strconv"
	"strings"

	"github.com/chinmay-sawant/ownframe"
)

// patchHoverPath updates the statusbar hover path in place via a retained
// element patch on #statusbar, avoiding a full template execution and IDE
// relayout on every pointer transition over explorer rows and tabs. Any patch
// error (including ownframe.ErrPatchRefused) falls back cleanly to a full
// refresh.
func (a *App) patchHoverPath(ctx context.Context, nextPath string) bool {
	prevEmpty := a.hoverPath == ""
	nextEmpty := nextPath == ""
	a.hoverPath = nextPath
	a.view.HoverPath = nextPath

	if a.page == nil {
		return false
	}

	text := nextPath
	edits := make([]ownframe.ElementEdit, 0, 2)
	edits = append(edits, ownframe.ElementEdit{
		ID:   "sb-hover-path",
		Text: &text,
	})
	if prevEmpty != nextEmpty {
		if nextEmpty {
			edits = append(edits, ownframe.ElementEdit{
				ID:       "sb-hover-path",
				AddClass: "empty",
			})
		} else {
			edits = append(edits, ownframe.ElementEdit{
				ID:          "sb-hover-path",
				RemoveClass: "empty",
			})
		}
	}

	if err := a.page.PatchElement(ctx, "statusbar", edits...); err != nil {
		a.refresh()
		return false
	}

	return true
}

// patchTerminalBody updates #term-body in place when PTY output arrives and
// the surrounding terminal chrome (sessions, scroll controls, note, running
// state) did not change. Returns false when the caller should fall back to
// SetData + RedrawIfChanged.
func (a *App) patchTerminalBody(ctx context.Context, prev, next terminalView) bool {
	if !canPatchTerminalBody(prev, next) || a.page == nil {
		return false
	}

	bodyHTML := renderTerminalBodyHTML(next)
	err := a.page.PatchElement(ctx, "term-body", ownframe.ElementEdit{
		InnerHTML: &bodyHTML,
	})
	return err == nil
}

func canPatchTerminalBody(prev, next terminalView) bool {
	if !prev.Open || !next.Open {
		return false
	}
	if prev.Focus != next.Focus || prev.Running != next.Running ||
		prev.Note != next.Note || prev.Shell != next.Shell ||
		prev.Scroll != next.Scroll || (prev.MaxScroll > 0) != (next.MaxScroll > 0) {
		return false
	}
	if len(prev.Sessions) == 0 || !sameTerminalSessions(prev.Sessions, next.Sessions) {
		return false
	}
	return true
}

func sameTerminalSessions(a, b []terminalTabRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// renderTerminalBodyHTML produces the exact inner HTML of #term-body matching
// components/html/terminal.html.
func renderTerminalBodyHTML(v terminalView) string {
	if len(v.Sessions) == 0 {
		return `<span class="term-empty">Open a session with +</span>`
	}

	var b strings.Builder
	b.Grow(len(v.Rows) * 96)
	for ri, row := range v.Rows {
		b.WriteString(`<div class="term-row">`)
		for si, seg := range row.Segments {
			if seg.LinkPath != "" {
				b.WriteString(`<button class="term-link t-`)
				b.WriteString(html.EscapeString(seg.Class))
				b.WriteByte('"')
				writeTerminalSegmentStyle(&b, seg.Foreground, seg.Background)
				b.WriteString(` id="term-link-`)
				b.WriteString(strconv.Itoa(ri))
				b.WriteByte('-')
				b.WriteString(strconv.Itoa(si))
				b.WriteString(`" data-action="term-link:`)
				b.WriteString(strconv.Itoa(ri))
				b.WriteByte(':')
				b.WriteString(strconv.Itoa(si))
				b.WriteString(`" type="button" tabindex="-1" title="`)
				b.WriteString(html.EscapeString(seg.LinkPath))
				b.WriteByte(':')
				b.WriteString(strconv.Itoa(seg.LinkLine))
				if seg.LinkCol != 0 {
					b.WriteByte(':')
					b.WriteString(strconv.Itoa(seg.LinkCol))
				}
				b.WriteString(`">`)
				b.WriteString(html.EscapeString(seg.Text))
				b.WriteString(`</button>`)
			} else {
				b.WriteString(`<span class="t-`)
				b.WriteString(html.EscapeString(seg.Class))
				b.WriteByte('"')
				writeTerminalSegmentStyle(&b, seg.Foreground, seg.Background)
				b.WriteByte('>')
				b.WriteString(html.EscapeString(seg.Text))
				b.WriteString(`</span>`)
			}
		}
		b.WriteString(`</div>`)
	}
	return b.String()
}

func writeTerminalSegmentStyle(b *strings.Builder, fg, bg string) {
	if fg == "" && bg == "" {
		return
	}
	b.WriteString(` style="`)
	if fg != "" {
		b.WriteString(`color:`)
		b.WriteString(html.EscapeString(fg))
		b.WriteByte(';')
	}
	if bg != "" {
		b.WriteString(`background-color:`)
		b.WriteString(html.EscapeString(bg))
		b.WriteByte(';')
	}
	b.WriteByte('"')
}

// patchOpenCodePanel applies a batched PatchElements update for streaming
// transcript and/or status updates while the OpenCode panel is already open.
// Returns false when structural controls changed or the patch is refused, so
// the caller falls back to a full refresh + Redraw.
func (a *App) patchOpenCodePanel(ctx context.Context, prev, next openCodePanelView) bool {
	if !canPatchOpenCodePanel(prev, next) || a.page == nil {
		return false
	}

	patches := make([]ownframe.ElementPatch, 0, 2)
	if prev.Status != next.Status {
		status := next.Status
		patches = append(patches, ownframe.ElementPatch{
			ID: "opencode-panel-heading",
			Edits: []ownframe.ElementEdit{{
				ID:   "opencode-panel-status",
				Text: &status,
			}},
		})
	}
	if !sameOpenCodeMessages(prev.Messages, next.Messages) {
		transcriptHTML := renderOpenCodeTranscriptHTML(next)
		patches = append(patches, ownframe.ElementPatch{
			ID: openCodeTranscriptInnerID,
			Edits: []ownframe.ElementEdit{{
				InnerHTML: &transcriptHTML,
			}},
		})
	}
	if len(patches) == 0 {
		return true
	}

	return a.page.PatchElements(ctx, patches) == nil
}

func canPatchOpenCodePanel(prev, next openCodePanelView) bool {
	if !prev.Open || !next.Open {
		return false
	}
	if prev.Busy != next.Busy ||
		prev.CanInterrupt != next.CanInterrupt ||
		prev.CanPrompt != next.CanPrompt ||
		prev.WorkspaceAvailable != next.WorkspaceAvailable ||
		prev.Connected != next.Connected ||
		prev.SelectedID != next.SelectedID ||
		prev.IncludeSelection != next.IncludeSelection ||
		prev.ContextPreview != next.ContextPreview ||
		prev.ContextAvailable != next.ContextAvailable ||
		prev.CanReview != next.CanReview ||
		prev.Running != next.Running ||
		prev.CanRetry != next.CanRetry ||
		prev.Notice != next.Notice ||
		prev.TranscriptTop != next.TranscriptTop ||
		(prev.PendingSwitch == nil) != (next.PendingSwitch == nil) ||
		len(prev.OtherSessions) != len(next.OtherSessions) ||
		len(prev.Archived) != len(next.Archived) ||
		!sameOpenCodeApproval(prev.Approval, next.Approval) {
		return false
	}
	return sameOpenCodeSessions(prev.Sessions, next.Sessions) &&
		sameOpenCodeProposals(prev.Proposals, next.Proposals) &&
		sameOpenCodeAgentProposals(prev.AgentProposals, next.AgentProposals)
}

func sameOpenCodeApproval(a, b *openCodePanelApproval) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameOpenCodeSessions(a, b []openCodePanelSessionRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameOpenCodeMessages(a, b []openCodePanelMessageRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameOpenCodeAgentProposals(a, b []openCodePanelAgentProposalRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameOpenCodeProposals(a, b []openCodePanelProposalRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// renderOpenCodeTranscriptHTML produces the exact inner HTML of
// #opencode-transcript matching components/html/opencode_panel.html.
func renderOpenCodeTranscriptHTML(v openCodePanelView) string {
	var b strings.Builder
	if len(v.Messages) == 0 {
		b.WriteString(`<div class="opencode-transcript-empty">`)
		if v.SelectedID != "" {
			b.WriteString(`No saved messages yet. Send a message to start.`)
		} else {
			b.WriteString(`Choose or create a local session.`)
		}
		b.WriteString(`</div>`)
	} else {
		for _, m := range v.Messages {
			b.WriteString(`<article class="opencode-message role-`)
			b.WriteString(html.EscapeString(m.Role))
			b.WriteString(`" id="opencode-message-`)
			b.WriteString(html.EscapeString(m.ID))
			b.WriteString(`"><b>`)
			switch m.Role {
			case "assistant":
				b.WriteString(`OpenCode`)
			case "tool":
				b.WriteString(`Tool`)
			case "system":
				b.WriteString(`Notice`)
			case "turn":
				b.WriteString(`Turn`)
			default:
				b.WriteString(`You`)
			}
			b.WriteString(`</b>`)
			b.WriteString(string(m.Body))
			if m.Note != "" {
				b.WriteString(`<span class="opencode-message-note">`)
				b.WriteString(html.EscapeString(m.Note))
				b.WriteString(`</span>`)
			}
			b.WriteString(`</article>`)
		}
	}
	if len(v.Proposals) > 0 {
		b.WriteString(`<section class="opencode-proposals" aria-label="Isolated proposal snapshot"><header><b>Isolated proposal snapshot</b><span>Read-only</span></header>`)
		for _, p := range v.Proposals {
			b.WriteString(`<article class="opencode-proposal-file"><h3><span>`)
			b.WriteString(html.EscapeString(p.Change))
			b.WriteString(`</span>`)
			b.WriteString(html.EscapeString(p.Path))
			b.WriteString(`</h3><pre>`)
			b.WriteString(html.EscapeString(p.Diff))
			b.WriteString(`</pre></article>`)
		}
		b.WriteString(`</section>`)
	}
	if len(v.AgentProposals) > 0 {
		b.WriteString(`<section class="opencode-proposals opencode-agent-proposals" aria-label="Agent proposals"><header><b>Agent proposals</b><span>Not applied until you accept</span></header>`)
		for _, p := range v.AgentProposals {
			id := html.EscapeString(p.ID)
			b.WriteString(`<article class="opencode-proposal-file" id="` + id + `"><h3><span>`)
			b.WriteString(html.EscapeString(p.Change))
			b.WriteString(`</span>`)
			b.WriteString(html.EscapeString(p.Path))
			b.WriteString(`</h3><pre>`)
			b.WriteString(html.EscapeString(p.Diff))
			b.WriteString(`</pre>`)
			if p.Note != "" {
				b.WriteString(`<span class="opencode-message-note">`)
				b.WriteString(html.EscapeString(p.Note))
				b.WriteString(`</span>`)
			}
			b.WriteString(`<div class="opencode-prompt-tools"><button class="settings-btn" id="` + id + `-reject" data-action="opencode-proposal-reject:` + id + `" type="button">Reject</button><button class="settings-btn primary" id="` + id + `-accept" data-action="opencode-proposal-accept:` + id + `" type="button">Accept</button></div></article>`)
		}
		b.WriteString(`</section>`)
	}
	return b.String()
}
