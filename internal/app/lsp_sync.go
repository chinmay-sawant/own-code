package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chinmay-sawant/own-code/internal/edit"
	"github.com/chinmay-sawant/own-code/internal/lsp"
)

const (
	// lspDebounce is the quiet time after an edit before didChange is sent.
	lspDebounce = 150 * time.Millisecond
	// lspIdleStop is how long a server with no open documents keeps running.
	lspIdleStop = 60 * time.Second
	// lspInitTimeout bounds the initialize handshake.
	lspInitTimeout = 30 * time.Second
	// lspStopTimeout bounds one server shutdown.
	lspStopTimeout = 3 * time.Second
)

// lspConnectFunc starts and returns an uninitialized client. Tests replace it
// with an in-process fake.
type lspConnectFunc func(cfg lsp.ServerConfig, root string, opt lsp.Options) (*lsp.Client, error)

// lspMissingError reports a server binary that is not installed.
type lspMissingError struct{ Command string }

func (e *lspMissingError) Error() string { return e.Command + " not found on PATH" }

func defaultLSPConnect(cfg lsp.ServerConfig, root string, opt lsp.Options) (*lsp.Client, error) {
	if _, err := exec.LookPath(cfg.Command); err != nil {
		return nil, &lspMissingError{Command: cfg.Command}
	}

	return lsp.Start(cfg, root, opt)
}

// lspDoc is the loop-side record of one synced document.
type lspDoc struct {
	path      string // workspace path (the tab key)
	abs       string
	uri       string
	langID    string
	server    *lspServer
	version   int       // last version sent
	pendingAt time.Time // last edit not yet sent; zero when in sync
	wasDirty  bool
	skipped   bool // over the size cap; never synced
}

// lspManager owns the servers and documents. All fields except those behind
// a mutex are touched only on the app loop.
type lspManager struct {
	app      *App
	cfg      *lspSettings
	reg      *lsp.Registry
	enabled  bool
	connect  lspConnectFunc
	debounce time.Duration

	servers map[string]*lspServer // key: name + root
	docs    map[string]*lspDoc    // by workspace path
	byURI   map[string]*lspDoc
	// failed holds server names that could not start; they are not retried
	// until settings change or the project switches.
	failed         map[string]bool
	diags          map[string][]lspDiag
	diagOwners     map[string]*lspServer
	problemSummary problemCounts
	problemsDirty  bool
	// problems is the flattened list the Details drawer shows, indexed by
	// the problem:N click action.
	problems []lspProblem

	timer *time.Timer
	log   *lspLog
	hv    hoverState

	inboxMu sync.Mutex
	inbox   []lspEvent
	wg      sync.WaitGroup
}

type lspEventKind int

const (
	lspEvDiag lspEventKind = iota
	lspEvFailed
	lspEvExited
	lspEvHover
)

// lspEvent travels from server goroutines to the loop.
type lspEvent struct {
	kind       lspEventKind
	server     *lspServer
	uri        string
	version    int
	hasVersion bool
	diags      []lspDiag
	err        error
	hoverID    int
	text       string
}

// lspManager returns the app's manager, creating it on first use.
func (a *App) lspManager() *lspManager {
	if a.lspm == nil {
		a.lspm = &lspManager{
			app:        a,
			connect:    defaultLSPConnect,
			debounce:   lspDebounce,
			servers:    map[string]*lspServer{},
			docs:       map[string]*lspDoc{},
			byURI:      map[string]*lspDoc{},
			failed:     map[string]bool{},
			diags:      map[string][]lspDiag{},
			diagOwners: map[string]*lspServer{},
			log:        newLSPLog(),
		}
		a.lspm.configure()
	}

	return a.lspm
}

// configure (re)loads settings into the manager.
func (m *lspManager) configure() {
	m.cfg = m.app.settings.LSP
	m.enabled = lspEnabled(m.cfg)
	m.reg = lspRegistry(m.cfg)
}

func (m *lspManager) wake() {
	if m.app.page != nil {
		m.app.page.WantFrameIn(0)
	}
}

