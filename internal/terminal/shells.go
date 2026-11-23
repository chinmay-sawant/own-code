package terminal

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ShellProfile names a shell that can be launched on the current platform.
type ShellProfile struct {
	Label   string
	Path    string
	Default bool
}

// InstalledShells resolves the configured default and known shells on demand.
// Shells are discovered on this host so the launcher only offers executables
// it can start with its native terminal transport.
func InstalledShells(configured string) []ShellProfile {
	type candidate struct {
		label string
		paths []string
	}
	candidates := []candidate{
		{"Bash", []string{"bash"}},
		{"Zsh", []string{"zsh"}},
		{"Fish", []string{"fish"}},
		{"POSIX sh", []string{"sh"}},
		{"PowerShell", []string{"pwsh"}},
	}
	if runtime.GOOS == "windows" {
		candidates = []candidate{
			{"Command Prompt", []string{os.Getenv("COMSPEC"), "cmd.exe"}},
			{"Windows PowerShell", []string{"powershell.exe"}},
			{"PowerShell", []string{"pwsh.exe"}},
		}
		if root := os.Getenv("SystemRoot"); root != "" {
			candidates[1].paths = append(candidates[1].paths, filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"))
		}
		gitBash := candidate{label: "Git Bash"}
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			if dir := os.Getenv(env); dir != "" {
				gitBash.paths = append(gitBash.paths, filepath.Join(dir, "Git", "bin", "bash.exe"))
			}
		}
		candidates = append(candidates, gitBash)
	}

	nameKey := func(path string) string {
		name := filepath.Base(path)
		if runtime.GOOS == "windows" {
			name = strings.ToLower(name)
		}
		return name
	}
	defaultPath, _ := exec.LookPath(configured)
	profiles := make([]ShellProfile, 0, len(candidates)+1)
	seen := make(map[string]bool, len(candidates)+1)
	if defaultPath != "" {
		label := filepath.Base(defaultPath)
		for _, c := range candidates {
			for _, option := range c.paths {
				if nameKey(option) == nameKey(defaultPath) {
					label = c.label
					break
				}
			}
		}
		profiles = append(profiles, ShellProfile{Label: label, Path: defaultPath, Default: true})
		seen[nameKey(defaultPath)] = true
	}
	for _, c := range candidates {
		for _, option := range c.paths {
			path, err := exec.LookPath(option)
			if err != nil || seen[nameKey(path)] {
				continue
			}
			profiles = append(profiles, ShellProfile{Label: c.label, Path: path})
			seen[nameKey(path)] = true
			break
		}
	}
	return profiles
}
