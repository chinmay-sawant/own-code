package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chinmay-sawant/own-code/internal/lsp"
)

// fakeLSP is an in-process language server over io.Pipe.
type fakeLSP struct {
	mu      sync.Mutex
	calls   []string     // method names in order
	changes []string     // texts from didChange
	ranges  []*lsp.Range // range of the first content change (nil = full)
	// syncKind is the advertised textDocumentSync (0 means 1, full).
	syncKind int
	opens    []lsp.TextDocumentItem
	// versionLag makes diagnostics carry version-lag, to model stale replies.
	versionLag int
	silent     bool
	connects   int
	exited     bool
	// hoverResult is the raw JSON result for textDocument/hover ("" = null).
	hoverResult string
	hoverDelay  time.Duration
	hovers      []lsp.HoverParams
	// defResult is the raw JSON result for definition/declaration/typeDefinition.
	defResult string
	defDelay  time.Duration
	defs      []lsp.TextDocumentPositionParams
	defMethod []string
}

func (f *fakeLSP) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := 0

	for _, c := range f.calls {
		if c == method {
			n++
		}
	}

	return n
}

func (f *fakeLSP) connect(_ lsp.ServerConfig, _ string, opt lsp.Options) (*lsp.Client, error) {
	f.mu.Lock()
	f.connects++
	f.mu.Unlock()

	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	conn := lsp.NewConn(sr, sw)

	go func() {
		for {
			m, err := conn.Read()
			if err != nil {
				_ = sw.Close()

				return
			}

			f.mu.Lock()
			f.calls = append(f.calls, m.Method)
			f.mu.Unlock()

			switch m.Method {
			case "initialize":
				f.mu.Lock()
				kind := f.syncKind
				f.mu.Unlock()

				if kind == 0 {
					kind = 1
				}

				_ = conn.Write(&lsp.Message{ID: m.ID, Result: json.RawMessage(fmt.Sprintf(`{"capabilities":{"textDocumentSync":%d}}`, kind))})
			case "textDocument/hover":
				var p lsp.HoverParams
				_ = json.Unmarshal(m.Params, &p)

				f.mu.Lock()
				f.hovers = append(f.hovers, p)
				res, delay := f.hoverResult, f.hoverDelay
				f.mu.Unlock()

				time.Sleep(delay)

				if res == "" {
					res = "null"
				}

				_ = conn.Write(&lsp.Message{ID: m.ID, Result: json.RawMessage(res)})
			case "textDocument/definition", "textDocument/declaration", "textDocument/typeDefinition":
				var p lsp.TextDocumentPositionParams
				_ = json.Unmarshal(m.Params, &p)

				f.mu.Lock()
				f.defs = append(f.defs, p)
				f.defMethod = append(f.defMethod, m.Method)
				res, delay := f.defResult, f.defDelay
				f.mu.Unlock()

				time.Sleep(delay)

				if res == "" {
					res = "null"
				}

				_ = conn.Write(&lsp.Message{ID: m.ID, Result: json.RawMessage(res)})
			case "shutdown":
				_ = conn.Write(&lsp.Message{ID: m.ID, Result: json.RawMessage("null")})
			case "exit":
				f.mu.Lock()
				f.exited = true
				f.mu.Unlock()

				_ = sw.Close()

				return
			case "textDocument/didOpen":
				var p lsp.DidOpenParams
				_ = json.Unmarshal(m.Params, &p)

				f.mu.Lock()
				f.opens = append(f.opens, p.TextDocument)
				f.mu.Unlock()
				f.publish(conn, p.TextDocument.URI, p.TextDocument.Version, p.TextDocument.Text)
			case "textDocument/didChange":
				var p lsp.DidChangeParams
				_ = json.Unmarshal(m.Params, &p)

				text := p.ContentChanges[0].Text

				f.mu.Lock()
				f.changes = append(f.changes, text)
				f.ranges = append(f.ranges, p.ContentChanges[0].Range)
				f.mu.Unlock()
				f.publish(conn, p.TextDocument.URI, p.TextDocument.Version, text)
			}
		}
	}()

	return lsp.NewClient(cr, cw, opt), nil
}