func (m *lspManager) post(ev lspEvent) {
	m.inboxMu.Lock()
	m.inbox = append(m.inbox, ev)
	m.inboxMu.Unlock()
	m.wake()
}

// lspTouch records an edit on path. It is cheap and a no-op for files that
// are not synced. The change goes out after the debounce on the loop.
func (a *App) lspTouch(path string) {
	m := a.lspm
	if m == nil {
		return
	}

	doc := m.docs[path]
	if doc == nil || doc.skipped {
		return
	}

	doc.pendingAt = time.Now()

	if m.timer != nil {
		m.timer.Stop()
	}

	m.timer = time.AfterFunc(m.debounce, m.wake)
}

// pollLSP runs on every tick: it applies server events, reconciles open tabs
// with synced documents, and flushes debounced changes. It does no I/O.
func (a *App) pollLSP(ctx context.Context) error {
	m := a.lspm
	if m == nil {
		// Nothing to do until a syncable file is open.
		if !a.lspCandidate() {
			return nil
		}

		m = a.lspManager()
	}

	hoverWasShown := m.hv.shown
	changed := m.drain()

	if !lspSettingsEqual(m.cfg, a.settings.LSP) {
		m.shutdownAll(false)
		m.failed = map[string]bool{}
		m.configure()

		changed = true
	}

	if m.reconcile() {
		changed = true
	}

	if m.flush() {
		changed = true
	}

	changed = m.stopIdle() || changed
	m.pollHover()

	if changed {
		a.refresh()
	}

	// Tick runs outside OwnFrame's input redraw batch. SetData publishes the
	// snapshot, but a redraw is needed to paint server events and hover exits.
	if changed || hoverWasShown != m.hv.shown {
		return a.page.Redraw(ctx)
	}

	return nil
}

// lspCandidate reports whether any open tab could be synced, so an app that
// never opens a source file never builds a manager.
func (a *App) lspCandidate() bool {
	if a.ws == nil || a.ws.Sample || !lspEnabled(a.settings.LSP) {
		return false
	}

	return len(a.tabs) > 0 || len(a.tabs2) > 0
}

// eligible reports whether path may be synced, and with which server.
func (m *lspManager) eligible(path string) (lsp.ServerConfig, bool) {
	a := m.app
	if a.ws == nil || a.ws.Sample || !m.enabled {
		return lsp.ServerConfig{}, false
	}

	doc := a.docs[path]
	if doc == nil || doc.Err != "" || doc.Binary || doc.Image != nil || doc.Truncated {
		return lsp.ServerConfig{}, false
	}

	if meta := a.ws.ByPath[path]; meta == nil || meta.Big {
		return lsp.ServerConfig{}, false
	}

	if a.mergeOpen && a.mergePath == path {
		return lsp.ServerConfig{}, false
	}

	cfg, ok := m.reg.ForPath(path)
	if !ok || m.failed[cfg.Name] {
		return lsp.ServerConfig{}, false
	}

	return cfg, true
}

func (a *App) lspAbsPath(path string) string {
	p := filepath.FromSlash(path)
	if filepath.IsAbs(p) {
		return p
	}

	root := a.ws.Root
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}

	return filepath.Join(root, p)
}

// docText returns the text to send for path, or ok=false when the bytes
// should be read from disk off the loop.
func (m *lspManager) docText(path string) (text string, fromDisk bool) {
	a := m.app

	if a.edit != nil && a.edit.Path == path {
		return a.edit.Text(), false
	}

	if a.isDirty(path) {
		if doc := a.docs[path]; doc != nil {
			return edit.JoinLines(doc.Lines), false
		}
	}

	return "", true
}

// reconcile opens documents for new tabs and closes those no longer open.
func (m *lspManager) reconcile() bool {
	a := m.app
	changed := false

	open := map[string]bool{}

	for _, path := range append(append([]string(nil), a.tabs...), a.tabs2...) {
		open[path] = true
	}

	for path, doc := range m.docs {
		if open[path] && m.stillEligible(path, doc) {
			continue
		}

		m.closeDoc(doc)
		changed = true
	}

	if !m.enabled {
		return changed
	}

	for path := range open {
		if m.docs[path] != nil {
			continue
		}

		cfg, ok := m.eligible(path)
		if !ok {
			continue
		}

		if m.openDoc(path, cfg) {
			changed = true
		}
	}

	return changed
}

