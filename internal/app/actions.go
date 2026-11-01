package app

import (
	"context"
	"runtime"
	"strconv"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/wsl"
	"github.com/chinmay-sawant/ownframe"
)

// onClick routes every control. State changes end in one refresh; the page

// onClick routes every control. State changes end in one refresh; the page
// redraws after the handler returns.
func (a *App) onClick(ctx context.Context, box ownframe.Box) error {
	if a.dismissOpenMenu(box.ID, box.Action) {
		return nil
	}

	if a.confirmOpen {
		switch box.Action {
		case "confirm-ok", "confirm-cancel", "confirm-save", "confirm-dont-save":
		default:
			if box.ID != "confirm-backdrop" {
				return nil
			}
		}
	}

	if a.commitDialogOpen && !a.confirmOpen {
		switch box.Action {
		case "commit-dialog", "commit-confirm", "commit-cancel":
		default:
			if box.ID != "commit-backdrop" && box.ID != "commit-message" {
				return nil
			}
		}
	}

	action := box.Action

	// Clicking outside the panel returns the keyboard to the editor. The
	// terminal body, tabs, tools, and resize strip keep focus.
	if a.termFocus && !strings.HasPrefix(box.ID, "term") && action != "terminal-toggle" {
		a.termFocus = false
		a.refresh()
	}

	switch {
	case action == "terminal-toggle":
		a.toggleTerminal(ctx)
	case action == "term-focus":
		a.focusTerminal(ctx)
	case action == "term-new":
		a.startTerminalSession(ctx)
	case strings.HasPrefix(action, "term-pick:"):
		if id, ok := actionIndex(action, "term-pick:"); ok {
			a.pickTerminal(ctx, id)
		}
	case strings.HasPrefix(action, "term-close:"):
		if id, ok := actionIndex(action, "term-close:"); ok {
			a.closeTerminal(ctx, id)
		}
	case action == "term-copy":
		if err := a.copyTerminal(ctx); err != nil {
			return err
		}
	case action == "term-clear":
		a.clearTerminal(ctx)
	case action == "term-scroll-up":
		if err := a.scrollTerminal(ctx, 3); err != nil {
			return err
		}
	case action == "term-scroll-down":
		if err := a.scrollTerminal(ctx, -3); err != nil {
			return err
		}
	case strings.HasPrefix(action, "term-link:"):
		if r, s, ok := parseTermLinkAction(action); ok {
			if err := a.openTerminalLink(ctx, r, s); err != nil {
				return err
			}
		}
	case action == "":
		if box.ID == "filter" || box.ID == "palette" || box.ID == "find" || box.ID == "find-replace" || box.ID == "branch-filter" ||
			box.ID == "workspace-search" || box.ID == "workspace-replace" || box.ID == "editor" || box.ID == "edit-surface" || box.ID == "project-path" ||
			box.ID == "drawer-path" || box.ID == "browse-filter" || box.ID == "commit-message" {
			return nil
		}

		// A press on the card body is the start of a header drag, not a
		// dismiss: the X button and Escape still close it.
		if box.ID == "inline-card" {
			return nil
		}

		if a.inlineCard.Show {
			a.closeInlineCard()
		}

		if a.fileMenu && !strings.HasPrefix(box.ID, "file-") &&
			!strings.HasPrefix(box.ID, "project-") && !strings.HasPrefix(box.ID, "recent-") &&
			!strings.HasPrefix(box.ID, "browse-") && !strings.HasPrefix(box.ID, "quick-") {
			a.fileMenu = false
			a.projectOpen = false
			a.recentOpen = false
			a.refresh()
		}

		// Click-to-focus: a press on either pane's body focuses that side
		// without disturbing caret clicks, which carry their own actions
		// and never reach this branch.
		if a.split && a.mode == "explorer" {
			if group, ok := focusGroupForClick(box.ID); ok && group != a.focusGroup {
				a.setFocusGroup(group)
			}
		}

		return a.page.Form().Focus(ctx, "")
	case action == "inline-card-close":
		a.closeInlineCard()
	case action == "inline-card-prev":
		a.inlineCardStep(-1)
	case action == "inline-card-next":
		a.inlineCardStep(1)
	case strings.HasPrefix(action, "inline-card:"):
		if line, ok := actionIndex(action, "inline-card:"); ok {
			a.showInlineCard(line)
		}
	case strings.HasPrefix(action, "inline-card-line:"):
		if line, ok := actionIndex(action, "inline-card-line:"); ok {
			a.showInlineCard(line + 1)
		}
	case strings.HasPrefix(action, "blame-commit:"):
		a.openBlameCommit(ctx, strings.TrimPrefix(action, "blame-commit:"))
	case action == "toast-close":
		a.clearToast()
	case action == "split-toggle":
		a.toggleSplit()
	case strings.HasPrefix(action, "focus-group:"):
		if index, ok := actionIndex(action, "focus-group:"); ok {
			a.setFocusGroup(index)
		}
	case action == "edit-begin":
		a.inlineCard = inlineCard{}
		if !a.editing {
			a.toggleEdit()
		}

		return a.page.Redraw(ctx)
	case strings.HasPrefix(action, "edit-begin-line:"):
		if line, ok := actionIndex(action, "edit-begin-line:"); ok {
			// Ctrl+Click jumps to the definition under the caret. Press
			// already placed the caret from the click point (entering edit
			// mode first when the file was in token view), so the word
			// resolves exactly where the user pointed.
			if a.ctrl {
				return a.gotoDefinitionAtCaret(ctx)
			}
			// Press already placed the caret with its column; a Click that
			// follows must not reset it to the line start.
			if a.editing && a.edit != nil {
				if doc := a.groupDoc(0); doc != nil && a.edit.Path == doc.Path {
					if curLine, _ := offsetLineCol(a.edit.Text(), a.editCaret); curLine == line {
						return a.page.Redraw(ctx)
					}
				}
			}
			a.beginEditAtLine(line)
		}

		return a.page.Redraw(ctx)
	case action == "edit-toggle":
		a.toggleEdit()
	case action == "edit-save":
		a.saveEdit()
	case action == "diff-edit":
		a.editDiffFile(-1)

		return a.page.Redraw(ctx)
	case strings.HasPrefix(action, "diff-edit-line:"):
		if line, ok := actionIndex(action, "diff-edit-line:"); ok && line > 0 {
			// Press already opened the file with its column; a Click that
			// follows must not reset it to the line start.
			if a.editing && a.edit != nil && a.edit.Path == a.diffPath {
				if curLine, _ := offsetLineCol(a.edit.Text(), a.editCaret); curLine == line-1 {
					return a.page.Redraw(ctx)
				}
			}
			a.editDiffFile(line - 1)
		}

		return a.page.Redraw(ctx)
	case action == "search-open":
		return a.openWorkspaceSearch(ctx)
	case action == "file-nav-prev":
		a.fileNavStep(-1)
	case action == "file-nav-next":
		a.fileNavStep(1)
	case action == "file-nav-change-prev":
		a.fileNavChangeStep(-1)
	case action == "file-nav-change-next":
		a.fileNavChangeStep(1)
	case action == "file-nav-open":
		a.fileNavOpen()
	case action == "nav-back":
		a.goNavBack()
	case action == "nav-forward":
		a.goNavForward()
	case action == "search-close":
		return a.closeWorkspaceSearch(ctx)
	case action == "search-case":
		a.toggleSearchCase(ctx)

		return a.page.Form().Focus(ctx, "workspace-search")
	case action == "search-regex":
		a.toggleSearchRegex(ctx)

		return a.page.Form().Focus(ctx, "workspace-search")
	case action == "search-word":
		a.toggleSearchWholeWord(ctx)

		return a.page.Form().Focus(ctx, "workspace-search")
	case action == "search-replace-all":
		return a.openReplaceConfirm(ctx)
	case action == "replace-undo":
		return a.undoWorkspaceReplace(ctx)
	case action == "search-prev":
		a.searchStep(-1)
	case action == "search-next":
		a.searchStep(1)
	case strings.HasPrefix(action, "search-hit:"):
		if index, ok := actionIndex(action, "search-hit:"); ok {
			a.openSearchHit(index)
		}
	case strings.HasPrefix(action, "search-toggle-file:"):
		if index, ok := actionIndex(action, "search-toggle-file:"); ok {
			a.toggleReplaceFile(index)

			return a.page.Redraw(ctx)
		}
	case action == "mode-explorer":
		a.setMode(ctx, "explorer")
	case action == "mode-changes":
		a.setMode(ctx, "changes")
	case action == "tab-close-working":
		a.closeWorkingTree(ctx)
	case action == "settings-open":
		a.settingsOpen = true
		a.paletteOpen = false
		a.searchOpen = false
		a.branchOpen = false
		a.scanDraft, a.scanTouched = "", false
		a.searchDraft, a.searchTouched = "", false
		a.cancelKeymapPending()
		a.resetSettingsScroll()
		if err := a.redrawSettings(ctx); err != nil {
			return err
		}
		return a.page.Form().Focus(ctx, "")
	case action == "settings-close":
		a.settingsOpen = false
		a.scanDraft, a.scanTouched = "", false
		a.searchDraft, a.searchTouched = "", false
		a.cancelKeymapPending()
		a.refresh()
		return a.page.Form().Focus(ctx, "act-settings")
	case action == "settings-zoom":
		a.settingsOpen = true
		a.settingsTab = "appearance"
		a.paletteOpen = false
		a.searchOpen = false
		a.branchOpen = false
		a.resetSettingsScroll()
		if err := a.redrawSettings(ctx); err != nil {
			return err
		}
		return a.page.Form().Focus(ctx, "")
	case action == "file-menu-open":
		a.fileMenu = !a.fileMenu
		if !a.fileMenu {
			a.projectOpen = false
			a.recentOpen = false
			a.browseOpen = false
		}
		a.paletteOpen = false
		a.searchOpen = false
		a.branchOpen = false
		a.settingsOpen = false
		a.refresh()

		if a.fileMenu {
			return a.focusAfterLayout(ctx, "project-toggle")
		}

		return a.page.Form().Focus(ctx, "")
	case action == "file-menu-close":
		a.fileMenu = false
		a.projectOpen = false
		a.recentOpen = false
		a.browseOpen = false
		a.fileAlert = ""
		a.refresh()
		return a.page.Form().Focus(ctx, "")
	case action == "file-reset":
		a.resetLayout()
		return a.page.Form().Focus(ctx, "")
	case action == "file-new-window":
		a.fileMenu = false
		a.projectOpen = false
		a.browseOpen = false
		a.refresh()
		return a.openNewWindow()
	case action == "project-open":
		a.projectOpen = !a.projectOpen
		a.refresh()
		return a.focusAfterLayout(ctx, "drawer-path")
	case action == "project-close":
		a.projectOpen = false
		a.refresh()
		return a.page.Form().Focus(ctx, "")
	case action == "recent-toggle":
		a.recentOpen = !a.recentOpen
		a.refresh()
		return nil
	case action == "project-submit":
		root := strings.TrimSpace(a.projectQuery)
		if root == "" {
			root = strings.TrimSpace(a.view.ProjectQuery)
		}
		if root == "" {
			return nil
		}
		if blockedProjectRoot(root) {
			a.fileAlert = "Drive roots can't be opened as projects. Pick a folder inside it."
			a.refresh()
			return nil
		}
		if a.tryHandoffToWSL(root) {
			return nil
		}
		a.browseOpen = false
		return a.switchProject(ctx, root)
	case strings.HasPrefix(action, "project-open-path:"):
		root := strings.TrimSpace(strings.TrimPrefix(action, "project-open-path:"))
		if root == "" {
			return nil
		}
		if blockedProjectRoot(root) {
			a.fileAlert = "Drive roots can't be opened as projects. Pick a folder inside it."
			a.refresh()
			return nil
		}
		if a.tryHandoffToWSL(root) {
			return nil
		}
		a.browseOpen = false
		return a.switchProject(ctx, root)
	case action == "browse-open":
		a.fileMenu = false
		a.projectOpen = false
		a.recentOpen = false
		a.startBrowse()
		a.refresh()
		return a.focusAfterLayout(ctx, "browse-filter")
	case action == "browse-close", action == "browse-backdrop":
		a.browseOpen = false
		a.refresh()
		return nil
	case action == "browse-filter-clear":
		a.browseFilter = ""
		a.page.Form().SetFormValue("browse-filter", "")
		a.browseList.reset()
		a.refresh()
		return a.page.Form().Focus(ctx, "browse-filter")
	case action == "browse-up":
		next, ok := browseUpTarget(a.browseRoot)
		if !ok {
			return nil
		}
		a.setBrowseRoot(next)
		a.refresh()
		return nil
	case action == "browse-wsl":
		// Inside the guest the choice jumps between the Linux home and the
		// Windows drives under /mnt. On a Windows host with WSL installed
		// it jumps into the virtual distro list instead.
		if !isWSL() && runtime.GOOS == "windows" && wsl.Available() {
			a.setBrowseRoot(wsl.WSLDistrosRoot)
			a.refresh()
			return nil
		}
		root := a.browseStartRoot()
		if root == "" {
			return nil
		}
		a.setBrowseRoot(root)
		a.refresh()
		return nil
	case action == "browse-windows":
		// Inside the guest this jumps to the first Windows drive under
		// /mnt. On a Windows host it jumps back to the This PC drives
		// level, the counterpart of the WSL distro list.
		if isWSL() {
			for c := 'c'; c <= 'z'; c++ {
				candidate := "/mnt/" + string(c)
				if isDir(candidate) {
					a.setBrowseRoot(candidate)
					a.refresh()
					return nil
				}
			}
			if isDir("/mnt") {
				a.setBrowseRoot("/mnt")
				a.refresh()
				return nil
			}
			a.fileAlert = "No Windows drives found under /mnt."
			a.refresh()
			return nil
		}
		a.setBrowseRoot("")
		a.refresh()
		return nil
	case strings.HasPrefix(action, "browse-enter:"):
		path := strings.TrimSpace(strings.TrimPrefix(action, "browse-enter:"))
		if path == "" {
			return nil
		}
		// The virtual levels need no directory probe: the distro list is
		// virtual and a listed distro exists even when its UNC is not yet
		// awake (entering it starts the guest on first ReadDir).
		if wsl.IsWSLDistrosRoot(path) || wsl.IsWSLDistroRoot(path) {
			a.setBrowseRoot(path)
			a.refresh()
			return nil
		}
		if !isDir(path) {
			a.fileAlert = "Folder not found: " + path
			a.refresh()
			return nil
		}
		a.setBrowseRoot(path)
		a.refresh()
		return nil
	case action == "browse-select":
		if a.browseRoot == "" {
			return nil
		}
		if blockedProjectRoot(a.browseRoot) {
			a.fileAlert = "Drive roots can't be opened as projects. Pick a folder inside it."
			a.refresh()
			return nil
		}
		if a.tryHandoffToWSL(a.browseRoot) {
			return nil
		}
		a.browseOpen = false
		return a.switchProject(ctx, a.browseRoot)
	case action == "file-alert-close":
		a.fileAlert = ""
		a.refresh()
		return nil
	case strings.HasPrefix(action, "theme:"):
		name := validTheme(strings.TrimPrefix(action, "theme:"))
		a.theme = name
		if err := a.page.SetTheme(a.activeThemeCSS()); err != nil {
			return err
		}
		// Gestures write session + global; the session wins on restore.
		a.persistSession()
		a.persistSettings()
		a.refresh()
	case strings.HasPrefix(action, "settings-tab:"):
		a.settingsTab = strings.TrimPrefix(action, "settings-tab:")
		a.resetSettingsScroll()
		return a.redrawSettings(ctx)
	case action == "font-inc":
		return a.setFontSize(ctx, a.baseFontSize()+1)
	case action == "font-dec":
		return a.setFontSize(ctx, a.baseFontSize()-1)
	case action == "font-reset":
		return a.resetFontSize(ctx)
	case action == "zoom-inc":
		return a.zoomIn(ctx)
	case action == "zoom-dec":
		return a.zoomOut(ctx)
	case action == "editor-zoom-inc":
		return a.setZoomPerView(ctx, "editor", a.effectiveZoom("editor")*zoomStep)
	case action == "editor-zoom-dec":
		return a.setZoomPerView(ctx, "editor", a.effectiveZoom("editor")/zoomStep)
	case action == "editor-zoom-clear":
		return a.setZoomPerView(ctx, "editor", 1)
	case action == "terminal-zoom-inc":
		return a.setZoomPerView(ctx, "terminal", a.effectiveZoom("terminal")*zoomStep)
	case action == "terminal-zoom-dec":
		return a.setZoomPerView(ctx, "terminal", a.effectiveZoom("terminal")/zoomStep)
	case action == "terminal-zoom-clear":
		return a.setZoomPerView(ctx, "terminal", 1)
	case strings.HasPrefix(action, "keymap-rebind:"):
		a.keymapCapture = strings.TrimPrefix(action, "keymap-rebind:")
		a.keymapPendingAction, a.keymapPendingChord, a.keymapPendingOthers = "", "", nil
		a.settingsNote = "Press the new shortcut, or Esc to cancel."
		a.refresh()
	case action == "keymap-cancel":
		a.cancelKeymapPending()
		a.refresh()
	case action == "keymap-swap":
		a.swapKeymapPending()
		a.refresh()
	case action == "keymap-replace":
		a.replaceKeymapPending()
		a.refresh()
	case strings.HasPrefix(action, "keymap-reset:"):
		a.resetKeymapAction(strings.TrimPrefix(action, "keymap-reset:"))
		a.refresh()
	case action == "keymap-reset-all":
		a.resetKeymapAll()
		a.showToast("success", "Shortcuts reset", "All shortcuts are back to their defaults.")
		a.refresh()
	case action == "settings-rescan":
		return a.applyExcludesRescan(ctx)
	case action == "settings-toggle-gitignore":
		e := a.settings.Excludes
		e.UseGitignore = !e.UseGitignore
		a.setScanExcludes(e)
		return a.applyExcludesRescan(ctx)
	case action == "settings-toggle-searchignored":
		e := a.settings.Excludes
		e.SearchIgnored = !e.SearchIgnored
		a.setScanExcludes(e)
		a.settingsNote = ""
		a.refresh()
	case strings.HasPrefix(action, "autosave:"):
		a.setAutoSave(strings.TrimPrefix(action, "autosave:"))
	case action == "autosave-delay-dec":
		a.setAutoSaveDelay(a.autoSaveDelayMs - 250)
	case action == "autosave-delay-inc":
		a.setAutoSaveDelay(a.autoSaveDelayMs + 250)
	case strings.HasPrefix(action, "fps:"):
		raw := strings.TrimPrefix(action, "fps:")
		if raw == "-1" || raw == "uncapped" {
			a.SetMaxFPS(FPSUncapped)
			return nil
		}
		v := 0
		for _, c := range raw {
			if c < '0' || c > '9' {
				v = -99
				break
			}
			v = v*10 + int(c-'0')
		}
		a.SetMaxFPS(v)
		return nil
	case action == "palette-open":
		return a.openPalette(ctx)
	case action == "goto-definition":
		return a.gotoDefinitionAtCaret(ctx)
	case action == "win-min":
		a.page.Window().Minimize()

		return nil
	case action == "win-max":
		a.page.Window().ToggleMaximize()
		a.refresh()

		return nil
	case action == "zoom-reset":
		return a.resetZoom(ctx)
	case action == "win-close":
		a.page.Window().Close()

		return nil
	case action == "palette-close":
		return a.closePalette(ctx)
	case action == "diff-refresh":
		a.ReloadChanges(ctx, true)
	case strings.HasPrefix(action, "merge-open:"):
		a.openMergeConflict(ctx, strings.TrimPrefix(action, "merge-open:"))
	case action == "merge-close":
		a.requestCloseMergeEditor()
	case action == "merge-prev":
		a.mergeStep(-1)
	case action == "merge-next":
		a.mergeStep(1)
	case action == "merge-compare":
		a.toggleMergeCompare()
	case action == "merge-save":
		a.saveMergeEditor(ctx)
	case action == "merge-stage":
		a.stageMergeConflict(ctx)
	case action == "merge-edit":
		a.editMergeResult()
	case strings.HasPrefix(action, "merge-stage-file:"):
		a.stageConflictPath(ctx, strings.TrimPrefix(action, "merge-stage-file:"))
	case strings.HasPrefix(action, "merge-accept:"):
		rest := strings.TrimPrefix(action, "merge-accept:")
		name, indexText, ok := strings.Cut(rest, ":")
		if !ok {
			break
		}

		index, indexOK := actionIndex(indexText, "")
		choice, choiceOK := parseMergeChoice(name)
		if indexOK && choiceOK {
			a.mergeAccept(choice, index)
		}
	case action == "stage-all":
		if err := a.stageAll(ctx); err != nil {
			return err
		}
	case action == "unstage-all":
		if err := a.unstageAll(ctx); err != nil {
			return err
		}
	case action == "discard-all":
		a.openDiscardConfirm("discard-all", a.workingAllPaths())
	case action == "commit-dialog":
		return a.openCommitDialog(ctx)
	case action == "commit-cancel":
		a.closeCommitDialog()
	case action == "commit-confirm":
		return a.submitCommitDialog(ctx)
	case action == "stage-selected":
		if err := a.stageSelected(ctx); err != nil {
			return err
		}
	case action == "unstage-selected":
		if err := a.unstageSelected(ctx); err != nil {
			return err
		}
	case action == "discard-selected":
		a.openDiscardConfirm("discard-selected", a.selectedList())
	case action == "select-all-files":
		a.selectAllFiles()
		a.refresh()
	case action == "select-clear":
		if a.diffPath != "" {
			a.setSingleSelect(a.diffPath)
		} else {
			a.ensureSelected()
			for key := range a.selected {
				delete(a.selected, key)
			}
		}
		a.refresh()
	case action == "confirm-ok":
		if a.confirmKind == confirmKindReplaceAll {
			a.closeConfirm()

			return a.applyWorkspaceReplace(ctx)
		}
		if unsavedKind(a.confirmKind) {
			if err := a.confirmUnsavedSave(ctx); err != nil {
				return err
			}
		} else if err := a.confirmDiscard(ctx); err != nil {
			return err
		}
	case action == "confirm-save":
		if err := a.confirmUnsavedSave(ctx); err != nil {
			return err
		}
	case action == "confirm-dont-save":
		if err := a.confirmUnsavedDontSave(ctx); err != nil {
			return err
		}
	case action == "confirm-cancel":
		a.closeConfirm()
	case strings.HasPrefix(action, "stage-file:"):
		if err := a.stageFile(ctx, strings.TrimPrefix(action, "stage-file:")); err != nil {
			return err
		}
	case strings.HasPrefix(action, "unstage-file:"):
		if err := a.unstageFile(ctx, strings.TrimPrefix(action, "unstage-file:")); err != nil {
			return err
		}
	case strings.HasPrefix(action, "discard-file:"):
		a.openDiscardConfirm("discard-file", []string{strings.TrimPrefix(action, "discard-file:")})
	case strings.HasPrefix(action, "revert-hunk:"):
		if idx, ok := actionIndex(action, "revert-hunk:"); ok {
			if err := a.revertClickedBlock(ctx, idx, box.ID); err != nil {
				return err
			}
		}
	case strings.HasPrefix(action, "copy-commit-message:"):
		a.copyCommitMessage(strings.TrimPrefix(action, "copy-commit-message:"))
	case strings.HasPrefix(action, "copy-commit-id:"):
		a.copyCommitID(strings.TrimPrefix(action, "copy-commit-id:"))
	case action == "diff-note":
		a.showToast("info", "Sample changes", a.changes.Note)
		a.refresh()
	case action == "diff-page-prev":
		a.scrollChanges(-1)
	case action == "diff-page-next":
		a.scrollChanges(1)
	case action == "diff-split":
		a.setSplit(true)
	case action == "diff-unified":
		a.setSplit(false)
	case action == "hunk-prev":
		a.jumpHunk(-1)
	case action == "hunk-next":
		a.jumpHunk(1)
	case action == "details-toggle":
		a.toggleDetails()
	case action == "scroll-left":
		a.scrollHorizontal(-a.zx(hscrollStep))
	case action == "scroll-right":
		a.scrollHorizontal(a.zx(hscrollStep))
	case action == "tabs-prev":
		a.scrollTabs(0, -1)
	case action == "tabs-next":
		a.scrollTabs(0, 1)
	case action == "tabs2-prev":
		a.scrollTabs(1, -1)
	case action == "tabs2-next":
		a.scrollTabs(1, 1)
	case action == "preview-toggle":
		a.togglePreview()
	case action == "wrap-toggle":
		a.toggleWrap()
	case action == "blame-toggle":
		a.toggleBlame()
	case action == "find-close":
		return a.closeFind(ctx)
	case action == "find-prev":
		a.findStep(-1)

		return a.page.Form().Focus(ctx, "find")
	case action == "find-next":
		a.findStep(1)

		return a.page.Form().Focus(ctx, "find")
	case action == "find-case":
		a.toggleFindCase()

		return a.page.Form().Focus(ctx, "find")
	case action == "find-regex":
		a.toggleFindRegex()

		return a.page.Form().Focus(ctx, "find")
	case action == "find-word":
		a.toggleFindWholeWord()

		return a.page.Form().Focus(ctx, "find")
	case action == "replace-one":
		return a.replaceOne(ctx)
	case action == "replace-all":
		return a.replaceAllInFile(ctx)
	case action == "details-close":
		a.details = false
		a.refresh()
	case action == "collapse-all":
		a.collapseAll()
	case action == "filter-clear":
		a.filter = ""
		a.treeOffset = 0
		a.page.Form().SetFormValue("filter", "")
		a.refresh()
	case action == "focus-filter":
		return a.focusFilter(ctx)
	case action == "copy-path":
		a.copyActive(false)
	case action == "copy-file":
		a.copyActive(true)
	case strings.HasPrefix(action, "menu-item:"):
		return a.runMenuItem(ctx, strings.TrimPrefix(action, "menu-item:"))
	case action == "devtools":
		a.showToast("info", "Developer tools", devMessage(!a.devOn))
		a.SetDevTools(!a.devOn)
	case action == "branch-picker":
		a.branchOpen = !a.branchOpen
		a.branchQuery = ""

		if !a.branchOpen {
			a.refresh()

			break
		}

		a.refresh()
		if err := a.page.Redraw(ctx); err != nil {
			return err
		}

		return a.focusAfterLayout(ctx, "branch-filter")
	case action == "branch-close":
		a.branchOpen = false
		a.branchQuery = ""
		a.refresh()
	case action == "branch-head":
		a.pickBranch(ctx, "")
	case strings.HasPrefix(action, "branch-pick:"):
		if index, ok := actionIndex(action, "branch-pick:"); ok && index < len(a.view.History.Branches) {
			a.pickBranch(ctx, a.view.History.Branches[index].Ref)
		}
	case action == "tree-up":
		a.scrollTree(-(a.treeVisibleRows() - 1))
	case action == "tree-down":
		a.scrollTree(a.treeVisibleRows() - 1)
	case action == "history-working":
		a.showWorkingDiff()
	case action == "history-branch-changes":
		a.showBranchChanges(ctx)
	case action == "history-prev":
		a.scrollHistory(ctx, -1)
	case action == "history-next":
		a.scrollHistory(ctx, 1)
	case strings.HasPrefix(action, "commit-file:"):
		rest := strings.TrimPrefix(action, "commit-file:")
		if parts := strings.SplitN(rest, ":", actionPair); len(parts) == actionPair {
			a.selectCommitFile(ctx, parts[0], parts[1])
		}
	case strings.HasPrefix(action, "commit-open:"):
		a.openCommitDiff(ctx, strings.TrimPrefix(action, "commit-open:"))
	case strings.HasPrefix(action, "commit:"):
		a.toggleCommit(ctx, strings.TrimPrefix(action, "commit:"))
	case strings.HasPrefix(action, "open:"):
		path := strings.TrimPrefix(action, "open:")
		if (a.ctrl || a.shift) && !a.editing {
			return a.clickTreeSelect(ctx, path)
		}
		a.clearExplorerSel()
		// Single-click tree / palette / welcome opens reuse the preview slot.
		a.openFilePreview(path)
	case strings.HasPrefix(action, "dir:"):
		path := strings.TrimPrefix(action, "dir:")
		if (a.ctrl || a.shift) && !a.editing {
			return a.clickTreeSelect(ctx, path)
		}
		a.clearExplorerSel()
		a.onToggleDir(path)
	case strings.HasPrefix(action, "tab-close:"):
		if index, ok := actionIndex(action, "tab-close:"); ok {
			a.requestCloseTabIn(0, index)
		}
	case strings.HasPrefix(action, "tab2-close:"):
		if index, ok := actionIndex(action, "tab2-close:"); ok {
			a.requestCloseTabIn(1, index)
		}
	case strings.HasPrefix(action, "tab2:"):
		if index, ok := actionIndex(action, "tab2:"); ok {
			a.selectTabIn(1, index)
		}
	case strings.HasPrefix(action, "tab:"):
		if index, ok := actionIndex(action, "tab:"); ok {
			a.selectTab(index)
		}
	case strings.HasPrefix(action, "diff-file:"):
		path := strings.TrimPrefix(action, "diff-file:")
		switch a.diffSource {
		case "commit":
			a.selectCommitFile(ctx, a.commitOpen, path)
		case "branch":
			a.selectBranchFile(path)
		default:
			if a.ctrl {
				a.toggleDiff(path)
			} else {
				a.selectDiff(path)
			}
		}
	case strings.HasPrefix(action, "diff-expand-up:"):
		if idx, ok := actionIndex(action, "diff-expand-up:"); ok {
			a.diffExpandUp(idx)
		}
	case strings.HasPrefix(action, "diff-expand-down:"):
		if idx, ok := actionIndex(action, "diff-expand-down:"); ok {
			a.diffExpandDown(idx)
		}
	case strings.HasPrefix(action, "diff-expand-all:"):
		if idx, ok := actionIndex(action, "diff-expand-all:"); ok {
			a.diffExpandAll(idx)
		}
	case strings.HasPrefix(action, "outline:"):
		if line, ok := actionIndex(action, "outline:"); ok {
			a.outlineLine(line)
		}
	case strings.HasPrefix(action, "sticky2:"):
		if line, ok := actionIndex(action, "sticky2:"); ok {
			a.scrollToSticky(1, line)
		}
	case strings.HasPrefix(action, "sticky:"):
		if line, ok := actionIndex(action, "sticky:"); ok {
			a.scrollToSticky(0, line)
		}
	}

	return a.page.Form().Focus(ctx, "")
}

