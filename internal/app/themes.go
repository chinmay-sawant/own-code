package app

import (
	"fmt"
	"strings"
)

type theme struct {
	ID, Name, Description                                                string
	Background, Panel, Panel2, Panel3, Line, Text, Muted, Dim, Accent    string
	Keyword, Type, Function, String, Number, Comment, Operator, Variable string
	Selection                                                            string
}

func themeCatalog() []theme {
	return []theme{
		{"midnight", "Midnight", "The original Code Review dark theme", "#0e111a", "#0c0e14", "#10131b", "#151924", "#262d3a", "#d7dce8", "#a0aabc", "#788397", "#7aa2f7", "#c792ea", "#ffcb6b", "#82aaff", "#c3e88d", "#f78c6c", "#8490a5", "#89ddff", "#f07178", "#334c79"},
		{"dracula", "Dracula Night", "Purple and pink on charcoal", "#282a36", "#21222c", "#282a36", "#343746", "#454858", "#f8f8f2", "#c1c5d4", "#9aa3bd", "#bd93f9", "#ff79c6", "#8be9fd", "#50fa7b", "#f1fa8c", "#bd93f9", "#9ba6c3", "#ff79c6", "#ffb86c", "#555b78"},
		{"light", "Light", "A clean, familiar light editor", "#ffffff", "#f3f3f3", "#eaeaea", "#dedede", "#c5c5c5", "#242424", "#505050", "#626262", "#005fb8", "#af00db", "#267f99", "#795e26", "#a31515", "#098658", "#477a48", "#333333", "#001080", "#add6ff"},
		{"solarized", "Solarized Light", "Warm paper with restrained color", "#fdf6e3", "#eee8d5", "#f5efdd", "#e5dec9", "#c8c0ab", "#344f56", "#526b70", "#637a7c", "#006da3", "#6c3fa3", "#9a6a00", "#006da3", "#557b00", "#a33a22", "#647b73", "#007f82", "#b22b54", "#c6dce0"},
		{"nord", "Nord", "Cool slate and muted frost", "#242933", "#2e3440", "#343c4a", "#3b4555", "#4c566a", "#eceff4", "#c0c9d8", "#9caac0", "#88c0d0", "#b48ead", "#8fbcbb", "#88c0d0", "#a3be8c", "#d08770", "#a2aec2", "#81a1c1", "#bf8b8b", "#465d75"},
		{"contrast", "High Contrast", "Bright text and strong boundaries", "#000000", "#101010", "#171717", "#252525", "#858585", "#ffffff", "#eeeeee", "#bdbdbd", "#75caff", "#ff9dff", "#ffdf75", "#75caff", "#a5f79a", "#ffba86", "#bdbdbd", "#a6eaff", "#ff9a9a", "#24598a"},
		{"glass", "Liquid Glass", "Layered slate chrome with cool glass highlights", "#0b111b", "#101824", "#172230", "#202d3d", "rgba(230,241,255,0.16)", "#eff4fb", "#b7c3d3", "#8794a7", "#76baff", "#c4a2ff", "#78c7f0", "#70d5a6", "#f0a57b", "#f1ca75", "#95a2b4", "#9ab6ff", "#e2ecfb", "rgba(118,186,255,0.22)"},
		{"glass-light", "Liquid Glass Light", "Clear chrome over a cool neutral canvas", "#f2f5f9", "#ffffff", "#eef2f7", "#e5ebf3", "rgba(31,55,86,0.14)", "#1c2737", "#536276", "#7c8ba0", "#0a84ff", "#a34479", "#1267b2", "#167346", "#96501e", "#805a13", "#77869a", "#365f9a", "#17263b", "rgba(10,132,255,0.18)"},
	}
}

type themeRow struct {
	ID, Name, Description string
	Active                bool
}

func validTheme(id string) string {
	for _, t := range themeCatalog() {
		if t.ID == id {
			return id
		}
	}
	return "midnight"
}

