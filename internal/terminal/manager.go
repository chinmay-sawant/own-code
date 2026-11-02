// Package terminal provides a PTY-backed shell session manager and an ANSI
// terminal screen emulator with scrollback.
package terminal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Session close waits this long for the reader goroutine to observe the killed
// process. Closing the PTY normally unblocks the reader immediately.
const closeTimeout = 5 * time.Second

var (
	// ErrNoSession reports a manager operation for an unknown session id.
	ErrNoSession = errors.New("terminal: no such session")
	// ErrSessionClosed reports a write to a session whose shell has exited.
	ErrSessionClosed = errors.New("terminal: session is closed")
	// ErrCloseTimeout reports that a session did not stop within closeTimeout.
	ErrCloseTimeout = errors.New("terminal: close timed out")
)

// Config describes one shell session. Empty Shell uses the platform default,
// a blank Dir inherits the current directory, and Env is appended to
// os.Environ. Non-positive Cols and Rows default to 80x24.
type Config struct {
	Shell string
	Args  []string
	Dir   string
	Env   []string
	Cols  int
	Rows  int
}

// SessionInfo is the cheap, copyable state of one session.
type SessionInfo struct {
	ID       int
	Title    string
	Running  bool
	ExitCode int
	Note     string
	// Version advances when the session's screen changes. Callers can skip a
	// Snapshot when the version is unchanged.
	Version uint64
}

// Session is a single running shell. Exit status, screen state, and the PTY
// are safe for concurrent use through a Manager or the exported methods.
type Session struct {
	id     int
	title  string
	note   string
	cmd    *exec.Cmd
	term   sessionIO
	screen *Screen

	mu       sync.Mutex
	running  bool
	exitCode int

	done      chan struct{}
	closeOnce sync.Once
	ioOnce    sync.Once
	ioMu      sync.Mutex
	ioClosed  bool
	closeErr  error

	notifyMu sync.Mutex
	notify   func()
}

// Manager owns shell sessions. Open makes the new session active. Exited
// sessions stay listed, with Running false, until Close removes them.
type Manager struct {
	mu       sync.Mutex
	nextID   int
	sessions map[int]*Session
	active   int
	notify   func()
}

// SetNotify registers a callback the manager invokes from its reader
// goroutine after shell output reaches a screen. The app uses it to ask
// the window for a frame, so typed echo and command output paint without
// waiting for the next incidental frame.
func (m *Manager) SetNotify(fn func()) {
	m.mu.Lock()
	m.notify = fn
	for _, session := range m.sessions {
		session.setNotify(fn)
	}
	m.mu.Unlock()
}

// DefaultShell returns the platform shell used when Config.Shell is empty.
func DefaultShell() string { return defaultShell() }

// NewManager returns an empty session manager.
func NewManager() *Manager {
	return &Manager{
		nextID:   1,
		sessions: make(map[int]*Session),
	}
}

// Open starts a shell session and returns its 1-based id. The new session
// becomes active.
func (m *Manager) Open(cfg Config) (int, error) {
	cfg = normalizeConfig(cfg)

	cmd := exec.Command(cfg.Shell, cfg.Args...)
	cmd.Dir = cfg.Dir
	cmd.Env = cfg.Env

	term, err := startCommand(cmd, cfg.Cols, cfg.Rows)
	if err != nil {
		return 0, fmt.Errorf("terminal: start %s: %w", cfg.Shell, err)
	}

	m.mu.Lock()
	if m.sessions == nil {
		m.sessions = make(map[int]*Session)
	}
	if m.nextID <= 0 {
		m.nextID = 1
	}
	id := m.nextID
	m.nextID++

	session := &Session{
		id:      id,
		title:   titleFor(cfg.Shell),
		note:    platformNote,
		cmd:     cmd,
		term:    term,
		screen:  NewScreen(cfg.Cols, cfg.Rows),
		running: true,
		done:    make(chan struct{}),
	}
	m.sessions[id] = session
	m.active = id
	notify := m.notify
	m.mu.Unlock()

	session.setNotify(notify)

	go session.readLoop()
	return id, nil
}

// Close stops and forgets a session. It is idempotent, including after the
// shell exited on its own, and returns nil for an unknown id.
func (m *Manager) Close(id int) error {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
		if m.active == id {
			m.active = 0
			for nextID := range m.sessions {
				if m.active == 0 || nextID < m.active {
					m.active = nextID
				}
			}
		}
	}
	m.mu.Unlock()

	if !ok {
		return nil
	}
	return session.close()
}