func (m *lspManager) stillEligible(path string, doc *lspDoc) bool {
	if doc.skipped {
		return true
	}

	_, ok := m.eligible(path)

	return ok
}

func (m *lspManager) openDoc(path string, cfg lsp.ServerConfig) bool {
	a := m.app
	abs := a.lspAbsPath(path)
	root := m.rootFor(abs, cfg)
	srv := m.serverFor(cfg, root)

	text, fromDisk := m.docText(path)
	if !fromDisk && len(text) > maxFileBytes {
		m.docs[path] = &lspDoc{path: path, skipped: true}

		return false
	}

	doc := &lspDoc{
		path:     path,
		abs:      abs,
		uri:      lsp.URIFromPath(abs),
		langID:   cfg.LanguageID(path),
		server:   srv,
		version:  1,
		wasDirty: a.isDirty(path),
	}
	m.docs[path] = doc
	m.byURI[doc.uri] = doc
	srv.docCount++
	srv.enqueue(lspOp{kind: lspOpOpen, uri: doc.uri, abs: abs, langID: doc.langID, version: 1, text: text, fromDisk: fromDisk})

	return true
}

// rootFor finds the server root for a file: the nearest marker directory
// inside the workspace, else the workspace root.
func (m *lspManager) rootFor(abs string, cfg lsp.ServerConfig) string {
	a := m.app
	wsRoot := a.ws.Root

	if r, err := filepath.Abs(wsRoot); err == nil {
		wsRoot = r
	}

	root := lsp.FindRoot(filepath.Dir(abs), cfg.RootMarkers, wsRoot, func(p string) bool {
		_, err := os.Stat(p)

		return err == nil
	})

	rel, err := filepath.Rel(wsRoot, root)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return wsRoot
	}

	return root
}

func (m *lspManager) serverFor(cfg lsp.ServerConfig, root string) *lspServer {
	key := cfg.Name + "\x00" + root

	if srv := m.servers[key]; srv != nil {
		srv.idleSince = time.Time{}

		return srv
	}

	srv := &lspServer{mgr: m, key: key, cfg: cfg, root: root, sent: map[string]*lspSent{}, wakeCh: make(chan struct{}, 1)}
	m.servers[key] = srv
	m.wg.Add(1)

	go srv.run()

	return srv
}

func (m *lspManager) closeDoc(doc *lspDoc) {
	delete(m.docs, doc.path)
	m.setDiagnostics(doc.path, doc.server, nil)

	if doc.server == nil {
		return
	}

	delete(m.byURI, doc.uri)
	doc.server.docCount--

	if doc.server.docCount <= 0 {
		doc.server.idleSince = time.Now()
	}

	doc.server.enqueue(lspOp{kind: lspOpClose, uri: doc.uri})
}

// flush sends debounced edits and detects saves (dirty to clean).
func (m *lspManager) flush() bool {
	a := m.app
	now := time.Now()
	changed := false

	for path, doc := range m.docs {
		if doc.skipped || doc.server == nil {
			continue
		}

		dirty := a.isDirty(path)
		saved := doc.wasDirty && !dirty
		due := !doc.pendingAt.IsZero() && now.Sub(doc.pendingAt) >= m.debounce

		doc.wasDirty = dirty

		if !due && !saved {
			continue
		}

		doc.pendingAt = time.Time{}
		text, fromDisk := m.docText(path)

		if !fromDisk && len(text) > maxFileBytes {
			m.closeDoc(doc)
			m.docs[path] = &lspDoc{path: path, skipped: true}
			changed = true

			continue
		}

		doc.version++
		doc.server.enqueue(lspOp{kind: lspOpChange, uri: doc.uri, abs: doc.abs, version: doc.version, text: text, fromDisk: fromDisk})

		if saved {
			doc.server.enqueue(lspOp{kind: lspOpSave, uri: doc.uri})
		}
	}

	return changed
}