// onChange keeps the filter and the palette query in app state after the
// binder writes the fields.
func (a *App) onChange(ctx context.Context, box ownframe.Box) error {
	switch box.ID {
	case "filter":
		next := a.view.Filter
		if next == a.filter {
			return nil
		}

		a.filter = next
		a.treeOffset = 0
		a.refresh()
	case "find":
		next := a.view.FindQuery
		if next == a.findQuery {
			return nil
		}

		a.findQuery = next
		a.reindexFind()
		a.refresh()
	case "find-replace":
		next := a.view.ReplaceQuery
		if next == a.replaceQuery {
			return nil
		}

		a.replaceQuery = next
		a.refresh()
	case "branch-filter":
		next := a.view.BranchQuery
		if next == a.branchQuery {
			return nil
		}

		a.branchQuery = next
		a.refresh()
	case "palette":
		next := a.view.PaletteQuery
		if next == a.paletteQuery {
			return nil
		}

		a.paletteQuery = next
		a.paletteIndex = 0

		// The symbol overlay filters on the mirrored query.
		if a.paletteMode == paletteModeSymbols {
			a.symbolQuery = next
		}

		a.refresh()
	case "editor":
		a.applyEditText(a.view.EditText)
	case "workspace-search":
		next := a.view.SearchQuery
		if next == a.searchQuery {
			return nil
		}

		a.searchQuery = next
		a.searchIndex = 0
		a.runWorkspaceSearch(ctx)
	case "workspace-replace":
		next := a.view.SearchReplace
		if next == a.searchReplace {
			return nil
		}

		a.searchReplace = next
		a.refresh()
	case "excludes-scan":
		next := a.view.ScanText
		if next == a.scanDraftText() && a.scanTouched {
			return nil
		}

		a.scanDraft, a.scanTouched = next, true
		a.noteInvalidGlobs()
		a.refresh()
	case "excludes-search":
		next := a.view.SearchText
		if next == a.searchDraftText() && a.searchTouched {
			return nil
		}

		a.searchDraft, a.searchTouched = next, true
		a.noteInvalidGlobs()
		a.refresh()
	case "project-path", "drawer-path":
		next := a.view.ProjectQuery
		if next == a.projectQuery {
			return nil
		}

		a.projectQuery = next
		a.refresh()
	case "browse-filter":
		next := a.view.BrowseQuery
		if next == a.browseFilter {
			return nil
		}

		a.browseFilter = next
		a.browseList.reset()
		a.refresh()
	case "commit-message":
		next := a.view.CommitMessage
		if next == a.commitDialogMsg {
			return nil
		}

		a.commitDialogMsg = next
		a.refresh()
	}

	return nil
}

