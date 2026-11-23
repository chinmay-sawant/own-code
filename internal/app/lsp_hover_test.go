package app

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chinmay-sawant/own-code/internal/lsp"
)

const hoverMarkdown = `{"contents":{"kind":"markdown","value":"` + "```go\\nvar foo int\\n```" + `\n\nA foo."}}`

// hoverSrc has a non-BMP rune before foo: rune column 11, UTF-16 column 12.
const hoverSrc = "package a\ns := \"😀\" + foo\n"

type hoverRig struct {
	app  *App
	fake *fakeLSP
	cx   int
	cy   int
}

// pointAt returns page coordinates over rune column col of source line line
// (no tabs, no wrap, no scroll).
func (r *hoverRig) pointAt(t *testing.T, line, col int) (int, int) {
	t.Helper()

	box, ok := findBoxID(r.app.page.Boxes(), "code-body")
	if !ok {
		t.Fatal("no code-body box")
	}

	cell := r.app.zf(codeCellW)
	x := box.X + float64(r.app.zx(editTextLeft)) + float64(col)*cell + cell/2
	y := box.Y + float64(line*r.app.zx(rowH)+r.app.zx(rowH)/2)

	return int(x), int(y)
}

func (r *hoverRig) move(t *testing.T, line, col int) {
	t.Helper()
	r.cx, r.cy = r.pointAt(t, line, col)
}

func newHoverRig(t *testing.T) *hoverRig {
	t.Helper()

	app, fake, _ := lspTestApp(t, map[string]string{"main.go": hoverSrc, "other.go": "package o\n"})
	fake.hoverResult = hoverMarkdown
	r := &hoverRig{app: app, fake: fake}

	app.lspm.hv.cursor = func() (int, int) { return r.cx, r.cy }
	app.lspm.hv.dwell = 30 * time.Millisecond

	app.openFile("main.go")
	pump(t, app, "didOpen", func() bool { return fake.count("textDocument/didOpen") == 1 })
	_ = app.Page().Redraw(context.Background())

	return r
}

func (r *hoverRig) hovers() []lsp.HoverParams {
	r.fake.mu.Lock()
	defer r.fake.mu.Unlock()

	return append([]lsp.HoverParams(nil), r.fake.hovers...)
}

// settle ticks for d without waiting on any condition.
func (r *hoverRig) settle(t *testing.T, d time.Duration) {
	t.Helper()

	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if err := r.app.tickWatch(context.Background()); err != nil {
			t.Fatal(err)
		}

		time.Sleep(5 * time.Millisecond)
	}
}

func (r *hoverRig) shown() bool { return r.app.popup.open && r.app.lspm.hv.shown }

func TestLSPHoverDwellShowsPopup(t *testing.T) {
	r := newHoverRig(t)
	r.move(t, 1, 12) // inside foo

	pump(t, r.app, "hover popup", r.shown)

	got := r.hovers()
	if len(got) != 1 {
		t.Fatalf("hover requests = %d", len(got))
	}

	if got[0].Position != (lsp.Position{Line: 1, Character: 12}) || !strings.HasSuffix(got[0].TextDocument.URI, "/main.go") {
		t.Fatalf("hover params = %+v (want line 1, UTF-16 col 12)", got[0])
	}

	if c := r.app.popup.spec.Content; !strings.Contains(c, "var foo int") || !strings.Contains(c, "A foo.") {
		t.Fatalf("content = %q", c)
	}

	v := r.app.View().Popup
	if !v.Show || v.List || len(v.Lines) == 0 {
		t.Fatalf("popup view = %+v", v)
	}

	// Resting longer never re-requests the same position.
	r.settle(t, 150*time.Millisecond)

	if n := len(r.hovers()); n != 1 {
		t.Fatalf("re-requested: %d", n)
	}
}

func TestLSPHoverWhileEditing(t *testing.T) {
	r := newHoverRig(t)
	r.app.toggleEdit()
	_ = r.app.Page().Redraw(context.Background())
	r.move(t, 1, 11)

	pump(t, r.app, "hover popup in edit mode", r.shown)

	if got := r.hovers(); len(got) != 1 || got[0].Position.Character != 12 {
		t.Fatalf("hover = %+v", got)
	}
}

