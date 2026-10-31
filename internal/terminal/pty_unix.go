//go:build !windows

package terminal

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

const platformNote = ""

type unixPTY struct {
	*os.File
}

func (p *unixPTY) Resize(cols, rows int) error {
	return pty.Setsize(p.File, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
}

func startCommand(cmd *exec.Cmd, cols, rows int) (sessionIO, error) {
	file, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	if err != nil {
		return nil, err
	}
	return &unixPTY{File: file}, nil
}

func defaultShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}
