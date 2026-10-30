//go:build !windows

package app

import "os/exec"

// hideConsoleWindow is a no-op outside Windows; child processes there share
// the terminal the app was launched from.
func hideConsoleWindow(_ *exec.Cmd) {}