func (f *fakeLSP) publish(conn *lsp.Conn, uri string, version int, text string) {
	f.mu.Lock()
	silent, lag := f.silent, f.versionLag
	f.mu.Unlock()

	if silent {
		return
	}

	v := version - lag
	// One error on the first four runes of line 0 whenever the text has "bad".
	var diags []lsp.Diagnostic

	if strings.Contains(text, "bad") {
		diags = append(diags, lsp.Diagnostic{
			Range:    lsp.Range{End: lsp.Position{Character: 4}},
			Severity: lsp.SeverityError, Message: "boom", Source: "fake",
		})
	}

	params, _ := json.Marshal(lsp.PublishDiagnosticsParams{URI: uri, Version: &v, Diagnostics: diags})
	_ = conn.Write(&lsp.Message{Method: "textDocument/publishDiagnostics", Params: params})
}

// lspTestApp builds an app on a temp dir with a fake server injected.
func lspTestApp(t *testing.T, files map[string]string) (*App, *fakeLSP, string) {
	t.Helper()

	dir := t.TempDir()
	for name, text := range files {
		writeFile(t, dir, name, text)
	}

	app, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(app.Close)

	if err := app.Page().Redraw(context.Background()); err != nil {
		t.Fatal(err)
	}

	t.Setenv(lspEnvOff, "on")

	fake := &fakeLSP{}
	m := app.lspManager()
	m.connect = fake.connect
	m.debounce = 20 * time.Millisecond

	return app, fake, dir
}

