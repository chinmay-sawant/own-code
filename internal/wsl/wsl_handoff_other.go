//go:build !windows

package wsl

import "errors"

// Available is always false outside Windows; only a Windows host can hand
// off into WSL.
func Available() bool {
	return false
}

// Enabled is always false outside Windows; the -wsl flag only exists on
// the Windows build.
func Enabled() bool {
	return false
}

// ListDistros has no distros outside Windows.
func ListDistros() []string {
	return nil
}

// CompanionBin has no companion outside Windows.
func CompanionBin() (string, bool) {
	return "", false
}

// SpawnHandoff never runs outside Windows.
func SpawnHandoff(_, _ string) error {
	return errors.New("wsl handoff requires Windows")
}
