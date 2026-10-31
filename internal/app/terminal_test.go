package app

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chinmay-sawant/ownframe"
)

// terminalTestTimeout bounds shell startup and command echo. It is generous
// enough for a busy CI machine but keeps a stuck shell from hanging the suite.
const terminalTestTimeout = 5 * time.Second

func newTerminalTestApp(t *testing.T) *App {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("terminal tests need a Unix PTY")
	}

	t.Setenv("OWN_CODE_SHELL", "/bin/sh")

	dir := t.TempDir()
	writeFile(t, dir, "README.md", "terminal test workspace\n")

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return app
}

func clickTerminal(t *testing.T, app *App, ctx context.Context, id, action string) {
	t.Helper()

	if err := app.onClick(ctx, ownframe.Box{ID: id, Action: action}); err != nil {
		t.Fatalf("onClick(%s): %v", action, err)
	}
}

func toggleTerminalForTest(t *testing.T, app *App, ctx context.Context) {
	t.Helper()

	clickTerminal(t, app, ctx, "act-terminal", "terminal-toggle")
}

func sendTerminalCommand(t *testing.T, app *App, ctx context.Context, command string) {
	t.Helper()

	if err := app.onType(ctx, command); err != nil {
		t.Fatalf("onType: %v", err)
	}

	if err := app.onKeyDown(ctx, "enter"); err != nil {
		t.Fatalf("onKeyDown(enter): %v", err)
	}
}

