package app

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chinmay-sawant/own-code/internal/lsp"
)

// LSP hover: when the pointer rests on an identifier in the code body of a
// synced file, ask the server for textDocument/hover and show the answer in
// the popup widget. The request runs off the UI loop through the server's op
// queue; a reply is dropped when the document version, the tab, or the
// hovered token changed meanwhile.

const (
	// hoverDwell is how long the pointer must rest before a request is made.
	hoverDwell = 400 * time.Millisecond
	// hoverPoll is how often a shown card re-checks the pointer.
	hoverPoll = 120 * time.Millisecond
	// hoverTimeout bounds one request.
	hoverTimeout = 5 * time.Second

	hoverMaxLines = 18
	hoverMaxRunes = 2400
	hoverMaxW     = 520

	// hoverCaretTag marks a popup opened by hover (popupSpec.Caret), so the
	// poller never dismisses or replaces a popup that belongs to someone else.
	hoverCaretTag = -0x484f56
)

// hoverKey identifies one hover target.
type hoverKey struct {
	path       string
	line       int
	start, end int // rune columns of the identifier in the raw line
	version    int
}

// hoverTarget is the identifier under the pointer, with its anchor in page
// pixels.
type hoverTarget struct {
	key               hoverKey
	group             int
	name              string
	left, top, bottom int
	scrollX, scrollY  int
}

// hoverState is the hover poller's state. It lives on the lspManager and is
// touched only on the app loop.
type hoverState struct {
	// cursor and dwell are test seams; zero values use the real window.
	cursor func() (int, int)
	dwell  time.Duration

	seen      bool
	x, y      int
	since     time.Time
	evaluated bool
	cur       hoverTarget
	curOK     bool

	reqKey     hoverKey
	pendingID  int
	pendingKey hoverKey
	nextID     int

	shown    bool
	shownTgt hoverTarget
}

func (h *hoverState) dwellTime() time.Duration {
	if h.dwell > 0 {
		return h.dwell
	}

	return hoverDwell
}

// hoverBlocked reports whether something else owns the pointer or the screen.
func (a *App) hoverBlocked() bool {
	return a.paletteOpen || a.branchOpen || a.searchOpen || a.settingsOpen || a.confirmOpen ||
		a.commitDialogOpen || a.fileMenu || a.browseOpen || a.termFocus || a.menuOpen ||
		a.dragSelect || a.mode != "explorer" || a.mergeOpen
}

// pollHover runs on every tick. It reads the pointer, maps it to a token,
// starts a request after the dwell, and dismisses a shown card that no longer
// applies. It reports whether the view needs a rebuild.
func (m *lspManager) pollHover() {
	a, h := m.app, &m.hv

	if a.page == nil || !m.enabled || len(m.docs) == 0 {
		m.hoverReset()

		return
	}

	if h.shown && (!a.popup.open || a.popup.spec.Caret != hoverCaretTag) {
		h.shown = false
	}

	if a.hoverBlocked() || (a.popup.open && !h.shown) {
		m.hoverHide()
		h.seen, h.curOK = false, false

		return
	}

	x, y := 0, 0
	if h.cursor != nil {
		x, y = h.cursor()
	} else {
		x, y = a.page.Window().CursorPosition()
	}

	now := time.Now()

	if !h.seen || x != h.x || y != h.y {
		h.seen, h.x, h.y = true, x, y
		h.since, h.evaluated = now, false
		h.cur, h.curOK = m.hoverTargetAt(float64(x), float64(y))

		if h.shown && (!h.curOK || h.cur.key != h.shownTgt.key) {
			m.hoverHide()
		}

		if !h.curOK || h.cur.key != h.reqKey {
			h.reqKey = hoverKey{}
		}

		if h.curOK && !h.shown {
			a.page.WantFrameIn(h.dwellTime())
		}
	}

	if h.shown {
		m.hoverValidate()

		if h.shown {
			a.page.WantFrameIn(hoverPoll)
		}

		return
	}

	if h.evaluated || !h.curOK {
		return
	}

	if wait := h.dwellTime() - now.Sub(h.since); wait > 0 {
		a.page.WantFrameIn(wait)

		return
	}

	doc := m.docs[h.cur.key.path]
	if doc == nil || doc.skipped || doc.server == nil || !doc.pendingAt.IsZero() {
		return // retry on a later tick once the document is in sync
	}

	h.evaluated = true

	if h.cur.key == h.reqKey {
		return
	}

	h.nextID++
	h.reqKey, h.pendingKey, h.pendingID = h.cur.key, h.cur.key, h.nextID

	doc.server.enqueue(lspOp{
		kind: lspOpHover, uri: doc.uri, version: doc.version,
		hoverID: h.pendingID, line: h.cur.key.line, col: h.cur.key.start,
	})
}

