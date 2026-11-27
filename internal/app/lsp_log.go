package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const lspLogLines = 2000

// lspLog is a bounded in-memory trace of server traffic and stderr.
type lspLog struct {
	mu    sync.Mutex
	lines []string
}

func newLSPLog() *lspLog { return &lspLog{} }

func (l *lspLog) addf(format string, args ...any) {
	line := time.Now().Format("15:04:05.000") + " " + strings.TrimRight(fmt.Sprintf(format, args...), "\n")

	l.mu.Lock()
	defer l.mu.Unlock()

	l.lines = append(l.lines, line)
	if len(l.lines) > lspLogLines {
		l.lines = append([]string(nil), l.lines[len(l.lines)-lspLogLines:]...)
	}
}

func (l *lspLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.lines) == 0 {
		return "No language server activity yet.\n"
	}

	return strings.Join(l.lines, "\n") + "\n"
}

// lspLogPath is where "LSP: Show log" writes the trace.
func lspLogPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(base, "ownframe", "code-view", "lsp.log"), nil
}

// showLSPLog writes the trace to the log file and opens it in a tab.
func (a *App) showLSPLog(_ context.Context) {
	path, err := lspLogPath()
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), sessionDirMode)
	}

	if err == nil {
		err = os.WriteFile(path, []byte(a.lspManager().log.text()), sessionFileMode)
	}

	if err != nil {
		a.showToast("error", "LSP log", err.Error())

		return
	}

	a.openFile(filepath.ToSlash(path))
}
