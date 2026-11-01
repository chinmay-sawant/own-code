# Explorer File/Folder Operations — Implementation Plan
## New, rename, delete, duplicate from explorer + command palette

> Scope: repo `/home/chinmay/ChinmayPersonalProjects/own-code`. No file writes in this plan. All symbols below were read from the tree.

---

## 1) Current behavior + exact file paths

### 1a. Explorer tree + selection
| What | Where |
|---|---|
| Tree rows render with `id="tree-{{.Path}}"`, `data-action="dir:{{.Path}}"` (folder) / `"open:{{.Path}}"` (file) | `components/html/sidebar.html:41-52`, sticky variant `:30-39` |
| Expand/collapse + lazy load | `internal/app/actions.go:2334-2349` `onToggleDir` → `findTreeNode` + `ws.loadNode` / `ws.loadDir`; `internal/app/workspace.go:952-1062` (`loadDir`, `loadNode`), `868-936` (`loadChildren`) |
| Multi-select: Ctrl+click toggle, Shift+click range, `explSel map[string]bool` + `explAnchor` | `internal/app/explorer_clipboard.go:286-381` (`toggleExplorerSel`, `extendExplorerSel`, `clearExplorerSel`, `explorerOrder`), `421-441` (`clickTreeSelect`); dispatched from `internal/app/actions.go:626-639` (`open:`/`dir:` with `a.ctrl \|\| a.shift`) |
| Focus-id helper | `internal/app/actions.go:2287-2301` `isTreeRowID`; tree keyboard nav `1023-1211` `treeKeyDown` (arrows/space only, no ops) |
| Validation primitive for any tree path | `internal/app/explorer_clipboard.go:145-173` `lookupTreePath(rel) (isDir, ok)` — rejects `""`, `"."`, absolute, `..`/`../`; checks `findTreeNode(ws.Tree)` → `ws.ByPath` → `a.view.Tree` |
| Path helpers | `internal/app/workspace.go:1341-1357` `baseName`/`dirName`; `539-599` `addDiskFile` (file-only insert, rejects dirs) |

### 1b. Copy / Cut / Paste (the only mutating explorer ops today)
| What | Where |
|---|---|
| Staged-copy store | `internal/app/explorer_clipboard.go:16-81` `PendingCopy{SrcPath, SrcPaths, IsCut}`, package-global `pendingCopy`, `stageCopy/stageCopies/stagedCopy/clearStagedCopy`, `App.pasteStaged *pasteStagedCopy` (alias, `25-27`); `App` field declared `internal/app/app.go:319-323` |
| Context menu rows (explorer) | `internal/app/explorer_clipboard.go:100-143` `contextMenuForExplorer`: empty-space → only `Paste`; file/folder row → `Cut / Copy / Copy Path / Copy Absolute Path / Paste`. Row ids are `contextMenuCutID+rel` etc. |
| Menu id constants | `internal/app/context_menu.go:23-60` (`contextMenuCutID`, `CopyID`, `PasteID`, `CopyPathID`, `CopyAbsID`, all prefixed `code-view:`) |
| Menu dispatch | `internal/app/context_menu.go:560-577` `MenuAction`: `copyTreePath`, `copyTreeAbsPath`, `stageTreeCopy(copyTargets(...))`, `pasteTreeCopy`; `ContextMenuFor` router `:197-246`, bridge `internal/app/context_menu_bridge.go:7-25`, template `components/html/context_menu.html:1-8` |
| Copy staging | `explorer_clipboard.go:192-224` `stageTreeCopy(paths, isCut)` (validates via `lookupTreePath`, writes OS clipboard text, success toast, `refresh()`); `177-190` `copyTargets`; `401-419` `copyExplorerSelection(ctx, isCut)`; `386-396` `explorerCopyReady`; keyboard `actions.go:1831-1835` Ctrl+C/X |
| Paste backend | `internal/app/explorer_paste.go:159-314` `PastePendingCopy/PastePendingCopies/pasteOneSource` (file byte-copy via `ws.createFile`+`ensureFile`; folder recursive via `os.MkdirAll`+`os.CopyFS`); unique-name `434-462` `uniquePasteCopyPath` (`stem (copy).ext`, `(copy 2)…`, cap `maxPasteNameTries=1000`, checks `ByPath` + disk); cut cleanup `339-374` `removeCutSources`; dir resolution `69-148` `ResolvePasteDir/resolvePasteCandidate/cleanPasteCandidate` |
| Paste wire (Ctrl+V / menu) | `internal/app/explorer_paste_wire.go:24-56` `pasteTargetReady` (blocks when `paletteOpen/branchOpen/searchOpen/settingsOpen/fileMenu/browseOpen` or focus in `palette/workspace-search/find/filter/…`), `pasteTargetDir`, `60-82` `writePastedBytes`, `119-207` `pasteFromClipboard/pasteClipboardText`; keyboard `actions.go:1827-1829`; UI entry `explorer_clipboard.go:446-507` `pasteTreeCopy` (toasts + `redrawTreeMenu`) |
| Refresh after paste | `explorer_clipboard.go:509-534` `refreshPastedFile` (lazy: `ws.loadDir(dest)` + `addDiskFile`; full: `rescanDiskWorkspace` + `installRescan`); `explorer_paste.go:264-313` inline lazy/full branches + `sortPasteFiles` (`464-470`) |
| Copy Path behaviors (read-only, exist today) | `explorer_clipboard.go:226-284`: `copyTreePath` (relative slash path → `ownframe.Write`), `copyTreeAbsPath` (via `explorerAbsPath` with WSL/Windows translation `245-271` using `internal/wsl`), `actions.go:2237-2263` `copyActive(false=path, true=whole file bytes)` |

