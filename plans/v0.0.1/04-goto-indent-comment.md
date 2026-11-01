# Plan: Go-to-Line / Symbol, Bracket Match, Auto-Indent, Toggle Comment, Indent/Outdent

## 1. Current behavior + file refs

### Outline / Details drawer
- `internal/app/outline.go:17` — `outline(lang, lines)` lightweight scanner, capped at `maxOutlineRows=60`, `maxSymbolRunes=64`.
  - Go (`outlineGo:55`): `func/type/const/var` prefix only; misses methods inside `(` blocks except receiver-strip.
  - Python (`outlinePython:82`): `def/async def/class`, depth = leading-spaces/4. Tab-indented code mis-depths.
  - JS (`outlineJS:101`): `function/class/export…`, `const/let/var … = (|async|function`. Misses arrow `=>`, object methods, TS `interface/enum`.
  - Markdown (`outlineMarkdown:127`): ATX headings via `headingLevel`, skips fenced code. No setext.
  - CSS (`outlineCSS:165`): lines ending in `{`, kind `rule/at`.
  - No symbols for JSON/HTML/YAML/Shell/plain.
- `internal/app/view.go:547` — `symbolRow{Name,Kind,Line,No,Depth}`.
- `internal/app/workspace.go:1279,1336` — `fileDoc.Outline` built in `openDoc`; refreshed in `edit_ops.go:171` `commitEditBuffer`.
- `internal/app/app.go:3527,3537` — `outlineLine(line)` → `scrollToSourceLine(line)`.
- `internal/app/actions.go:682` — `outline:<n>` action; `input_test.go:350 TestOutlineClickScrolls`.
- `internal/app/app.go:3560` — `toggleDetails()`; key `3`, `details-toggle`/`details-close` actions.

### Palette (prior art for quick-jump UI)
- `internal/app/palette.go:18,37` — `paletteRows()`, `paletteView()`; default 5 rows, max 8; absolute-path row, tabs → recent → workspace → disk `rg/git/walk` (`palette_search.go:17`).
- `actions.go:1786,1870,2142` — `Ctrl+P` open, arrows/enter/escape; `onChange:721` resets `paletteIndex`.

### Edit buffer + key pipeline
- `internal/edit/edit_buffer.go:10` — `Buffer{Path,Lines,Dirty,Original}`; CRLF→LF, empty file = 0 lines.
- Limits (`workspace.go:17`): `maxFileBytes=512KB`, `maxFileLines=20000`, `maxLineBytes=32KB`.
- `edit_ops.go:11 toggleEdit`, `58 beginEditAtLine`, `63 beginEditAt`, `124 applyEditText`, `136 finishEdit`, `152 commitEditBuffer`, `181 saveEdit`.
- `text_edit.go:27 onType` — CRLF→LF, `\t` → `tabSpaces="    "`, then `insertEditText`. No auto-indent, no bracket-close, no comment logic.
- `text_edit.go:262 editKey` — only `Ctrl+S`; arrows/home/end/pagedown/delete/escape. **Enter is not here** — `actions.go:2143 onSubmit` does `insertEditText(ctx,"\n")`. No indent copy.
- `actions.go:1574 onKeyDown` — global dispatch; **no `Ctrl+G`, `Ctrl+/`, `Tab` hook exists while editing**. `Ctrl+Z/Y` explicitly no-op.
- Undo: `text_edit.go:13 editUndoLimit=100`, snapshot whole `Text+Caret+Anchor`; `replaceEditRange:334` clamps, clears redo, `markDirty`, `commitEditBuffer`, `ensureEditCaretVisible`, redraw.
- Selection: `selection_ops.go`, `edit_hit.go` visual→source mapping; `workspace.go:18 tabWidth=4`, `1229 detectIndent`.

### Syntax / language data usable for indent + comments
- `syntax.go:47` langs: `go/python/javascript/markdown/css/json/html/yaml/shell/plain`; `cSpecFor:414` already encodes `lineComment` (`//`), `hashComment` (`#`), `blockComment` (`/*…*/`). No comment table for JSON/HTML/YAML/Shell/CSS/Markdown.
- `syntaxCache:163 markDirty/tokenize:191` incremental; multi-line edits must mark range.