func findBoxID(boxes []ownframe.Box, id string) (ownframe.Box, bool) {
	for _, box := range boxes {
		if box.ID == id {
			return box, true
		}
	}

	return ownframe.Box{}, false
}

// onWheel scrolls the explorer tree, the changed-file list, the history list,
// or the unwrapped editor under the pointer. A vertical wheel over the editor
// returns false so the window can pan a long file. Ctrl+wheel does not zoom
// this page.
func (a *App) onWheel(ctx context.Context, x, y, dx, dy float64) (bool, error) {
	if a.confirmOpen {
		return true, nil
	}

	// Ctrl+wheel zooms the layout like the keyboard chords. The window
	// leaves the wheel to the page because wheel zoom is off.
	if a.ctrl && dy != 0 {
		if dy > 0 {
			return true, a.zoomIn(ctx)
		}

		return true, a.zoomOut(ctx)
	}

	if a.searchOpen {
		sx, sy := a.page.Scroll().ScrollOffset()
		x -= float64(sx)
		y -= float64(sy)
		box, ok := findBoxID(a.page.Boxes(), "search-results")
		if ok && dy != 0 && x >= box.X && x <= box.X+box.W && y >= box.Y && y <= box.Y+box.H {
			rows := a.searchScroll.WheelRows(-dy * treeWheelRows)
			if rows != 0 {
				a.scrollSearchTo(a.searchScroll.Offset + rows)
			}
			return true, a.page.Redraw(ctx)
		}
		return true, nil
	}
	if a.settingsOpen {
		return true, a.settingsWheelAt(ctx, x, y, dy)
	}
	if a.paletteOpen || a.branchOpen {
		return false, nil
	}

	scrollX, scrollY := a.page.Scroll().ScrollOffset()
	x -= float64(scrollX)
	y -= float64(scrollY)

	if a.termOpen && dy != 0 {
		box, ok := findBoxID(a.page.Boxes(), "term-body")
		if ok && x >= box.X && x <= box.X+box.W && y >= box.Y && y <= box.Y+box.H {
			delta := int(-dy * treeWheelRows)
			a.termScroll = terminalScrollBy(a.termScroll, a.terminalMaxScroll(), delta)
			a.refresh()

			return true, a.page.Redraw(ctx)
		}
	}

	if handled, err := a.browseWheelAt(ctx, x, y, dy); handled || err != nil {
		return handled, err
	}

	if !a.wrap && ((dx != 0) || (a.shift && dy != 0)) && a.isOverEditor(x, y) {
		delta := -dx
		if dx == 0 && a.shift && dy != 0 {
			delta = -dy
		}

		step := int(delta * float64(hscrollWheelStep))
		if step == 0 && delta != 0 {
			if delta > 0 {
				step = 1
			} else {
				step = -1
			}
		}

		// In split mode the hovered pane owns the gesture: the right pane
		// is tested first so the shared gutter never steals it.
		group := 0
		if a.split && a.mode == "explorer" {
			switch {
			case a.isOverPane(1, x, y):
				group = 1
			case a.isOverPane(0, x, y):
				group = 0
			default:
				group = a.focusedScrollGroup()
			}
		}

		a.scrollHorizontalFor(group, step)

		return true, a.page.Redraw(ctx)
	}

	if dy == 0 {
		return false, nil
	}

	if a.mode == "changes" && a.view.Diff.Scroll {
		box, ok := findBoxID(a.page.Boxes(), "changes-list")
		if ok && x >= box.X && x <= box.X+box.W && y >= box.Y && y <= box.Y+box.H {
			rows := a.changesScroll.WheelRows(-dy * treeWheelRows)
			if rows != 0 {
				a.scrollChanges(rows)
			}

			return true, a.page.Redraw(ctx)
		}
	}

	if a.mode == "changes" && a.view.History.Visible {
		box, ok := findBoxID(a.page.Boxes(), "history-pane")
		if ok && x >= box.X && x <= box.X+box.W && y >= box.Y && y <= box.Y+box.H {
			rows := a.historyScroll.WheelRows(-dy * treeWheelRows)
			if rows != 0 {
				a.scrollHistory(ctx, rows)
			}

			return true, a.page.Redraw(ctx)
		}
	}

	if a.mode == "explorer" {
		if a.split {
			if a.isOverPane(1, x, y) {
				a.scrollEditorGroupByWheel(1, dy)

				return true, a.page.Redraw(ctx)
			}

			if a.isOverPane(0, x, y) {
				a.scrollEditorGroupByWheel(0, dy)

				return true, a.page.Redraw(ctx)
			}
		}

		if !a.view.TreeScroll {
			return false, nil
		}

		tree, ok := findBoxID(a.page.Boxes(), "tree-pane")
		if !ok {
			return false, nil
		}

		if x < tree.X || x > tree.X+tree.W || y < tree.Y || y > tree.Y+tree.H {
			a.treeWheelAccum = 0

			return false, nil
		}

		a.treeWheelAccum += -dy * treeWheelRows
		rows := int(a.treeWheelAccum)
		a.treeWheelAccum -= float64(rows)

		if rows != 0 {
			a.scrollTree(rows)
			a.keepTreeFocus(ctx)
		}

		return true, a.page.Redraw(ctx)
	}

	return false, nil
}

