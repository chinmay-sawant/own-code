package app

import "strings"

// shellHead opens the document and starts the shared stylesheet.
const shellHead = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Code Review</title><style>`

// shellBody opens the app shell after the styles.
const shellBody = `</style></head><body class="mode-{{.Mode}}{{if .Details}} with-details{{end}}"><div class="app" id="app-shell">`

// buildHTML assembles the code view from its component fragments.
func buildHTML() string {
	var b strings.Builder

	b.WriteString(shellHead)

	for _, name := range []string{
		"base.css", "window-control.css", "sidebar.css",
		"settings.css", "editor.css", "diff.css", "history.css", "scrollrails.css", "details.css", "palette.css", "preview.css", "file_nav.css", "inline_card.css", "motion.css",
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
	b.WriteString(file("components/html/file_menu.html"))
	b.WriteString(`</div></body></html>`)

	return b.String()
}
