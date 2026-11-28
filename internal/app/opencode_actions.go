package app

import (
	"context"
	"errors"
	"strings"
)

// Palette and quick actions for the OpenCode panel. Every context action
// assembles ONLY the context its name declares as removable chips, fills the
// composer and stops: the user inspects the exact prompt and context preview
// and presses Send. Nothing is sent automatically and the selected session is
// never changed. The chips go through the same capture, stale-check and
// bounded-serialization path as an @ mention.
const (
	paletteCmdOCStartTask        = "opencode-action-start-task"
	paletteCmdOCOpenSession      = "opencode-action-open-session"
	paletteCmdOCExplainSelection = "opencode-action-explain-selection"
	paletteCmdOCExplainProblem   = "opencode-action-explain-problem"
	paletteCmdOCFixProblem       = "opencode-action-fix-problem"
	paletteCmdOCReviewChanges    = "opencode-action-review-changes"
	paletteCmdOCInterrupt        = "opencode-action-interrupt"
	paletteCmdOCActionPrefix     = "opencode-action-"
)

func openCodeActionRows() []paletteRow {
	return []paletteRow{
		{Cmd: paletteCmdOCStartTask, Name: "OpenCode: Start Agent Task", Dir: "Focus the composer in the selected session"},
		{Cmd: paletteCmdOCOpenSession, Name: "OpenCode: Open Saved Session…", Dir: "Pick a saved session from the history menu"},
		{Cmd: paletteCmdOCExplainSelection, Name: "OpenCode: Explain Selection", Dir: "Attach only the editor selection and draft a prompt"},
		{Cmd: paletteCmdOCExplainProblem, Name: "OpenCode: Explain Problem", Dir: "Attach only the Problem at the caret (or the first in this file)"},
		{Cmd: paletteCmdOCFixProblem, Name: "OpenCode: Fix Problem", Dir: "Attach only that Problem; edits still arrive as reviewable proposals"},
		{Cmd: paletteCmdOCReviewChanges, Name: "OpenCode: Review Pending Changes", Dir: "Attach only the working-tree diff (tracked and untracked)"},
		{Cmd: paletteCmdOCInlineEdit, Name: "OpenCode: Edit Selection Inline…", Dir: "Ask the selected model to change the selection or caret; review before applying"},
		{Cmd: paletteCmdOCInterrupt, Name: "OpenCode: Interrupt Run", Dir: "Stop the running turn of the selected session"},
	}
}

// openCodeActionPlan is the deterministic result of a context action.
type openCodeActionPlan struct {
	Prompt string
	Chips  []openCodeChip
}

// planOpenCodeAction captures exactly the declared sources for one action.
func (a *App) planOpenCodeAction(cmd string) (openCodeActionPlan, error) {
	if _, err := a.openCodeChipRoot(); err != nil {
		return openCodeActionPlan{}, err
	}
	switch cmd {
	case paletteCmdOCExplainSelection:
		chip, err := a.captureOpenCodeChip(openCodeMentionCandidate{Kind: openCodeChipSelection})
		if err != nil {
			return openCodeActionPlan{}, err
		}
		return openCodeActionPlan{
			Prompt: "Explain the attached selection (" + chip.Detail + "): what it does, why it may be written this way, and anything surprising.",
			Chips:  []openCodeChip{chip},
		}, nil
	case paletteCmdOCExplainProblem, paletteCmdOCFixProblem:
		cand, ok := a.openCodeActionProblem()
		if !ok {
			return openCodeActionPlan{}, errors.New("there are no Problems to use; open a file with diagnostics first")
		}
		chip, err := a.captureOpenCodeChip(cand)
		if err != nil {
			return openCodeActionPlan{}, err
		}
		prompt := "Explain the attached problem (" + chip.Detail + "): the likely cause and how to resolve it."
		if cmd == paletteCmdOCFixProblem {
			prompt = "Propose a minimal fix for the attached problem (" + chip.Detail + "). Read the file with your tools if you need more context; edits will be reviewed before they apply."
		}
		return openCodeActionPlan{Prompt: prompt, Chips: []openCodeChip{chip}}, nil
	case paletteCmdOCReviewChanges:
		chip, err := a.captureOpenCodeChip(openCodeMentionCandidate{Kind: openCodeChipDiff})
		if err != nil {
			return openCodeActionPlan{}, err
		}
		return openCodeActionPlan{
			Prompt: "Review the attached uncommitted changes (" + chip.Detail + "). List correctness bugs, risky edits and missing tests, most severe first.",
			Chips:  []openCodeChip{chip},
		}, nil
	}
	return openCodeActionPlan{}, errors.New("unknown OpenCode action")
}