// keepTreeFocus moves focus to the first visible row when a wheel scroll
// carried the focused row out of the window.
func (a *App) keepTreeFocus(ctx context.Context) {
	focus := a.page.Form().FocusID()
	if !strings.HasPrefix(focus, "tree-") {
		return
	}

	path := strings.TrimPrefix(focus, "tree-")

	for _, row := range a.view.Tree {
		if row.Path == path {
			return
		}
	}

	if len(a.view.Tree) > 0 {
		_ = a.page.Form().Focus(ctx, "tree-"+a.view.Tree[0].Path)
	}
}

// scrollTree moves the explorer window by rows.
func (a *App) scrollTree(rows int) {
	a.treeOffset += rows
	a.clampTreeOffset(a.view.TreeTotal, a.treeVisibleRows())
	a.refresh()
}

// onHover shows the commit message card while a history row is hovered.
func (a *App) onHover(_ context.Context, _, next ownframe.Box) (bool, error) {
	if a.historyDrag {
		return false, nil
	}

	short := ""
	path := ""

	switch {
	case strings.HasPrefix(next.ID, "tree-"):
		path = strings.TrimPrefix(next.ID, "tree-")
	case strings.HasPrefix(next.ID, "tab-") && !strings.HasPrefix(next.ID, "tab-close-") && next.ID != "tab-details":
		if index, ok := actionIndex(next.ID, "tab-"); ok && index < len(a.tabs) {
			path = a.tabs[index]
		}
	case strings.HasPrefix(next.ID, "tab2-") && !strings.HasPrefix(next.ID, "tab2-close-"):
		if index, ok := actionIndex(next.ID, "tab2-"); ok && index < len(a.tabs2) {
			path = a.tabs2[index]
		}
	}

	if strings.HasPrefix(path, "close") || strings.HasPrefix(path, "working") || strings.HasPrefix(path, "details") {
		path = ""
	}

	if path != "" {
		if doc := a.docs[path]; doc != nil {
			if path != a.hoverPath {
				a.hoverPath = path
				a.refresh()
			}

			return false, nil
		}

		for _, file := range a.ws.Files {
			if file.Path == path {
				if path != a.hoverPath {
					a.hoverPath = path
					a.refresh()
				}

				return false, nil
			}
		}
	}

	if strings.HasPrefix(next.ID, "commit-") &&
		!strings.HasPrefix(next.ID, "commit-file-") &&
		!strings.HasPrefix(next.ID, "commit-open-") {
		short = strings.TrimPrefix(next.ID, "commit-")
	}

	if short == a.hoverCommit && path == "" && a.hoverPath == "" {
		return false, nil
	}

	if short == a.hoverCommit && a.hoverPath != "" {
		a.hoverPath = ""
		a.refresh()
	}

	a.hoverCommit = short
	a.hoverPath = ""
	a.refresh()

	return false, nil
}

// treeKeyDown moves focus through the visible explorer rows.
func (a *App) treeKeyDown(ctx context.Context, key string) (bool, error) {
	focus := a.page.Form().FocusID()
	if focus == "tree-up" || focus == "tree-down" {
		switch key {
		case "arrowdown", "pagedown":
			a.scrollTree(a.treeVisibleRows() - 1)
		case "arrowup", "pageup":
			a.scrollTree(-(a.treeVisibleRows() - 1))
		default:
			return false, nil
		}

		a.refresh()

		return true, a.page.Redraw(ctx)
	}

	if !strings.HasPrefix(focus, "tree-") || focus == "tree-scroll" {
		return false, nil
	}

	path := strings.TrimPrefix(focus, "tree-")
	index := -1

	for i, row := range a.view.Tree {
		if row.Path == path {
			index = i

			break
		}
	}

	stickyIndex := -1

	for i, row := range a.view.StickyTree {
		if row.Path == path {
			stickyIndex = i

			break
		}
	}

	if index < 0 && stickyIndex < 0 {
		return false, nil
	}

	if key == "space" {
		var isDir bool

		if index >= 0 {
			isDir = a.view.Tree[index].Dir
		} else {
			isDir = a.view.StickyTree[stickyIndex].Dir
		}

		if isDir {
			a.onToggleDir(path)
			if err := a.page.Redraw(ctx); err != nil {
				return true, err
			}

			return true, a.page.Form().Focus(ctx, focus)
		}

		a.openFilePreview(path)
		if err := a.page.Redraw(ctx); err != nil {
			return true, err
		}

		return true, a.page.Form().Focus(ctx, focus)
	}

	// Enter on a tree file row pins it (open-then-pin when not open). There
	// is no press-count for a real double-click, so Enter/Ctrl+K/menu carry
	// the pin action.
	if key == "enter" {
		var isDir bool

		if index >= 0 {
			isDir = a.view.Tree[index].Dir
		} else {
			isDir = a.view.StickyTree[stickyIndex].Dir
		}

		if !isDir {
			a.pinPath(a.activeGroup(), path)
			if err := a.page.Redraw(ctx); err != nil {
				return true, err
			}

			return true, a.page.Form().Focus(ctx, focus)
		}
	}

	switch key {
	case "arrowdown":
		if index+1 >= len(a.view.Tree) {
			a.scrollTree(1)
			if err := a.page.Redraw(ctx); err != nil {
				return true, err
			}

			index = 0
		} else {
			index++
		}
	case "arrowup":
		if index == 0 {
			a.scrollTree(-1)
			if err := a.page.Redraw(ctx); err != nil {
				return true, err
			}

			index = 0
		} else {
			index--
		}
	case "pagedown":
		a.scrollTree(a.treeVisibleRows() - 1)
		if err := a.page.Redraw(ctx); err != nil {
			return true, err
		}

		index = 0
	case "pageup":
		a.scrollTree(-(a.treeVisibleRows() - 1))
		if err := a.page.Redraw(ctx); err != nil {
			return true, err
		}

		index = len(a.view.Tree) - 1
	case "home":
		a.treeOffset = 0
		a.refresh()

		if err := a.page.Redraw(ctx); err != nil {
			return true, err
		}

		index = 0
	case "end":
		a.treeOffset = max(0, a.view.TreeTotal-a.treeVisibleRows())
		a.refresh()

		if err := a.page.Redraw(ctx); err != nil {
			return true, err
		}

		index = len(a.view.Tree) - 1
	case "arrowright":
		if a.folders[path] {
			return true, nil
		}

		if a.view.Tree[index].Dir {
			a.onToggleDir(path)
			if err := a.page.Redraw(ctx); err != nil {
				return true, err
			}
		}

		return true, a.page.Form().Focus(ctx, focus)
	case "arrowleft":
		if a.folders[path] {
			a.onToggleDir(path)
			if err := a.page.Redraw(ctx); err != nil {
				return true, err
			}

			return true, a.page.Form().Focus(ctx, focus)
		}

		parent := dirName(path)
		if parent == "." || parent == "" {
			return true, nil
		}

		visible := false

		for _, row := range a.view.Tree {
			if row.Path == parent {
				visible = true

				break
			}
		}

		if !visible {
			index := 0
			if treeRowIndex(a.ws.Tree, a.folders, parent, &index) {
				a.treeOffset = max(0, index-treeParentPad)
				a.refresh()

				if err := a.page.Redraw(ctx); err != nil {
					return true, err
				}
			}
		}

		return true, a.page.Form().Focus(ctx, "tree-"+parent)
	default:
		return false, nil
	}

	if index >= 0 && index < len(a.view.Tree) {
		return true, a.page.Form().Focus(ctx, "tree-"+a.view.Tree[index].Path)
	}

	return true, nil
}