func (m *lspManager) hoverReset() {
	h := &m.hv
	m.hoverHide()
	h.seen, h.curOK, h.pendingID, h.reqKey = false, false, 0, hoverKey{}
}

// hoverHide closes the hover card when it is the open popup.
func (m *lspManager) hoverHide() {
	a, h := m.app, &m.hv
	if h.shown && a.popup.open && a.popup.spec.Caret == hoverCaretTag {
		a.hidePopup(popupDismissCode)
	}

	h.shown = false
}

// hoverValidate dismisses a shown card after an edit, a tab switch, or a
// scroll.
func (m *lspManager) hoverValidate() {
	a, h := m.app, &m.hv
	t := h.shownTgt
	doc := m.docs[t.key.path]

	switch {
	case doc == nil || doc.skipped || doc.server == nil,
		doc.version != t.key.version || !doc.pendingAt.IsZero(),
		a.groupDoc(t.group) == nil || a.groupDoc(t.group).Path != t.key.path,
		a.scrollXFor(t.group) != t.scrollX,
		a.hoverScrollY(t.group) != t.scrollY:
		m.hoverHide()
	}
}

// hoverScrollY is the vertical offset one group paints with.
func (a *App) hoverScrollY(group int) int {
	if a.split && (group == 0 || group == 1) {
		return a.scrollGroup[group]
	}

	return a.scrollY
}

// hoverTargetAt maps a page point to the identifier under it in a synced
// document of either editor group. It reuses the click-to-caret geometry:
// visualRowAt, sourceLineAt, the wrap plan, tab expansion, and the gutter.
func (m *lspManager) hoverTargetAt(x, y float64) (hoverTarget, bool) {
	a := m.app

	for group := 0; group < 2; group++ {
		if group == 1 && !a.split {
			break
		}

		id := "code-body"
		if group == 1 {
			id = "code-body-2"
		}

		box, ok := a.page.BoxByID(id)
		if !ok || x < box.X || x > box.X+box.W || y < box.Y || y > box.Y+box.H {
			continue
		}

		return m.hoverTargetIn(group, box.X, box.Y, x, y)
	}

	return hoverTarget{}, false
}

func (m *lspManager) hoverTargetIn(group int, boxX, boxY, x, y float64) (hoverTarget, bool) {
	a := m.app

	doc := a.groupDoc(group)
	if doc == nil || (a.preview && doc.Lang == langMarkdown && a.activeGroup() == group) {
		return hoverTarget{}, false
	}

	ld := m.docs[doc.Path]
	if ld == nil || ld.skipped || ld.server == nil {
		return hoverTarget{}, false
	}

	sticky := a.stickyTopPx(group)
	if y-boxY < float64(sticky) {
		return hoverTarget{}, false
	}

	scrollY := a.hoverScrollY(group)
	rh := a.zx(rowH)
	visual := visualRowAt(int(y)-sticky, int(boxY), scrollY, rh)
	line, rowStart := visual, 0

	if plan := a.rowPlan(doc, group); plan != nil {
		if visual < 0 || visual >= len(plan.Rows) {
			return hoverTarget{}, false
		}

		line, rowStart = plan.Rows[visual].Line, plan.Rows[visual].Start
	}

	raw := ""

	switch {
	case a.editing && a.edit != nil && a.edit.Path == doc.Path:
		raw = lineTextAt(a.edit.Text(), line)
	case line >= 0 && line < len(doc.Lines):
		raw = doc.Lines[line]
	default:
		return hoverTarget{}, false
	}

	textLeft := boxX + float64(a.zx(editTextLeft))
	if a.blameShows(doc) {
		textLeft += float64(a.zx(blameGutterW))
	}

	scrollX := 0
	if !a.wrap {
		scrollX = a.scrollXFor(group)
	}

	if x < textLeft {
		return hoverTarget{}, false
	}

	cell := a.zf(codeCellW)
	displayCol := rowStart + int((x-textLeft+float64(scrollX))/cell)

	raw = strings.TrimSuffix(raw, "\r")
	if displayCol >= utf8.RuneCountInString(expandTabs(raw)) {
		return hoverTarget{}, false
	}

	col := rawColumnAt(raw, displayCol)

	runes := []rune(raw)
	if col < 0 || col >= len(runes) || !isIdentRune(runes[col]) {
		return hoverTarget{}, false
	}

	start, end := col, col+1
	for start > 0 && isIdentRune(runes[start-1]) {
		start--
	}

	for end < len(runes) && isIdentRune(runes[end]) {
		end++
	}

	startDisp := utf8.RuneCountInString(expandTabs(string(runes[:start])))
	left := int(textLeft) - scrollX + int(float64(max(startDisp-rowStart, 0))*cell)
	top := int(boxY) + sticky + visual*rh - scrollY

	return hoverTarget{
		key:   hoverKey{path: doc.Path, line: line, start: start, end: end, version: ld.version},
		group: group, name: string(runes[start:end]),
		left: left, top: top, bottom: top + rh,
		scrollX: scrollX, scrollY: scrollY,
	}, true
}

