package app

import (
	"context"
	"os/exec"
	"strings"
)

// lspServerRow is one row of Settings, Language servers.
type lspServerRow struct {
	Name      string
	Langs     string
	Command   string
	Enabled   bool
	Status    string
	StatusCls string
	Hint      string
}

// lspSettingsRows lists the built-in and configured servers with their
// enabled switch and live status.
func (a *App) lspSettingsRows() []lspServerRow {
	reg := lspRegistry(a.settings.LSP)
	master := lspEnabled(a.settings.LSP)

	running := map[string]bool{}
	failed := map[string]bool{}

	if m := a.lspm; m != nil {
		for _, srv := range m.servers {
			running[srv.cfg.Name] = true
		}

		for name, bad := range m.failed {
			failed[name] = bad
		}
	}

	var rows []lspServerRow

	for _, cfg := range reg.Servers() {
		row := lspServerRow{
			Name:    cfg.Name,
			Langs:   strings.Join(cfg.LanguageIDs, ", "),
			Command: cfg.Command,
			Enabled: !cfg.Disabled,
		}

		switch {
		case !master:
			row.Status, row.StatusCls = "Off (all language servers disabled)", "off"
		case cfg.Disabled:
			row.Status, row.StatusCls = "Disabled", "off"
		case failed[cfg.Name]:
			row.Status, row.StatusCls = "Failed to start", "bad"
			row.Hint = lspInstallHint(cfg.Name)
		case running[cfg.Name]:
			row.Status, row.StatusCls = "Running", "ok"
		default:
			if _, err := exec.LookPath(cfg.Command); err != nil {
				row.Status, row.StatusCls = "Not installed", "bad"
				row.Hint = lspInstallHint(cfg.Name)
			} else {
				row.Status, row.StatusCls = "Ready, starts on first matching file", "idle"
			}
		}

		rows = append(rows, row)
	}

	return rows
}

// cloneLSPSettings copies the block so replacing it is visible to the
// manager's settings comparison (a mutated shared pointer would compare
// equal to itself and never reconfigure).
func cloneLSPSettings(s *lspSettings) *lspSettings {
	out := &lspSettings{}
	if s == nil {
		return out
	}

	out.Enabled = s.Enabled
	if s.Enabled != nil {
		v := *s.Enabled
		out.Enabled = &v
	}

	if s.Servers != nil {
		out.Servers = map[string]lspServerSettings{}
		for k, v := range s.Servers {
			out.Servers[k] = v
		}
	}

	return out
}

// setLSPEnabled flips the master switch and saves settings.
func (a *App) setLSPEnabled(on bool) {
	next := cloneLSPSettings(a.settings.LSP)
	next.Enabled = &on
	a.settings.LSP = next
	a.persistSettings()
}

// setLSPServerDisabled flips one server by registry name and saves.
func (a *App) setLSPServerDisabled(name string, disabled bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}

	known := false

	for _, cfg := range lspRegistry(a.settings.LSP).Servers() {
		if cfg.Name == name {
			known = true

			break
		}
	}

	if !known {
		return
	}

	next := cloneLSPSettings(a.settings.LSP)
	if next.Servers == nil {
		next.Servers = map[string]lspServerSettings{}
	}

	over := next.Servers[name]
	over.Disabled = disabled
	next.Servers[name] = over

	a.settings.LSP = next.sanitize()
	a.persistSettings()
}

// lspSettingsAction handles the Settings, Language servers controls.
func (a *App) lspSettingsAction(ctx context.Context, action string) (bool, error) {
	switch {
	case action == "lsp-master-toggle":
		a.setLSPEnabled(!lspEnabled(a.settings.LSP))
	case strings.HasPrefix(action, "lsp-server-toggle:"):
		name := strings.TrimPrefix(action, "lsp-server-toggle:")

		disabled := false

		for _, row := range a.lspSettingsRows() {
			if row.Name == name {
				disabled = row.Enabled

				break
			}
		}

		a.setLSPServerDisabled(name, disabled)
	default:
		return false, nil
	}

	return true, a.redrawSettings(ctx)
}
