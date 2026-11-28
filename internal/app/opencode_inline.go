package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/chinmay-sawant/own-code/internal/agent"
)

// Inline edits: a single-selection request to the selected Console model that
// never touches the document until the user accepts. The request carries an
// immutable snapshot (path, selection, full-buffer version, workspace root);
// the model runs with NO tools; the reply becomes an explicit patch against
// that snapshot; accept applies it as one undoable buffer operation and is
// refused when the buffer no longer matches the snapshot.

const (
	paletteCmdOCInlineEdit = "opencode-action-inline-edit"

	openCodeInlineSystem = "You edit one code selection for the user. Reply with ONLY the complete replacement text for the selection inside a single fenced code block. " +
		"If the selection is empty, the replacement is inserted at the caret marked <caret/>. Do not explain, do not repeat unrelated code, and do not touch other files."
	openCodeInlineMaxReply  = 256 << 10
	openCodeInlineMaxTokens = 8192
	openCodeInlineCtxLines  = "surrounding"
	openCodeInlineCtxPath   = "path"
)

type openCodeInlinePhase string

const (
	inlineDraft   openCodeInlinePhase = "draft"
	inlineRunning openCodeInlinePhase = "running"
	inlineReady   openCodeInlinePhase = "ready"
	inlineFailed  openCodeInlinePhase = "failed"
	inlineStale   openCodeInlinePhase = "stale"
)

// openCodeInlinePatch is the explicit change: replace runes [From,To) of the
// document whose full-buffer digest is BaseVersion. From==To is an insertion.
type openCodeInlinePatch struct {
	Path        string
	BaseVersion string
	From, To    int
	Original    string
	Replacement string
}

type openCodeInline struct {
	id          string
	phase       openCodeInlinePhase
	snap        agentContextSnapshot
	instruction string
	removed     map[string]bool
	response    string
	note        string
	patch       *openCodeInlinePatch
	cancel      context.CancelFunc
}

type openCodeInlineEvent struct {
	id    string
	kind  string // "delta", "done", "error"
	text  string
	final string
}

// openCodeInlineCard is the comparable panel view of the inline card.
type openCodeInlineCard struct {
	Phase, Path, Range, Instruction, Preview, Response, Diff, Note string
	Draft, Running, Ready, Stale, Failed                           bool
}