// CloseAll stops every session and empties the manager.
func (m *Manager) CloseAll() error {
	m.mu.Lock()
	ids := make([]int, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	sessions := make([]*Session, 0, len(ids))
	for _, id := range ids {
		sessions = append(sessions, m.sessions[id])
	}
	m.sessions = make(map[int]*Session)
	m.active = 0
	m.mu.Unlock()

	var errs []error
	for _, session := range sessions {
		if err := session.close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// SetActive marks an existing session active. Passing zero clears the active
// selection; unknown ids are ignored.
func (m *Manager) SetActive(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id == 0 {
		m.active = 0
		return
	}
	if _, ok := m.sessions[id]; ok {
		m.active = id
	}
}

// Active returns the active session id and pointer, or zero and nil.
func (m *Manager) Active() (int, *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active == 0 {
		return 0, nil
	}
	session, ok := m.sessions[m.active]
	if !ok {
		return 0, nil
	}
	return m.active, session
}

// Sessions returns the open and exited-but-not-closed sessions sorted by id.
func (m *Manager) Sessions() []SessionInfo {
	m.mu.Lock()
	ids := make([]int, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	sessions := make([]*Session, 0, len(ids))
	for _, id := range ids {
		sessions = append(sessions, m.sessions[id])
	}
	m.mu.Unlock()

	infos := make([]SessionInfo, 0, len(sessions))
	for _, session := range sessions {
		infos = append(infos, session.Info())
	}
	return infos
}

// Write sends input bytes to a session's shell.
func (m *Manager) Write(id int, p []byte) error {
	session := m.session(id)
	if session == nil {
		return ErrNoSession
	}
	return session.Write(p)
}

// Resize changes a session's PTY and screen dimensions.
func (m *Manager) Resize(id, cols, rows int) error {
	session := m.session(id)
	if session == nil {
		return ErrNoSession
	}
	return session.Resize(cols, rows)
}

// Snapshot returns a copy of a session's screen. The bool is false for an
// unknown or closed session.
func (m *Manager) Snapshot(id int) (Snapshot, bool) {
	session := m.session(id)
	if session == nil {
		return Snapshot{}, false
	}
	return session.Snapshot(), true
}

// Clear erases one session's screen and scrollback. The shell keeps running.
func (m *Manager) Clear(id int) error {
	session := m.session(id)
	if session == nil {
		return ErrNoSession
	}
	session.screen.Clear()
	return nil
}

func (m *Manager) session(id int) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

// ID returns the 1-based session id.
func (s *Session) ID() int {
	return s.id
}

// Snapshot returns a copy of the terminal screen with session state filled in.
func (s *Session) Snapshot() Snapshot {
	snapshot := s.screen.Snapshot()
	return s.snapshotState(snapshot)
}

// SnapshotViewport returns a bounded copy of a session's output.
func (s *Session) SnapshotViewport(rows, scroll int) Snapshot {
	return s.snapshotState(s.screen.SnapshotViewport(rows, scroll))
}

func (s *Session) snapshotState(snapshot Snapshot) Snapshot {
	s.mu.Lock()
	snapshot.Title = s.title
	snapshot.Running = s.running
	snapshot.Note = s.note
	s.mu.Unlock()
	return snapshot
}

// Info returns the session's copyable state.
func (s *Session) Info() SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SessionInfo{
		ID:       s.id,
		Title:    s.title,
		Running:  s.running,
		ExitCode: s.exitCode,
		Note:     s.note,
		Version:  s.screen.Version(),
	}
}

// Write sends input bytes to the shell. A session whose shell exited reports
// ErrSessionClosed.
func (s *Session) Write(p []byte) error {
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	if !running {
		return ErrSessionClosed
	}

	if _, err := s.term.Write(p); err != nil {
		return fmt.Errorf("terminal: write: %w", err)
	}
	return nil
}

// Resize updates the screen and, while the shell is running, the PTY size.
func (s *Session) Resize(cols, rows int) error {
	cols, rows = normalizeSize(cols, rows)

	s.mu.Lock()
	running := s.running
	s.mu.Unlock()

	var err error
	if running {
		s.ioMu.Lock()
		if !s.ioClosed {
			if resizeErr := s.term.Resize(cols, rows); resizeErr != nil {
				err = fmt.Errorf("terminal: resize: %w", resizeErr)
			}
		}
		s.ioMu.Unlock()
	}
	s.screen.Resize(cols, rows)
	return err
}

// readLoop is the only reader of the PTY and the only caller of cmd.Wait. It
// exits after the shell closes, which also releases the PTY and wakes Close.
func (s *Session) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.term.Read(buf)
		if n > 0 {
			s.screen.Feed(buf[:n])
			s.emitOutput()
		}
		if err != nil {
			break
		}
	}

	exitCode := exitCode(s.cmd.Wait())
	s.closeIO()

	s.mu.Lock()
	s.running = false
	s.exitCode = exitCode
	s.mu.Unlock()

	s.emitOutput()
	close(s.done)
}

func (s *Session) close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		running := s.running
		s.mu.Unlock()

		if running {
			if s.cmd.Process != nil {
				_ = s.cmd.Process.Kill()
			}
			s.closeIO()

			timer := time.NewTimer(closeTimeout)
			defer timer.Stop()
			select {
			case <-s.done:
			case <-timer.C:
				s.mu.Lock()
				s.running = false
				s.exitCode = -1
				if s.note == "" {
					s.note = "close timed out"
				}
				s.mu.Unlock()
				s.closeErr = ErrCloseTimeout
			}
			return
		}
		s.closeIO()
	})
	return s.closeErr
}

