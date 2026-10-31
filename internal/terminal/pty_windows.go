//go:build windows

package terminal

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
)

const platformNote = "Windows fallback: pipes only, no PTY"

// pipeIO is the Windows fallback transport. It connects the shell's stdin and
// stdout to the session without a pseudo-terminal, so full-screen programs and
// terminal resize have limited support.
type pipeIO struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser

	closeOnce sync.Once
	closeErr  error
}

func (p *pipeIO) Read(b []byte) (int, error) {
	return p.stdout.Read(b)
}

func (p *pipeIO) Write(b []byte) (int, error) {
	return p.stdin.Write(b)
}

func (p *pipeIO) Close() error {
	p.closeOnce.Do(func() {
		p.closeErr = errors.Join(p.stdin.Close(), p.stdout.Close())
	})
	return p.closeErr
}

func (p *pipeIO) Resize(cols, rows int) error {
	return nil
}

func startCommand(cmd *exec.Cmd, cols, rows int) (sessionIO, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	// StdoutPipe owns cmd.Stdout, so stderr cannot share that writer. Send
	// stderr to the app's console instead of dropping it.
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	return &pipeIO{stdin: stdin, stdout: stdout}, nil
}

func defaultShell() string {
	if shell := os.Getenv("COMSPEC"); shell != "" {
		return shell
	}
	return "cmd.exe"
}