// onDragStart claims the history divider or the explorer thumb.
func (a *App) onDragStart(ctx context.Context, box ownframe.Box) (bool, error) {
	// The inline card roams by its header in every direction. Buttons inside
	// carry their own ids, so they never reach this branch.
	if box.ID == "inline-card" && a.inlineCard.Show {
		a.cardDrag = true
		a.cardDx = 0
		a.cardDy = 0
		a.cardStartLeft = a.inlineCard.Left
		a.cardStartTop = a.inlineCard.Top
		return true, nil
	}
	if box.ID == "term-resize" && a.termOpen {
		a.termDrag = true
		a.termDragStartH = a.termHeight
		a.termDragDy = 0
		a.termFocus = true

		return true, nil
	}
	if box.ID == "editor-scroll-thumb" || box.ID == "editor-scroll-thumb-2" {
		group := 0
		if box.ID == "editor-scroll-thumb-2" {
			group = 1
		}
		return a.startEditorScrollDrag(group), nil
	}
	if box.ID == "split-divider" && a.split && a.mode == "explorer" {
		a.splitDrag = splitDividerDrag{active: true, start: a.splitRatio}
		if err := a.page.Form().Focus(ctx, "split-divider"); err != nil {
			return false, err
		}

		return true, nil
	}
	if box.ID == "search-thumb" && a.searchOpen && a.view.Search.Scroll {
		track := searchWindowRows * a.zx(searchRowHeight)
		thumb := a.view.Search.ScrollHeight
		if track <= thumb {
			return false, nil
		}
		a.searchDrag = true
		a.searchDragAt = a.searchScroll.Offset
		a.searchDragScale = float64(len(a.searchHits)-searchWindowRows) / float64(track-thumb)
		return true, nil
	}
	if a.onDragClaim(box) {
		return true, nil
	}

	if box.ID == "history-thumb" && a.mode == "changes" && a.view.History.Scroll {
		visible := a.historyVisibleRows()
		track := a.historyTrackHeight()
		thumb := a.view.History.ScrollHeight
		total := len(a.commits.Commits)
		if total <= visible || track <= thumb {
			return false, nil
		}

		a.historyListDrag = true
		a.historyListAt = a.historyScroll.Offset
		a.historyListScale = float64(total-visible) / float64(track-thumb)

		return true, nil
	}

	if box.ID == "changes-thumb" && a.mode == "changes" && a.view.Diff.Scroll {
		visible := a.changeVisibleRows()
		track := a.changesTrackHeight()
		thumb := a.view.Diff.ScrollHeight
		total := len(a.sidebarChangeFiles())
		if total <= visible || track <= thumb {
			return false, nil
		}

		a.changesDrag = true
		a.changesDragAt = a.changesScroll.Offset
		a.changesScale = float64(total-visible) / float64(track-thumb)

		return true, nil
	}

	if box.ID == "tree-thumb" && a.mode == "explorer" && a.view.TreeScroll {
		visible := a.treeVisibleRows()
		track := visible * a.zx(treeRowH)
		thumb := a.view.TreeThumbH
		total := a.view.TreeTotal

		if total <= visible || track <= thumb {
			return false, nil
		}

		a.treeDrag = true
		a.treeDragAt = a.treeOffset
		a.treeScale = float64(total-visible) / float64(track-thumb)

		return true, nil
	}

	if box.ID == "browse-thumb" && a.browseOpen && a.view.BrowseScroll {
		track := a.browseTrackHeight()
		thumb := a.view.BrowseThumbH
		total := len(a.browseFilteredEntries())
		if total <= browseListVisible || track <= thumb {
			return false, nil
		}

		a.browseDrag = true
		a.browseDragAt = a.browseList.Offset
		a.browseDragScale = float64(total-browseListVisible) / float64(track-thumb)

		return true, nil
	}

	if box.ID == "browse-scroll" && a.browseOpen && a.view.BrowseScroll {
		track := a.browseTrackHeight()
		thumb := a.view.BrowseThumbH
		total := len(a.browseFilteredEntries())
		if total <= browseListVisible || track <= thumb {
			return false, nil
		}

		a.browseDrag = true
		a.browseDragAt = a.browseList.Offset
		a.browseDragScale = float64(total-browseListVisible) / float64(track-thumb)

		return true, nil
	}

	if (box.ID == "settings-thumb" || box.ID == "settings-rail") && a.settingsOpen {
		return a.startSettingsThumbDrag(), nil
	}

	if box.ID == "drawer-resize" {
		a.drawerDrag = true
		a.drawerDragExtra = a.drawerExtra
		a.drawerDx = 0
		if err := a.page.Form().Focus(ctx, "drawer-resize"); err != nil {
			return false, err
		}

		return true, nil
	}

	if box.ID != "history-divider" || !a.historyVisible() {
		return false, nil
	}

	a.historyDrag = true
	a.historyStart = float64(a.historyHeight()) / float64(a.drawerStackHeight())
	a.historyManual = true
	a.historyDy = 0
	a.hoverCommit = ""

	if err := a.page.Form().Focus(ctx, "history-divider"); err != nil {
		return false, err
	}

	return true, nil
}