// stopIdle shuts down servers that have had no documents for a while.
func (m *lspManager) stopIdle() bool {
	changed := false
	for key, srv := range m.servers {
		if srv.docCount > 0 || srv.idleSince.IsZero() || time.Since(srv.idleSince) < lspIdleStop {
			continue
		}

		srv.enqueue(lspOp{kind: lspOpStop})
		delete(m.servers, key)
		changed = m.clearServerDiagnostics(srv) || changed
	}
	return changed
}

// drain applies server events on the loop.
func (m *lspManager) drain() bool {
	m.inboxMu.Lock()
	events := m.inbox
	m.inbox = nil
	m.inboxMu.Unlock()

	changed := false

	for _, ev := range events {
		switch ev.kind {
		case lspEvDiag:
			if m.applyDiag(ev) {
				changed = true
			}
		case lspEvHover:
			if m.applyHover(ev) {
				changed = true
			}
		case lspEvFailed:
			if m.servers[ev.server.key] == ev.server && !m.failed[ev.server.cfg.Name] {
				m.reportFailure(ev)
			}

			m.serverGone(ev.server, true)

			changed = true
		case lspEvExited:
			m.serverGone(ev.server, false)

			changed = true
		}
	}

	return changed
}

// applyDiag installs a diagnostics batch unless a newer document version has
// already been sent (stale response).
func (m *lspManager) applyDiag(ev lspEvent) bool {
	if ev.server == nil || m.servers[ev.server.key] != ev.server {
		return false
	}

	if doc := m.byURI[ev.uri]; doc != nil {
		if doc.server != ev.server || (ev.hasVersion && ev.version < doc.version) {
			return false
		}
		return m.setDiagnostics(doc.path, ev.server, ev.diags)
	}

	if ev.hasVersion {
		return false
	}

	path, err := lsp.PathFromURI(ev.uri)
	if err != nil || !filepath.IsAbs(path) {
		return false
	}

	root := m.app.lspAbsPath(".")
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return false
	}
	serverRel, err := filepath.Rel(ev.server.root, path)
	if err != nil || !filepath.IsLocal(serverRel) {
		return false
	}

	cfg, ok := m.reg.ForPath(rel)
	if !ok || cfg.Name != ev.server.cfg.Name {
		return false
	}

	rel = filepath.ToSlash(rel)
	if owner := m.diagOwners[rel]; owner != nil && owner != ev.server {
		return false
	}
	return m.setDiagnostics(rel, ev.server, ev.diags)
}

func (m *lspManager) setDiagnostics(path string, server *lspServer, next []lspDiag) bool {
	if slices.Equal(m.diags[path], next) && (len(next) == 0 || m.diagOwners[path] == server) {
		return false
	}
	m.problemSummary.add(m.diags[path], -1)
	m.problemSummary.add(next, 1)
	if len(next) == 0 {
		delete(m.diags, path)
		delete(m.diagOwners, path)
	} else {
		m.diags[path] = next
		m.diagOwners[path] = server
	}
	m.problemsDirty = true
	return true
}

func (m *lspManager) clearServerDiagnostics(server *lspServer) bool {
	changed := false
	for path, owner := range m.diagOwners {
		if owner == server {
			m.setDiagnostics(path, server, nil)
			changed = true
		}
	}
	return changed
}

// serverGone drops a dead server and forgets its documents' diagnostics. The
// documents stay recorded (without a server) so nothing retries in a loop.
func (m *lspManager) serverGone(srv *lspServer, failed bool) {
	if srv == nil {
		return
	}

	if m.servers[srv.key] == srv {
		delete(m.servers, srv.key)
	}
	m.clearServerDiagnostics(srv)

	for _, doc := range m.docs {
		if doc.server != srv {
			continue
		}

		delete(m.byURI, doc.uri)
		doc.server = nil
		doc.skipped = true
	}

	if failed {
		m.failed[srv.cfg.Name] = true
	}
}

