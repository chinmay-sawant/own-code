package app

import "strconv"

// Desktop present cap without VSync.
//
// OwnFrame paces heavy presents to MaxFPS with a monotonic sleep; Updates
// run hotter at cap+20 TPS headroom, so the FE shows the cap while the app
// ticks faster (60 shows, 80 runs). Idle parking is untouched. The default
// is uncapped; 60/120/144/240 are validated choices, -1 is explicit
// uncapped for comparison.
const (
	// DefaultMaxFPS is the suggested present cap (Settings/CLI choice).
	// The runtime default is uncapped (0).
	DefaultMaxFPS = 60
	// FPSUncapped is the explicit uncapped sentinel for settings and CLI.
	FPSUncapped = -1
)

// FPSOptions are the validated user choices shown in Settings.
func FPSOptions() []int { return []int{60, 120, 144, 240} }

// IsValidFPS reports whether v is a selectable cap (uncapped excluded).
func IsValidFPS(v int) bool {
	for _, o := range FPSOptions() {
		if v == o {
			return true
		}
	}
	return false
}

// NormalizeFPS maps a settings/CLI value to its effective form: 0 missing
// and FPSUncapped both mean uncapped, valid caps are kept, anything else
// falls back to uncapped.
func NormalizeFPS(v int) int {
	if v == 0 {
		return 0
	}
	if v == FPSUncapped {
		return FPSUncapped
	}
	if IsValidFPS(v) {
		return v
	}
	// OwnFrame accepts 30..1000 generally, but OwnCode validates to the
	// listed choices; anything else is a hand-edit fallback to uncapped.
	return 0
}

// EffectiveFPS resolves CLI > settings > default: 0 and -1 both mean
// uncapped (the default).
func EffectiveFPS(cliSet bool, cli int, settings int) int {
	if cliSet {
		return NormalizeFPS(cli)
	}
	return NormalizeFPS(settings)
}

// WindowMaxFPS maps an effective FPS (0/missing and -1 both uncapped, else
// cap) to the ownframe.WindowOptions.MaxFPS value: 0 is uncapped, -1 is
// explicit uncapped, positive caps pass through.
func WindowMaxFPS(effective int) int {
	if effective == 0 {
		return 0
	}
	if effective == FPSUncapped {
		return -1
	}
	return effective
}

// FPSLabel renders an effective FPS for the Settings panel.
func FPSLabel(effective int) string {
	if effective == FPSUncapped || effective == 0 {
		if effective == 0 {
			return "Uncapped (default)"
		}
		return "Uncapped"
	}
	return strconv.Itoa(effective) + " FPS"
}

// fpsOptionRow is one frame-rate button in Settings → Appearance.
type fpsOptionRow struct {
	Value  int
	Label  string
	Active bool
}

// fpsRows builds the Settings rows for the effective FPS.
func fpsRows(effective int) []fpsOptionRow {
	vals := append(append([]int(nil), FPSOptions()...), FPSUncapped)
	rows := make([]fpsOptionRow, 0, len(vals))
	for _, v := range vals {
		label := strconv.Itoa(v) + " FPS"
		if v == FPSUncapped {
			label = "Uncapped"
		}
		active := effective == v
		if effective == 0 && v == FPSUncapped {
			active = true
		}
		rows = append(rows, fpsOptionRow{Value: v, Label: label, Active: active})
	}
	return rows
}

// EffectiveMaxFPS resolves CLI > settings > default (0 and -1 both uncapped).
func (a *App) EffectiveMaxFPS() int {
	return EffectiveFPS(a.fpsCLISet, a.fpsCLI, a.settings.MaxFPS)
}

// WindowMaxFPSValue maps the effective FPS to ownframe.WindowOptions.MaxFPS.
func (a *App) WindowMaxFPSValue() int {
	return WindowMaxFPS(a.EffectiveMaxFPS())
}

// SetFPSCLI installs the --fps override (CLI wins over settings).
func (a *App) SetFPSCLI(v int, set bool) {
	if !set {
		a.fpsCLISet = false
		return
	}
	a.fpsCLI = NormalizeFPS(v)
	a.fpsCLISet = true
}

// SetMaxFPS stores one Settings frame-rate choice (restart to apply).
func (a *App) SetMaxFPS(v int) {
	a.settings.MaxFPS = NormalizeFPS(v)
	a.settingsNote = "Frame rate applies on restart."
	a.persistSettings()
	a.refresh()
}