### 1c. Workspace write guards + validation
| What | Where |
|---|---|
| Guards | `internal/app/workspace_write.go:27-30` `writable()` (= `!Sample`); `22-25` `errSampleReadOnly` / `sampleReadOnlyMessage`; `37-66` `createFile` (rejects sample, `..`-escape, existing in `ByPath` or on disk, `MkdirAll` parent, `0o644`/`0o750`); `72-108` `writeFile` (existing files only) |
| Disk mapping | `workspace.go:1188-1221` `diskPath` (abs = external, rel must not escape), `readFile`, `740-833` `ensureFile/ensureExternalFile` |
| Scan/filter rules a new file must survive | `skipDir` (`1101-1111`: `.git/node_modules/dist/build/…`, dot-dirs except `.github`), `skipExt` (`1113-1125`: images/audio/video/archives incl. `.png/.jpg/.pdf` — paste path deliberately still byte-copies them), dotfile rule, symlink skip, `maxTreeDepth=12`, `MaxWorkspaceFiles=10000` |

### 1d. Palette (quick-open only today — no commands)
| What | Where |
|---|---|
| State + rows | `internal/app/palette.go:18-224` `paletteView/paletteRow`, `paletteRows()` (tabs → recents → `ws.Files` prefix-then-contains, plus disk fallback `diskSearchFiles` w/ `750ms` timeout), `237-262` `paletteView`, `264-304` `openPalette/closePalette/movePalette` |
| Absolute + search variants | `internal/app/palette_absolute.go`, `internal/app/palette_search.go` |
| Submit opens file only | `internal/app/actions.go:2140-2222` `onSubmit` → `openFile(rows[index].Path)`; palette keys (`1870-1887`: up/down/enter/esc); open chord `1786-1788` Ctrl+P; template `components/html/palette.html:39-64`, CSS `components/css/palette.css` |

