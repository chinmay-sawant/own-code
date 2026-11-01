package app

import (
	"os"
	"testing"
)

// TestMain isolates every test in this package from the operator's real
// config directory. New and switchProject persist recent projects under
// os.UserConfigDir, so without this the suite would push /tmp test roots
// into ~/.config/ownframe/code-view/recent-projects.json and evict the
// user's live projects from the capped list. Individual tests that need
// their own directory keep using t.Setenv, which overrides this default
// for the duration of the test.
func TestMain(m *testing.M) {
	if os.Getenv("XDG_CONFIG_HOME") == "" {
		dir, err := os.MkdirTemp("", "own-code-test-config")
		if err == nil {
			_ = os.Setenv("XDG_CONFIG_HOME", dir)

			code := m.Run()

			_ = os.RemoveAll(dir)
			os.Exit(code)
		}
	}

	os.Exit(m.Run())
}