// pump ticks the app until cond holds or the deadline passes.
func pump(t *testing.T, app *App, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		if err := app.tickWatch(context.Background()); err != nil {
			t.Fatal(err)
		}

		if cond() {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", what)
}

func TestLSPSyncLifecycleAndDiagnostics(t *testing.T) {
	app, fake, _ := lspTestApp(t, map[string]string{"main.go": "bad := 1\nx := 2\n"})
	ctx := context.Background()

	app.openFile("main.go")
	pump(t, app, "didOpen", func() bool { return fake.count("textDocument/didOpen") == 1 })

	fake.mu.Lock()
	open := fake.opens[0]
	fake.mu.Unlock()

	if open.LanguageID != "go" || open.Version != 1 || !strings.HasSuffix(open.URI, "/main.go") {
		t.Fatalf("didOpen = %+v", open)
	}

	// The diagnostic arrives, paints a squiggle, a gutter mark and counts.
	pump(t, app, "diagnostic", func() bool { return len(app.lspm.diags["main.go"]) == 1 })

	v := app.View()
	if v.LSP.Errors != 1 || !v.LSP.Show || v.LSP.Total != 1 {
		t.Fatalf("lsp view = %+v", v.LSP)
	}

	row := v.Rows[0]
	if row.DiagClass != "gutter-diag-err" || row.DiagMark != "!" || row.DiagHint != "boom" {
		t.Fatalf("gutter = %q %q %q", row.DiagClass, row.DiagMark, row.DiagHint)
	}

	squiggle := ""

	for _, seg := range row.Segments {
		if strings.Contains(seg.Class, "diag-err") {
			squiggle += seg.Text
		}
	}

	if squiggle != "bad " {
		t.Fatalf("squiggle covers %q, want %q", squiggle, "bad ")
	}

	// Several quick keystrokes collapse into one didChange (debounce).
	app.toggleEdit()

	for _, ch := range []string{"a", "b", "c"} {
		if err := app.Page().Edit().Type(ctx, ch); err != nil {
			t.Fatal(err)
		}
	}

	pump(t, app, "didChange", func() bool { return fake.count("textDocument/didChange") >= 1 })
	time.Sleep(60 * time.Millisecond)
	_ = app.tickWatch(ctx)

	if n := fake.count("textDocument/didChange"); n != 1 {
		t.Fatalf("didChange count = %d, want 1 (debounced)", n)
	}

	fake.mu.Lock()
	sent := fake.changes[0]
	fake.mu.Unlock()

	if !strings.Contains(sent, "abc") {
		t.Fatalf("didChange text %q lacks the typed text", sent)
	}

	// Saving sends didSave.
	app.saveEdit()
	pump(t, app, "didSave", func() bool { return fake.count("textDocument/didSave") == 1 })

	// Closing the tab sends didClose and clears the diagnostics.
	app.finishEdit()
	app.closeTab(app.active)
	pump(t, app, "didClose", func() bool { return fake.count("textDocument/didClose") == 1 })

	if len(app.lspm.diags) != 0 {
		t.Fatalf("diagnostics survive close: %v", app.lspm.diags)
	}

	// Exit shuts the server down.
	app.Close()

	if !fake.exited {
		t.Fatal("server did not receive exit")
	}
}

func TestLSPStaleResponseDropped(t *testing.T) {
	app, fake, _ := lspTestApp(t, map[string]string{"main.go": "bad\n"})
	fake.versionLag = 1 // every reply claims the previous version

	app.openFile("main.go")
	pump(t, app, "didOpen", func() bool { return fake.count("textDocument/didOpen") == 1 })
	time.Sleep(50 * time.Millisecond)

	for range 5 {
		_ = app.tickWatch(context.Background())
		time.Sleep(5 * time.Millisecond)
	}

	if got := app.lspm.diags["main.go"]; len(got) != 0 {
		t.Fatalf("stale diagnostics applied: %v", got)
	}

	// Loop-side check too: a reply older than the last sent version drops.
	doc := app.lspm.docs["main.go"]
	doc.version = 3

	if app.lspm.applyDiag(lspEvent{kind: lspEvDiag, server: doc.server, uri: doc.uri, version: 2, hasVersion: true, diags: []lspDiag{{Message: "old"}}}) {
		t.Fatal("applyDiag accepted version 2 while 3 was sent")
	}

	if !app.lspm.applyDiag(lspEvent{kind: lspEvDiag, server: doc.server, uri: doc.uri, version: 3, hasVersion: true, diags: []lspDiag{{Message: "new"}}}) {
		t.Fatal("applyDiag rejected the current version")
	}
}

func TestLSPMissingBinaryToastsOnce(t *testing.T) {
	app, _, _ := lspTestApp(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n"})

	calls := 0

	app.lspm.connect = func(cfg lsp.ServerConfig, _ string, _ lsp.Options) (*lsp.Client, error) {
		calls++

		return nil, &lspMissingError{Command: cfg.Command}
	}

	app.openFile("a.go")
	pump(t, app, "toast", func() bool { return app.toast.Show })

	if !strings.Contains(app.toast.Title, "gopls not found") || !strings.Contains(app.toast.Message, "go install") {
		t.Fatalf("toast = %+v", app.toast)
	}

	app.clearToast()

	// A second Go file and many ticks never retry or toast again.
	app.openFile("b.go")

	for range 20 {
		_ = app.tickWatch(context.Background())
		time.Sleep(5 * time.Millisecond)
	}

	if calls != 1 {
		t.Fatalf("connect called %d times, want 1", calls)
	}

	if app.toast.Show {
		t.Fatalf("second toast: %+v", app.toast)
	}
}

func TestLSPSkipsLargeFilesAndDisabled(t *testing.T) {
	big := strings.Repeat("// pad pad pad pad\n", maxFileBytes/10)
	app, fake, _ := lspTestApp(t, map[string]string{"big.go": big, "ok.go": "package ok\n", "notes.txt": "hi\n"})

	app.openFile("big.go")
	app.openFile("notes.txt")

	for range 10 {
		_ = app.tickWatch(context.Background())
		time.Sleep(5 * time.Millisecond)
	}

	if fake.connects != 0 {
		t.Fatalf("server started for a big or unknown file (%d connects)", fake.connects)
	}

	// Disabled in settings: nothing starts for a normal file either.
	off := false
	app.settings.LSP = &lspSettings{Enabled: &off}

	app.openFile("ok.go")

	for range 10 {
		_ = app.tickWatch(context.Background())
		time.Sleep(5 * time.Millisecond)
	}

	if fake.connects != 0 {
		t.Fatalf("server started while disabled (%d connects)", fake.connects)
	}
}

func TestLSPProblemJump(t *testing.T) {
	app, fake, _ := lspTestApp(t, map[string]string{"a.go": "bad\n", "b.go": "package b\n"})

	app.openFile("a.go")
	pump(t, app, "diagnostic", func() bool { return len(app.lspm.diags["a.go"]) == 1 })
	app.openFile("b.go")
	app.showProblems()

	if got := app.View().LSP.Problems; len(got) != 1 || got[0].Name != "a.go" || got[0].Where != "1:1" {
		t.Fatalf("problems = %+v", got)
	}

	app.openProblem(0)

	if app.activeDocPath() != "a.go" {
		t.Fatalf("active = %q after problem jump", app.activeDocPath())
	}

	_ = fake
}

func TestLSPShowLogOpensFile(t *testing.T) {
	app, _, _ := lspTestApp(t, map[string]string{"a.go": "package a\n"})
	app.lspm.log.addf("hello from the trace")

	app.showLSPLog(context.Background())

	path, err := lspLogPath()
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil || !strings.Contains(string(data), "hello from the trace") {
		t.Fatalf("log file = %q, %v", data, err)
	}
}

func TestWorkspaceProblemsFromUnopenedFiles(t *testing.T) {
	ctx := context.Background()
	app, fake, dir := lspTestApp(t, map[string]string{
		"main.go":       "bad\n",
		"sub/unseen.go": "package sub\n",
	})
	app.ReloadChanges(ctx, false)
	app.openFile("main.go")
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	pump(t, app, "first diagnostics", func() bool { return app.View().LSP.Errors == 1 })
	m := app.lspm
	server := m.docs["main.go"].server
	report := func(path string, diags ...lsp.Diagnostic) {
		t.Helper()
		params, err := json.Marshal(lsp.PublishDiagnosticsParams{URI: lsp.URIFromPath(path), Diagnostics: diags})
		if err != nil {
			t.Fatal(err)
		}
		server.onNotify("textDocument/publishDiagnostics", params)
	}

	unseen := filepath.Join(dir, "sub", "unseen.go")
	warning := lsp.Diagnostic{
		Range:    lsp.Range{Start: lsp.Position{Line: 4, Character: 3}},
		Severity: lsp.SeverityWarning, Message: "unopened",
	}
	info := lsp.Diagnostic{Severity: lsp.SeverityInfo, Message: "info"}
	report(unseen, warning, info)
	report(filepath.Join(dir, "..", "outside.go"), warning)
	pump(t, app, "workspace counts", func() bool { return app.View().LSP.Total == 3 })

	v := app.View().LSP
	if v.Errors != 1 || v.Warnings != 1 || v.Info != 1 || v.Files != 2 ||
		len(v.Problems) != 0 || len(m.problems) != 0 || fake.count("textDocument/didOpen") != 1 {
		t.Fatalf("status = %+v, index rows = %d, opens = %d", v, len(m.problems), fake.count("textDocument/didOpen"))
	}

	clickID(t, app, "sb-branch")
	v = app.View().LSP
	if !v.Open || len(v.Problems) != 3 || v.Problems[1].Path != "sub/unseen.go" || v.Problems[1].Where != "5:4" {
		t.Fatalf("workspace drawer = %+v", v)
	}
	clickID(t, app, "problem-1")
	if app.activeDocPath() != "sub/unseen.go" {
		t.Fatalf("jump opened %s", app.activeDocPath())
	}

	report(unseen)
	pump(t, app, "cleared diagnostics", func() bool { return app.View().LSP.Total == 1 })
	if v := app.View().LSP; v.Files != 1 || v.Warnings != 0 || v.Info != 0 {
		t.Fatalf("after clear = %+v", v)
	}
}

func TestWorkspaceProblemsCanReachLastRow(t *testing.T) {
	app, _, dir := lspTestApp(t, map[string]string{"main.go": "package main\n"})
	app.ReloadChanges(context.Background(), false)
	if err := app.Page().Redraw(context.Background()); err != nil {
		t.Fatal(err)
	}
	m := app.lspm
	for i := 0; i < 80; i++ {
		path := fmt.Sprintf("folder/%02d.go", i)
		m.setDiagnostics(path, nil, []lspDiag{{Severity: lsp.SeverityError, Message: filepath.Join(dir, path)}})
	}

	clickID(t, app, "sb-branch")
	first := app.View().LSP
	if !first.Open || first.Total != 80 || first.First != 1 || first.Last >= first.Total || !first.Next {
		t.Fatalf("first page = %+v", first)
	}
	for app.View().LSP.Next {
		before := app.View().LSP.First
		clickID(t, app, "problems-next")
		if got := app.View().LSP.First; got <= before {
			t.Fatalf("Next did not advance: %d -> %d", before, got)
		}
	}
	last := app.View().LSP
	if last.Last != 80 || last.Problems[len(last.Problems)-1].Path != "folder/79.go" || !last.Prev {
		t.Fatalf("last page = %+v", last)
	}
	clickID(t, app, "problems-prev")
	if got := app.View().LSP.First; got >= last.First {
		t.Fatalf("Previous did not go back: %d", got)
	}

	m.shutdownAll(false)
	app.refresh()
	if v := app.View().LSP; v.Total != 0 || v.Prev || v.Next || len(v.Problems) != 0 {
		t.Fatalf("after shutdown = %+v", v)
	}
}

// TestLSPIncrementalDidChange checks that a server advertising incremental
// sync receives range edits (UTF-16) that reproduce the buffer when applied.
func TestLSPIncrementalDidChange(t *testing.T) {
	app, fake, _ := lspTestApp(t, map[string]string{"a.go": "package a\n\nvar s = \"😀x\"\n"})
	fake.syncKind = 2
	ctx := context.Background()

	app.openFile("a.go")
	pump(t, app, "didOpen", func() bool { return fake.count("textDocument/didOpen") == 1 })

	app.toggleEdit()

	if err := app.Page().Edit().Type(ctx, "Q"); err != nil {
		t.Fatal(err)
	}

	// The first sync after a disk-backed open is the full text; the next
	// one is incremental.
	pump(t, app, "didChange", func() bool { return fake.count("textDocument/didChange") >= 1 })
	time.Sleep(60 * time.Millisecond)

	if err := app.Page().Edit().Type(ctx, "R"); err != nil {
		t.Fatal(err)
	}

	pump(t, app, "didChange 2", func() bool { return fake.count("textDocument/didChange") >= 2 })

	fake.mu.Lock()
	defer fake.mu.Unlock()

	if len(fake.ranges) < 2 || fake.ranges[1] == nil {
		t.Fatalf("no incremental range sent: %+v", fake.ranges)
	}

	if fake.ranges[0] != nil {
		t.Fatalf("first change after disk open should be full text")
	}

	if got := fake.ranges[1].Start; got.Line != 0 || got.Character != 1 {
		t.Fatalf("range start %+v, want 0:1 (typed after Q)", got)
	}
}