// reportFailure toasts once per server name.
func (m *lspManager) reportFailure(ev lspEvent) {
	a := m.app
	name := ev.server.cfg.Name

	var missing *lspMissingError
	if errors.As(ev.err, &missing) {
		a.showToast("info", name+" not found",
			lspInstallHint(name))
	} else {
		msg := ev.err.Error()
		if name == "typescript-language-server" && strings.Contains(msg, "TypeScript installation") {
			// The server looks for typescript in the workspace's node_modules
			// (or tsserver.path); a global TypeScript 7 has no tsserver.js.
			msg = "It needs TypeScript 5 or 6 in the project: run `npm i -D typescript@5` in the workspace, or set lsp.servers.typescript-language-server.initializationOptions.tsserver.path in settings.json."
		}

		a.showToast("error", name+" failed to start", msg)
	}

	m.log.addf("%s: %v", name, ev.err)
}

// shutdownAll stops every server. With wait it blocks (bounded) for exit.
func (m *lspManager) shutdownAll(wait bool) {
	for key, srv := range m.servers {
		srv.enqueue(lspOp{kind: lspOpStop})
		delete(m.servers, key)
	}

	clear(m.docs)
	clear(m.byURI)
	clear(m.diags)
	clear(m.diagOwners)
	m.problemSummary = problemCounts{}
	m.problems = nil
	m.problemsDirty = false

	if m.timer != nil {
		m.timer.Stop()
	}

	if wait {
		done := make(chan struct{})

		go func() { m.wg.Wait(); close(done) }()

		select {
		case <-done:
		case <-time.After(lspStopTimeout + time.Second):
		}
	}
}

// lspShutdown stops all servers; called from App.Close.
func (a *App) lspShutdown() {
	if a.lspm != nil {
		a.lspm.shutdownAll(true)
	}
}

// lspReset drops all synced state for a project switch.
func (a *App) lspReset() {
	if a.lspm != nil {
		a.lspm.shutdownAll(false)
		a.lspm.failed = map[string]bool{}
	}
}

// ---- server goroutine ----

type lspOpKind int

const (
	lspOpOpen lspOpKind = iota
	lspOpChange
	lspOpSave
	lspOpClose
	lspOpStop
	lspOpHover
)

type lspOp struct {
	kind     lspOpKind
	uri      string
	abs      string
	langID   string
	version  int
	text     string
	fromDisk bool
	hoverID  int
	line     int // hover: zero-based source line
	col      int // hover: rune column in the raw line
}

// lspSent is what the server was last told about one document, so
// diagnostics can be converted against exactly that text.
type lspSent struct {
	version int
	lines   []string
}

type lspServer struct {
	mgr  *lspManager
	key  string
	cfg  lsp.ServerConfig
	root string

	// Loop-side only.
	docCount  int
	idleSince time.Time

	qmu    sync.Mutex
	queue  []lspOp
	wakeCh chan struct{}

	smu    sync.Mutex
	sent   map[string]*lspSent
	client *lsp.Client
}

func (s *lspServer) enqueue(op lspOp) {
	s.qmu.Lock()
	s.queue = append(s.queue, op)
	s.qmu.Unlock()

	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

func (s *lspServer) next() lspOp {
	for {
		s.qmu.Lock()
		if len(s.queue) > 0 {
			op := s.queue[0]
			s.queue = s.queue[1:]
			s.qmu.Unlock()

			return op
		}
		s.qmu.Unlock()

		<-s.wakeCh
	}
}

func (s *lspServer) run() {
	defer s.mgr.wg.Done()

	log := s.mgr.log.addf
	opt := lsp.Options{
		Handler: s.onNotify,
		Log:     func(f string, a ...any) { log("["+s.cfg.Name+"] "+f, a...) },
	}

	if len(s.cfg.InitOptions) > 0 {
		opt.InitOptions = s.cfg.InitOptions
	}

	log("[%s] starting %s in %s", s.cfg.Name, s.cfg.Command, s.root)

	client, err := s.mgr.connect(s.cfg, s.root, opt)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), lspInitTimeout)
		err = client.Initialize(ctx, s.root)

		cancel()

		if err != nil {
			client.Close()
		}
	}

	if err != nil {
		s.mgr.post(lspEvent{kind: lspEvFailed, server: s, err: err})
		s.discard()

		return
	}

	s.smu.Lock()
	s.client = client
	s.smu.Unlock()

	stopping := make(chan struct{})

	go func() {
		select {
		case <-client.Done():
			select {
			case <-stopping:
			default:
				s.mgr.post(lspEvent{kind: lspEvExited, server: s})
			}
		case <-stopping:
		}
	}()

	defer close(stopping)

	for {
		op := s.next()
		if op.kind == lspOpStop {
			ctx, cancel := context.WithTimeout(context.Background(), lspStopTimeout)
			_ = client.Shutdown(ctx)

			cancel()

			return
		}

		s.apply(client, op)
	}
}

