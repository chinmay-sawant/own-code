package app

import "context"

// Palette debug commands that exercise the popup widget.
const (
	paletteCmdPopupList = "popup-demo-list"
	paletteCmdPopupCard = "popup-demo-card"
)

// popupDemoCatalog lists the debug commands for the `>` palette.
func popupDemoCatalog() []paletteRow {
	return []paletteRow{
		{Cmd: paletteCmdPopupList, Name: "Debug: Show Completion Popup (demo)", Dir: "Show a keyboard-navigable popup list"},
		{Cmd: paletteCmdPopupCard, Name: "Debug: Show Hover Popup (demo)", Dir: "Show a read-only Markdown popup card"},
	}
}

// runPopupDemo opens a demo popup near the middle of the window.
func (a *App) runPopupDemo(ctx context.Context, id string) error {
	a.paletteOpen = false
	a.paletteQuery = ""
	a.paletteIndex = 0

	winW, winH := a.page.Size()
	spec := popupSpec{AnchorX: winW / 3, AnchorTop: winH / 2, AnchorBottom: winH/2 + 18}

	if id == paletteCmdPopupCard {
		spec.Label = "Hover details"
		spec.Content = "## func Open\n\nOpens a file and returns a `*File`.\n\n```go\nfunc Open(name string) (*File, error)\n```"
	} else {
		spec.Label = "Completions"
		for _, name := range []string{"Open", "OpenFile", "ReadFile", "ReadDir", "Remove", "RemoveAll", "Rename", "Stat", "Stdin", "Stdout"} {
			spec.Items = append(spec.Items, popupItem{Label: name, Kind: "func", Detail: "package os"})
		}

		spec.OnSelect = func(item popupItem, _ int) { a.showToast("info", "Popup", "Picked "+item.Label) }
	}

	a.showPopup(spec)

	return a.page.Redraw(ctx)
}