### 1e. Confirm / toast / refresh / watcher / git patterns to reuse
| What | Where |
|---|---|
| Confirm dialog (only consumer: SCM discard) | `internal/app/scm_ops.go:470-575` `openDiscardConfirm/closeConfirm/confirmDiscard/confirmViewState`; `App` fields `confirmOpen/Kind/Paths/Title/Msg` (`app.go:230-234`); template `components/html/confirm.html:1-26` (fixed `Discard` button label, `confirm-ok`/`confirm-cancel` actions in `actions.go:22-30, 485-491, 1632-1648`); opener guards via `scmGuard` |
| Toasts | `internal/app/app.go:3494-3503` `showToast(kind,title,msg)/clearToast`, `toastView`, `actions.go:111-113, 2113-2114` dismiss; layer `components/html/statusbar.html:30-34`, CSS `components/css/window-control.css:567-615` |
| Refresh | `app.go:802-808` `refresh()` (`buildView` + `SetData` + `persistSession`); explorer ops keep scroll via `redrawTreeMenu` (`explorer_clipboard.go:601-609`, nil-safe `page`) vs tab ops which reset scroll (`contextMenuFinish`) |
| Watcher/rescan | `internal/app/watch.go:538-623` `installRescan` (swaps `ws`, `keepExistingTabs`, drops `docs/dirty` for removed paths, re-opens group paths, `syncFileNav`, `rescanNote`), `80-95` `rescan.go:rescanDiskWorkspace/diffWorkspace`; `refreshAfterSCMMutation` (`scm_ops.go:622-629`) as the post-mutation rescan template; `ReloadChanges` for git side |
| Git integration | Explorer mutations do **not** call git today. `discardPaths` (`scm_ops.go:252-312`) + `stageFile/stageAll` show the `gitBytes(ctx, root, …)` pattern; `statusMap` (`app.go:3565-3578`) re-derives badges after rescan; `forgetSCMDoc` (`606-620`) is the template for closing docs on delete/rename |
| Ignored-file display | `workspace.go:514-616` `loadIgnoredPaths/loadIgnoredDirs/markIgnored/addIgnoredDirs`, `Ignored` flag on `fileMeta/fileNode`, `tree-note` in `sidebar.html:35,49` |

---

## 2) Gaps vs requested feature