func waitTerminalContains(t *testing.T, app *App, want string) {
	t.Helper()

	ctx := context.Background()
	deadline := time.Now().Add(terminalTestTimeout)

	for {
		app.pollTerminal(ctx)

		text := terminalRowsText(app.View().Terminal.Rows)
		if strings.Contains(text, want) {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("terminal output never contained %q:\n%s", want, text)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func TestTerminalToggleStartsSession(t *testing.T) {
	app := newTerminalTestApp(t)
	defer app.Close()

	ctx := context.Background()

	if view := app.View().Terminal; view.Open {
		t.Fatal("terminal is open before the toggle")
	}

	toggleTerminalForTest(t, app, ctx)

	view := app.View().Terminal
	if !view.Open {
		t.Fatal("terminal did not open")
	}

	if len(view.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(view.Sessions))
	}

	if !app.termFocus {
		t.Fatal("terminal did not take focus")
	}

	if view.Shell == "" {
		t.Fatal("terminal shell is empty")
	}

	if view.Height <= 0 {
		t.Fatalf("terminal height = %d, want positive", view.Height)
	}

	// The panel traps Tab so the shell receives completion instead of the
	// window moving focus through the rest of the app.
	if !app.buildView().Modal {
		t.Fatal("terminal focus did not disable background tab stops")
	}
}

func TestTerminalCommandOutputAndCopy(t *testing.T) {
	app := newTerminalTestApp(t)
	defer app.Close()

	ctx := context.Background()
	toggleTerminalForTest(t, app, ctx)

	sendTerminalCommand(t, app, ctx, "printf 'terminal-ok\\n'")
	waitTerminalContains(t, app, "terminal-ok")

	view := app.View().Terminal
	if !view.Running {
		t.Fatal("terminal shell is not running")
	}

	if copyText := terminalCopyText(view); !strings.Contains(copyText, "terminal-ok") {
		t.Fatalf("copied text = %q, want terminal-ok", copyText)
	}

	clipboard, ok, err := app.onCopy(ctx)
	if err != nil {
		t.Fatalf("onCopy: %v", err)
	}

	if !ok || !strings.Contains(clipboard, "terminal-ok") {
		t.Fatalf("onCopy = %q, %v, want terminal-ok", clipboard, ok)
	}
}

func TestTerminalCopyChordDoesNotStealInterrupt(t *testing.T) {
	app := newTerminalTestApp(t)
	defer app.Close()

	ctx := context.Background()
	toggleTerminalForTest(t, app, ctx)
	sendTerminalCommand(t, app, ctx, "printf 'copy-marker\\n'")
	waitTerminalContains(t, app, "copy-marker")

	app.ctrl = true
	app.shift = false

	if text, ok, err := app.onCopy(ctx); err != nil || ok || text != "" {
		t.Fatalf("Ctrl+C copy = (%q, %v, %v), want no copy while interrupting", text, ok, err)
	}

	app.shift = true

	text, ok, err := app.onCopy(ctx)
	if err != nil || !ok || !strings.Contains(text, "copy-marker") {
		t.Fatalf("Ctrl+Shift+C copy = (%q, %v, %v), want terminal output", text, ok, err)
	}

	app.ctrl = false
	app.shift = false
}

func TestTerminalSessionLifecycle(t *testing.T) {
	app := newTerminalTestApp(t)
	defer app.Close()

	ctx := context.Background()
	toggleTerminalForTest(t, app, ctx)

	first := app.View().Terminal.Active
	if first == 0 {
		t.Fatal("first session has no id")
	}

	clickTerminal(t, app, ctx, "term-new", "term-new")

	view := app.View().Terminal
	if len(view.Sessions) != 2 {
		t.Fatalf("sessions after new = %d, want 2", len(view.Sessions))
	}

	second := view.Active
	if second == 0 || second == first {
		t.Fatalf("active session = %d, want a new id besides %d", second, first)
	}

	clickTerminal(t, app, ctx, fmt.Sprintf("term-close-%d", second), fmt.Sprintf("term-close:%d", second))

	view = app.View().Terminal
	if len(view.Sessions) != 1 {
		t.Fatalf("sessions after closing active = %d, want 1", len(view.Sessions))
	}

	if view.Active != first {
		t.Fatalf("active session = %d, want %d", view.Active, first)
	}

	if !view.Open {
		t.Fatal("closing a session closed the panel")
	}

	clickTerminal(t, app, ctx, fmt.Sprintf("term-close-%d", first), fmt.Sprintf("term-close:%d", first))

	view = app.View().Terminal
	if len(view.Sessions) != 0 || view.Active != 0 {
		t.Fatalf("sessions after closing all = %+v, active %d", view.Sessions, view.Active)
	}

	if !view.Open {
		t.Fatal("closing the last session closed the panel")
	}
}

func TestTerminalFocusRoutingAndHide(t *testing.T) {
	app := newTerminalTestApp(t)
	defer app.Close()

	ctx := context.Background()
	toggleTerminalForTest(t, app, ctx)

	id := app.View().Terminal.Active
	if id == 0 {
		t.Fatal("terminal session has no id")
	}

	// A click outside the panel drops terminal focus, so typed text stays
	// out of the shell.
	clickTerminal(t, app, ctx, "editor", "")

	if app.termFocus {
		t.Fatal("outside click kept terminal focus")
	}

	if err := app.onType(ctx, "printf 'hidden-marker\\n'"); err != nil {
		t.Fatalf("onType while unfocused: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	app.pollTerminal(ctx)
	if strings.Contains(terminalCopyText(app.View().Terminal), "hidden-marker") {
		t.Fatal("unfocused terminal received typed text")
	}

	clickTerminal(t, app, ctx, "term-body", "term-focus")

	if !app.termFocus {
		t.Fatal("term-focus did not focus the panel")
	}

	sendTerminalCommand(t, app, ctx, "printf 'focused-marker\\n'")
	waitTerminalContains(t, app, "focused-marker")

	// Hiding keeps the shell alive and clears focus; showing reuses the same
	// session instead of starting another shell.
	toggleTerminalForTest(t, app, ctx)

	view := app.View().Terminal
	if view.Open {
		t.Fatal("terminal stayed open after hide")
	}

	if app.termFocus {
		t.Fatal("hide kept terminal focus")
	}

	if len(view.Sessions) != 1 || view.Active != id {
		t.Fatalf("hidden sessions = %+v, active %d, want one active %d", view.Sessions, view.Active, id)
	}

	toggleTerminalForTest(t, app, ctx)

	view = app.View().Terminal
	if !view.Open || !app.termFocus {
		t.Fatal("terminal did not reopen with focus")
	}

	if len(view.Sessions) != 1 || view.Active != id {
		t.Fatalf("reopened sessions = %+v, active %d, want one active %d", view.Sessions, view.Active, id)
	}
}

func TestTerminalClearAction(t *testing.T) {
	app := newTerminalTestApp(t)
	defer app.Close()

	ctx := context.Background()
	toggleTerminalForTest(t, app, ctx)

	sendTerminalCommand(t, app, ctx, "printf 'clear-marker\\n'")
	waitTerminalContains(t, app, "clear-marker")

	clickTerminal(t, app, ctx, "term-clear", "term-clear")

	if text := terminalCopyText(app.View().Terminal); strings.Contains(text, "clear-marker") {
		t.Fatalf("clear left output behind: %q", text)
	}
}

func TestTerminalResizeSyncsSession(t *testing.T) {
	app := newTerminalTestApp(t)
	defer app.Close()

	ctx := context.Background()
	toggleTerminalForTest(t, app, ctx)

	id := app.View().Terminal.Active
	if id == 0 {
		t.Fatal("terminal session has no id")
	}

	before, ok := app.term.Snapshot(id)
	if !ok {
		t.Fatal("no snapshot before resize")
	}

	app.termHeight = 400
	app.syncTerminalSize(ctx)
	app.pollTerminal(ctx)

	after, ok := app.term.Snapshot(id)
	if !ok {
		t.Fatal("no snapshot after resize")
	}

	if after.Rows <= before.Rows {
		t.Fatalf("snapshot rows = %d after resize, want more than %d", after.Rows, before.Rows)
	}
}

func TestTerminalCloseClosesSessions(t *testing.T) {
	app := newTerminalTestApp(t)

	ctx := context.Background()
	toggleTerminalForTest(t, app, ctx)

	id := app.View().Terminal.Active
	if id == 0 {
		t.Fatal("terminal session has no id")
	}

	app.Close()

	if active, session := app.term.Active(); active != 0 || session != nil {
		t.Fatalf("active after Close = %d, %v, want 0, nil", active, session)
	}

	if _, ok := app.term.Snapshot(id); ok {
		t.Fatalf("session %d still has a snapshot after Close", id)
	}

	// Close is idempotent.
	app.Close()
}

func TestTerminalBuildIncludesPanel(t *testing.T) {
	t.Parallel()

	html := buildHTML()

	for _, want := range []string{
		`id="terminal"`,
		"term-body",
		"term-open",
		"--term-h:",
		".terminal {",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("buildHTML() is missing %q", want)
		}
	}
}
