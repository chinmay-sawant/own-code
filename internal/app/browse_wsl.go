package app

import (
	"runtime"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/wsl"
)

// WSL handoff test seams. runtime.GOOS and the wsl package are stubbed in
// tests so the Windows-only handoff decision stays testable on Linux.
var (
	wslHandoffGOOS      = runtime.GOOS     //nolint:gochecknoglobals // stubbed in tests
	wslHandoffAvailable = wsl.Available    //nolint:gochecknoglobals // stubbed in tests
	wslHandoffEnabled   = wsl.Enabled      //nolint:gochecknoglobals // stubbed in tests
	wslHandoffSpawn     = wsl.SpawnHandoff //nolint:gochecknoglobals // stubbed in tests
	wslHandoffClose     = false            //nolint:gochecknoglobals // reserved: close behavior is fixed, kept as a seam for future options
)

// wslHandoffTarget maps a Windows-side pick to its WSL destination. It
// reports ok only when the host is Windows, WSL is installed and enabled
// (-wsl), and root is a non-blocked WSL network path. Blocked roots (the
// distro list, a bare distro root) are not handoff targets: the caller shows
// the "pick a folder inside" alert instead.
func wslHandoffTarget(root string) (distro, linuxPath string, ok bool) {
	if wslHandoffGOOS != "windows" {
		return "", "", false
	}
	if !wslHandoffAvailable() || !wslHandoffEnabled() {
		return "", "", false
	}
	trimmed := strings.TrimSpace(root)
	if trimmed == "" || blockedProjectRoot(trimmed) {
		return "", "", false
	}
	distro, linuxPath, isWSL := wsl.ParseWSLRoot(trimmed)
	if !isWSL {
		return "", "", false
	}
	return distro, linuxPath, true
}

// tryHandoffToWSL spawns the Linux companion inside WSL for root and closes
// this Windows window, mirroring the startup handoff. It reports handled
// true when root was a WSL pick (spawned or failed with an alert), so the
// caller must not fall through to switchProject. A nil page (tests) skips
// window work but still reports handled when the target matches.
func (a *App) tryHandoffToWSL(root string) bool {
	distro, linuxPath, ok := wslHandoffTarget(root)
	if !ok {
		return false
	}
	if err := wslHandoffSpawn(distro, linuxPath); err != nil {
		a.fileAlert = "Could not open in WSL: " + err.Error()
		if a.page != nil {
			a.refresh()
		}
		return true
	}
	a.browseOpen = false
	a.fileMenu = false
	a.projectOpen = false
	a.recentOpen = false
	if a.page != nil {
		a.refresh()
		a.page.Window().Close()
	}
	_ = wslHandoffClose
	return true
}