// onDragMove resizes the panes or moves the explorer thumb.
func (a *App) onDragMove(ctx context.Context, dx, dy float64) error {
	if a.splitDrag.active {
		a.splitDrag.dx += dx

		total := a.editorWidth()
		ratio := a.splitDrag.start

		if total > 0 {
			// The pointer delta arrives in screen px; the editor geometry
			// is in page px, so it scales down by the layout zoom first
			// (the same conversion the terminal drag uses).
			ratio += (a.splitDrag.dx / a.uiZoom()) / float64(total)
		}

		// A clamped tick moves nothing: skip the refresh so a drag pinned
		// at the rail does not rebuild the view per event.
		if !a.setSplitRatio(ratio) {
			return nil
		}

		return a.page.Redraw(ctx)
	}
	if a.termDrag {
		a.termDragDy += dy
		maxBase := max(termMinHeight, int(float64(a.termMaxHeight())/a.uiZoom()))
		h := a.termDragStartH - int(a.termDragDy/a.uiZoom())
		h = max(termMinHeight, min(h, maxBase))
		if h != a.termHeight {
			a.termHeight = h
			a.syncTerminalSize(ctx)
			a.refresh()
		}

		return a.page.Redraw(ctx)
	}
	if a.cardDrag {
		a.cardDx += dx
		a.cardDy += dy
		a.inlineCard.Left = a.clampCardLeft(a.cardStartLeft + int(a.cardDx+roundHalf))
		a.inlineCard.Top = a.clampCardTop(a.cardStartTop + int(a.cardDy+roundHalf))
		a.refresh()

		return a.page.Redraw(ctx)
	}
	if a.editorDrag.active {
		a.moveEditorScrollDrag(dy)
		return a.page.Redraw(ctx)
	}
	if a.searchDrag {
		a.scrollSearchTo(a.searchDragAt + int(dy*a.searchDragScale+roundHalf))
		return a.page.Redraw(ctx)
	}
	if a.dragSelect {
		a.selX += dx
		a.selY += dy
		a.extendEditSelection(a.selX, a.selY)
		a.refreshInlineCardSelection()
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if a.historyListDrag {
		offset := a.historyListAt + int(dy*a.historyListScale+roundHalf)
		total := len(a.commits.Commits)
		visible := a.historyVisibleRows()
		a.historyScroll.SetOffset(offset, total, visible)
		a.maybeLoadMoreHistory(ctx)
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if a.changesDrag {
		offset := a.changesDragAt + int(dy*a.changesScale+roundHalf)
		total := len(a.sidebarChangeFiles())
		visible := a.changeVisibleRows()
		a.changesScroll.SetOffset(offset, total, visible)
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if a.treeDrag {
		offset := a.treeDragAt + int(dy*a.treeScale+roundHalf)
		maxOffset := a.view.TreeTotal - a.treeVisibleRows()
		if maxOffset < 0 {
			maxOffset = 0
		}

		if offset < 0 {
			offset = 0
		}

		if offset > maxOffset {
			offset = maxOffset
		}

		if offset == a.treeOffset {
			return nil
		}

		a.treeOffset = offset
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if a.browseDrag {
		total := len(a.browseFilteredEntries())
		maxOffset := total - browseListVisible
		if maxOffset < 0 {
			maxOffset = 0
		}
		offset := a.browseDragAt + int(dy*a.browseDragScale+roundHalf)
		if offset < 0 {
			offset = 0
		}
		if offset > maxOffset {
			offset = maxOffset
		}
		if offset == a.browseList.Offset {
			return nil
		}
		a.browseList.Offset = offset
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if a.settingsDrag {
		before := a.settingsScrollOffset
		a.moveSettingsThumbDrag(dy)
		if a.settingsScrollOffset == before {
			return nil
		}
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if a.drawerDrag {
		a.drawerDx += dx
		extra := a.drawerDragExtra + int(a.drawerDx+roundHalf)
		if extra < 0 {
			extra = 0
		}
		if extra > drawerMaxExtra {
			extra = drawerMaxExtra
		}
		if extra == a.drawerExtra {
			return nil
		}
		a.setDrawerExtra(extra)
		a.persistSession()

		return a.page.Redraw(ctx)
	}

	if !a.historyDrag {
		return nil
	}

	a.historyDy += dy

	body := float64(a.drawerStackHeight())
	if body <= 0 {
		return nil
	}

	ratio := a.historyStart - a.historyDy/body
	if ratio < historyMinRatio {
		ratio = historyMinRatio
	}

	if ratio > historyMaxRatio {
		ratio = historyMaxRatio
	}

	if ratio == a.historyRatio {
		return nil
	}

	a.historyRatio = ratio
	a.refresh()

	return a.page.Redraw(ctx)
}

// onDragEnd releases the history divider.
func (a *App) onDragEnd(ctx context.Context) error {
	if a.splitDrag.active {
		a.splitDrag = splitDividerDrag{}
		a.persistSession()
		a.refresh()

		return a.page.Form().Focus(ctx, "")
	}

	if a.termDrag {
		a.termDrag = false
		a.termDragDy = 0
		a.syncTerminalSize(ctx)
		a.refresh()

		return a.page.Redraw(ctx)
	}

	a.editorDrag.active = false
	wasSelect := a.dragSelect
	wasSearch := a.searchDrag
	wasBrowse := a.browseDrag
	a.dragSelect = false
	a.searchDrag = false
	a.searchDragScale = 0
	a.historyDrag = false
	a.historyDy = 0
	a.treeDrag = false
	a.treeScale = 0
	a.changesDrag = false
	a.changesScale = 0
	a.historyListDrag = false
	a.historyListScale = 0
	a.browseDrag = false
	a.browseDragScale = 0
	a.settingsDrag = false
	a.settingsDragScale = 0
	a.drawerDrag = false
	a.drawerDx = 0
	if a.cardDrag {
		a.stampCardOffset()
	}
	a.cardDrag = false
	a.cardDx = 0
	a.cardDy = 0
	if wasBrowse && a.browseOpen {
		return a.page.Form().Focus(ctx, "browse-filter")
	}
	if wasSearch && a.searchOpen {
		return a.page.Form().Focus(ctx, "workspace-search")
	}

	if wasSelect {
		a.refresh()

		return a.page.Redraw(ctx)
	}

	return nil
}

// onKeyDown maps the single-key shortcuts and tracks the modifiers that the
// window sends as separate key events.
func (a *App) onKeyDown(ctx context.Context, key string) error {
	switch key {
	case "control", "controlleft", "controlright", "meta", "metaleft", "metaright":
		a.ctrl = true

		return nil
	case "shift", "shiftleft", "shiftright":
		a.shift = true

		return nil
	}

	// Layout zoom owns Ctrl+=, Ctrl+-, and Ctrl+0 everywhere, including
	// while editing: the window leaves these chords to the page because
	// key zoom is off. The allows gate kills the old chord when the action
	// is rebound; the dispatcher below fires the new chord. Uncustomized
	// behavior (including shift-insensitive Ctrl+Shift+=) is unchanged.
	if a.ctrl {
		switch key {
		case "equal", "kpadd":
			if !a.keymapAllows("zoom-in", key) {
				return nil
			}
			return a.zoomIn(ctx)
		case "minus", "kpsubtract":
			if !a.keymapAllows("zoom-out", key) {
				return nil
			}
			return a.zoomOut(ctx)
		case "0", "kp0":
			if !a.keymapAllows("zoom-reset", key) {
				return nil
			}
			return a.resetZoom(ctx)
		}
	}

	// Ctrl+` toggles the terminal from anywhere, including while the shell
	// has focus.
	if a.ctrl && !a.shift && (key == "backquote" || key == "grave" || key == "`") && a.keymapAllows("toggle-terminal", key) {
		a.toggleTerminal(ctx)

		return a.page.Redraw(ctx)
	}

	// Ctrl+1 / Ctrl+2 focus the left / right editor from anywhere, including
	// while the shell has focus (like Ctrl+`). Plain 1/2 keep switching
	// modes. In web mode the browser may capture these chords; F6 and the
	// pane menu cover the same switch there.
	if a.ctrl && !a.shift && (key == "1" || key == "kp1") && a.keymapAllows("focus-left", key) {
		a.setFocusGroup(0)

		return a.page.Redraw(ctx)
	}

	if a.ctrl && !a.shift && (key == "2" || key == "kp2") && a.keymapAllows("focus-right", key) {
		a.setFocusGroup(1)

		return a.page.Redraw(ctx)
	}

	// The shell owns the keyboard while the terminal is focused. This sits
	// before every global shortcut so typing w, r, n, or a navigation chord
	// reaches the shell instead of closing tabs or moving the editor.
	if !a.confirmOpen {
		if handled, err := a.handleTerminalKey(ctx, key); handled || err != nil {
			return err
		}
	}

	// VS Code-style history: Alt+Left goes back, Alt+Right goes forward. The
	// window sends these as single names, so they work while editing and win
	// over the horizontal scroll below. Rebinding kills the old chord.
	if key == "alt+arrowleft" && a.keymapAllows("history-back", key) {
		a.goNavBack()

		return a.page.Redraw(ctx)
	}

	if key == "alt+arrowright" && a.keymapAllows("history-forward", key) {
		a.goNavForward()

		return a.page.Redraw(ctx)
	}

	// F6 cycles the focused editor pane while a split is open. It sits after
	// the terminal branch so a focused shell keeps the key.
	if key == "f6" && a.split && a.mode == "explorer" && a.keymapAllows("cycle-pane", key) {
		if a.focusGroup == 1 {
			a.setFocusGroup(0)
		} else {
			a.setFocusGroup(1)
		}

		return a.page.Redraw(ctx)
	}

	if a.confirmOpen && key == "escape" {
		a.closeConfirm()

		return a.page.Redraw(ctx)
	}

	if a.confirmOpen && key == "enter" {
		if a.confirmKind == confirmKindReplaceAll {
			a.closeConfirm()

			if err := a.applyWorkspaceReplace(ctx); err != nil {
				return err
			}

			return a.page.Redraw(ctx)
		}
		if unsavedKind(a.confirmKind) {
			if err := a.confirmUnsavedSave(ctx); err != nil {
				return err
			}
		} else if err := a.confirmDiscard(ctx); err != nil {
			return err
		}

		return a.page.Redraw(ctx)
	}

	if a.confirmOpen && unsavedKind(a.confirmKind) && !a.ctrl && !a.shift && key == "d" {
		if err := a.confirmUnsavedDontSave(ctx); err != nil {
			return err
		}

		return a.page.Redraw(ctx)
	}

	if a.commitDialogOpen && !a.confirmOpen {
		switch {
		case key == "escape":
			a.closeCommitDialog()

			return a.page.Redraw(ctx)
		case key == "enter" && a.ctrl:
			return a.submitCommitDialog(ctx)
		}

		return nil
	}

	if a.confirmOpen {
		return nil
	}

	// A Rebind capture eats the next key: Esc cancels, anything else becomes
	// the candidate chord. It sits before the settings escape so cancelling
	// a capture never closes the panel.
	if a.keymapCapture != "" && key == "escape" {
		a.cancelKeymapPending()
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if a.keymapCapture != "" {
		a.processKeymapCapture(chordFromEvent(a.ctrl, a.shift, key))

		return a.page.Redraw(ctx)
	}

	if a.settingsOpen && key == "escape" {
		a.settingsOpen = false
		a.refresh()
		if err := a.page.Redraw(ctx); err != nil {
			return err
		}
		return a.page.Form().Focus(ctx, "act-settings")
	}

	if a.menuOpen && key == "escape" {
		a.closeMenu()
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if a.browseOpen && key == "escape" {
		if a.fileAlert != "" {
			a.fileAlert = ""
			a.refresh()

			return a.page.Redraw(ctx)
		}
		if a.browseFilter != "" {
			a.browseFilter = ""
			a.page.Form().SetFormValue("browse-filter", "")
			a.browseList.reset()
			a.refresh()

			return a.page.Redraw(ctx)
		}
		a.browseOpen = false
		a.refresh()
		if err := a.page.Redraw(ctx); err != nil {
			return err
		}
		return a.page.Form().Focus(ctx, "file-menu")
	}

	if a.fileMenu && key == "escape" {
		if a.fileAlert != "" {
			a.fileAlert = ""
			a.refresh()

			return a.page.Redraw(ctx)
		}
		if a.recentOpen || a.projectOpen {
			a.recentOpen = false
			a.projectOpen = false
			a.refresh()

			return a.page.Redraw(ctx)
		}

		a.fileMenu = false
		a.projectOpen = false
		a.recentOpen = false
		a.refresh()

		return a.page.Redraw(ctx)
	}

	// Alt+C/R/W flip the match toggles for the bar that owns the keyboard:
	// the in-file find/replace bar or the workspace search/replace panel.
	// The window sends Alt chords as "alt+<key>".
	switch key {
	case "alt+c", "alt+r", "alt+w":
		return a.toggleMatchKeys(ctx, key)
	}

	// Space toggles the current result's file checkbox while the search
	// panel owns the keyboard but no text field or button has focus. Enter
	// still opens the hit; focused checkboxes toggle through activation.
	if a.searchOpen && !a.paletteOpen && key == "space" && (a.page.Form().FocusID() == "" ||
		strings.HasPrefix(a.page.Form().FocusID(), "search-hit-")) {
		a.toggleReplaceFile(a.searchIndex)

		return a.page.Redraw(ctx)
	}

	if !a.paletteOpen && (key == "enter" || key == "space") && a.page.Edit().FocusedField() == "" {
		if id := a.page.Form().FocusID(); id != "" {
			if key == "space" && isTreeRowID(id) {
				// Space on explorer rows belongs to treeKeyDown so a folder
				// toggles with its lazy load and a file opens with focus
				// kept on the same row. The filter input still types because
				// its focus is "filter", not a tree row.
			} else if box, ok := findBoxID(a.page.Boxes(), id); ok {
				if err := a.onClick(ctx, box); err != nil {
					return err
				}

				if a.page.Form().FocusID() == "" {
					_ = a.page.Form().Focus(ctx, id)
				}

				return a.page.Redraw(ctx)
			}
		}
	}

	if a.settingsOpen {
		return nil
	}

	// Ctrl+I opens the line card at the current source line, whether the
	// editor is in token view or mid-edit. It stays before the editing branch
	// so the caret line names the card while typing.
	if a.ctrl && !a.shift && key == "i" && a.keymapAllows("line-card", key) {
		a.showInlineCardAtCurrent()

		return a.page.Redraw(ctx)
	}

	// The goto and symbol overlays open while editing, so their navigation
	// keys route before the editing branch: arrows move the overlay
	// selection and Esc closes it instead of leaving edit mode. Editor
	// chords (Ctrl+S and friends) keep working mid-edit; every other key
	// belongs to the overlay input. Files-mode palettes never reach here
	// while editing (Ctrl+P is unreachable mid-edit).
	if a.paletteOpen && (a.paletteMode == paletteModeGoto || a.paletteMode == paletteModeSymbols) {
		switch key {
		case "arrowdown":
			a.movePalette(1)

			return a.page.Redraw(ctx)
		case "arrowup":
			a.movePalette(-1)

			return a.page.Redraw(ctx)
		case "escape":
			return a.closePalette(ctx)
		default:
			// Editor chords (Ctrl+S and friends) keep working mid-edit;
			// every other key belongs to the overlay input.
			if !(a.editing && a.ctrl) {
				return nil
			}
		}
	}

	if a.editing {
		return a.editKey(ctx, key)
	}

	// Merge mode owns Ctrl+S and n/p while the result is not in the text
	// editor. The editing branch above already returned, so keys typed into
	// the result buffer are never stolen. Text fields and overlays keep
	// their own keys.
	if a.mode == "merge" && !mergeTextFocus(a.page.Form().FocusID()) {
		switch {
		case a.ctrl && key == "s":
			a.saveMergeEditor(ctx)

			return a.page.Redraw(ctx)
		case !a.ctrl && !a.shift && key == "n":
			a.mergeStep(1)

			return a.page.Redraw(ctx)
		case !a.ctrl && !a.shift && key == "p":
			a.mergeStep(-1)

			return a.page.Redraw(ctx)
		}
	}

	if a.ctrl && key == "a" && !a.shift && a.keymapAllows("select-all-files", key) {
		if a.mode == "changes" && !a.confirmOpen {
			switch a.page.Form().FocusID() {
			case "filter", "palette", "find", "find-replace", "branch-filter", "workspace-search", "workspace-replace", "project-path", "drawer-path", "browse-filter", "editor", "edit-surface":
			default:
				a.selectAllFiles()
				a.refresh()

				return a.page.Redraw(ctx)
			}
		}

		return nil
	}

	if a.ctrl && key == "p" && a.keymapAllows("quick-open", key) {
		return a.openPalette(ctx)
	}

	// Ctrl+G opens the go-to-line overlay, in or out of edit mode.
	if a.ctrl && !a.shift && key == "g" {
		return a.openGotoPalette(ctx)
	}

	// Ctrl+Shift+O opens the file-scoped symbol overlay. Key recon: the
	// window sends "o" with both ctrl and shift tracked from their key
	// events, mirroring the Ctrl+Shift+F/V chords below.
	if a.ctrl && a.shift && key == "o" {
		return a.openSymbolPalette(ctx)
	}

	// Ctrl+D jumps to the definition of the symbol under the caret. F12
	// stays with the dev dock, so Ctrl+D is the definition chord; it was
	// verified free (no other branch or catalog default consumes it).
	if a.ctrl && !a.shift && key == "d" && a.keymapAllows("goto-definition", key) {
		return a.gotoDefinitionAtCaret(ctx)
	}

	// Ctrl+M jumps to the matching bracket (Ctrl+Shift+M selects to it).
	// Both need a live edit buffer; preview and token views no-op with a
	// toast instead of mutating anything.
	if a.ctrl && key == "m" {
		if !a.editing || a.edit == nil {
			a.showToast("info", "Bracket match", "Press Ctrl+E to edit, then Ctrl+M jumps to the matching bracket.")
			a.refresh()

			return a.page.Redraw(ctx)
		}

		return a.jumpToBracket(ctx, a.shift)
	}

	// Ctrl+/ toggles line comments. The edit path toasts JSON and plain
	// text (no style); outside edit mode it asks for Ctrl+E first.
	// Key recon: "slash" on most layouts, "/" on others.
	if a.ctrl && !a.shift && (key == "slash" || key == "/") {
		return a.toggleLineComment(ctx)
	}

	// Ctrl+K pins/unpins the focused tab; Ctrl+Shift+T reopens the last
	// closed tab. Both were verified free (no prior branch) and share the
	// terminal/form focus guards in pinKeyReady; the terminal branch above
	// already swallowed shell-focused keys.
	if a.ctrl && a.shift && key == "t" {
		if !a.pinKeyReady() {
			return nil
		}

		a.reopenClosedTab()

		return a.page.Redraw(ctx)
	}

	if a.ctrl && !a.shift && key == "k" {
		if !a.pinKeyReady() {
			return nil
		}

		a.togglePinFocused()

		return a.page.Redraw(ctx)
	}

	if a.ctrl && a.shift && key == "f" && a.keymapAllows("workspace-search", key) {
		return a.openWorkspaceSearch(ctx)
	}

	if a.ctrl && a.shift && key == "h" && a.keymapAllows("workspace-replace", key) {
		return a.openWorkspaceReplace(ctx)
	}

	if a.ctrl && key == "f" && a.keymapAllows("find-in-file", key) {
		return a.openFind(ctx)
	}

	if a.ctrl && key == "h" && a.keymapAllows("replace-in-file", key) {
		return a.openReplace(ctx)
	}

	if a.ctrl && key == "s" && a.keymapAllows("save-file", key) {
		a.saveDirtyActive()

		return a.page.Redraw(ctx)
	}

	if a.ctrl && key == "e" && a.keymapAllows("toggle-edit", key) {
		a.toggleEdit()

		return a.page.Redraw(ctx)
	}

	if a.ctrl && key == "backslash" && a.keymapAllows("toggle-split", key) {
		a.toggleSplit()

		return a.page.Redraw(ctx)
	}

	if a.ctrl && a.shift && key == "v" && a.keymapAllows("toggle-preview", key) {
		a.togglePreview()

		return a.page.Redraw(ctx)
	}

	// Plain Ctrl+V pastes into the explorer folder while the editor is
	// closed. The editing branch above already returned, Ctrl+Shift+V
	// preview keeps its own branch, and overlays and text fields keep the
	// chord through pasteTargetReady. This sits before treeKeyDown so the
	// chord pastes instead of moving focus. The allows gates kill the old
	// chords when paste/copy/cut are rebound; uncustomized behavior is
	// unchanged.
	if a.ctrl && !a.shift && key == "v" && a.pasteTargetReady() && a.keymapAllows("paste-explorer", key) {
		return a.pasteFromClipboard(ctx)
	}

	if a.ctrl && !a.shift && !a.editing && a.explorerCopyReady() && (a.keymapMatches("cut-path", key) || a.keymapMatches("copy-path", key)) {
		if done, err := a.copyExplorerSelection(ctx, a.keymapMatches("cut-path", key)); done {
			return err
		}
	}

	if a.searchOpen && key == "escape" {
		return a.closeWorkspaceSearch(ctx)
	}

	if a.page.Form().FocusID() == "workspace-search" {
		switch key {
		case "arrowdown":
			a.searchStep(1)

			return a.page.Redraw(ctx)
		case "arrowup":
			a.searchStep(-1)

			return a.page.Redraw(ctx)
		case "enter":
			if a.ctrl {
				return a.openReplaceConfirm(ctx)
			}
			a.openSearchHit(a.searchIndex)

			return a.page.Redraw(ctx)
		case "escape":
			return a.closeWorkspaceSearch(ctx)
		default:
			return nil
		}
	}

	if a.branchOpen && key == "escape" {
		a.branchOpen = false
		a.branchQuery = ""
		a.refresh()

		return a.page.Redraw(ctx)
	}

	if a.paletteOpen {
		switch key {
		case "arrowdown":
			a.movePalette(1)

			return a.page.Redraw(ctx)
		case "arrowup":
			a.movePalette(-1)

			return a.page.Redraw(ctx)
		case "escape":
			return a.closePalette(ctx)
		case "enter":
			return a.onSubmit(ctx)
		default:
			return nil
		}
	}

	if a.page.Form().FocusID() == "find" {
		switch key {
		case "escape":
			return a.closeFind(ctx)
		case "enter":
			a.findStep(1)

			return a.page.Redraw(ctx)
		default:
			return nil
		}
	}

	if a.page.Form().FocusID() == "find-replace" {
		switch key {
		case "escape":
			return a.closeFind(ctx)
		case "enter":
			if a.ctrl {
				return a.replaceAllInFile(ctx)
			}

			return a.replaceOne(ctx)
		default:
			return nil
		}
	}

	if handled, err := a.treeKeyDown(ctx, key); handled || err != nil {
		return err
	}

	if a.page.Form().FocusID() == "workspace-replace" {
		switch key {
		case "arrowdown":
			a.searchStep(1)

			return a.page.Redraw(ctx)
		case "arrowup":
			a.searchStep(-1)

			return a.page.Redraw(ctx)
		case "enter":
			if a.ctrl {
				return a.openReplaceConfirm(ctx)
			}
			a.openSearchHit(a.searchIndex)

			return a.page.Redraw(ctx)
		case "escape":
			return a.closeWorkspaceSearch(ctx)
		default:
			return nil
		}
	}

	if a.page.Form().FocusID() == "branch-filter" {
		switch key {
		case "escape":
			a.branchOpen = false
			a.branchQuery = ""
			a.refresh()

			return a.page.Redraw(ctx)
		case "enter":
			if len(a.view.History.Branches) > 0 {
				a.pickBranch(ctx, a.view.History.Branches[0].Ref)
			}

			return a.page.Redraw(ctx)
		default:
			return nil
		}
	}

	if a.page.Form().FocusID() == "browse-filter" {
		switch key {
		case "arrowdown":
			a.browseList.scrollBy(1, len(a.browseFilteredEntries()))
			a.refresh()

			return a.page.Redraw(ctx)
		case "arrowup":
			a.browseList.scrollBy(-1, len(a.browseFilteredEntries()))
			a.refresh()

			return a.page.Redraw(ctx)
		case "pagedown":
			a.browseList.scrollBy(browseListVisible-1, len(a.browseFilteredEntries()))
			a.refresh()

			return a.page.Redraw(ctx)
		case "pageup":
			a.browseList.scrollBy(-(browseListVisible - 1), len(a.browseFilteredEntries()))
			a.refresh()

			return a.page.Redraw(ctx)
		case "home":
			a.browseList.Offset = 0
			a.refresh()

			return a.page.Redraw(ctx)
		case "end":
			total := len(a.browseFilteredEntries())
			a.browseList.Offset = browseClampOffset(total, total)
			a.refresh()

			return a.page.Redraw(ctx)
		case "enter":
			rows := a.browseFilteredEntries()
			if len(rows) > 0 {
				idx := a.browseList.Offset
				if idx < 0 || idx >= len(rows) {
					idx = 0
				}
				path := rows[idx].Path
				if wsl.IsWSLDistrosRoot(path) || wsl.IsWSLDistroRoot(path) || isDir(path) {
					a.setBrowseRoot(path)
					a.refresh()

					return a.page.Redraw(ctx)
				}
			}

			return a.page.Redraw(ctx)
		case "escape":
			if a.browseFilter != "" {
				a.browseFilter = ""
				a.page.Form().SetFormValue("browse-filter", "")
				a.browseList.reset()
				a.refresh()

				return a.page.Redraw(ctx)
			}
			// Empty filter falls through to the browseOpen escape above
			// on the next pass; keep focus here for now.
			return nil
		default:
			return nil
		}
	}

	if a.page.Form().FocusID() == "filter" {
		switch key {
		case "arrowup", "arrowdown", "pageup", "pagedown":
		default:
			return nil
		}
	}

	if a.page.Form().FocusID() == "history-divider" && a.historyVisible() {
		if !a.historyManual {
			a.historyRatio = float64(a.historyHeight()) / float64(a.drawerStackHeight())
		}

		ratio := a.historyRatio

		switch key {
		case "arrowup":
			ratio += historyStep
		case "arrowdown":
			ratio -= historyStep
		case "home":
			ratio = historyDefaultRatio
		default:
			return nil
		}

		if ratio < historyMinRatio {
			ratio = historyMinRatio
		}

		if ratio > historyMaxRatio {
			ratio = historyMaxRatio
		}

		a.historyRatio = ratio
		a.historyManual = true
		a.refresh()

		return a.page.Redraw(ctx)
	}

	// A focused split divider resizes the panes from the keyboard: arrows
	// nudge, Home restores the default. Other keys fall through so the
	// divider never traps navigation.
	if a.page.Form().FocusID() == "split-divider" && a.split && a.mode == "explorer" {
		ratio := a.splitRatio

		switch key {
		case "arrowleft":
			ratio -= splitRatioStep
		case "arrowright":
			ratio += splitRatioStep
		case "home":
			ratio = defaultSplitRatio
		default:
			return nil
		}

		a.setSplitRatio(ratio)
		a.refresh()

		return a.page.Redraw(ctx)
	}

	// Ctrl+Z / Ctrl+Y belong to the Undo/Redo chord (desktop) and must not
	// fall through to the plain-key wrap toggle below.
	if a.ctrl && (key == "z" || key == "y") {
		return nil
	}

	// Rebound chords no home branch names fire here, after every overlay and
	// editing guard, so precedence for defaults is unchanged. A rebound old
	// chord is dead by the allows gates in the switch below.
	if handled, err := a.dispatchCustomKeymap(ctx, key); handled || err != nil {
		return err
	}

	switch key {
	case "1":
		if !a.keymapAllows("mode-explorer", key) {
			return nil
		}
		a.setMode(ctx, "explorer")
	case "2":
		if !a.keymapAllows("mode-changes", key) {
			return nil
		}
		a.setMode(ctx, "changes")
	case "3":
		if !a.keymapAllows("toggle-details", key) {
			return nil
		}
		a.toggleDetails()
	case "arrowdown":
		a.scrollFocusedGroup(a.zx(scrollStep))
	case "arrowup":
		a.scrollFocusedGroup(-a.zx(scrollStep))
	case "arrowleft":
		if !a.wrap {
			a.scrollHorizontal(-a.zx(hscrollStep))

			return a.page.Redraw(ctx)
		}
	case "arrowright":
		if !a.wrap {
			a.scrollHorizontal(a.zx(hscrollStep))

			return a.page.Redraw(ctx)
		}
	case "pagedown":
		a.scrollFocusedGroup(a.zx(pageScroll))
	case "pageup":
		a.scrollFocusedGroup(-a.zx(pageScroll))
	case "home":
		a.scrollFocusedGroupTo(0)
	case "end":
		a.scrollFocusedGroupToEnd()
	case "f":
		if !a.keymapAllows("focus-filter", key) {
			return nil
		}
		return a.focusFilter(ctx)
	case "w":
		if !a.keymapAllows("close-tab", key) {
			return nil
		}
		if a.mode == "changes" {
			a.closeWorkingTree(ctx)
		} else if a.mode == "merge" {
			a.requestCloseMergeEditor()
		} else {
			group := a.activeGroup()
			a.requestCloseTabIn(group, a.groupActive(group))
		}
	case "z":
		if !a.keymapAllows("toggle-wrap", key) {
			return nil
		}
		a.toggleWrap()
	case "b":
		// Blame owns the bare B chord: no home branch names it (verified
		// against the mode/filter/tab keys above), so it fires only when
		// uncustomized or rebound to B, like the Z wrap toggle beside it.
		if !a.keymapAllows("toggle-blame", key) {
			return nil
		}
		a.toggleBlame()
	case "t":
		if !a.keymapAllows("toggle-sticky", key) {
			return nil
		}
		a.toggleStickyScroll()
	case "m":
		if !a.keymapAllows("toggle-preview-key", key) {
			return nil
		}
		a.togglePreview()

		return a.page.Redraw(ctx)
	case "r":
		if !a.keymapAllows("reload-file", key) {
			return nil
		}
		if a.mode == "changes" {
			a.ReloadChanges(ctx, true)
		} else {
			a.reloadActive(ctx)
		}
	case "s":
		if a.shift && !a.ctrl && a.keymapAllows("stage-hunk", key) {
			if a.mode == "changes" {
				if err := a.stageHunkAtCursor(ctx); err != nil {
					return err
				}
			}

			return a.page.Redraw(ctx)
		}
		if a.mode == "changes" {
			a.setSplit(true)
		}
	case "u":
		if a.mode == "changes" {
			a.setSplit(false)
		}
	case "n":
		if !a.keymapAllows("hunk-next", key) {
			return nil
		}
		if a.mode == "changes" {
			a.jumpHunk(1)
		}
	case "p":
		if !a.keymapAllows("hunk-prev", key) {
			return nil
		}
		if a.mode == "changes" {
			a.jumpHunk(-1)
		}
	case "escape":
		switch {
		case a.inlineCard.Show:
			a.closeInlineCard()
		case a.details:
			a.details = false
			a.refresh()
		case a.toast.Show:
			a.clearToast()
		}
	default:
		return nil
	}

	return a.page.Redraw(ctx)
}

// onKeyUp clears the tracked modifiers and closes the palette on the release
// so the input blur does not swallow the close.
func (a *App) onKeyUp(ctx context.Context, key string) error {
	switch key {
	case "control", "controlleft", "controlright", "meta", "metaleft", "metaright":
		a.ctrl = false
	case "shift", "shiftleft", "shiftright":
		a.shift = false
	case "escape":
		if a.paletteOpen {
			return a.closePalette(ctx)
		}
	}

	return nil
}

// onSubmit opens the selected palette row or advances find. Enter and
// NumpadEnter arrive here.
func (a *App) onSubmit(ctx context.Context) error {
	if a.commitDialogOpen && !a.confirmOpen {
		if a.ctrl {
			return a.submitCommitDialog(ctx)
		}

		return nil
	}

	// Goto and symbol overlays submit from the palette input in or out of
	// edit mode; they route before the editing branch so Enter jumps
	// instead of inserting a newline.
	if a.paletteOpen && a.page.Form().FocusID() == "palette" {
		switch a.paletteMode {
		case paletteModeGoto:
			return a.submitGoto(ctx)
		case paletteModeSymbols:
			return a.submitSymbol(ctx)
		}
	}

	if a.editing {
		return a.insertNewlineWithIndent(ctx)
	}

	if a.browseOpen && a.page.Form().FocusID() == "browse-filter" {
		rows := a.browseFilteredEntries()
		if len(rows) > 0 {
			idx := a.browseList.Offset
			if idx < 0 || idx >= len(rows) {
				idx = 0
			}
			path := rows[idx].Path
			if wsl.IsWSLDistrosRoot(path) || wsl.IsWSLDistroRoot(path) || isDir(path) {
				a.setBrowseRoot(path)
				a.refresh()

				return a.page.Redraw(ctx)
			}
		}

		return nil
	}

	if (a.fileMenu || a.browseOpen) && (a.page.Form().FocusID() == "project-path" ||
		a.page.Form().FocusID() == "drawer-path") {
		root := strings.TrimSpace(a.view.ProjectQuery)
		if root == "" {
			root = strings.TrimSpace(a.projectQuery)
		}
		if root == "" {
			return nil
		}
		if blockedProjectRoot(root) {
			a.fileAlert = "Drive roots can't be opened as projects. Pick a folder inside it."
			a.refresh()
			return nil
		}
		if a.tryHandoffToWSL(root) {
			return nil
		}
		return a.switchProject(ctx, root)
	}

	if a.findOpen && a.page.Form().FocusID() == "find" {
		delta := 1
		if a.shift {
			delta = -1
		}

		a.findStep(delta)

		return a.page.Redraw(ctx)
	}

	if a.findOpen && a.page.Form().FocusID() == "find-replace" {
		if a.ctrl {
			return a.replaceAllInFile(ctx)
		}

		return a.replaceOne(ctx)
	}

	if a.searchOpen && a.page.Form().FocusID() == "workspace-replace" {
		if a.ctrl {
			return a.openReplaceConfirm(ctx)
		}

		a.openSearchHit(a.searchIndex)

		return a.page.Redraw(ctx)
	}

	if !a.paletteOpen || a.page.Form().FocusID() != "palette" {
		return nil
	}

	rows := a.paletteRows()
	if len(rows) == 0 {
		return nil
	}

	index := a.paletteIndex
	if index < 0 {
		index = 0
	}

	if index >= len(rows) {
		index = len(rows) - 1
	}

	row := rows[index]

	// A `>` command row dispatches instead of opening a file.
	if row.Cmd != "" {
		return a.runPaletteCommand(ctx, row.Cmd)
	}

	// A ":line" query in the files overlay is a go-to alias.
	if a.paletteMode == paletteModeFiles && isGotoQuery(a.paletteQuery) {
		return a.submitGoto(ctx)
	}

	// A symbol row jumps to its line with the caret, like submitSymbol.
	if row.Symbol {
		line := row.No
		path := row.Path

		if err := a.closePalette(ctx); err != nil {
			return err
		}

		if path != "" && (a.activeDoc() == nil || a.activeDoc().Path != path) {
			a.openFileAtPreview(a.activeGroup(), path, true)
		}

		return a.gotoLine(ctx, line, 1)
	}

	// Palette Enter opens reuse the preview slot.
	a.openFileAtPreview(a.activeGroup(), row.Path, true)

	if err := a.page.Form().Focus(ctx, ""); err != nil {
		return err
	}

	return a.page.Redraw(ctx)
}

// focusFilter switches to the explorer and focuses the filter input.
func (a *App) focusFilter(ctx context.Context) error {
	if a.mode != "explorer" {
		a.setMode(ctx, "explorer")

		if err := a.page.Redraw(ctx); err != nil {
			return err
		}
	}

	return a.page.Form().Focus(ctx, "filter")
}

// copyActive puts the active path, or the whole file, on the clipboard.
func (a *App) copyActive(wholeFile bool) {
	path := a.activeDocPath()
	if path == "" {
		return
	}

	if !wholeFile {
		ownframe.Write(path)
		a.showToast("success", "Path copied", path)
		a.refresh()

		return
	}

	raw, err := a.ws.readFile(path)
	if err != nil {
		a.showToast("error", "Cannot copy", err.Error())
		a.refresh()

		return
	}

	ownframe.Write(string(raw))
	a.showToast("success", "File copied", path)
	a.refresh()
}

// reloadActive re-reads the active file from the workspace.
func (a *App) reloadActive(_ context.Context) {
	doc := a.activeDoc()
	if doc == nil {
		a.showToast("info", "No file open", "Open a file before reloading it.")
		a.refresh()

		return
	}

	next := a.ws.openDoc(doc.Path)
	a.docs[doc.Path] = next

	if next.Err != "" {
		a.showToast("error", "Reload failed", next.Err)
	} else {
		a.showToast("success", "File reloaded", doc.Path)
	}

	a.refresh()
}

// isTreeRowID reports whether a focus id is an explorer file or folder row.
// Control ids like tree-up, tree-down, tree-scroll, and tree-pane stay with
// the generic activator; only tree-<path> rows belong to treeKeyDown.
func isTreeRowID(id string) bool {
	if !strings.HasPrefix(id, "tree-") {
		return false
	}

	switch id {
	case "tree-up", "tree-down", "tree-scroll", "tree-pane", "tree-thumb", "tree-sticky":
		return false
	}

	return true
}

// mergeTextFocus reports whether a text field or search overlay owns the
// keyboard, so merge n/p shortcuts do not steal typed characters.
func mergeTextFocus(focus string) bool {
	switch focus {
	case "filter", "palette", "find", "find-replace", "branch-filter", "workspace-search",
		"workspace-replace",
		"project-path", "drawer-path", "browse-filter", "editor", "edit-surface":
		return true
	default:
		return false
	}
}

// actionIndex parses the number after an action prefix.
func actionIndex(action, prefix string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimPrefix(action, prefix))
	if err != nil || n < 0 {
		return 0, false
	}

	return n, true
}

// parseTermLinkAction parses term-link:row:seg click actions.
func parseTermLinkAction(action string) (int, int, bool) {
	rest, ok := strings.CutPrefix(action, "term-link:")
	if !ok {
		return 0, 0, false
	}
	rowText, segText, ok := strings.Cut(rest, ":")
	if !ok {
		return 0, 0, false
	}
	row, err := strconv.Atoi(rowText)
	if err != nil || row < 0 {
		return 0, 0, false
	}
	seg, err := strconv.Atoi(segText)
	if err != nil || seg < 0 {
		return 0, 0, false
	}
	return row, seg, true
}

// devMessage is the toast copy for the dock toggle.
func devMessage(on bool) string {
	if on {
		return "The dock is open. F12 or Ctrl+Shift+I closes it."
	}

	return "The dock is closed. F12 or Ctrl+Shift+I opens it."
}

// onToggleDir handles expanding or collapsing a directory in the tree.
// It is enqueue-only: expanding an unloaded directory marks it Loading and
// queues a background read. The row paints a spinner in one frame with no
// input freeze; collapsing drops the token so a late reply is discarded.
func (a *App) onToggleDir(path string) {
	for _, suf := range []string{"/\u2026retry", "/\u2026more", "/\u2026"} {
		if strings.HasSuffix(path, suf) {
			base := strings.TrimSuffix(path, suf)
			if strings.HasSuffix(path, "/\u2026retry") && base != "" {
				a.requestDirLoad(base)
				a.refresh()
			}
			return
		}
	}
	a.folders[path] = !a.folders[path]

	if a.folders[path] {
		a.requestDirLoad(path)
		a.revealTreeFolder(path)
	} else {
		// Collapse cancels: drop the token and the Loading mark.
		delete(a.dirLoads, path)
		if node := findTreeNode(a.ws.Tree, path); node != nil {
			node.Loading = false
		}
	}

	a.refresh()
}

// retryDirLoad re-enqueues a failed directory load (retry row).
func (a *App) retryDirLoad(path string) {
	a.requestDirLoad(path)
	a.refresh()
}