### Markdown preview interaction
- `markdown_preview.go:43 ensurePreview`, `73 previewRows`; `app.go:3505 togglePreview` (Markdown-only, `finishEdit()` on entering preview). Goto/bracket/comment/indent must be disabled or auto-exit preview.

## 2. Gaps

| Feature | Gap |
|---|---|
| Go to line | No parser for `:line:col`, no input affordance, no caret-move+scroll API. Palette only matches paths. |
| Go to symbol | Outline click-only, no filter/keyboard, 60-row cap, missing langs. |
| Bracket match | No bracket scanner; highlighter discards nesting. No flash channel. |
| Auto-indent | Bare `\n` insert; no indent copy, no extra level after `{:(`, no dedent. |
| Toggle comment | No command; no comment map outside `cSpec`. |
| Indent/outdent Tab | `Tab` arrives as text (4 spaces); `Shift+Tab` unbound; no line-range op, no undo coalescing. |

## 3. Design

### 3.1 Ctrl+G Go to line (`:line[:col]`)
- **UX:** `Ctrl+G` opens palette overlay in *goto mode* (new `paletteMode="goto"` or separate `gotoOpen`/`gotoQuery` to avoid breaking `palette_test.go`). Input prefilled `:`; hint `:<line>[:<col>]`. `Enter` jumps, `Esc` closes.
- **Parsing:** `parseGoto(query, maxLine, maxCol)`: accept `:42`, `42:7`, `42`; 1-based; clamp with toast `Line 9999/170`.
- **Jump:** `gotoLine(line, col)` — exit preview if needed; `beginEditAt` or set caret via `edit.OffsetAtPoint` + `ensureEditCaretVisible`; collapse anchor; never blanket `resetScroll`.
- New `internal/app/goto.go`; `onKeyDown` branch `ctrl+g` before editing branch; also accept `:` prefix in palette as alias.

### 3.2 Outline quick-jump (`Ctrl+Shift+O` + Details stays)
- `Ctrl+Shift+O` opens palette in *symbol mode*: lists `doc.Outline` filtered fuzzily (prefix→contains ranking). Row shows kind badge + name + `:No` + depth indent. Enter → `outlineLine(sym.Line)` + caret move; Esc closes.
- Data fixes: extend `outline()` — JSON (`"key":`), YAML (`^[\w-]+:`), HTML tags/ids, Shell (`function name`/`name()`); skip comment lines; keep 60-cap for drawer but full list for palette.
- Files: `outline.go` extensions, `palette_symbol.go` / `symbolRows(query)`, key branch + `onSubmit` routing.

### 3.3 Bracket jump / select with flash
- Scope `() [] {}` (`<>` excluded v1).
- `bracketMatch(text, caret)`: look at `runes[caret]`/`runes[caret-1]`, walk with depth, skipping strings/comments via `scanLineWithState`/`tokenize` (carry block state across lines). Exact-hit v1; else toast.
- Bindings: `Ctrl+M` jump, `Ctrl+Shift+M` select to match. Flash via `a.bracketFlash` + `markBracketRows` mirroring `markFindRows`; CSS `bracket-flash` persistent until next move (no timer).
- New `internal/app/bracket.go`; handle inside `editKey` ctrl-switch + `onKeyDown`.

### 3.4 Auto-indent on Enter
- Replace bare `insertEditText(ctx,"\n")` in `onSubmit` with `insertNewlineWithIndent(ctx)`.
- Rule v1: `base = leadingWhitespace(prev)`; extra `+tabSpaces` if Go/JS/CSS/JSON/HTML prev ends with `{(/[`; Python/Shell/YAML ends with `:`; Markdown list repeats marker. Single undo snapshot.
- Files: `text_edit.go` (`insertNewlineWithIndent`, `indentForNewline`), one-line `actions.go` call change.

