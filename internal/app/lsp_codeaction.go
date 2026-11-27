package app

import (
	"context"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/lsp"
)

// LSP code actions (quick fixes). The palette command "LSP: Code Actions"
// (Ctrl+.) asks textDocument/codeAction for the caret line with the
// diagnostics that cover it and lists the answers in the popup. Choosing one
// stages its WorkspaceEdit through the shared confirm dialog (the rename
// path) and runs its command, if any, on the server.

const (
	// codeActionCaretTag marks the popup as the code action list.
	codeActionCaretTag = -0x434143

	paletteCmdLSPCodeAction = "lsp-code-action"
	paletteCmdLSPSignature  = "lsp-signature-help"

	confirmTitleCodeAction = "Apply code action"
)

type codeActionState struct {
	nextID  int
	pending int
	// title of the action whose edit is staged, shown in the confirm.
	staged string
}

// codeActionsAtCaret requests and lists the code actions for the caret.
func (a *App) codeActionsAtCaret(ctx context.Context) error {
	toast := func(msg string) error {
		a.showToast("info", "Code actions", msg)
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if !a.editing || a.edit == nil || a.mode != "explorer" {
		return toast("Press Ctrl+E to edit, then Ctrl+. lists code actions.")
	}

	m, ld := a.syncedDoc(a.edit.Path)
	if m == nil {
		return toast("No language server is running for this file.")
	}

	line, col := a.editCaretLineCol()
	st := &m.ext.acts
	st.nextID++
	id := st.nextID
	st.pending = id

	diags := m.caretDiagnostics(ld.path, line, col)

	m.callDoc(ld, line, col, func(ctx context.Context, c *lsp.Client, uri string, pos lsp.Position) (any, error) {
		if !c.SupportsCodeActions() {
			return []lsp.CodeAction(nil), nil
		}

		r := lsp.Range{Start: pos, End: pos}
		for i := range diags {
			diags[i].Range.Start.Line, diags[i].Range.End.Line = pos.Line, pos.Line
		}

		return c.CodeActions(ctx, uri, r, diags)
	}, func(_ context.Context, res any, err error, fresh bool) bool {
		if id != st.pending {
			return false
		}

		st.pending = 0
		acts, _ := res.([]lsp.CodeAction)

		switch {
		case err != nil:
			a.showToast("error", "Code actions", err.Error())
		case !fresh || !a.editing || a.edit == nil || a.edit.Path != ld.path:
		case len(acts) == 0:
			a.showToast("info", "Code actions", "No code actions available here.")
		default:
			m.showCodeActions(ld, acts)
		}

		return true
	})

	return nil
}

// caretDiagnostics returns the diagnostics of path that touch the caret line.
func (m *lspManager) caretDiagnostics(path string, line, col int) []lsp.Diagnostic {
	var out []lsp.Diagnostic

	for _, d := range m.diags[path] {
		if d.Unopened || d.Line != line {
			continue
		}

		out = append(out, lsp.Diagnostic{
			Range:    lsp.Range{Start: lsp.Position{Line: d.Line, Character: d.From}, End: lsp.Position{Line: d.Line, Character: d.To}},
			Severity: d.Severity, Source: d.Source, Message: d.Message,
		})
	}

	return out
}

// showCodeActions opens the list popup at the caret.
func (m *lspManager) showCodeActions(ld *lspDoc, acts []lsp.CodeAction) {
	a := m.app
	rows := make([]popupItem, 0, len(acts))

	for _, ca := range acts {
		if ca.Disabled {
			continue
		}

		label := ca.Title
		if ca.IsPreferred {
			label += " *"
		}

		rows = append(rows, popupItem{Label: label, Detail: ca.Kind, Kind: "fix", Data: ca})
	}

	if len(rows) == 0 {
		a.showToast("info", "Code actions", "No code actions available here.")

		return
	}

	x, top, bottom := a.editCaretAnchor()

	a.showPopup(popupSpec{
		Label:   "Code actions",
		AnchorX: x, AnchorTop: top, AnchorBottom: bottom,
		Width:    420,
		Caret:    codeActionCaretTag,
		Items:    rows,
		OnSelect: func(item popupItem, _ int) { m.runCodeAction(ld, item) },
	})
}

// runCodeAction resolves a lazily filled action, then stages its edit and
// runs its command.
func (m *lspManager) runCodeAction(ld *lspDoc, item popupItem) {
	a := m.app

	ca, ok := item.Data.(lsp.CodeAction)
	if !ok {
		return
	}

	if ca.Edit != nil || ca.Command != nil {
		m.finishCodeAction(ld, ca)

		return
	}

	m.callServer(ld.server, ld.uri, func(ctx context.Context, c *lsp.Client, _ *lspSent) (any, error) {
		return c.ResolveCodeAction(ctx, ca)
	}, func(_ context.Context, res any, err error) bool {
		if err != nil {
			a.showToast("error", "Code actions", err.Error())

			return true
		}

		if got, ok := res.(lsp.CodeAction); ok {
			ca = got
		}

		m.finishCodeAction(ld, ca)

		return true
	})
}

func (m *lspManager) finishCodeAction(ld *lspDoc, ca lsp.CodeAction) {
	a := m.app
	ctx := context.Background()

	if ca.Edit != nil {
		m.ext.acts.staged = ca.Title
		a.stageRename(ctx, "", "", ca.Edit)

		if a.confirmOpen {
			a.confirmTitle = confirmTitleCodeAction + ": " + ca.Title + "?"
		}
	}

	if ca.Command != nil {
		cmd := *ca.Command

		m.callServer(ld.server, ld.uri, func(ctx context.Context, c *lsp.Client, _ *lspSent) (any, error) {
			return nil, c.ExecuteCommand(ctx, cmd)
		}, func(_ context.Context, _ any, err error) bool {
			if err != nil {
				a.showToast("error", "Code actions", err.Error())
			} else if ca.Edit == nil {
				a.showToast("success", "Code actions", strings.TrimSpace(ca.Title))
			}

			return true
		})
	}

	if ca.Edit == nil && ca.Command == nil {
		a.showToast("info", "Code actions", "The server returned nothing to apply.")
	}

	a.refresh()
}