func (a *App) themeRows() []themeRow {
	rows := make([]themeRow, 0, len(themeCatalog()))
	for _, t := range themeCatalog() {
		rows = append(rows, themeRow{t.ID, t.Name, t.Description, t.ID == validTheme(a.theme)})
	}
	return rows
}

func themeCSS(id string) string {
	id = validTheme(id)
	for _, t := range themeCatalog() {
		if t.ID != id {
			continue
		}
		semantic := "--green:#3ddc84; --red:#f0606a; --amber:#f0b75a; --purple:#c792ea;"
		if id == "light" || id == "solarized" {
			semantic = "--green:#176b35; --red:#b32930; --amber:#865400; --purple:#733d96;"
		}
		if id == "nord" {
			semantic = `--green:#a3be8c; --red:#e48a91; --amber:#ebcb8b; --purple:#b48ead;
			--accent-soft:rgba(136,192,208,0.14); --green-bg:rgba(163,190,140,0.10);
			--green-word:rgba(163,190,140,0.27); --green-active:rgba(163,190,140,0.18);
			--green-gutter:rgba(163,190,140,0.08); --red-bg:rgba(191,97,106,0.10);
			--red-word:rgba(191,97,106,0.27); --red-active:rgba(191,97,106,0.18);
			--red-gutter:rgba(191,97,106,0.08); --amber-soft:rgba(235,203,139,0.12);`
		}
		if id == "glass" {
			semantic = `--green:#70d5a6; --red:#ef7d89; --amber:#e8bd69; --purple:#c4a2ff;
			--accent-soft:rgba(118,186,255,0.14); --green-bg:rgba(112,213,166,0.10);
			--green-word:rgba(112,213,166,0.25); --green-active:rgba(112,213,166,0.17);
			--green-gutter:rgba(112,213,166,0.08); --red-bg:rgba(239,125,137,0.10);
			--red-word:rgba(239,125,137,0.25); --red-active:rgba(239,125,137,0.17);
			--red-gutter:rgba(239,125,137,0.08); --amber-soft:rgba(232,189,105,0.12);`
		}
		if id == "glass-light" {
			semantic = `--green:#167346; --red:#a83445; --amber:#805a13; --purple:#8a4c98;
			--accent-soft:rgba(10,132,255,0.12); --green-bg:rgba(22,115,70,0.08);
			--green-word:rgba(22,115,70,0.20); --green-active:rgba(22,115,70,0.14);
			--green-gutter:rgba(22,115,70,0.07); --red-bg:rgba(168,52,69,0.08);
			--red-word:rgba(168,52,69,0.20); --red-active:rgba(168,52,69,0.14);
			--red-gutter:rgba(168,52,69,0.07); --amber-soft:rgba(128,90,19,0.10);`
		}
		icons := ""
		if id == "light" || id == "solarized" || id == "glass-light" {
			icons = `
.commit-tip-float, .inline-card { box-shadow:none !important; }
.lang-go { background:#d5eef2 !important; color:#075c70 !important; }
.lang-python { background:#f5e9c8 !important; color:#715300 !important; }
.lang-javascript { background:#f5edc3 !important; color:#675500 !important; }
.lang-markdown { background:#dce8f8 !important; color:#24578f !important; }
.lang-css { background:#eae0f7 !important; color:#663994 !important; }
.lang-json { background:#f7e7ce !important; color:#795000 !important; }
.lang-html { background:#fae0d9 !important; color:#9b3c25 !important; }
.lang-yaml { background:#d8efe8 !important; color:#146451 !important; }
.lang-shell { background:#e5eed9 !important; color:#466420 !important; }
.lang-plain { background:#e4e7eb !important; color:#485464 !important; }`
		}
		glass := ""
		if id == "glass" || id == "glass-light" {
			glass = glassCSS(id)
		}
		return fmt.Sprintf(`:root { %s --bg:%s !important; --panel:%s !important; --panel-2:%s !important; --panel-3:%s !important; --line:%s !important; --line-soft:%s !important; --text:%s !important; --muted:%s !important; --dim:%s !important; --icon:%s !important; --icon-strong:%s !important; --accent:%s !important; --syntax-keyword:%s !important; --syntax-type:%s !important; --syntax-function:%s !important; --syntax-string:%s !important; --syntax-number:%s !important; --syntax-comment:%s !important; --syntax-operator:%s !important; --syntax-variable:%s !important; --selection:%s !important; }`, semantic, t.Background, t.Panel, t.Panel2, t.Panel3, t.Line, t.Line, t.Text, t.Muted, t.Dim, t.Muted, t.Muted, t.Accent, t.Keyword, t.Type, t.Function, t.String, t.Number, t.Comment, t.Operator, t.Variable, t.Selection) + icons + glass + termCSS(id)
	}
	return ""
}