### 3.5 Ctrl+/ toggle comment
- Binding `Ctrl+/` while editing; table: Go/JS `//` (+`/* */`), Python/Shell/YAML `#`, CSS `/* */`, HTML/Markdown `<!-- -->`, JSON → toast. Derive where possible from `cSpecFor`, rest in new `comment.go`.
- Behavior: line-range toggle; all-commented → uncomment (strip one prefix + optional space), else comment (`prefix + " "` after indent; skip blanks). Preserve indent; one undo; loop `markDirty`.
- New `internal/app/comment.go` (`commentPrefix`, `toggleLineComment` pure).

### 3.6 Tab / Shift+Tab indent & outdent
- Intercept `Tab`/`Shift+Tab` in `onKeyDown` before `onType` when editing (verify key-vs-text delivery; keep `onType` expand as fallback).
- Collapsed: Tab inserts `tabSpaces` (or tab-stop); Shift+Tab strips one level. Multi-line: indent every line in range (exclude last line if selection ends at col 0); single undo; expand selection to block.
- New `internal/app/indent.go` (`indentLines` pure + `indentSelection`).

## 4. Checklist

- [ ] Recon key names for `Ctrl+G`, `Ctrl+/`, `Tab`/`Shift+Tab`, `Ctrl+M`, `Ctrl+Shift+O` (window key map; Enter path audit).
- [ ] `goto.go`: `parseGoto` + `gotoLine` + palette-mode plumbing + `Ctrl+G` + `onChange:"goto"` + template.
- [ ] `outline` extend: comment skip, JSON/YAML/HTML/Shell, drawer-60 vs palette-full split.
- [ ] Symbol palette: `symbolRows(query)` + ranking + `Ctrl+Shift+O` + `onSubmit` routing.
- [ ] `bracket.go`: `matchBracket` pure + jump/select + flash + keys.
- [ ] `insertNewlineWithIndent` + `indentForNewline`; swap `onSubmit` call.
- [ ] `comment.go` + `Ctrl+/` wiring.
- [ ] `indent.go` + Tab interception.
- [ ] Dirty-range correctness (loop `markDirty`); preview guards (no-op/exit with toast).
- [ ] Docs + accessibility labels for new boxes.

## 5. Acceptance + tests

- `Ctrl+G :42:7` moves caret visibly; `:99999` clamps with toast; Esc closes; works in/out of edit; preview exits/maps.
- `Ctrl+Shift+O` filters + Enter jumps; drawer unchanged.
- `Ctrl+M` skips strings/comments; `Ctrl+Shift+M` selects; flash until move; unmatched toasts.
- Enter indents per lang; Markdown repeats list marker.
- `Ctrl+/` toggles per lang, idempotent, single undo; JSON toasts.
- Tab/Shift+Tab indent/outdent incl. `end-at-col-0` exclusion, single undo.
- New tests: `goto_test.go` (`parseGoto` table + caret/scroll), outline extension tables, `palette_symbol_test.go`, `bracket_test.go` (pure + integration), `text_edit_indent_test.go`, `comment_test.go`, `indent_test.go`.
- Regression: `go test ./internal/app/ ./internal/edit/` incl. `TestOutlineClickScrolls`, `TestDetailsMetadata`, `TestEditUndoRedoHistory`, `TestQuickOpenPalette`, `TestMarkdownPreview`.

## 6. Risks

- **Comment styles:** new table needed beyond `cSpec`; JSON none → toast; block-toggle v2.
- **Tabs vs spaces:** buffers tab-expanded; `detectIndent` read-only. v1: use `doc.Indent` (`\t` vs N spaces); document.
- **Large files:** 20k-line rebuild per keystroke; keep single-`replaceEditRange`, avoid per-line pushes.
- **Preview+dirty:** refuse/mirror via `finishEdit`/toast; never mutate preview rows.
- **Key routing:** Tab-as-text vs key, terminal focus, form-field focus, `slash` layout variants; claim before `treeKeyDown`/terminal like `Ctrl+I`.
- **Undo:** one snapshot per gesture; note 32KB×100 memory shape.
- **Wrap cols:** round-trip `rawColumnAt`/`displayColumn`; clamp defensively.
