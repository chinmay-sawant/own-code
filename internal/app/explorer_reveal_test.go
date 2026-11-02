package app

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRevealMenuLabels(t *testing.T) {
	oldGOOS, oldWSL := revealGOOS, revealInWSL
	defer func() {
		revealGOOS, revealInWSL = oldGOOS, oldWSL
	}()

	cases := []struct {
		goos string
		wsl  bool
		want string
	}{
		{"darwin", false, "Reveal in Finder"},
		{"windows", false, "Reveal in File Explorer"},
		{"linux", true, "Reveal in Windows Explorer"},
		{"linux", false, "Reveal in File Manager"},
	}

	for _, tc := range cases {
		revealGOOS = tc.goos
		revealInWSL = func() bool { return tc.wsl }

		if got := revealMenuLabel(); got != tc.want {
			t.Fatalf("label for %s (wsl %v) = %q, want %q", tc.goos, tc.wsl, got, tc.want)
		}
	}
}

func TestRevealCommands(t *testing.T) {
	linuxFile := "/home/me/project/a.go"
	linuxDir := "/home/me/project"
	windowsFile := `C:\Users\me\project\a.go`
	windowsDir := `C:\Users\me\project`
	wslWindowsFile := `\\wsl$\Ubuntu\home\me\project\a.go`

	dbusSelect := revealCommand{
		name: "dbus-send",
		args: []string{
			"--session",
			"--print-reply",
			"--reply-timeout=2000",
			"--type=method_call",
			"--dest=org.freedesktop.FileManager1",
			"/org/freedesktop/FileManager1",
			"org.freedesktop.FileManager1.ShowItems",
			"array:string:file:///home/me/project/a.go",
			"string:",
		},
		wait: true,
	}

	cases := []struct {
		name        string
		goos        string
		wsl         bool
		path        string
		windowsPath string
		isDir       bool
		want        []revealCommand
	}{
		{
			name: "mac file",
			goos: "darwin", path: linuxFile,
			want: []revealCommand{{name: "open", args: []string{"-R", linuxFile}}},
		},
		{
			name: "mac folder",
			goos: "darwin", path: linuxDir, isDir: true,
			want: []revealCommand{{name: "open", args: []string{linuxDir}}},
		},
		{
			name: "windows file",
			goos: "windows", path: windowsFile,
			want: []revealCommand{{name: "explorer.exe", args: []string{"/select,", windowsFile}}},
		},
		{
			name: "windows folder",
			goos: "windows", path: windowsDir, isDir: true,
			want: []revealCommand{{name: "explorer.exe", args: []string{windowsDir}}},
		},
		{
			name: "linux file selects then reveals folder",
			goos: "linux", path: linuxFile,
			want: []revealCommand{dbusSelect, {name: "xdg-open", args: []string{linuxDir}}},
		},
		{
			name: "linux folder opens itself",
			goos: "linux", path: linuxDir, isDir: true,
			want: []revealCommand{{name: "xdg-open", args: []string{linuxDir}}},
		},
		{
			name: "wsl file uses windows explorer first",
			goos: "linux", wsl: true, path: linuxFile, windowsPath: wslWindowsFile,
			want: []revealCommand{
				{name: "explorer.exe", args: []string{"/select,", wslWindowsFile}},
				{name: wslExplorerPath, args: []string{"/select,", wslWindowsFile}},
				dbusSelect,
				{name: "xdg-open", args: []string{linuxDir}},
			},
		},
		{
			name: "wsl folder uses windows explorer first",
			goos: "linux", wsl: true, path: linuxDir, windowsPath: `\\wsl$\Ubuntu\home\me\project`, isDir: true,
			want: []revealCommand{
				{name: "explorer.exe", args: []string{`\\wsl$\Ubuntu\home\me\project`}},
				{name: wslExplorerPath, args: []string{`\\wsl$\Ubuntu\home\me\project`}},
				{name: "xdg-open", args: []string{linuxDir}},
			},
		},
		{
			name: "wsl without translation falls back to linux",
			goos: "linux", wsl: true, path: linuxFile,
			want: []revealCommand{dbusSelect, {name: "xdg-open", args: []string{linuxDir}}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := revealCommandsFor(tc.goos, tc.wsl, tc.path, tc.windowsPath, tc.isDir)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("commands = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRevealFileSkipsDBusForCommaPath(t *testing.T) {
	commands := linuxRevealCommands("/home/me/a,b.go", false)

	if len(commands) != 1 || commands[0].name != "xdg-open" || commands[0].args[0] != "/home/me" {
		t.Fatalf("commands = %+v, want the containing folder fallback", commands)
	}
}

func TestRevealExplorerPath(t *testing.T) {
	oldOpener := revealOpener
	defer func() { revealOpener = oldOpener }()

	var (
		gotPath string
		gotDir  bool
		calls   int
	)

	revealOpener = func(path string, isDir bool) error {
		calls++
		gotPath, gotDir = path, isDir

		return nil
	}

	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n")
	writeFile(t, dir, "sub/b.go", "package sub\n")

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := app.Page().Redraw(context.Background()); err != nil {
		t.Fatalf("redraw: %v", err)
	}

	if err := app.MenuAction(context.Background(), "code-view:reveal:a.go"); err != nil {
		t.Fatalf("reveal file: %v", err)
	}

	if calls != 1 || gotDir || gotPath != filepath.Join(dir, "a.go") {
		t.Fatalf("file reveal = (%q, %v), calls %d", gotPath, gotDir, calls)
	}

	if err := app.MenuAction(context.Background(), "code-view:reveal:sub"); err != nil {
		t.Fatalf("reveal dir: %v", err)
	}

	if calls != 2 || !gotDir || gotPath != filepath.Join(dir, "sub") {
		t.Fatalf("folder reveal = (%q, %v), calls %d", gotPath, gotDir, calls)
	}

	if err := app.MenuAction(context.Background(), "code-view:reveal:"); err != nil {
		t.Fatalf("reveal root: %v", err)
	}

	if calls != 3 || !gotDir || gotPath != dir {
		t.Fatalf("root reveal = (%q, %v), calls %d", gotPath, gotDir, calls)
	}

	if err := app.MenuAction(context.Background(), "code-view:reveal:missing.go"); err != nil {
		t.Fatalf("reveal missing: %v", err)
	}

	if calls != 3 {
		t.Fatalf("missing path called the opener %d times", calls)
	}

	if toast := app.View().Toast; !toast.Show || toast.Kind != "error" {
		t.Fatalf("toast = %+v, want an error for a missing path", toast)
	}
}

func TestRevealSampleWorkspaceExplainsEmbeddedTree(t *testing.T) {
	oldOpener := revealOpener
	defer func() { revealOpener = oldOpener }()

	calls := 0
	revealOpener = func(string, bool) error {
		calls++

		return nil
	}

	app := newTestApp(t)
	app.revealExplorerPath("backend/probe.go")

	if calls != 0 {
		t.Fatal("sample workspace tried to open a path on disk")
	}

	if toast := app.View().Toast; !toast.Show || toast.Kind != "info" {
		t.Fatalf("toast = %+v, want an info note about the embedded sample", toast)
	}
}

func TestRevealOpenFailureShowsToast(t *testing.T) {
	oldOpener := revealOpener
	defer func() { revealOpener = oldOpener }()

	revealOpener = func(string, bool) error {
		return errNoFileManager()
	}

	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n")

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	app.revealExplorerPath("a.go")

	if toast := app.View().Toast; !toast.Show || toast.Kind != "error" || toast.Title != "Cannot reveal" {
		t.Fatalf("toast = %+v, want a reveal error", toast)
	}
}
