package app

//go:generate go run github.com/chinmay-sawant/ownframe/cmd/ownframegen -bind View -bind-dir . -pkg app -func RenderAppBody -out view_render_gen.go -in ../../components/html/titlebar.html,../../components/html/activity.html,../../components/html/sidebar.html,../../components/html/editor.html,../../components/html/details.html,../../components/html/statusbar.html,../../components/html/file_nav.html,../../components/html/commit_tip.html,../../components/html/inline_card.html,../../components/html/palette.html,../../components/html/settings.html,../../components/html/context_menu.html,../../components/html/confirm.html,../../components/html/popup.html,../../components/html/commit_dialog.html,../../components/html/file_menu.html,../../components/html/terminal.html,../../components/html/problems.html,../../components/html/opencode_panel.html

import (
	"bytes"
	"html"
	"io"
	"strconv"
	"strings"
	"sync"
)

// shellHead opens the document and starts the shared stylesheet.
const shellHead = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Code Review</title><style>`

// shellBody opens the app shell after the styles.
const shellBody = `</style></head><body class="mode-{{.Mode}}{{if .Details}} with-details{{end}}{{if or .Terminal.Open .LSP.Open}} term-open{{end}}" style="--term-h:{{.Terminal.Height}}px;--agent-w:{{.OpenCodePanel.DockWidth}}px"><div class="app" id="app-shell">`

var (
	shellHeadCSSOnce sync.Once
	shellHeadCSSVal  string
)

func cachedShellHeadCSS() string {
	shellHeadCSSOnce.Do(func() {
		var b strings.Builder
		b.WriteString(shellHead)
		for _, name := range []string{
			"base.css", "window-control.css", "sidebar.css",
			"settings.css", "editor.css", "diff.css", "history.css", "scrollrails.css", "details.css", "palette.css", "preview.css", "file_nav.css", "inline_card.css", "popup.css", "merge.css", "terminal.css", "opencode_panel.css", "motion.css",
		} {
			b.WriteString(file("components/css/" + name))
		}
		shellHeadCSSVal = b.String()
	})
	return shellHeadCSSVal
}

// renderAppPage implements ownframe.Config.Render using the ownframegen-compiled
// zero-reflection RenderAppBodyBuf across all 19 HTML component templates.
func renderAppPage(w io.Writer, data any) error {
	var src *View
	switch d := data.(type) {
	case *View:
		src = d
	case View:
		src = &d
	}
	if src == nil {
		return nil
	}
	if buf, ok := w.(*bytes.Buffer); ok {
		writeAppPageBuf(buf, src)
		return nil
	}
	var buf bytes.Buffer
	writeAppPageBuf(&buf, src)
	_, err := w.Write(buf.Bytes())
	return err
}

func writeAppPageBuf(b *bytes.Buffer, v *View) {
	headCSS := cachedShellHeadCSS()
	b.Grow(len(headCSS) + 16384)
	b.WriteString(headCSS)
	var numBuf [32]byte
	b.WriteString(`</style></head><body class="mode-`)
	b.WriteString(html.EscapeString(v.Mode))
	if v.Details {
		b.WriteString(` with-details`)
	}
	if v.Terminal.Open || v.LSP.Open {
		b.WriteString(` term-open`)
	}
	b.WriteString(`" style="--term-h:`)
	b.Write(strconv.AppendInt(numBuf[:0], int64(v.Terminal.Height), 10))
	b.WriteString(`px;--agent-w:`)
	b.Write(strconv.AppendInt(numBuf[:0], int64(v.OpenCodePanel.DockWidth), 10))
	b.WriteString(`px"><div class="app" id="app-shell">`)
	RenderAppBodyBuf(b, v)
	b.WriteString(`</div></body></html>`)
}

// buildHTML assembles the code view from its component fragments.
func buildHTML() string {
	var b strings.Builder

	b.WriteString(shellHead)

	for _, name := range []string{
		"base.css", "window-control.css", "sidebar.css",
		"settings.css", "editor.css", "diff.css", "history.css", "scrollrails.css", "details.css", "palette.css", "preview.css", "file_nav.css", "inline_card.css", "popup.css", "merge.css", "terminal.css", "opencode_panel.css", "motion.css",
	} {
		b.WriteString(file("components/css/" + name))
	}

	b.WriteString(shellBody)
	b.WriteString(file("components/html/titlebar.html"))
	b.WriteString(file("components/html/activity.html"))
	b.WriteString(file("components/html/sidebar.html"))
	b.WriteString(file("components/html/editor.html"))
	b.WriteString(file("components/html/details.html"))
	b.WriteString(file("components/html/statusbar.html"))
	b.WriteString(file("components/html/file_nav.html"))
	b.WriteString(file("components/html/commit_tip.html"))
	b.WriteString(file("components/html/inline_card.html"))
	b.WriteString(file("components/html/palette.html"))
	b.WriteString(file("components/html/settings.html"))
	b.WriteString(file("components/html/context_menu.html"))
	b.WriteString(file("components/html/confirm.html"))
	b.WriteString(file("components/html/popup.html"))
	b.WriteString(file("components/html/commit_dialog.html"))
	b.WriteString(file("components/html/file_menu.html"))
	b.WriteString(file("components/html/terminal.html"))
	b.WriteString(file("components/html/problems.html"))
	b.WriteString(file("components/html/opencode_panel.html"))
	b.WriteString(`</div></body></html>`)

	return b.String()
}
