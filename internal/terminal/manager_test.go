package terminal

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const sessionTimeout = 5 * time.Second

func requireUnixShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("requires a Unix /bin/sh")
	}
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(sessionTimeout)
	for {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func snapshotText(snapshot Snapshot) string {
	var b strings.Builder
	for _, line := range snapshot.Lines {
		for _, cell := range line {
			if cell.Rune == 0 {
				b.WriteByte(' ')
				continue
			}
			b.WriteRune(cell.Rune)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestManagerShellCommandExit(t *testing.T) {
	requireUnixShell(t)

	manager := NewManager()
	t.Cleanup(func() { _ = manager.CloseAll() })

	id, err := manager.Open(Config{
		Shell: "/bin/sh",
		Args:  []string{"-c", "printf 'hello\\nworld\\n'"},
		Cols:  40,
		Rows:  10,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if id != 1 {
		t.Errorf("first id = %d, want 1", id)
	}

	var snapshot Snapshot
	waitFor(t, "shell output and exit", func() bool {
		var ok bool
		snapshot, ok = manager.Snapshot(id)
		if !ok {
			return false
		}
		text := snapshotText(snapshot)
		return !snapshot.Running && strings.Contains(text, "hello") && strings.Contains(text, "world")
	})

	if snapshot.Title != "sh" {
		t.Errorf("title = %q, want sh", snapshot.Title)
	}
	if snapshot.Cols != 40 || snapshot.Rows != 10 {
		t.Errorf("size = %dx%d, want 40x10", snapshot.Cols, snapshot.Rows)
	}
	if !snapshot.CursorVisible {
		t.Error("cursor should be visible")
	}

	infos := manager.Sessions()
	if len(infos) != 1 {
		t.Fatalf("session count = %d, want 1", len(infos))
	}
	if infos[0].Running {
		t.Error("session should not be running")
	}
	if infos[0].ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", infos[0].ExitCode)
	}

	if err := manager.Close(id); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := manager.Close(id); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, ok := manager.Snapshot(id); ok {
		t.Error("snapshot should be absent after Close")
	}
	if got := manager.Sessions(); len(got) != 0 {
		t.Errorf("sessions after Close = %d, want 0", len(got))
	}
}

func TestManagerExitCode(t *testing.T) {
	requireUnixShell(t)

	manager := NewManager()
	t.Cleanup(func() { _ = manager.CloseAll() })

	id, err := manager.Open(Config{Shell: "/bin/sh", Args: []string{"-c", "exit 7"}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var info SessionInfo
	waitFor(t, "nonzero exit", func() bool {
		infos := manager.Sessions()
		if len(infos) == 0 {
			return false
		}
		info = infos[0]
		return !info.Running
	})
	if info.ExitCode != 7 {
		t.Errorf("exit code = %d, want 7", info.ExitCode)
	}
	if err := manager.Close(id); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestManagerWriteAndResize(t *testing.T) {
	requireUnixShell(t)

	manager := NewManager()
	t.Cleanup(func() { _ = manager.CloseAll() })

	id, err := manager.Open(Config{Shell: "/bin/sh", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := manager.Write(id, []byte("printf 'round-trip\\n'\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitFor(t, "written output", func() bool {
		snapshot, ok := manager.Snapshot(id)
		return ok && strings.Contains(snapshotText(snapshot), "round-trip")
	})

	if err := manager.Resize(id, 100, 30); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	snapshot, ok := manager.Snapshot(id)
	if !ok {
		t.Fatal("snapshot missing after Resize")
	}
	if snapshot.Cols != 100 || snapshot.Rows != 30 {
		t.Errorf("size = %dx%d, want 100x30", snapshot.Cols, snapshot.Rows)
	}

	if err := manager.Write(id, []byte("exit\n")); err != nil {
		t.Fatalf("exit write: %v", err)
	}
	waitFor(t, "interactive shell exit", func() bool {
		snapshot, ok := manager.Snapshot(id)
		return ok && !snapshot.Running
	})
}

func TestManagerEnvAndDir(t *testing.T) {
	requireUnixShell(t)

	dir := t.TempDir()
	manager := NewManager()
	t.Cleanup(func() { _ = manager.CloseAll() })

	id, err := manager.Open(Config{
		Shell: "/bin/sh",
		Args:  []string{"-c", `printf '%s\n' "$OWN_CODE_TEST"; pwd`},
		Dir:   dir,
		Env:   []string{"OWN_CODE_TEST=present"},
		Cols:  80,
		Rows:  10,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var snapshot Snapshot
	waitFor(t, "env and dir output", func() bool {
		var ok bool
		snapshot, ok = manager.Snapshot(id)
		if !ok {
			return false
		}
		text := snapshotText(snapshot)
		return !snapshot.Running &&
			strings.Contains(text, "present") &&
			strings.Contains(text, filepath.Base(dir))
	})
}

func TestManagerDefaultShellAndSize(t *testing.T) {
	requireUnixShell(t)

	manager := NewManager()
	t.Cleanup(func() { _ = manager.CloseAll() })

	id, err := manager.Open(Config{Args: []string{"-c", "printf 'default-shell\\n'"}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var snapshot Snapshot
	waitFor(t, "default shell output", func() bool {
		var ok bool
		snapshot, ok = manager.Snapshot(id)
		return ok && !snapshot.Running && strings.Contains(snapshotText(snapshot), "default-shell")
	})
	if snapshot.Cols != 80 || snapshot.Rows != 24 {
		t.Errorf("default size = %dx%d, want 80x24", snapshot.Cols, snapshot.Rows)
	}
	if snapshot.Title == "" {
		t.Error("title should not be empty")
	}
}

func TestManagerUnknownSession(t *testing.T) {
	manager := NewManager()

	if err := manager.Write(42, []byte("x")); !errors.Is(err, ErrNoSession) {
		t.Errorf("Write error = %v, want ErrNoSession", err)
	}
	if err := manager.Resize(42, 80, 24); !errors.Is(err, ErrNoSession) {
		t.Errorf("Resize error = %v, want ErrNoSession", err)
	}
	if _, ok := manager.Snapshot(42); ok {
		t.Error("Snapshot should report false")
	}
	if err := manager.Close(42); err != nil {
		t.Errorf("Close unknown: %v", err)
	}
	if id, session := manager.Active(); id != 0 || session != nil {
		t.Errorf("Active = (%d, %v), want (0, nil)", id, session)
	}
	if got := manager.Sessions(); len(got) != 0 {
		t.Errorf("Sessions = %d, want 0", len(got))
	}
	if err := manager.CloseAll(); err != nil {
		t.Errorf("CloseAll: %v", err)
	}
}

func TestManagerOpenFailure(t *testing.T) {
	manager := NewManager()

	if _, err := manager.Open(Config{Shell: filepath.Join(t.TempDir(), "missing-shell")}); err == nil {
		t.Fatal("Open should fail for a missing shell")
	}
	if got := manager.Sessions(); len(got) != 0 {
		t.Errorf("Sessions = %d, want 0", len(got))
	}
}

func TestManagerActiveAndIDs(t *testing.T) {
	requireUnixShell(t)

	manager := NewManager()
	t.Cleanup(func() { _ = manager.CloseAll() })

	first, err := manager.Open(Config{Shell: "/bin/sh", Args: []string{"-c", "sleep 30"}})
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	second, err := manager.Open(Config{Shell: "/bin/sh", Args: []string{"-c", "sleep 30"}})
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}

	if first != 1 || second != 2 {
		t.Fatalf("ids = (%d, %d), want (1, 2)", first, second)
	}
	if id, _ := manager.Active(); id != second {
		t.Errorf("active id = %d, want %d", id, second)
	}

	manager.SetActive(first)
	if id, session := manager.Active(); id != first || session == nil || session.ID() != first {
		t.Errorf("Active after SetActive = (%d, %v)", id, session)
	}

	infos := manager.Sessions()
	if len(infos) != 2 || infos[0].ID != first || infos[1].ID != second {
		t.Errorf("Sessions = %+v, want ids [%d %d]", infos, first, second)
	}

	if err := manager.CloseAll(); err != nil {
		t.Errorf("CloseAll: %v", err)
	}
	if id, session := manager.Active(); id != 0 || session != nil {
		t.Errorf("Active after CloseAll = (%d, %v), want (0, nil)", id, session)
	}
	if got := manager.Sessions(); len(got) != 0 {
		t.Errorf("Sessions after CloseAll = %d, want 0", len(got))
	}
}

func TestManagerCloseRunningIsIdempotent(t *testing.T) {
	requireUnixShell(t)

	manager := NewManager()
	id, err := manager.Open(Config{Shell: "/bin/sh", Args: []string{"-c", "sleep 30"}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := manager.Close(id); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := manager.Close(id); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, ok := manager.Snapshot(id); ok {
		t.Error("snapshot should be absent after Close")
	}
}

func TestTerminalEnv(t *testing.T) {
	env := terminalEnv([]string{"TERM=dumb", "COLORTERM="})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "TERM=xterm-256color") {
		t.Errorf("TERM was not replaced: %v", env)
	}
	if !strings.Contains(joined, "COLORTERM=truecolor") {
		t.Errorf("COLORTERM was not set: %v", env)
	}

	env = terminalEnv([]string{"TERM=screen-256color", "COLORTERM=24bit"})
	if got := strings.Join(env, "\n"); strings.Contains(got, "TERM=xterm") || strings.Contains(got, "COLORTERM=truecolor") {
		t.Errorf("explicit values were overwritten: %v", env)
	}
}

func TestManagerConcurrentReaders(t *testing.T) {
	requireUnixShell(t)

	manager := NewManager()
	t.Cleanup(func() { _ = manager.CloseAll() })

	id, err := manager.Open(Config{
		Shell: "/bin/sh",
		Args:  []string{"-c", "i=0; while [ $i -lt 200 ]; do printf 'line %d\\n' $i; i=$((i+1)); done"},
		Cols:  80,
		Rows:  24,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = manager.Snapshot(id)
				_ = manager.Sessions()
				_, _ = manager.Active()
				_ = manager.Resize(id, 80+(worker%2), 24)
			}
		}(i)
	}

	waitFor(t, "concurrent output session exit", func() bool {
		snapshot, ok := manager.Snapshot(id)
		return ok && !snapshot.Running
	})
	wg.Wait()

	snapshot, ok := manager.Snapshot(id)
	if !ok {
		t.Fatal("snapshot missing after concurrent reads")
	}
	if !strings.Contains(snapshotText(snapshot), "line 199") {
		t.Error("snapshot should contain the final output line")
	}
}
