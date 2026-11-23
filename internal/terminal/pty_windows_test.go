//go:build windows

package terminal

import (
	"strings"
	"testing"
)

func TestWindowsShellProfilesAcceptEnter(t *testing.T) {
	for _, profile := range InstalledShells(DefaultShell()) {
		if profile.Label != "Command Prompt" && profile.Label != "Windows PowerShell" {
			continue
		}
		t.Run(profile.Label, func(t *testing.T) {
			dir := t.TempDir()
			manager := NewManager()
			t.Cleanup(func() { _ = manager.CloseAll() })
			id, err := manager.Open(Config{Shell: profile.Path, Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.Write(id, []byte("echo owncodewindowsprobe")); err != nil {
				t.Fatal(err)
			}
			if err := manager.Write(id, []byte("\r")); err != nil {
				t.Fatal(err)
			}
			_, session := manager.Active()
			waitFor(t, "command output", func() bool {
				return strings.Contains("\n"+session.Snapshot().Text()+"\n", "\nowncodewindowsprobe\n")
			})
		})
	}
}