func (s *Session) closeIO() {
	s.ioOnce.Do(func() {
		s.ioMu.Lock()
		s.ioClosed = true
		_ = s.term.Close()
		s.ioMu.Unlock()
	})
}

// setNotify replaces the output callback. A nil callback disables it.
func (s *Session) setNotify(fn func()) {
	s.notifyMu.Lock()
	s.notify = fn
	s.notifyMu.Unlock()
}

// emitOutput runs the output callback, if any, outside the session locks.
func (s *Session) emitOutput() {
	s.notifyMu.Lock()
	fn := s.notify
	s.notifyMu.Unlock()

	if fn != nil {
		fn()
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func normalizeConfig(cfg Config) Config {
	if strings.TrimSpace(cfg.Shell) == "" {
		cfg.Shell = defaultShell()
	}
	cfg.Cols, cfg.Rows = normalizeSize(cfg.Cols, cfg.Rows)
	cfg.Env = append(os.Environ(), cfg.Env...)
	cfg.Env = terminalEnv(cfg.Env)
	return cfg
}

// terminalEnv makes sure a GUI launch still presents a capable terminal.
// Explicit caller values win; an unset or dumb TERM is replaced.
func terminalEnv(env []string) []string {
	term := ""
	colorTerm := ""
	for _, entry := range env {
		switch {
		case strings.HasPrefix(entry, "TERM="):
			term = strings.TrimPrefix(entry, "TERM=")
		case strings.HasPrefix(entry, "COLORTERM="):
			colorTerm = strings.TrimPrefix(entry, "COLORTERM=")
		}
	}

	out := make([]string, 0, len(env)+2)
	for _, entry := range env {
		switch {
		case strings.HasPrefix(entry, "TERM="):
			if term == "" || term == "dumb" {
				entry = "TERM=xterm-256color"
			}
		case strings.HasPrefix(entry, "COLORTERM="):
			if colorTerm == "" {
				entry = "COLORTERM=truecolor"
			}
		}
		out = append(out, entry)
	}

	if term == "" || term == "dumb" {
		// The loop already replaced an existing TERM entry. With no entry at
		// all, add one.
		found := false
		for _, entry := range out {
			if strings.HasPrefix(entry, "TERM=") {
				found = true
				break
			}
		}
		if !found {
			out = append(out, "TERM=xterm-256color")
		}
	}
	if colorTerm == "" {
		found := false
		for _, entry := range out {
			if strings.HasPrefix(entry, "COLORTERM=") {
				found = true
				break
			}
		}
		if !found {
			out = append(out, "COLORTERM=truecolor")
		}
	}

	return out
}

func titleFor(shell string) string {
	title := filepath.Base(shell)
	if title == "" || title == "." || title == string(filepath.Separator) {
		return shell
	}
	return title
}

type sessionIO interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}
