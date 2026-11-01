package app

import (
	"errors"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/wsl"
)

func stubWSLHandoff(goos string, avail, enabled bool, spawnErr error) func() {
	oldGOOS, oldAvail, oldEnabled, oldSpawn := wslHandoffGOOS, wslHandoffAvailable, wslHandoffEnabled, wslHandoffSpawn
	oldList := listWSLDistros
	wslHandoffGOOS = goos
	wslHandoffAvailable = func() bool { return avail }
	wslHandoffEnabled = func() bool { return enabled }
	wslHandoffSpawn = func(_, _ string) error { return spawnErr }
	return func() {
		wslHandoffGOOS, wslHandoffAvailable, wslHandoffEnabled, wslHandoffSpawn = oldGOOS, oldAvail, oldEnabled, oldSpawn
		listWSLDistros = oldList
	}
}

func TestBrowseUpWSLLevels(t *testing.T) {
	next, ok := browseUpTarget(`\\wsl$`)
	if !ok || next != "" {
		t.Fatalf("Up from WSL list = %q,%v; want empty,true", next, ok)
	}
	next, ok = browseUpTarget(`\\wsl$\Ubuntu`)
	if !ok || next != wsl.WSLDistrosRoot {
		t.Fatalf("Up from distro root = %q,%v; want sentinel", next, ok)
	}
	next, ok = browseUpTarget(`\\wsl$\Ubuntu\home\user`)
	if !ok || next != `\\wsl$\Ubuntu\home` {
		t.Fatalf("Up from nested = %q,%v; want parent", next, ok)
	}
	next, ok = browseUpTarget(`\\wsl$\Ubuntu\home`)
	if !ok || next != `\\wsl$\Ubuntu` {
		t.Fatalf("Up to distro = %q,%v", next, ok)
	}
}

func TestBrowseParentWSL(t *testing.T) {
	if got := browseParentDir(`\\wsl$\Ubuntu`); got != wsl.WSLDistrosRoot {
		t.Fatalf("parent distro root = %q", got)
	}
	if got := browseParentDir(`\\wsl$\Ubuntu\home\user\`); got != `\\wsl$\Ubuntu\home` {
		t.Fatalf("parent nested = %q", got)
	}
}

func TestBlockedWSLRoots(t *testing.T) {
	for _, p := range []string{`\\wsl$`, `\\wsl$\Ubuntu`, `\\wsl$\Ubuntu\`} {
		if !blockedProjectRoot(p) {
			t.Errorf("blockedProjectRoot(%q) = false, want true", p)
		}
	}
	if blockedProjectRoot(`\\wsl$\Ubuntu\home\user`) {
		t.Error("blocked nested WSL path, want false")
	}
}

func TestBrowseDisplayWSL(t *testing.T) {
	a := &App{browseRoot: `\\wsl$`}
	if got := a.browseDisplayName(); got != "WSL" {
		t.Fatalf("display sentinel = %q, want WSL", got)
	}
	a.browseRoot = `\\wsl$\Ubuntu`
	if got := a.browseDisplayName(); got != "Ubuntu" {
		t.Fatalf("display distro = %q", got)
	}
	a.browseRoot = `\\wsl$\Ubuntu\home\user`
	if got := a.browseDisplayName(); got != "user" {
		t.Fatalf("display nested = %q", got)
	}
}

func TestBrowseWSLDistroRowsStub(t *testing.T) {
	restore := stubWSLHandoff("windows", true, true, nil)
	defer restore()
	listWSLDistros = func() []string { return []string{"Ubuntu", "Debian"} }
	a := &App{browseRoot: wsl.WSLDistrosRoot}
	rows := a.browseAllEntries()
	if len(rows) != 2 || rows[0].Name != "Debian" || rows[1].Name != "Ubuntu" {
		t.Fatalf("distro rows = %+v, want sorted Debian,Ubuntu", rows)
	}
	if rows[0].Path != `\\wsl$\Debian` {
		t.Fatalf("distro path = %q", rows[0].Path)
	}
}

func TestWSLHandoffTarget(t *testing.T) {
	restore := stubWSLHandoff("windows", true, true, nil)
	defer restore()
	distro, linuxPath, ok := wslHandoffTarget(`\\wsl$\Ubuntu\home\user`)
	if !ok || distro != "Ubuntu" || linuxPath != "/home/user" {
		t.Fatalf("handoff target = %q,%q,%v", distro, linuxPath, ok)
	}
	if _, _, ok := wslHandoffTarget(`C:\Users\name`); ok {
		t.Fatal("drive path should not handoff")
	}
	if _, _, ok := wslHandoffTarget(`\\wsl$\Ubuntu`); ok {
		t.Fatal("bare distro root is blocked, should not handoff")
	}
}

func TestWSLHandoffGating(t *testing.T) {
	for _, tc := range []struct {
		name          string
		goos          string
		avail, enabled bool
		want          bool
	}{
		{"linux host", "linux", true, true, false},
		{"no wsl", "windows", false, true, false},
		{"flag off", "windows", true, false, false},
		{"windows ready", "windows", true, true, true},
	} {
		restore := stubWSLHandoff(tc.goos, tc.avail, tc.enabled, nil)
		_, _, ok := wslHandoffTarget(`\\wsl$\Ubuntu\home\user`)
		restore()
		if ok != tc.want {
			t.Errorf("%s: ok=%v want %v", tc.name, ok, tc.want)
		}
	}
}

func TestTryHandoffSpawnsAndHandlesSpawnError(t *testing.T) {
	restore := stubWSLHandoff("windows", true, true, nil)
	defer restore()
	called := ""
	wslHandoffSpawn = func(distro, linuxPath string) error {
		called = distro + ":" + linuxPath
		return nil
	}
	a := &App{}
	if !a.tryHandoffToWSL(`\\wsl$\Ubuntu\home\user`) {
		t.Fatal("tryHandoff = false, want true")
	}
	if called != "Ubuntu:/home/user" {
		t.Fatalf("spawn called with %q", called)
	}
	wslHandoffSpawn = func(_, _ string) error { return errors.New("boom") }
	a = &App{}
	if !a.tryHandoffToWSL(`\\wsl$\Ubuntu\home\user`) {
		t.Fatal("spawn-error should still report handled")
	}
	if a.fileAlert == "" {
		t.Fatal("spawn-error should set fileAlert")
	}
	a = &App{}
	if a.tryHandoffToWSL(`C:\Users\name`) {
		t.Fatal("native path should not handoff")
	}
}
