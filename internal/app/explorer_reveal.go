package app

import (
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/wsl"
)

// Reveal opens one explorer row in the host GUI file manager: the folder
// itself for a folder, and the containing folder with the file selected for
// a file. Inside WSL it uses Windows Explorer through interop; macOS uses
// Finder, Windows uses Explorer, and Linux uses the desktop's file manager.
//
// The launcher seams are variables so tests can drive every platform branch
// on the Linux test host without opening a real file manager.
var (
	revealGOOS        = runtime.GOOS      //nolint:gochecknoglobals // stubbed in tests
	revealInWSL       = isWSL             //nolint:gochecknoglobals // stubbed in tests
	revealWindowsPath = wslWindowsPath    //nolint:gochecknoglobals // stubbed in tests
	revealLookPath    = exec.LookPath     //nolint:gochecknoglobals // stubbed in tests
	revealStart       = startRevealCmd    //nolint:gochecknoglobals // stubbed in tests
	revealOpener      = openInFileManager //nolint:gochecknoglobals // stubbed in tests
)

// revealCommand is one file-manager launch. wait marks a command whose exit
// status decides whether the next fallback runs; viewers start detached.
type revealCommand struct {
	name string
	args []string
	wait bool
}

// wslExplorerPath is the usual Windows Explorer location for a WSL guest
// whose PATH does not include the Windows interop directories.
const wslExplorerPath = `/mnt/c/Windows/explorer.exe`

// revealMenuLabel names the host file manager in the context-menu row.
func revealMenuLabel() string {
	switch {
	case revealGOOS == "darwin":
		return "Reveal in Finder"
	case revealGOOS == "windows":
		return "Reveal in File Explorer"
	case revealInWSL():
		return "Reveal in Windows Explorer"
	default:
		return "Reveal in File Manager"
	}
}

// revealExplorerPath opens a workspace row in the host file manager. An empty
// rel reveals the workspace root. The embedded sample has no disk path, so it
// reports why instead of opening a directory that does not exist.
func (a *App) revealExplorerPath(rel string) {
	if a == nil || a.ws == nil {
		return
	}

	if a.ws.Sample {
		a.showToast("info", "Embedded sample", "Reveal is available for a project on disk.")
		a.refresh()

		return
	}

	isDir := true
	if rel != "" {
		var ok bool

		isDir, ok = a.lookupTreePath(rel)
		if !ok {
			a.showToast("error", "Cannot reveal", "The file is not in the workspace.")
			a.refresh()

			return
		}
	}

	if err := revealOpener(a.explorerAbsPath(rel), isDir); err != nil {
		a.showToast("error", "Cannot reveal", err.Error())
	}

	a.refresh()
}

// openInFileManager launches the host file manager for path.
func openInFileManager(path string, isDir bool) error {
	inWSL := revealInWSL()

	windowsPath := ""
	if inWSL {
		windowsPath, _ = revealWindowsPath(path)
	}

	return launchReveal(revealCommandsFor(revealGOOS, inWSL, path, windowsPath, isDir))
}

// revealCommandsFor builds the launch order for one platform. The pure
// builder keeps Windows and macOS command shapes testable on Linux.
func revealCommandsFor(goos string, inWSL bool, path, windowsPath string, isDir bool) []revealCommand {
	switch {
	case goos == "darwin":
		if isDir {
			return []revealCommand{{name: "open", args: []string{path}}}
		}

		return []revealCommand{{name: "open", args: []string{"-R", path}}}
	case goos == "windows":
		return []revealCommand{windowsExplorerCommand(path, isDir)}
	case inWSL:
		commands := make([]revealCommand, 0, 3)
		if windowsPath != "" {
			commands = append(commands,
				windowsExplorerCommand(windowsPath, isDir),
				windowsExplorerCommandAt(wslExplorerPath, windowsPath, isDir),
			)
		}

		return append(commands, linuxRevealCommands(path, isDir)...)
	default:
		return linuxRevealCommands(path, isDir)
	}
}