// openCodeActionProblem picks the Problem at the caret line of the active
// file, else the first Problem of that file, else the first Problem overall.
func (a *App) openCodeActionProblem() (openCodeMentionCandidate, bool) {
	if a.lspm == nil {
		return openCodeMentionCandidate{}, false
	}
	a.lspm.rebuildProblems()
	var inFile, atCaret, first *lspProblem
	active := ""
	if a.editing && a.edit != nil {
		active = a.editPath()
	}
	line := -1
	if snap, err := a.currentOpenCodeContext(); err == nil {
		line = snap.StartLine - 1
	}
	for i := range a.lspm.problems {
		p := &a.lspm.problems[i]
		if isSensitiveAgentContextPath(p.Path) {
			continue
		}
		if first == nil {
			first = p
		}
		if active != "" && p.Path == active {
			if inFile == nil {
				inFile = p
			}
			if atCaret == nil && p.Line == line {
				atCaret = p
			}
		}
	}
	for _, p := range []*lspProblem{atCaret, inFile, first} {
		if p != nil {
			return openCodeMentionCandidate{Kind: openCodeChipProblems, Label: "@problem", Path: p.Path, Problem: openCodeProblemLine(*p)}, true
		}
	}
	return openCodeMentionCandidate{}, false
}

// openCodeActionNotice is the visible state when a prepared prompt cannot be
// sent yet: offline or missing key, no model, no session, a pending
// permission, or a running turn. Empty means Send is available.
func (a *App) openCodeActionNotice() string {
	if a.ws == nil || a.ws.Sample {
		return "Open a project folder to use OpenCode."
	}
	if n := a.openCodeRequestNotice(); n != "" {
		return n
	}
	if a.openCode.selectedSessionID == "" {
		return "No session is selected. Choose or create one to send."
	}
	if run := a.openCodeRunFor(a.openCode.selectedSessionID); run != nil {
		if run.approval != nil {
			return "Permission needed: approve or deny the pending tool call before sending."
		}
		return "A turn is running. Send when it finishes."
	}
	return ""
}

// runOpenCodeAction runs one palette action through the panel's own state.
func (a *App) runOpenCodeAction(ctx context.Context, cmd string) error {
	if !a.openCode.panelOpen {
		if err := a.openOpenCodePanel(ctx); err != nil {
			return err
		}
	}
	a.openCode.historyOpen, a.openCode.modelMenuOpen = false, false
	switch cmd {
	case paletteCmdOCStartTask:
		if a.openCode.selectedSessionID == "" && a.openCode.connected && a.ws != nil && !a.ws.Sample {
			return a.createOpenCodeSession()
		}
		a.openCode.panelStatus = firstNonEmpty(a.openCodeActionNotice(), "Describe the task, then press Send.")
		a.refresh()
		return a.focusOpenCodePrompt(ctx)
	case paletteCmdOCOpenSession:
		a.openCode.historyOpen = true
		a.openCode.panelStatus = "Choose a saved session."
		a.refresh()
		return nil
	case paletteCmdOCInlineEdit:
		return a.beginOpenCodeInline(ctx)
	case paletteCmdOCInterrupt:
		if a.openCodeRunFor(a.openCode.selectedSessionID) == nil {
			a.openCode.panelStatus = "No turn is running in this session."
			a.refresh()
			return nil
		}
		return a.interruptOpenCodeSession()
	}
	plan, err := a.planOpenCodeAction(cmd)
	if err != nil {
		a.openCode.panelStatus = err.Error()
		a.refresh()
		return nil
	}
	if err := a.fillOpenCodeComposer(plan); err != nil {
		a.openCode.panelStatus = err.Error()
		a.refresh()
		return nil
	}
	a.openCode.panelStatus = firstNonEmpty(a.openCodeActionNotice(), "Review the prompt and attached context, then press Send.")
	a.refresh()
	return a.focusOpenCodePrompt(ctx)
}

// fillOpenCodeComposer replaces the chips with the plan's and sets the
// prompt text. A typed draft is kept: the action text goes after it.
func (a *App) fillOpenCodeComposer(plan openCodeActionPlan) error {
	draft := strings.TrimSpace(openCodeTypedPart(a.openCode.composer.text))
	a.clearOpenCodeComposer()
	for _, chip := range plan.Chips {
		if err := a.addOpenCodeChip(chip); err != nil {
			a.clearOpenCodeComposer()
			return err
		}
	}
	text := plan.Prompt
	if draft != "" {
		text = draft + "\n\n" + plan.Prompt
	}
	a.openCode.composer.text = clampOpenCodePrompt(text)
	if a.page != nil {
		a.page.Form().SetFormValue("opencode-prompt", a.openCode.composer.text)
	}
	return nil
}

func (a *App) focusOpenCodePrompt(ctx context.Context) error {
	if a.page == nil {
		return nil
	}
	return a.focusAfterLayout(ctx, "opencode-prompt")
}