func TestLSPHoverNothingToShow(t *testing.T) {
	r := newHoverRig(t)
	r.fake.hoverResult = "" // null

	r.move(t, 1, 12)
	pump(t, r.app, "request", func() bool { return len(r.hovers()) == 1 })
	r.settle(t, 100*time.Millisecond)

	if r.app.popup.open {
		t.Fatal("popup for an empty hover")
	}

	// Not over an identifier (the ":=" operator, the quote, the gutter).
	r.move(t, 1, 2)
	r.settle(t, 150*time.Millisecond)

	box, _ := findBoxID(r.app.page.Boxes(), "code-body")
	r.cx, r.cy = int(box.X)+5, int(box.Y)+r.app.zx(rowH)+4
	r.settle(t, 150*time.Millisecond)

	if n := len(r.hovers()); n != 1 {
		t.Fatalf("requests for non-identifiers: %d", n)
	}
}

func TestLSPHoverNoRequestWhilePointerMoves(t *testing.T) {
	r := newHoverRig(t)

	end := time.Now().Add(200 * time.Millisecond)
	for i := 0; time.Now().Before(end); i++ {
		r.move(t, 1, 11+i%2) // jitters by one pixel-ish: same token, new pixel
		r.cx += i % 3
		_ = r.app.tickWatch(context.Background())

		time.Sleep(5 * time.Millisecond)
	}

	if n := len(r.hovers()); n != 0 {
		t.Fatalf("requests while moving: %d", n)
	}
}

func TestLSPHoverStaleReplyDropped(t *testing.T) {
	// The pointer leaves the token while the server is still thinking.
	r := newHoverRig(t)
	r.fake.hoverDelay = 150 * time.Millisecond
	r.move(t, 1, 12)

	pump(t, r.app, "request", func() bool { return len(r.hovers()) == 1 })
	r.move(t, 1, 2)
	r.settle(t, 400*time.Millisecond)

	if r.app.popup.open {
		t.Fatal("stale reply (pointer moved) was shown")
	}

	// The document changes while the server is still thinking.
	r.move(t, 1, 12)
	r.settle(t, 50*time.Millisecond)
	r.move(t, 1, 11)

	pump(t, r.app, "second request", func() bool { return len(r.hovers()) == 2 })
	r.app.toggleEdit()

	if err := r.app.Page().Edit().Type(context.Background(), "z"); err != nil {
		t.Fatal(err)
	}

	r.settle(t, 500*time.Millisecond)

	if r.app.popup.open {
		t.Fatal("stale reply (document edited) was shown")
	}
}

func TestLSPHoverEventStaleChecks(t *testing.T) {
	r := newHoverRig(t)
	m := r.app.lspm
	r.move(t, 1, 12)
	m.hoverTargetCheck(t, r)

	doc := m.docs["main.go"]
	h := &m.hv
	h.pendingID, h.pendingKey = 7, h.cur.key

	if m.applyHover(lspEvent{kind: lspEvHover, server: doc.server, hoverID: 6, version: doc.version, text: "x"}) {
		t.Fatal("old request id accepted")
	}

	h.pendingID = 7

	if m.applyHover(lspEvent{kind: lspEvHover, server: doc.server, hoverID: 7, version: doc.version - 1, text: "x"}) {
		t.Fatal("old version accepted")
	}

	h.pendingID = 7

	if !m.applyHover(lspEvent{kind: lspEvHover, server: doc.server, hoverID: 7, version: doc.version, text: "ok"}) {
		t.Fatal("current reply rejected")
	}
}

func (m *lspManager) hoverTargetCheck(t *testing.T, r *hoverRig) {
	t.Helper()

	tgt, ok := m.hoverTargetAt(float64(r.cx), float64(r.cy))
	if !ok || tgt.name != "foo" || tgt.key.start != 11 || tgt.key.end != 14 {
		t.Fatalf("target = %+v ok=%v", tgt, ok)
	}

	m.hv.cur, m.hv.curOK = tgt, true
}