1. **No New file/folder**: only `createFile` exists as a backend primitive (used by paste). No menu row, palette entry, shortcut, or empty-file flow. No `MkdirAll`-only directory creation path.
2. **No rename**: no `os.Rename` path, no inline input, no tab/doc path migration. `addDiskFile` is file-only and errors on dirs.
3. **No delete**: no `os.Remove/RemoveAll` path for explorer. Only SCM `discardPaths` deletes untracked (`U`) files and `removeCutSources` deletes cut sources after move.
4. **No duplicate**: paste's `uniquePasteCopyPath` (` (copy)` suffix) implements the naming rule but is paste-internal; no one-click "duplicate in place" op.
5. **Context menu incomplete**: `contextMenuForExplorer` offers exactly Cut/Copy/Copy Path/Copy Absolute Path/Paste. Missing New File, New Folder, Rename, Delete, Duplicate (+ separators — template has no separator support).
6. **Palette is file-open only**: `onSubmit` opens the selected file; there is no `>` command mode, no `File: …` commands, no input for a new name.
7. **No name-input UX anywhere**: confirm dialog is title+message+file-list with `Cancel/Discard` only (`confirm.html`); `filter`/`palette`/`project-path` inputs bind single queries. Inline explorer rename input does not exist.
8. **No delete confirmation for explorer paths**: `openDiscardConfirm` filters to `scmChangeFile` paths only — it silently no-ops for untracked/new files. Needs generalization or a parallel explorer confirm.
9. **No post-op doc/tab hygiene for rename/delete**: `installRescan.keepExistingTabs` drops tabs for vanished paths, but open `docs/dirty/editing` entries, `explSel`, `folders` expansion, and `recent` need explicit migration (rename) / cleanup (delete).
10. **No name validation helper**: `cleanPasteCandidate` / `createFile` escape checks exist but there is no shared `validateExplorerName` (empty, `/`, `\`, NUL, `.`/`..`, trailing dot/space on Windows, `skipExt` warning vs block policy).

---

## 3) Design

### 3.1 Model changes (new `internal/app/explorer_ops.go`)

Create `internal/app/explorer_ops.go` (backend, UI-free like `explorer_paste.go`). Extend `workspace_write.go` only with tiny primitives; keep all toast/refresh in `App` wrappers.

```go
// workspace_write.go additions
func (w *workspace) createDir(path string) error
func (w *workspace) renamePath(oldRel, newRel string) error
func (w *workspace) removePath(rel string) error
```

```go
// explorer_ops.go — pure backend (return rels/errors for toasts; no refresh)
func cleanExplorerRel(path string) (string, bool)
func validateExplorerName(name string) error
func (a *App) createExplorerFile(dir, name string) (string, error)
func (a *App) createExplorerDir(dir, name string) (string, error)
func (a *App) renameExplorerPath(oldRel, newName string) (string, error)
func (a *App) deleteExplorerPaths(rels []string) (deleted []string, err error)
func (a *App) duplicateExplorerPath(rel string) (string, error)
func (a *App) forgetExplorerDocs(rels []string, newRel map[string]string)
```

Rules:
- **Same-dir only for v1** rename/duplicate (no drag-move). Cross-dir move stays Cut/Paste.
- **Never overwrite**: duplicate auto-suffixes; rename/new fail with `file exists %s` and surface as error toast.
- **Sample workspace**: all five ops return `errSampleReadOnly()` when `!writable()`.
- **Ignored files**: allowed (display-only flag). After op, re-run ignore marking via rescan so badges are correct.

### 3.2 New actions + routing (`context_menu.go`, `actions.go`)

New menu-id constants alongside existing (`context_menu.go:23-60`):

```go
contextMenuNewFileID  = contextMenuIDPrefix + "new-file:"
contextMenuNewFolderID = contextMenuIDPrefix + "new-folder:"
contextMenuRenameID   = contextMenuIDPrefix + "rename:"
contextMenuDeleteID   = contextMenuIDPrefix + "delete:"
contextMenuDuplicateID = contextMenuIDPrefix + "duplicate:"
```

- `contextMenuForExplorer` (`explorer_clipboard.go:106-143`): row menu becomes `New File / New Folder / ─ / Cut / Copy / Copy Path / Copy Absolute Path / Paste / ─ / Duplicate / Rename / Delete`. Empty-space becomes `New File / New Folder / Paste`. Requires separator support: add `{ID:"", Label:"-"}` convention + `context_menu.html` `<hr>` branch.
- `MenuAction`: new branches — `new-file`/`new-folder` → `beginExplorerInput`, `rename` → `beginExplorerInput`, `duplicate` → direct exec + toast + refresh, `delete` → `openExplorerDeleteConfirm`.
- `onClick`: `explorer-input-submit` / inline row buttons reuse same handlers; `confirm-ok` must branch on `confirmKind`.
- Keyboard: with `explorerCopyReady`-style gate add `explorerOpReady()`: `Delete/Backspace` → delete confirm; `F2` → rename; New/Duplicate are menu+palette only in v1 (avoid `Ctrl+N`/`Ctrl+D` conflicts).

### 3.3 Palette entries (command mode, minimal)

- **`>` command mode in existing palette**: if `paletteQuery` starts with `>`, `paletteRows()` returns filtered command rows (`File: New File`, `File: New Folder`, `File: Rename`, `File: Delete`, `File: Duplicate`) instead of file rows. `onSubmit` dispatches command. Target = focused `tree-<path>` row → `resolvePasteCandidate`, else active doc dir, else root (same precedence as `pasteTargetDir`).
- Template: `palette.html` row block gains `data-action="palette-cmd:<id>"` branch.

### 3.4 Inline name input (new-file / new-folder / rename)

- `App` fields: `explorerInput explorerInputState{Mode, Dir, Target, Value, Open}`.
- `sidebar.html`: when open, render inline row (new: under `Dir`; rename: in place of target) with `<input id="explorer-input">`. Focus via `page.Form().Focus(ctx, "explorer-input")`.
- `onChange`: `case "explorer-input"` updates value + refresh. `onSubmit`: commit branch + Escape cancel.
- Validation errors stay inline (red border + toast on commit): reuse `validateExplorerName` + `cleanExplorerRel`.

### 3.5 Confirmations + error toasts

- **Delete → confirm dialog**. Add `openExplorerDeleteConfirm(rels)` setting `confirmKind="explorer-delete"`, title `Delete N file(s)?`, msg `This deletes from disk and cannot be undone.` + same file list. `confirmDiscard` branches for explorer-delete. `confirm.html` button label parameterized to `{{.Confirm.OkLabel}}` (Delete vs Discard).
- Toasts via `showToast`: success create/rename/duplicate/delete, error with `err.Error()`, info for no-op. Sample workspace shows read-only toast.

### 3.6 Watcher / rescan hooks

New helper `refreshExplorerAfterMutation(minted, removed)`:
1. `forgetExplorerDocs` first (migrate/close docs, `finishEdit` if editing deleted path, clear `explSel`, drop `folders[target]` on delete, set `folders[dir]=true` on create).
2. Lazy (`ws.Lazy`): `ws.loadDir(parent)` per affected parent + `addDiskFile`/`addDiskDir`; fall back to full rescan on error.
3. Full: `rescanDiskWorkspace` + `installRescan` — preserves tabs, sets `watchNote`.
4. `refresh()` + `redrawTreeMenu` (keep scroll).
5. Git: no explicit staging; badges recompute on next tick.

## 4) Phase-wise checklist

### Phase 0 — Shared validation + tests (no UI)
- [ ] `explorer_ops.go` + `cleanExplorerRel` + `validateExplorerName`
- [ ] `workspace_write.go` + `createDir` / `renamePath` / `removePath`
- [ ] `explorer_ops_test.go` + traversal + sample read-only cases

### Phase 1 — Backend ops
- [ ] `createExplorerFile` / `createExplorerDir` / `renameExplorerPath` / `deleteExplorerPaths` / `duplicateExplorerPath`
- [ ] `forgetExplorerDocs` + `refreshExplorerAfterMutation` (lazy vs full)
- [ ] Round-trip tests on temp roots + lazy-workspace cases

### Phase 2 — Context menu + keys
- [ ] Five `contextMenu*ID` constants + `contextMenuForExplorer` rows + separator support
- [ ] `MenuAction` branches + `explorerOpReady` gate + `Delete`/`F2` keys
- [ ] `context_menu_test.go` row assertions

### Phase 3 — Inline input + confirm
- [ ] `explorerInput` state + sidebar inline row + `onChange`/`onSubmit`/cancel
- [ ] `beginExplorerInput/commitExplorerInput/cancelExplorerInput`
- [ ] `openExplorerDeleteConfirm` + `confirmDiscard` branch + `OkLabel`

### Phase 4 — Palette commands
- [ ] `>` command mode + `runPaletteCommand` + `palette-cmd:` rows + tests

### Phase 5 — Polish + gates
- [ ] Toast copy audit + focus restoration + `folders`/`explSel`/`recent` hygiene
- [ ] `go test -p 1 ./...` green + manual sweep

## 5) Acceptance + gates

1. Right-click file → full menu; empty space → New File/Folder + Paste.
2. Each op works from menu AND palette `>` commands.
3. Inline prompt: Enter commits, Esc cancels; invalid names error without disk change.
4. Rename migrates tabs/docs/dirty; duplicate suffixes ` (copy)`; delete confirms with `Delete` label.
5. Sample workspace: read-only toast, no writes.
6. Tree updates without manual refresh (lazy + full); scroll preserved.
7. Gates: `go vet ./...`, `go test -p 1 ./internal/app/ -run 'Explorer|Palette|ContextMenu|Paste|Clipboard|Workspace'`, then full `go test -p 1 ./...`.

## 6) Risks

- **Path traversal**: `../`/abs/NUL must never reach `filepath.Join`. Single choke point + tests.
- **WSL**: operate on workspace-relative paths only; test with `WSL_DISTRO_NAME`.
- **Ignored/skipped files**: creating `skipExt`/ignored paths warns but still creates; document scanner rules.
- **Data loss**: delete confirms; rename/new never overwrite; `finishEdit` only after disk success.
- **Watcher races**: re-validate at commit; fall back to full rescan.
- **Singleton tests**: `pendingCopy` global requires `-p 1`; use `t.TempDir()` roots.
