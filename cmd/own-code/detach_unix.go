//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// startDetached relaunches this executable in a new session with stdin on
// /dev/null and stdout/stderr in logPath. The child keeps the caller's
// working directory and environment, including DISPLAY and WAYLAND_DISPLAY.
func startDetached(logPath string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	defer logFile.Close()
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return 0, err
	}
	defer devNull.Close()

	cmd := exec.Command(exe, os.Args[1:]...) //nolint:gosec // re-exec of this binary with the operator's own arguments
	cmd.Env = append(os.Environ(), detachEnv+"=1")
	cmd.Stdin = devNull
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}
