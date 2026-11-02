package app

import (
	"bytes"
	"context"
	"html/template"
	"runtime"
	"strings"
	"testing"
	"time"

	owncode "github.com/chinmay-sawant/own-code"
	"github.com/chinmay-sawant/own-code/internal/terminal"
)

func TestTerminalInputAfterEditing(t *testing.T) {
	a := newTerminalTestApp(t)
	defer a.Close()
	ctx := context.Background()
	a.beginEditAtLine(0)
	if !a.editing {
		t.Fatal("editor did not enter edit mode")
	}
	if err := a.Page().Edit().Type(ctx, "unsaved "); err != nil {
		t.Fatal(err)
	}
	before := a.edit.Text()
	if err := a.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	clickID(t, a, "act-terminal")
	if !a.terminalOwnsKeys() {
		t.Fatal("terminal cannot receive input while editor is in edit mode")
	}
	if err := a.Page().Edit().Type(ctx, "printf 'ready-%s\\n' terminal"); err != nil {
		t.Fatal(err)
	}
	if err := a.Page().Edit().KeyDown(ctx, "enter"); err != nil {
		t.Fatal(err)
	}
	waitTerminalContains(t, a, "ready-terminal")
	if !a.editing || a.edit.Text() != before {
		t.Fatal("terminal input changed the editor buffer")
	}
}

func TestTerminalInputAfterFormFocus(t *testing.T) {
	a := newTerminalTestApp(t)
	defer a.Close()
	ctx := context.Background()
	if err := a.Page().Form().Focus(ctx, "filter"); err != nil {
		t.Fatal(err)
	}
	toggleTerminalForTest(t, a, ctx)
	if !a.terminalOwnsKeys() {
		t.Fatalf("terminal left form focused: %q", a.Page().Form().FocusID())
	}
}

func BenchmarkTerminalOutputRefresh(b *testing.B) {
	if runtime.GOOS == "windows" {
		b.Skip("requires a Unix shell")
	}
	a, err := New("")
	if err != nil {
		b.Fatal(err)
	}
	defer a.Close()
	a.term = terminal.NewManager()
	id, err := a.term.Open(terminal.Config{Shell: "/bin/sh", Args: []string{"-c", "seq 1 1500"}, Cols: 160, Rows: 12})
	if err != nil {
		b.Fatal(err)
	}
	_, session := a.term.Active()
	deadline := time.Now().Add(terminalTestTimeout)
	for session.Info().Running {
		if time.Now().After(deadline) {
			b.Fatal("shell did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if snap, _ := a.term.Snapshot(id); len(snap.Lines) < 1000 {
		b.Fatal("scrollback not filled")
	}
	a.termOpen = true
	a.refresh()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		a.termVersion = ^uint64(0)
		a.pollTerminal(context.Background())
	}
}

func TestTerminalBlankCursorRendered(t *testing.T) {
	s := terminal.NewScreen(20, 2).Snapshot()
	s.Running = true
	v := terminalViewState(true, true, 240, 1, nil, s, true, 2, 0)
	source, err := owncode.Components.ReadFile("components/html/terminal.html")
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := template.New("terminal").Parse(string(source))
	if err != nil {
		t.Fatal(err)
	}
	v.Sessions = []terminalTabRow{{ID: 1}}
	var output bytes.Buffer
	if err := tpl.Execute(&output, struct{ Terminal terminalView }{Terminal: v}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `class="t-cursor"`) {
		t.Fatal("template suppressed cursor on blank line")
	}
}