// glassCSS uses OwnFrame's backdrop blur and edge refraction for glass surfaces.
func glassCSS(id string) string {
	if id == "glass" {
		return `
.palette, .settings-panel, .confirm-panel, .inline-card, .commit-tip-float, .recent-flyout { background-color:rgba(18,27,42,0.15) !important; background-image:linear-gradient(180deg, rgba(255,255,255,0.13), rgba(255,255,255,0.018) 42%, rgba(118,186,255,0.03)) !important; backdrop-filter:blur(34px) saturate(1.35) refract(8px) !important; border-color:rgba(225,239,255,0.24) !important; border-radius:16px !important; }
.palette-back, .confirm-backdrop { background-color:rgba(3,8,16,0.26) !important; }
#settings-layer .palette-back { background-color:rgba(3,8,16,0.02) !important; backdrop-filter:blur(6px) saturate(1) !important; }
.sidebar, .tabstrip { background-color:rgba(23,32,47,0.64) !important; background-image:linear-gradient(180deg, rgba(255,255,255,0.19), rgba(255,255,255,0.025)) !important; border-color:rgba(225,239,255,0.25) !important; backdrop-filter:blur(16px) saturate(1.3) refract(4px) !important; }
.sidebar .side-hsplit { background-color:rgba(23,32,47,0.72) !important; border-color:transparent !important; backdrop-filter:none !important; }
.sidebar .side-hsplit .hsplit-grip { color:transparent !important; }
.sidebar .side-hsplit:hover .hsplit-grip, .sidebar .side-hsplit:focus-visible .hsplit-grip { color:rgba(118,186,255,0.8) !important; }
.titlebar, .statusbar { background-color:rgba(17,24,38,0.70) !important; background-image:linear-gradient(180deg, rgba(255,255,255,0.20), rgba(255,255,255,0.025)) !important; border-color:rgba(225,239,255,0.24) !important; backdrop-filter:blur(12px) saturate(1.25) refract(2.5px) !important; }
.settings-btn, .tab, .palette-row { background-image:linear-gradient(180deg, rgba(255,255,255,0.075), rgba(255,255,255,0.01)) !important; }
.settings-header { background-color:rgba(255,255,255,0.02) !important; background-image:linear-gradient(108deg, rgba(255,255,255,0.07), rgba(255,255,255,0.01) 48%, rgba(118,186,255,0.035)) !important; }
.settings-nav { background-color:rgba(14,23,37,0.20) !important; background-image:linear-gradient(148deg, rgba(255,255,255,0.04), rgba(255,255,255,0.008) 58%, rgba(118,186,255,0.025)) !important; border-right-color:rgba(225,239,255,0.16) !important; }
.settings-content { background-color:rgba(16,25,38,0.045) !important; background-image:linear-gradient(128deg, rgba(255,255,255,0.025), rgba(255,255,255,0) 34%, rgba(255,255,255,0) 70%, rgba(118,186,255,0.05)) !important; }
.settings-nav-item.settings-nav-active { background-color:rgba(118,186,255,0.12) !important; background-image:linear-gradient(115deg, rgba(255,255,255,0.08), rgba(118,186,255,0.06)) !important; border-color:rgba(190,220,255,0.24) !important; }
.settings-theme { background-color:rgba(25,36,51,0.06) !important; background-image:linear-gradient(118deg, rgba(255,255,255,0.07), rgba(255,255,255,0.025) 55%, rgba(118,186,255,0.06)) !important; border-color:rgba(225,239,255,0.14) !important; border-radius:12px !important; }
.settings-theme.selected { background-color:rgba(38,61,85,0.14) !important; background-image:linear-gradient(118deg, rgba(255,255,255,0.12), rgba(255,255,255,0.035) 48%, rgba(118,186,255,0.12)) !important; border-color:rgba(118,186,255,0.38) !important; }
.settings-key, .settings-perview, .settings-area { background-color:rgba(25,36,51,0.09) !important; background-image:linear-gradient(125deg, rgba(255,255,255,0.04), rgba(255,255,255,0.01) 54%, rgba(118,186,255,0.02)) !important; border-color:rgba(225,239,255,0.15) !important; }
.settings-btn:not(.primary) { background-color:rgba(25,36,51,0.09) !important; background-image:linear-gradient(145deg, rgba(255,255,255,0.06), rgba(255,255,255,0.01) 62%, rgba(118,186,255,0.025)) !important; border-color:rgba(225,239,255,0.16) !important; }
.settings-toggle:hover, .settings-toggle:focus-visible { background-color:rgba(118,186,255,0.08) !important; background-image:linear-gradient(115deg, rgba(255,255,255,0.06), rgba(118,186,255,0.04)) !important; }
.settings-theme.selected, .palette-row.on, .tab.on { border-color:rgba(118,186,255,0.54) !important; }
.settings-panel, .palette, .confirm-panel, .inline-card, .commit-tip-float, .settings-theme, .settings-key, .settings-perview, .settings-area, .settings-btn { border-top-color:rgba(240,247,255,0.22) !important; }
* { box-shadow:none !important; }
.editor.focus { border-top:2px solid rgba(118,186,255,0.42) !important; }
.term-body.focus { outline:1px solid rgba(118,186,255,0.46) !important; outline-offset:-1px; }
.merge-actions.current { border-left:2px solid var(--red) !important; }`
	}
	return `
.palette, .settings-panel, .confirm-panel, .inline-card, .commit-tip-float, .recent-flyout { background-color:rgba(245,248,252,0.17) !important; background-image:linear-gradient(180deg, rgba(255,255,255,0.19), rgba(255,255,255,0.045) 42%, rgba(10,132,255,0.018)) !important; backdrop-filter:blur(34px) saturate(1.2) refract(8px) !important; border-color:rgba(31,55,86,0.18) !important; border-radius:16px !important; }
.palette-back, .confirm-backdrop { background-color:rgba(28,43,64,0.16) !important; }
#settings-layer .palette-back { background-color:rgba(28,43,64,0.015) !important; backdrop-filter:blur(6px) saturate(1) !important; }
.sidebar, .tabstrip { background-color:rgba(239,244,250,0.72) !important; background-image:linear-gradient(180deg, rgba(255,255,255,0.76), rgba(255,255,255,0.16)) !important; border-color:rgba(31,55,86,0.24) !important; backdrop-filter:blur(16px) saturate(1.25) refract(4px) !important; }
.sidebar .side-hsplit { background-color:rgba(239,244,250,0.78) !important; border-color:transparent !important; backdrop-filter:none !important; }
.sidebar .side-hsplit .hsplit-grip { color:transparent !important; }
.sidebar .side-hsplit:hover .hsplit-grip, .sidebar .side-hsplit:focus-visible .hsplit-grip { color:rgba(10,132,255,0.8) !important; }
.titlebar, .statusbar { background-color:rgba(244,247,251,0.82) !important; background-image:linear-gradient(180deg, rgba(255,255,255,0.88), rgba(255,255,255,0.18)) !important; border-color:rgba(31,55,86,0.22) !important; backdrop-filter:blur(12px) saturate(1.2) refract(2.5px) !important; }
.settings-btn, .tab, .palette-row { background-image:linear-gradient(180deg, rgba(255,255,255,0.62), rgba(255,255,255,0.06)) !important; }
.settings-header { background-color:rgba(255,255,255,0.025) !important; background-image:linear-gradient(108deg, rgba(255,255,255,0.23), rgba(255,255,255,0.055) 48%, rgba(10,132,255,0.035)) !important; }
.settings-nav { background-color:rgba(239,244,250,0.14) !important; background-image:linear-gradient(148deg, rgba(255,255,255,0.18), rgba(255,255,255,0.035) 58%, rgba(10,132,255,0.025)) !important; border-right-color:rgba(31,55,86,0.13) !important; }
.settings-content { background-color:rgba(245,248,252,0.045) !important; background-image:linear-gradient(128deg, rgba(255,255,255,0.075), rgba(255,255,255,0) 34%, rgba(255,255,255,0) 70%, rgba(10,132,255,0.06)) !important; }
.settings-nav-item.settings-nav-active { background-color:rgba(10,132,255,0.09) !important; background-image:linear-gradient(115deg, rgba(255,255,255,0.24), rgba(10,132,255,0.055)) !important; border-color:rgba(31,93,160,0.18) !important; }
.settings-theme { background-color:rgba(251,252,254,0.07) !important; background-image:linear-gradient(118deg, rgba(255,255,255,0.18), rgba(255,255,255,0.055) 55%, rgba(10,132,255,0.05)) !important; border-color:rgba(31,55,86,0.13) !important; border-radius:12px !important; }
.settings-theme.selected { background-color:rgba(232,242,255,0.16) !important; background-image:linear-gradient(118deg, rgba(255,255,255,0.27), rgba(255,255,255,0.11) 48%, rgba(10,132,255,0.11)) !important; border-color:rgba(10,132,255,0.36) !important; }
.settings-key, .settings-perview, .settings-area { background-color:rgba(245,248,252,0.09) !important; background-image:linear-gradient(125deg, rgba(255,255,255,0.15), rgba(255,255,255,0.035) 54%, rgba(10,132,255,0.025)) !important; border-color:rgba(31,55,86,0.15) !important; }
.settings-btn:not(.primary) { background-color:rgba(245,248,252,0.08) !important; background-image:linear-gradient(145deg, rgba(255,255,255,0.18), rgba(255,255,255,0.045) 62%, rgba(10,132,255,0.025)) !important; border-color:rgba(31,55,86,0.15) !important; }
.settings-toggle:hover, .settings-toggle:focus-visible { background-color:rgba(10,132,255,0.06) !important; background-image:linear-gradient(115deg, rgba(255,255,255,0.20), rgba(10,132,255,0.045)) !important; }
.settings-theme.selected, .palette-row.on, .tab.on { border-color:rgba(10,132,255,0.52) !important; }
.settings-panel, .palette, .confirm-panel, .inline-card, .commit-tip-float, .settings-theme, .settings-key, .settings-perview, .settings-area, .settings-btn { border-top-color:rgba(255,255,255,0.52) !important; }
* { box-shadow:none !important; }
.editor.focus { border-top:2px solid rgba(10,132,255,0.36) !important; }
.term-body.focus { outline:1px solid rgba(10,132,255,0.44) !important; outline-offset:-1px; }
.merge-actions.current { border-left:2px solid var(--red) !important; }`
}

