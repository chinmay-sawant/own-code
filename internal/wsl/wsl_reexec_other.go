//go:build !windows

package wsl

import "flag"

// AddWSLFlags registers nothing outside Windows; the WSL handoff only runs
// on Windows, so other platforms take no -wsl or -wsl-bin flags.
func AddWSLFlags(_ *flag.FlagSet) {}

// MaybeReexecInWSL never hands off outside Windows.
func MaybeReexecInWSL() bool {
	return false
}