// windowsExplorerCommand opens a folder or reveals a file beside it.
func windowsExplorerCommand(path string, isDir bool) revealCommand {
	return windowsExplorerCommandAt("explorer.exe", path, isDir)
}

// windowsExplorerCommandAt is windowsExplorerCommand with an explicit
// executable path, for the WSL interop fallback.
func windowsExplorerCommandAt(name, path string, isDir bool) revealCommand {
	if isDir {
		return revealCommand{name: name, args: []string{path}}
	}

	return revealCommand{name: name, args: []string{"/select,", path}}
}

// linuxRevealCommands opens a folder with xdg-open. A file asks the
// freedesktop FileManager1 interface to select it first, then falls back to
// opening the containing folder when no file manager answers.
func linuxRevealCommands(target string, isDir bool) []revealCommand {
	if isDir {
		return []revealCommand{{name: "xdg-open", args: []string{target}}}
	}

	commands := make([]revealCommand, 0, 2)
	if uri, ok := fileURI(target); ok {
		commands = append(commands, revealCommand{
			name: "dbus-send",
			args: []string{
				"--session",
				"--print-reply",
				"--reply-timeout=2000",
				"--type=method_call",
				"--dest=org.freedesktop.FileManager1",
				"/org/freedesktop/FileManager1",
				"org.freedesktop.FileManager1.ShowItems",
				"array:string:" + uri,
				"string:",
			},
			wait: true,
		})
	}

	return append(commands, revealCommand{name: "xdg-open", args: []string{path.Dir(target)}})
}

// fileURI converts an absolute POSIX path to a file URL. dbus-send separates
// array values with commas and has no escape for one inside a value, so a
// comma path skips the selection attempt and falls back to the folder.
func fileURI(target string) (string, bool) {
	if strings.ContainsAny(target, ",\n") {
		return "", false
	}

	return (&url.URL{Scheme: "file", Path: target}).String(), true
}

// wslWindowsPath translates a Linux guest path to the Windows form Explorer
// needs. wslpath is authoritative for custom mount points; the manual
// fallbacks cover minimal images where it is missing.
func wslWindowsPath(path string) (string, bool) {
	if out, err := exec.Command("wslpath", "-w", path).Output(); err == nil {
		converted := strings.TrimSpace(string(out))
		if converted != "" {
			return converted, true
		}
	}

	if converted, ok := wsl.WSLMntToWindows(path); ok {
		return converted, true
	}

	distro := strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME"))
	if distro == "" || !strings.HasPrefix(path, "/") {
		return "", false
	}

	converted := wsl.WSLDistrosRoot + `\` + distro
	if tail := strings.TrimPrefix(path, "/"); tail != "" {
		converted += `\` + strings.ReplaceAll(tail, "/", `\`)
	}

	return converted, true
}

// startRevealCmd starts one child process. It is a seam for tests.
func startRevealCmd(cmd *exec.Cmd) error {
	return cmd.Start()
}

// launchReveal runs the first available command. A wait command that fails
// falls through to the next candidate so Linux still opens the containing
// folder when the D-Bus selection is unavailable.
func launchReveal(commands []revealCommand) error {
	for i, candidate := range commands {
		exe, err := revealLookPath(candidate.name)
		if err != nil {
			continue
		}

		cmd := exec.Command(exe, candidate.args...) //nolint:gosec // fixed helper binaries; the workspace path is one argument
		hideConsoleWindow(cmd)

		if err := revealStart(cmd); err != nil {
			continue
		}

		if candidate.wait {
			rest := commands[i+1:]
			go func(cmd *exec.Cmd, rest []revealCommand) {
				if waitErr := cmd.Wait(); waitErr != nil {
					_ = launchReveal(rest)
				}
			}(cmd, rest)
		} else {
			_ = cmd.Process.Release()
		}

		return nil
	}

	return errNoFileManager()
}

// errNoFileManager reports that no launch helper is installed on this host.
func errNoFileManager() error {
	return errors.New("no file manager found on this system")
}
