# Plan: Go to Definition (symbol index only, no LSP)

## 1. Spec

- F12 stays with the dev dock (`settings.go` marks `devtools`/`devtools-alt` Fixed; `isReservedChord` rejects rebinding to F12). The definition chord is **Ctrl+D**: verified free — no other `onKeyDown` branch consumes Ctrl+D, no catalog default uses it (`resolveChord(nil, "Ctrl+D")` returns only `goto-definition`), and the only bare `"d"` consumer is the confirm dialog's Don't-Save key (`actions.go`, non-ctrl).
- New keymap action id **`goto-definition`**, default `Ctrl+D` (`settings.go:624`), rebindable through Settings → Keyboard like every other action; `runKeymapAction` (`settings.go:972-973`) fires custom rebinds.
- Same action id doubles as the click action (`actions.go:519-520`) and the `>` palette command id (`paletteCmdGotoDefinition`, `explorer_ops.go:45`), cataloged as "Go: Go to Definition" (`explorer_ops.go:1057`).
- Resolution order for the word under the caret:
  1. Current file outline, exact name, over the live buffer (`outlineFull(doc.Lang, lines)` in `goto_definition.go:305-311`) — unsaved edits resolve.
  2. Workspace symbol index, exact name, ordered by recency (open tabs, then recents; unranked last), then shortest path, then path, then line (`definitionCandidates`, `goto_definition.go:157-212`). Same-file hits are excluded (step 1 owns them).
  3. Textual fallback: literal case-sensitive workspace scan; zero hits → `Definition not found for "w".`; N hits → `Definition not found for "w"; N textual matches — see workspace search.` with the query opened in workspace search (`goto_definition.go:323-343`).
- Word extraction (`symbolAtCaret`, `goto_definition.go:74-97`): prefer the syntax token at the caret when the highlighter marked it `fn`/`type`/`var` (tokens trusted only when the buffer line still equals the document line they were built from and the segments still partition it); else identifier runes (`isIdentRune`: letters, digits, `_`, `$`) around the caret, resolving to the left word at boundaries.
- Jump (`jumpToDefinition`, `goto_definition.go:349-357`): same file → caret move in place; cross-file → `openFile` (pins, dirty-guard dialogs, records nav history) + `outlineLine` scroll + `gotoLine` caret move. Reuses the normal open path, so preview tabs, dirty guards, and nav history behave like normal opens.
- Ambiguous (2+ workspace candidates): jump to the best, toast `N definitions for "w"; jumped to path. Ctrl+T lists every match.` (`goto_definition.go:315-319`).
- Workspace candidates skip vendor/ignored/generated (`gotoSkipCandidate`, `goto_definition.go:218-238`): `meta.Ignored`, any path segment matching `skipDir` (vendor, node_modules, dist, dot dirs), `skipExt` extensions, and generated sources (`gotoGeneratedFile`: `*.pb.go`, `*.gen.go`, `*generated*` names, or a `code generated` / `@generated` / `auto-generated` marker in the first 8 lines).
- Entry points: Ctrl+D out of edit mode (`actions.go:2187-2189`) and mid-edit (`text_edit.go:324-332`, gated by `keymapAllows` so a rebind kills the old chord); Ctrl+Click on a code line (`actions.go:edit-begin-line` branch — Press already placed the caret from the click point, entering edit mode first when the file was in token view).

## 2. Checklist (boxes checked only with test evidence)

- [x] Same-file jump lands the caret on the definition line, no toast — `TestGotoDefinitionSameFile` (caret on `once` call site in `backend/probe.go` → line 134, the `func (p *Prober) once` method; path unchanged, toast hidden).
- [x] Cross-file jump prefers the real definition file — `TestGotoDefinitionCrossFile` (caret on `Service` in `probe.go` → `backend/main.go` line 27, `type Service`; toast hidden) and `TestGotoDefinitionCrossFilePreference` (injected stale `vendor/vv/widget.go` row does not shadow `defs.go`).
- [x] Fallback message with textual-match count + workspace search offered — `TestGotoDefinitionFallbackOpensSearch` (caret on `beacon`, string-only text → toast contains `Definition not found` + `textual matches`; `searchOpen` with query `beacon`; file unchanged) and `TestGotoDefinitionNoMatch` (buffer-only `zzzqxj` → exact `Definition not found for "zzzqxj".`, search stays closed).
- [x] Vendor/ignored/generated skipped — `TestGotoSkipCandidate` table (`vendor/`, `node_modules/`, `dist/`, gitignored meta, `genmark.go` via its on-disk `Code generated` header, `*.pb.go`, `*.gen.go`, `img.png` skipped; plain files kept), `TestGotoDefinitionSkipsIgnored` (ignored `dup2.go` drops out of candidates), ambiguous jump lands on `dup1.go` while `genmark.go` also defines `DupThing` (`TestGotoDefinitionAmbiguousJumpsToBest`).
- [x] Nav history recorded — `TestGotoDefinitionCrossFile` (`canNavBack`, `navBackPath() == "backend/probe.go"`) and `TestGotoDefinitionNavHistory` (`goNavBack()` returns to `backend/probe.go`).
- [x] Keybinding — `TestGotoDefinitionKeybinding` (live chord is `Ctrl+D`, resolves to only `goto-definition`, `onKeyDown` Ctrl+D jumps while editing), `TestGotoDefinitionKeybindingNotEditing` (token-view Ctrl+D reaches the handler and asks for a caret), `TestGotoDefinitionCtrlClick` (Ctrl+Click on the `once` call site jumps to line 134), `TestGotoDefinitionPaletteCommand` (catalog contains the command; running it jumps).
- [x] Recency then shortest-path ordering — `TestGotoDefinitionRecencyBeatsShortPath` (recent tab `dup2.go` outranks alphabetically-first `dup1.go`) and `TestGotoDefinitionAmbiguousJumpsToBest` (unranked tie breaks to shortest path, then `dup1.go`).
- [x] Word extraction edges — `TestGotoDefinitionWordAt` (mid-word, end-of-word, boundaries, empty buffer, `+` separator).

## 3. Validation

- `gofmt -l internal/app/` — clean.
- `go vet ./internal/app/` — clean.
- `go test -p 1 ./internal/app/ -run 'TestGotoDefinition|TestOutline|TestSymbol|TestPalette|TestNav' -count=1` — ok (1.3s).
- Full suite `go test -p 1 ./internal/app/ -count=1` — ok (165s), no regressions.
- Environment note: the sibling `../ownframe` checkout now requires `blinkless v0.1.1`, so `go mod tidy` bumped one indirect pin in `go.mod`/`go.sum` (pre-existing drift, unrelated to this change). Left uncommitted with the rest.