// applyHover installs a reply unless it is stale.
func (m *lspManager) applyHover(ev lspEvent) bool {
	a, h := m.app, &m.hv

	if ev.hoverID != h.pendingID {
		return false
	}

	h.pendingID = 0

	if ev.text == "" || !h.curOK || h.cur.key != h.pendingKey || a.hoverBlocked() || (a.popup.open && !h.shown) {
		return false
	}

	t := h.cur
	doc := m.docs[t.key.path]

	if doc == nil || doc.server != ev.server || doc.version != ev.version || t.key.version != ev.version ||
		!doc.pendingAt.IsZero() || a.groupDoc(t.group) == nil || a.groupDoc(t.group).Path != t.key.path {
		return false
	}

	text, w := hoverCard(ev.text)

	a.showPopup(popupSpec{
		Label:   "Hover: " + t.name,
		AnchorX: t.left, AnchorTop: t.top, AnchorBottom: t.bottom,
		Width:     w,
		Caret:     hoverCaretTag,
		Content:   text,
		OnDismiss: func(popupDismiss) { h.shown = false },
	})

	h.shown, h.shownTgt = true, t

	return true
}

// hoverCard caps the Markdown and picks a card width from its longest line.
func hoverCard(text string) (string, int) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n")
	cut := false

	if len(lines) > hoverMaxLines {
		lines, cut = lines[:hoverMaxLines], true
	}

	out := strings.Join(lines, "\n")
	if r := []rune(out); len(r) > hoverMaxRunes {
		out, cut = string(r[:hoverMaxRunes]), true
	}

	if cut {
		// Close an open fence so the ellipsis is not rendered as code.
		if strings.Count(out, "```")%2 == 1 {
			out += "\n```"
		}

		out += "\n\n..."
	}

	longest := 0
	for _, l := range strings.Split(out, "\n") {
		longest = max(longest, utf8.RuneCountInString(l))
	}

	return out, max(popupMinW, min(hoverMaxW, longest*popupCharW+24))
}

// hoverContext is the request context used by the server goroutine.
func hoverContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), hoverTimeout)
}

// startHover sends one hover request from the server goroutine. The wait
// happens on its own goroutine so edits keep flowing, and the position is
// converted against the exact text the server holds for that version.
func (s *lspServer) startHover(c *lsp.Client, op lspOp) {
	s.smu.Lock()
	sent := s.sent[op.uri]
	s.smu.Unlock()

	reply := func(text string) {
		s.mgr.post(lspEvent{kind: lspEvHover, server: s, uri: op.uri, version: op.version, hoverID: op.hoverID, text: text})
	}

	if sent == nil || sent.version != op.version || op.line < 0 || op.line >= len(sent.lines) {
		reply("")

		return
	}

	pos := lsp.PositionFromRunes(sent.lines[op.line], op.line, op.col)

	s.mgr.wg.Add(1)

	go func() {
		defer s.mgr.wg.Done()

		ctx, cancel := hoverContext()
		defer cancel()

		text, err := c.Hover(ctx, op.uri, pos)
		if err != nil {
			s.mgr.log.addf("[%s] hover: %v", s.cfg.Name, err)
		}

		reply(text)
	}()
}