// termPalette is the terminal background plus the 16 ANSI colors for one
// app theme. Dark themes keep a dark terminal body; light themes use a
// light body with darkened ANSI colors so output stays readable.
type termPalette struct {
	bg     string
	colors [16]string
}

func termPaletteFor(id string) termPalette {
	switch id {
	case "dracula":
		return termPalette{"#282a36", [16]string{
			"#21222c", "#ff5555", "#50fa7b", "#f1fa8c",
			"#bd93f9", "#ff79c6", "#8be9fd", "#f8f8f2",
			"#6272a4", "#ff6e6e", "#69ff94", "#ffffa5",
			"#d6acff", "#ff92df", "#a4ffff", "#ffffff",
		}}
	case "light":
		return termPalette{"#f4f4f4", [16]string{
			"#1e1e1e", "#c50f1f", "#117743", "#7c4a03",
			"#005fb8", "#af00db", "#267f99", "#595959",
			"#717171", "#e51400", "#098658", "#9d6b00",
			"#0067c0", "#c000df", "#038387", "#1e1e1e",
		}}
	case "solarized":
		return termPalette{"#fdf6e3", [16]string{
			"#073642", "#dc322f", "#859900", "#b58900",
			"#268bd2", "#d33682", "#2aa198", "#657b83",
			"#839496", "#cb4b16", "#586e75", "#657b83",
			"#839496", "#6c71c4", "#93a1a1", "#002b36",
		}}
	case "nord":
		return termPalette{"#2e3440", [16]string{
			"#3b4252", "#bf616a", "#a3be8c", "#ebcb8b",
			"#81a1c1", "#b48ead", "#88c0d0", "#e5e9f0",
			"#4c566a", "#bf616a", "#a3be8c", "#ebcb8b",
			"#81a1c1", "#b48ead", "#8fbcbb", "#eceff4",
		}}
	case "contrast":
		return termPalette{"#000000", [16]string{
			"#000000", "#ff9a9a", "#a5f79a", "#ffdf75",
			"#75caff", "#ff9dff", "#a6eaff", "#ffffff",
			"#858585", "#ffb3b3", "#c4ffb8", "#fff3b3",
			"#a8dcff", "#ffc2ff", "#cdf4ff", "#ffffff",
		}}
	case "glass":
		return termPalette{"#0a1226", [16]string{
			"#060b1a", "#ff5f57", "#7dffc4", "#ffd60a",
			"#0a84ff", "#ff7ab2", "#64d2ff", "#c7d2e4",
			"#67779a", "#ff8a7a", "#a9ffc9", "#ffe45a",
			"#7ab8ff", "#d8a6ff", "#8ae9ff", "#f2f6ff",
		}}
	case "glass-light":
		return termPalette{"#e8eef6", [16]string{
			"#0a1930", "#c8102e", "#067c3a", "#7a5200",
			"#0a84ff", "#d7006d", "#0071e3", "#4a5c7a",
			"#7a8aa6", "#e51400", "#0a9b4a", "#a66c00",
			"#3b9bff", "#e04a9b", "#33b8ff", "#0a1930",
		}}
	default:
		return termPalette{"#080a10", [16]string{
			"#1b1f27", "#f07178", "#c3e88d", "#ffcb6b",
			"#82aaff", "#c792ea", "#89ddff", "#c5cad6",
			"#4a5262", "#ff8b92", "#b9f18d", "#ffe0a3",
			"#9ab8ff", "#d7b4ff", "#b6f2ff", "#f7f9ff",
		}}
	}
}

// termCSS layers the terminal palette after the theme so it wins over the
// base defaults without touching the catalog above.
func termCSS(id string) string {
	p := termPaletteFor(id)

	var b strings.Builder
	b.WriteString(":root { --term-bg:" + p.bg + " !important;")

	for i, c := range p.colors {
		fmt.Fprintf(&b, " --term%d:%s !important;", i, c)
	}

	b.WriteString(" }")

	return b.String()
}
