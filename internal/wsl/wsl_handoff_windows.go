//go:build windows

package wsl

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// errHandoffDisabled reports a runtime handoff blocked by -wsl=false or a
// missing launcher. errHandoffNoBinary reports an untranslatable companion
// path (for example an UNC-hosted exe with no /mnt mapping).
var (
	errHandoffDisabled = errors.New("wsl handoff disabled")
	errHandoffNoBinary = errors.New("wsl companion binary not found")
)

// Available reports whether the WSL launcher is present. The folder browser
// uses it to decide whether to offer the WSL entry point on Windows.
func Available() bool {
	return wslAvailable()
}

// Enabled reports the -wsl flag: false forces native Windows behavior and
// disables both startup and runtime handoffs into WSL.
func Enabled() bool {
	return wslEnabled
}

// ListDistros returns installed distro names. It runs `wsl.exe -l -q` and
// falls back to reading the \\wsl$ share when the launcher output cannot
// be parsed, so the browser still lists distros on machines where the
// command's encoding surprises the decoder.
func ListDistros() []string {
	if !wslAvailable() {
		return nil
	}
	if launcher, err := exec.LookPath("wsl.exe"); err == nil {
		cmd := exec.Command(launcher, "-l", "-q") //nolint:gosec // launcher resolved by LookPath; fixed list flags
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if out, err := cmd.Output(); err == nil {
			if names := ParseDistroListText(DecodeWSLListOutput(out)); len(names) > 0 {
				return names
			}
		}
	}
	for _, share := range []string{WSLDistrosRoot, WSLDistrosRootAlt} {
		ents, err := os.ReadDir(share)
		if err != nil {
			continue
		}
		var out []string
		for _, ent := range ents {
			if ent.IsDir() && strings.TrimSpace(ent.Name()) != "" {
				out = append(out, ent.Name())
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

// CompanionBin resolves the Linux companion binary inside WSL: the -wsl-bin
// flag when set, otherwise this executable translated to /mnt/<drive>/....
func CompanionBin() (string, bool) {
	if wslBin != "" {
		return wslBin, true
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	if strings.HasSuffix(strings.ToLower(exe), wslExeSuffix) {
		exe = exe[:len(exe)-len(wslExeSuffix)]
	}
	bin, ok := WindowsToWSLPath(exe)
	if !ok {
		return "", false
	}
	return bin, true
}

// SpawnHandoff launches the Linux companion inside distro for linuxPath and
// returns without waiting, so the caller can close its Windows window. It
// rebuilds argv from the current launch (dropping -wsl/-wsl-bin, pointing
// -root at linuxPath) to mirror MaybeReexecInWSL without exiting itself.
func SpawnHandoff(distro, linuxPath string) error {
	if !wslEnabled || !wslAvailable() {
		return errHandoffDisabled
	}
	bin, ok := CompanionBin()
	if !ok {
		return errHandoffNoBinary
	}
	launcher, err := exec.LookPath("wsl.exe")
	if err != nil {
		return err
	}
	childArgs := append([]string{"-d", distro, "--", bin}, wslRebuildArgs(os.Args[1:], linuxPath)...)
	cmd := exec.Command(launcher, childArgs...) //nolint:gosec // launcher resolved by LookPath; distro, binary, and args are the operator's own launch
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	return cmd.Start()
}