func sameOpenCodeInline(a, b *openCodeInlineCard) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func inlineDocVersion(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// inlinePromptText is the exact user message sent: the declared context only.
func inlinePromptText(snap agentContextSnapshot, instruction string, removed map[string]bool) string {
	var b strings.Builder
	if !removed[openCodeInlineCtxPath] {
		fmt.Fprintf(&b, "File: %s (lines %d-%d)\n", snap.RelativePath, snap.StartLine, snap.EndLine)
	}
	fmt.Fprintf(&b, "Instruction: %s\n", strings.TrimSpace(instruction))
	if !removed[openCodeInlineCtxLines] {
		rs := []rune(snap.Text)
		from, to := min(max(snap.SelectionFrom, 0), len(rs)), min(max(snap.SelectionTo, 0), len(rs))
		if from == to {
			b.WriteString("Surrounding code (caret marked <caret/>):\n```\n" + string(rs[:from]) + "<caret/>" + string(rs[from:]) + "\n```\n")
		} else {
			b.WriteString("Surrounding code:\n```\n" + snap.Text + "\n```\n")
		}
	}
	if snap.SelectionText == "" {
		b.WriteString("Selection: (empty; insert at the caret)\n")
	} else {
		b.WriteString("Selection to replace:\n```\n" + snap.SelectionText + "\n```\n")
	}
	return b.String()
}

// extractInlineReplacement returns the first fenced block's body, or the whole
// reply when the model used no fence.
func extractInlineReplacement(reply string) string {
	reply = strings.ReplaceAll(reply, "\r\n", "\n")
	start := strings.Index(reply, "```")
	if start < 0 {
		return strings.TrimSuffix(reply, "\n")
	}
	rest := reply[start+3:]
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		return ""
	}
	rest = rest[nl+1:]
	if end := strings.Index(rest, "```"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSuffix(rest, "\n")
}

// matchInlineEnds makes the replacement follow the original's line ending
// style and final-newline state, so an inline edit never flips either.
func matchInlineEnds(original, replacement string) string {
	replacement = strings.ReplaceAll(replacement, "\r\n", "\n")
	crlf := strings.Contains(original, "\r\n")
	endsNL := strings.HasSuffix(original, "\n")
	if endsNL && replacement != "" && !strings.HasSuffix(replacement, "\n") {
		replacement += "\n"
	}
	if !endsNL {
		replacement = strings.TrimSuffix(replacement, "\n")
	}
	if crlf {
		replacement = strings.ReplaceAll(replacement, "\n", "\r\n")
	}
	return replacement
}

// buildInlinePatch converts a model reply into a patch against the snapshot.
func buildInlinePatch(snap agentContextSnapshot, reply string) (*openCodeInlinePatch, error) {
	if len(reply) > openCodeInlineMaxReply {
		return nil, errors.New("The reply is too large for an inline edit. Use the workspace review flow for large changes.")
	}
	repl := matchInlineEnds(snap.SelectionText, extractInlineReplacement(reply))
	if strings.TrimSpace(repl) == "" {
		return nil, errors.New("The model returned no replacement text.")
	}
	if repl == snap.SelectionText {
		return nil, errors.New("The model proposed no change.")
	}
	if !utf8.ValidString(repl) {
		return nil, errors.New("The replacement is not valid UTF-8.")
	}
	return &openCodeInlinePatch{
		Path: snap.RelativePath, BaseVersion: snap.Version,
		From: snap.DocumentFrom, To: snap.DocumentTo,
		Original: snap.SelectionText, Replacement: repl,
	}, nil
}

func inlineDiffText(p *openCodeInlinePatch) string {
	var b strings.Builder
	write := func(prefix, text string) {
		if text == "" {
			return
		}
		lines := strings.Split(strings.ReplaceAll(strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\r", ""), "\n")
		for i, l := range lines {
			if i == 40 {
				fmt.Fprintf(&b, "%s… %d more line(s)\n", prefix, len(lines)-40)
				break
			}
			b.WriteString(prefix + l + "\n")
		}
	}
	write("- ", p.Original)
	write("+ ", p.Replacement)
	return strings.TrimSuffix(b.String(), "\n")
}

func (a *App) openCodeInlineView() (*openCodeInlineCard, []openCodeChipRow) {
	in := a.openCode.inline
	if in == nil {
		return nil, nil
	}
	card := &openCodeInlineCard{
		Phase: string(in.phase), Path: in.snap.RelativePath, Instruction: in.instruction,
		Response: in.response, Note: in.note,
		Draft: in.phase == inlineDraft, Running: in.phase == inlineRunning, Ready: in.phase == inlineReady,
		Stale: in.phase == inlineStale, Failed: in.phase == inlineFailed,
	}
	if in.snap.DocumentFrom == in.snap.DocumentTo {
		card.Range = "caret"
	} else {
		card.Range = fmt.Sprintf("selection, %d chars", utf8.RuneCountInString(in.snap.SelectionText))
	}
	card.Preview = inlinePromptText(in.snap, in.instruction, in.removed)
	if in.patch != nil {
		card.Diff = inlineDiffText(in.patch)
	}
	chips := []openCodeChipRow{{ID: "selection", Label: "selection", Detail: "required"}}
	if !in.removed[openCodeInlineCtxLines] {
		chips = append(chips, openCodeChipRow{ID: openCodeInlineCtxLines, Label: "surrounding lines", Detail: fmt.Sprintf("lines %d-%d", in.snap.StartLine, in.snap.EndLine), Truncated: in.snap.Truncated})
	}
	if !in.removed[openCodeInlineCtxPath] {
		chips = append(chips, openCodeChipRow{ID: openCodeInlineCtxPath, Label: "file path", Detail: in.snap.RelativePath})
	}
	return card, chips
}

// beginOpenCodeInline captures the selection (or caret) and shows the draft
// card. Nothing is sent until the user presses Send.
func (a *App) beginOpenCodeInline(ctx context.Context) error {
	if in := a.openCode.inline; in != nil && in.phase == inlineRunning {
		a.openCode.panelStatus = "An inline edit is already running. Stop it first."
		a.refresh()
		return nil
	}
	snap, err := a.currentOpenCodeContext()
	if err != nil {
		a.openCode.panelStatus = err.Error()
		a.refresh()
		return nil
	}
	instruction := ""
	if old := a.openCode.inline; old != nil {
		instruction = old.instruction
	}
	a.openCode.inline = &openCodeInline{phase: inlineDraft, snap: snap, instruction: instruction, removed: map[string]bool{}}
	a.openCode.panelStatus = firstNonEmpty(a.openCodeRequestNotice(), "Describe the change, review the context, then press Send.")
	a.refresh()
	if a.page == nil {
		return nil
	}
	return a.focusAfterLayout(ctx, "opencode-inline-instruction")
}

func (a *App) syncInlineInstruction() {
	if in := a.openCode.inline; in != nil && in.phase == inlineDraft {
		if v := a.opencodeCredentialInput("opencode-inline-instruction"); v != "" || in.instruction == "" {
			in.instruction = v
		}
	}
}

func (a *App) openCodeInlineAction(ctx context.Context, action string) error {
	in := a.openCode.inline
	if in == nil {
		return nil
	}
	switch {
	case strings.HasPrefix(action, "opencode-panel-inline-ctx-remove:"):
		a.syncInlineInstruction()
		if id := strings.TrimPrefix(action, "opencode-panel-inline-ctx-remove:"); id == openCodeInlineCtxLines || id == openCodeInlineCtxPath {
			in.removed[id] = true
		}
		a.refresh()
	case action == "opencode-panel-inline-send":
		a.syncInlineInstruction()
		return a.sendOpenCodeInline(in)
	case action == "opencode-panel-inline-stop":
		if in.cancel != nil {
			in.cancel()
		}
		in.phase, in.note = inlineFailed, "Stopped. The document was not changed."
		a.refresh()
	case action == "opencode-panel-inline-dismiss", action == "opencode-panel-inline-reject":
		if in.cancel != nil {
			in.cancel()
		}
		a.openCode.inline = nil
		a.openCode.panelStatus = "Inline edit discarded. The document was not changed."
		a.refresh()
	case action == "opencode-panel-inline-retry":
		if in.phase == inlineStale {
			return nil
		}
		return a.sendOpenCodeInline(in)
	case action == "opencode-panel-inline-refresh", action == "opencode-panel-inline-rerun":
		snap, err := a.currentOpenCodeContext()
		if err != nil {
			a.openCode.panelStatus = err.Error()
			a.refresh()
			return nil
		}
		in.snap, in.phase, in.patch, in.response, in.note = snap, inlineDraft, nil, "", ""
		a.refresh()
		if action == "opencode-panel-inline-rerun" {
			return a.sendOpenCodeInline(in)
		}
	case action == "opencode-panel-inline-accept":
		return a.acceptOpenCodeInline(ctx)
	}
	return nil
}

// sendOpenCodeInline starts the no-tools request off the UI loop.
func (a *App) sendOpenCodeInline(in *openCodeInline) error {
	fail := func(msg string) error {
		in.phase, in.note = inlineFailed, msg
		a.openCode.panelStatus = msg
		a.refresh()
		return nil
	}
	if strings.TrimSpace(in.instruction) == "" {
		in.phase = inlineDraft
		a.openCode.panelStatus = "Describe the change first."
		a.refresh()
		return nil
	}
	if in.phase == inlineRunning {
		return nil
	}
	if n := a.openCodeRequestNotice(); n != "" {
		return fail(n)
	}
	if a.ws == nil || a.ws.Sample {
		return fail("Open a project folder to use inline edits.")
	}
	pid, mid := a.openCodeSelectedModel()
	var target agent.ModelTarget
	found := false
	for _, m := range agent.FilterEligible(a.openCode.consoleModels) {
		if m.ProviderID == pid && m.ID == mid {
			t, err := agent.ResolveModel(m)
			if err != nil {
				return fail("Model rejected: " + err.Error())
			}
			target, found = t, true
			break
		}
	}
	if !found {
		return fail("Choose an available Console model first.")
	}
	raw := make([]byte, 9)
	if _, err := rand.Read(raw); err != nil {
		return fail("Could not create a request ID.")
	}
	in.id = "inline-" + base64.RawURLEncoding.EncodeToString(raw)
	rctx, cancel := context.WithCancel(a.openCode.lifeCtx)
	in.cancel, in.phase, in.response, in.note, in.patch = cancel, inlineRunning, "", "", nil
	a.openCode.panelStatus = "Asking the model… the document is not changed."
	a.refresh()
	req := agent.Request{
		SessionID: in.id, TurnID: in.id, Model: target, Tools: nil, MaxOutputTokens: openCodeInlineMaxTokens,
		Messages: []agent.Message{
			{Role: agent.RoleSystem, Content: openCodeInlineSystem},
			{Role: agent.RoleUser, Content: inlinePromptText(in.snap, in.instruction, in.removed)},
		},
	}
	go executeOpenCodeInline(rctx, in.id, req, a.openCode.testProvider, a.openCodeBackend(), a.openCode.results, a.openCode.lifeCtx)
	return nil
}

// executeOpenCodeInline is the single no-tools request path. It never
// advertises or executes tools and reports only through the results channel.
func executeOpenCodeInline(ctx context.Context, id string, req agent.Request, provider agent.Provider, backend agent.ProviderDescriptor, results chan<- openCodeReply, life context.Context) {
	send := func(ev openCodeInlineEvent) {
		ev.id = id
		select {
		case results <- openCodeReply{inline: &ev}:
		case <-life.Done():
		}
	}
	if provider == nil {
		key, err := loadOpenCodeKeyForDir("")
		if err != nil {
			send(openCodeInlineEvent{kind: "error", text: "Could not read the OpenCode Console API key."})
			return
		}
		client, err := backend.NewRequestProvider(key)
		if err != nil {
			send(openCodeInlineEvent{kind: "error", text: "The OpenCode Console API key is invalid."})
			return
		}
		provider = client
		if f, ok := client.(interface{ Forget() }); ok {
			defer f.Forget()
		}
	}
	res, err := provider.Stream(ctx, req, func(e agent.Event) {
		if e.Kind == agent.EventTextDelta && e.Text != "" {
			send(openCodeInlineEvent{kind: "delta", text: e.Text})
		}
	})
	switch {
	case ctx.Err() != nil || res.Status == agent.StatusInterrupted:
		send(openCodeInlineEvent{kind: "error", text: "Stopped. The document was not changed."})
	case err != nil || res.Status == agent.StatusFailed:
		send(openCodeInlineEvent{kind: "error", text: "The request failed. The document was not changed."})
	default:
		send(openCodeInlineEvent{kind: "done", final: res.Text})
	}
}

// handleOpenCodeInlineEvent runs on the UI thread; stale request IDs drop.
func (a *App) handleOpenCodeInlineEvent(ev *openCodeInlineEvent) bool {
	in := a.openCode.inline
	if in == nil || in.id != ev.id || in.phase != inlineRunning {
		return false
	}
	switch ev.kind {
	case "delta":
		if len(in.response) < openCodeInlineMaxReply {
			in.response += ev.text
		}
	case "error":
		in.phase, in.note = inlineFailed, ev.text
		a.openCode.panelStatus = ev.text
	case "done":
		reply := firstNonEmpty(ev.final, in.response)
		patch, err := buildInlinePatch(in.snap, reply)
		if err != nil {
			in.phase, in.note = inlineFailed, err.Error()
			a.openCode.panelStatus = err.Error()
			break
		}
		in.patch, in.phase = patch, inlineReady
		in.note = "Single-selection edit. Multi-file changes belong in the workspace review flow."
		a.openCode.panelStatus = "Inline proposal ready. Nothing is applied until you accept."
	default:
		return false
	}
	if in.phase != inlineRunning && in.cancel != nil {
		in.cancel()
	}
	return true
}

// acceptOpenCodeInline applies the patch as one undoable buffer operation,
// refusing when the buffer no longer matches the request snapshot.
func (a *App) acceptOpenCodeInline(ctx context.Context) error {
	in := a.openCode.inline
	if in == nil || in.phase != inlineReady || in.patch == nil {
		return nil
	}
	p := in.patch
	stale := func(why string) error {
		in.phase, in.note = inlineStale, why+" Refresh to re-capture the selection, or Rerun the request. Nothing was applied."
		a.openCode.panelStatus = "Inline edit not applied: the file changed."
		a.refresh()
		return nil
	}
	if !a.editing || a.edit == nil || a.editPath() != p.Path {
		return stale("The file is no longer the active editor buffer.")
	}
	text := a.edit.Text()
	if inlineDocVersion(text) != p.BaseVersion {
		return stale("The file changed after the request was made.")
	}
	runes := []rune(text)
	if p.From < 0 || p.To < p.From || p.To > len(runes) || string(runes[p.From:p.To]) != p.Original {
		return stale("The selected range no longer matches.")
	}
	var err error
	if p.From < p.To {
		err = a.replaceEditRanges(ctx, []bufferRep{{Start: p.From, End: p.To, Insert: p.Replacement}})
	} else {
		err = a.insertInlineEditText(ctx, p.From, p.Replacement)
	}
	if err != nil {
		return err
	}
	a.openCode.inline = nil
	a.openCode.panelStatus = "Inline edit applied. Undo restores the original."
	a.refresh()
	return nil
}

// insertInlineEditText is replaceEditRanges for a pure insertion (empty range),
// with the same single undo point.
func (a *App) insertInlineEditText(ctx context.Context, at int, insert string) error {
	text := a.edit.Text()
	runes := []rune(text)
	if at < 0 || at > len(runes) {
		return errors.New("insert position is outside the document")
	}
	a.pushUndo()
	a.editRedo = nil
	ins := []rune(insert)
	out := append(append(append([]rune(nil), runes[:at]...), ins...), runes[at:]...)
	a.edit.SetText(string(out))
	a.dirty[a.edit.Path] = a.edit.Dirty
	a.editCaret = at + len(ins)
	a.editAnchor = a.editCaret
	line, _ := a.editDisplayLineCol()
	if doc := a.docs[a.edit.Path]; doc != nil && doc.Syntax != nil {
		doc.Syntax.markDirty(line)
	}
	a.commitEditBuffer()
	a.ensureEditCaretVisible()
	a.refresh()
	if a.page == nil {
		return nil
	}
	return a.page.Redraw(ctx)
}
