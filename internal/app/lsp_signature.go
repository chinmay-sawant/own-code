package app

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/chinmay-sawant/own-code/internal/lsp"
)

// LSP signature help: typing "(" or "," (or any server trigger character)
// asks textDocument/signatureHelp and shows the active signature in the
// popup card below the caret. ")" and Esc close it; a "," refreshes it.

// sigCaretTag marks the popup as signature help (popupSpec.Caret).
const sigCaretTag = -0x534947

type signatureState struct {
	nextID  int
	pending int
	shown   bool
}

// sigTriggers are the characters used when the server declares none.
var sigDefaultTriggers = []string{"(", ","}

// signatureTyped is called with each typed text. It requests, refreshes or
// closes the card.
func (a *App) signatureTyped(text string) {
	m := a.lspm
	if m == nil || !m.enabled || a.edit == nil || utf8.RuneCountInString(text) != 1 {
		return
	}

	st := &m.ext.sig

	if text == ")" {
		m.signatureHide()

		return
	}

	ld := m.docs[a.edit.Path]
	if ld == nil || ld.server == nil {
		return
	}

	triggers := sigDefaultTriggers

	ld.server.smu.Lock()
	c := ld.server.client
	ld.server.smu.Unlock()

	if c != nil {
		triggers = c.SignatureTriggers()
	}

	hit := false

	for _, t := range triggers {
		if t == text {
			hit = true
		}
	}

	if hit && a.popup.open && a.popup.spec.Caret == cmpCaretTag {
		m.completionHide()
	}

	if !hit || (a.popup.open && !st.shown) {
		return
	}

	_ = a.requestSignatureHelp(text)
}

// signatureHelpAtCaret is the palette command.
func (a *App) signatureHelpAtCaret(ctx context.Context) error {
	if !a.editing || a.edit == nil || a.mode != "explorer" || !a.requestSignatureHelp("") {
		a.showToast("info", "Signature help", "Press Ctrl+E in a file with a running language server first.")
		a.refresh()

		return a.page.Redraw(ctx)
	}

	return nil
}

func (a *App) requestSignatureHelp(trigger string) bool {
	m, ld := a.syncedDoc(a.edit.Path)
	if m == nil {
		return false
	}

	st := &m.ext.sig
	st.nextID++
	id := st.nextID
	st.pending = id

	line, col := a.editCaretLineCol()

	m.callDoc(ld, line, col, func(ctx context.Context, c *lsp.Client, uri string, pos lsp.Position) (any, error) {
		return c.SignatureHelp(ctx, uri, pos, trigger)
	}, func(_ context.Context, res any, err error, fresh bool) bool {
		if id != st.pending {
			return false
		}

		st.pending = 0
		help, _ := res.(*lsp.SignatureHelp)

		if err != nil || help == nil || !fresh || !a.editing || a.edit == nil || a.edit.Path != ld.path || (a.popup.open && !st.shown) {
			m.signatureHide()

			return true
		}

		x, top, bottom := a.editCaretAnchor()
		text, w := signatureCard(help)

		a.showPopup(popupSpec{
			Label:   "Signature help",
			AnchorX: x, AnchorTop: top, AnchorBottom: bottom,
			Width:     w,
			Caret:     sigCaretTag,
			Content:   text,
			OnDismiss: func(popupDismiss) { st.shown = false },
		})

		st.shown = true

		return true
	})

	return true
}

// signatureHide closes the card when it is the open popup.
func (m *lspManager) signatureHide() {
	a, st := m.app, &m.ext.sig
	if st.shown && a.popup.open && a.popup.spec.Caret == sigCaretTag {
		a.hidePopup(popupDismissCode)
	}

	st.shown, st.pending = false, 0
}

// signatureCard renders the active signature as Markdown: the label in a
// fence, then the active parameter and the documentation.
func signatureCard(h *lsp.SignatureHelp) (string, int) {
	sig := h.Signatures[min(max(h.ActiveSignature, 0), len(h.Signatures)-1)]

	var b strings.Builder

	if len(h.Signatures) > 1 {
		b.WriteString("(" + strconv.Itoa(h.ActiveSignature+1) + "/" + strconv.Itoa(len(h.Signatures)) + ")\n")
	}

	b.WriteString("```\n" + sig.Label + "\n```\n")

	active := h.ActiveParameter
	if sig.ActiveParam >= 0 {
		active = sig.ActiveParam
	}

	if active >= 0 && active < len(sig.Parameters) && sig.Parameters[active].Label != "" {
		b.WriteString("Parameter: `" + sig.Parameters[active].Label + "`\n")
	}

	if doc := strings.TrimSpace(sig.Documentation); doc != "" {
		b.WriteString("\n" + doc + "\n")
	}

	return hoverCard(b.String())
}