// discard keeps draining ops until a stop arrives so enqueue never piles up
// on a dead server; ops are dropped.
func (s *lspServer) discard() {
	s.qmu.Lock()
	s.queue = nil
	s.qmu.Unlock()
}

func (s *lspServer) apply(c *lsp.Client, op lspOp) {
	logf := s.mgr.log.addf

	switch op.kind {
	case lspOpOpen, lspOpChange:
		text := op.text

		if op.fromDisk {
			data, err := os.ReadFile(op.abs)
			if err != nil || len(data) > maxFileBytes {
				logf("[%s] skip %s: unreadable or over %d KB", s.cfg.Name, op.abs, maxFileBytes>>10)

				return
			}

			text = string(data)
		}

		s.smu.Lock()
		s.sent[op.uri] = &lspSent{version: op.version, lines: lsp.SplitLines(text)}
		s.smu.Unlock()

		var err error
		if op.kind == lspOpOpen {
			err = c.Notify("textDocument/didOpen", lsp.DidOpenParams{TextDocument: lsp.TextDocumentItem{
				URI: op.uri, LanguageID: op.langID, Version: op.version, Text: text,
			}})
		} else {
			err = c.Notify("textDocument/didChange", lsp.DidChangeParams{
				TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: op.uri, Version: op.version},
				ContentChanges: []lsp.ContentChange{{Text: text}},
			})
		}

		if err != nil {
			logf("[%s] notify: %v", s.cfg.Name, err)
		}
	case lspOpHover:
		s.startHover(c, op)
	case lspOpSave:
		if err := c.Notify("textDocument/didSave", lsp.DidSaveParams{TextDocument: lsp.TextDocumentIdentifier{URI: op.uri}}); err != nil {
			logf("[%s] didSave: %v", s.cfg.Name, err)
		}
	case lspOpClose:
		s.smu.Lock()
		delete(s.sent, op.uri)
		s.smu.Unlock()

		if err := c.Notify("textDocument/didClose", lsp.DidCloseParams{TextDocument: lsp.TextDocumentIdentifier{URI: op.uri}}); err != nil {
			logf("[%s] didClose: %v", s.cfg.Name, err)
		}
	}
}

// onNotify runs on the client's read goroutine and must not block.
func (s *lspServer) onNotify(method string, params json.RawMessage) {
	if method != "textDocument/publishDiagnostics" {
		return
	}

	var p lsp.PublishDiagnosticsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}

	s.smu.Lock()
	sent := s.sent[p.URI]
	s.smu.Unlock()

	// A response for an older version than the one last sent is stale.
	if p.Version != nil && (sent == nil || *p.Version < sent.version) {
		return
	}

	var lines []string
	if sent != nil {
		lines = sent.lines
	}
	diags := make([]lspDiag, 0, len(p.Diagnostics))
	for _, d := range p.Diagnostics {
		if d.Severity == lsp.SeverityHint {
			continue
		}

		diags = append(diags, newLSPDiag(lines, d))
	}

	ev := lspEvent{kind: lspEvDiag, server: s, uri: p.URI, diags: diags}
	if p.Version != nil {
		ev.version, ev.hasVersion = *p.Version, true
	}

	s.mgr.post(ev)
}