func TestLSPHoverDismissal(t *testing.T) {
	show := func(t *testing.T) *hoverRig {
		t.Helper()

		r := newHoverRig(t)
		r.move(t, 1, 12)
		pump(t, r.app, "hover popup", r.shown)

		return r
	}

	t.Run("pointer leaves token", func(t *testing.T) {
		r := show(t)
		r.move(t, 1, 3)
		r.settle(t, 30*time.Millisecond)

		if r.app.popup.open {
			t.Fatal("popup stays after the pointer left")
		}
	})

	t.Run("pointer stays on token", func(t *testing.T) {
		r := show(t)
		r.cx++
		r.settle(t, 60*time.Millisecond)

		if !r.shown() {
			t.Fatal("popup closed while still over the token")
		}
	})

	t.Run("escape", func(t *testing.T) {
		r := show(t)

		if !r.app.popupKeyDown("escape") || r.app.popup.open {
			t.Fatal("escape did not close the card")
		}

		// The pointer still rests on the token: it must not come back.
		r.settle(t, 150*time.Millisecond)

		if r.app.popup.open || len(r.hovers()) != 1 {
			t.Fatalf("card reopened (%d requests)", len(r.hovers()))
		}

		// Leaving and coming back asks again.
		r.move(t, 1, 3)
		r.settle(t, 30*time.Millisecond)
		r.move(t, 1, 12)
		pump(t, r.app, "reopened", r.shown)
	})

	t.Run("scroll", func(t *testing.T) {
		r := show(t)
		r.app.scrollY += 7
		r.settle(t, 30*time.Millisecond)

		if r.app.popup.open {
			t.Fatal("popup stays after a scroll")
		}
	})

	t.Run("edit", func(t *testing.T) {
		r := show(t)
		r.app.toggleEdit()

		if err := r.app.Page().Edit().Type(context.Background(), "q"); err != nil {
			t.Fatal(err)
		}

		r.settle(t, 30*time.Millisecond)

		if r.app.popup.open {
			t.Fatal("popup stays after an edit")
		}
	})

	t.Run("tab switch", func(t *testing.T) {
		r := show(t)
		r.app.openFile("other.go")
		r.settle(t, 30*time.Millisecond)

		if r.app.popup.open {
			t.Fatal("popup stays after a tab switch")
		}
	})
}

func TestLSPHoverBlockedByOverlayAndOtherPopup(t *testing.T) {
	r := newHoverRig(t)
	r.app.paletteOpen = true
	r.move(t, 1, 12)
	r.settle(t, 150*time.Millisecond)

	if len(r.hovers()) != 0 || r.app.popup.open {
		t.Fatal("hover while the palette is open")
	}

	r.app.paletteOpen = false
	r.app.showPopup(popupSpec{Items: demoItems(3)})
	r.move(t, 1, 11)
	r.settle(t, 150*time.Millisecond)

	if len(r.hovers()) != 0 || !r.app.popup.isList() {
		t.Fatal("hover replaced a foreign popup")
	}
}

func TestHoverCardCaps(t *testing.T) {
	long := strings.Repeat("line\n", 100)
	got, w := hoverCard(long)

	if n := strings.Count(got, "\n"); n > hoverMaxLines+3 || !strings.HasSuffix(got, "...") {
		t.Fatalf("not capped: %d newlines", n)
	}

	if w < popupMinW || w > hoverMaxW {
		t.Fatalf("width %d", w)
	}

	fenced, _ := hoverCard("```go\n" + strings.Repeat("x\n", 100) + "```")
	if strings.Count(fenced, "```")%2 != 0 {
		t.Fatal("fence left open")
	}
}

// TestLSPHoverRealGopls talks to a real gopls; skipped when it is not on PATH.
func TestLSPHoverRealGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH")
	}

	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/x\n\ngo 1.21\n")
	writeFile(t, root, "main.go", "package main\n\n// Add sums.\nfunc Add(a, b int) int { return a + b }\n\nfunc main() { _ = \"😀\" + string(rune(Add(1, 2))) }\n")

	app, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(app.Close)

	if err := app.Page().Redraw(context.Background()); err != nil {
		t.Fatal(err)
	}

	t.Setenv(lspEnvOff, "on")

	m := app.lspManager()
	r := &hoverRig{app: app}
	m.hv.cursor = func() (int, int) { return r.cx, r.cy }
	m.hv.dwell = 30 * time.Millisecond

	app.openFile("main.go")
	_ = app.Page().Redraw(context.Background())

	// "Add" in the call: after the emoji on the same line.
	line := "func main() { _ = \"😀\" + string(rune(Add(1, 2))) }"
	col := len([]rune(line[:strings.Index(line, "Add")]))
	r.cx, r.cy = r.pointAt(t, 5, col+1)

	deadline := time.Now().Add(60 * time.Second)

	for time.Now().Before(deadline) && !r.shown() {
		_ = app.tickWatch(context.Background())

		time.Sleep(50 * time.Millisecond)
	}

	if !r.shown() {
		t.Skip("gopls produced no hover in time (still loading)")
	}

	if !strings.Contains(app.popup.spec.Content, "Add") {
		t.Fatalf("hover content = %q", app.popup.spec.Content)
	}
}
