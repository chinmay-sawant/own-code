# 22 - Liquid Glass theme consistency

Scope: themes `glass` (Liquid Glass) and `glass-light` (Liquid Glass Light).
Flat themes (`midnight`, `light`) are the control and must not change.

## Method

- `internal/app/screenshot_glass_test.go` (gated, `OWNFRAME_CODE_VIEW_SCREENSHOT=1`) renders every
  major surface at 1600x900 under `glass`, `glass-light`, `midnight`, `light`. It sets the theme the
  way the app does (`app.theme = ...; page.SetTheme(app.activeThemeCSS())`).
  Run: `OWNFRAME_CODE_VIEW_SCREENSHOT=1 xvfb-run -a go test -p 1 -count=1 -run TestScreenshotGlass ./internal/app`
  Frames land in `/tmp/opencode/code-view-shots/glass-<theme>-<NN>-<surface>.png`.
- Flat-theme frames were byte-compared before and after the change. Only the commit-hash text in
  history rows (random per fixture repo) and the toast frame (toast was not refreshed in the first
  run) differ, so the flat themes are visually identical.
- ownframe CSS subset: only existing `backdrop-filter: blur() saturate() refract()` and plain
  fills/gradients/borders are used; no new properties. `* { box-shadow:none }` already removes
  shadows under glass, so heavy shadows are not an issue.

## What was inconsistent (before)

Glass only covered `.palette/.settings-panel/.confirm-panel/.inline-card/.commit-tip-float/
.recent-flyout`, `.sidebar/.tabstrip`, `.titlebar/.statusbar` and settings internals. Everything else
kept the opaque `--panel/--panel-2` fill of the flat themes.

## Fixes

All in `internal/app/themes.go` (token or glass rule based; none in files other agents are editing):

- `glassSurfaceCSS`: new rules appended to the glass output.
  - Small floating widgets (`.ctx-menu .popup .file-drawer .browse-panel .file-alert .hist-tip
    .hist-preview`, plus `.toast .file-nav`) get the translucent fill, edge highlight, blur and
    refract of the large panels. Toast keeps its per-kind left border; file-nav keeps its accent border.
  - Docked chrome (`.details .activity .terminal`) uses the sidebar glass recipe; nested panes
    (`.history .term-head .term-foot .term-note`) go transparent so they do not double-tint.
  - Inset fields (`.filter .branch-input .find-input .explorer-input* .commit-message .cmdbar
    .project-input .browse-filter .palette-input`) get a translucent inset tint.
  - Buttons (`.crumb-btn .commit-btn .confirm-btn .button .hist-btn .replace-btn .seg .term-tab
    .quick-row .welcome-card`, excluding primary/danger/on/stage states) get the settings-button gradient.
  - `.browse-backdrop` scrim now matches `.palette-back`; popup/menu hover rows use `--accent-soft`.
- `glassTokenCSS` + new tokens in `components/css/base.css`: hard-coded merge-conflict reds/green/amber,
  meta-row amber washes and find-hit/find-cur colors became `var(--tint-*)`, `--find-hit`, `--find-cur`,
  `--find-cur-text`. Defaults equal the old literals (flat themes unchanged); glass themes retint them
  with their own red/green/amber. Files touched: `merge.css`, `diff.css`, `history.css`, `base.css`.
- Contrast: `glass-light` `--dim` was `#7c8ba0` (2.9-3.5:1 on its panels, visible as washed-out paths in
  the commit dialog, details labels, history). Now `#5c6b80` (4.5-5.4:1). Glass dark `--dim` was already
  4.5-6.2:1; muted/text are 9-15:1 on both.

## Checklist

Images: `.../scratchpad/glass/compare/<theme>-<surface>-{before,after}.png`
(`/tmp/claude-1000/-home-chinmay-ChinmayPersonalProjects-own-code/59781964-37fe-4148-879e-37624f339527/scratchpad/glass/`).
Four-theme contact sheets (glass, glass-light / midnight, light): `sheets/before/` and `sheets/after/`.
Raw originals: `before-raw/`. Frame numbers refer to `glass-<theme>-<NN>-<name>.png`.

| # | Surface | Before (glass) | After | Compare image |
|---|---------|----------------|-------|---------------|
| 01 | Explorer + editor tabs | glass (sidebar/tabs/title/status); inputs and buttons flat | fields/buttons glass-tinted | sheets/after/01 |
| 02 | Edit mode + find bar | find input and buttons opaque panel-2 | translucent inset/gradient | 02-edit-find |
| 03 | Split editor | OK (tab strips glass) | OK | sheets/after/03 |
| 04 | Details drawer + outline | flat opaque `--panel` | sidebar glass recipe | 04-details-drawer |
| 05 | Markdown preview | content surface, intentionally opaque | unchanged | sheets/after/05 |
| 06 | Image preview | content surface, opaque | unchanged | sheets/after/06 |
| 07 | Source Control + history | history pane opaque inside glass sidebar; buttons flat | history transparent over sidebar glass; buttons gradient | 07-source-control-history |
| 08 | Diff unified | content surface | unchanged | sheets/after/08 |
| 09 | Diff split | content surface | unchanged | sheets/after/09 |
| 10 | Merge conflict | hard-coded `#f85149`-family washes clashed with glass red | `--tint-red-*` follow glass red | 10-merge-conflict |
| 11 | Commit dialog | panel glass; textarea/buttons flat; faint paths in glass-light | field/buttons glass; dim contrast fixed | 11-commit-dialog |
| 12 | Confirm dialog | panel glass; buttons flat | buttons glass (danger kept) | sheets/after/12 |
| 13 | Toast | opaque panel | glass fill, kind border kept | 13-toast |
| 14 | Context menu | opaque panel-2, 6px radius | glass, 10px radius, accent hover | 14-context-menu |
| 15 | Command palette | glass | glass, input tinted | 15-command-palette |
| 16 | Workspace search | glass | glass, inputs tinted | sheets/after/16 |
| 17 | Settings (all 5 tabs) | glass | unchanged (reference look) | 17-settings-appearance, sheets/after/17-* |
| 18 | Terminal panel | opaque panel + opaque header/footer | glass shell, transparent header/footer; body keeps `--term-bg` for ANSI contrast | 18-terminal |
| 20 | Diagnostics squiggle/status | n/a (text colors only) | unchanged | sheets/after/20 |
| 21 | Problems list in Details | drawer flat | drawer glass | sheets/after/21 |
| 22 | Popup list | opaque panel-2 | glass | 22-popup-list |
| 23 | Popup card | opaque panel-2 | glass | 23-popup-card |
| - | Activity bar | opaque panel | glass | any frame |
| - | Status bar / title bar | glass | glass, cmdbar tinted | any frame |
| - | Scroll rails | universal mid-gray by design | unchanged | - |
| - | Welcome (no tabs) | welcome-card opaque | gradient button/card | not in matrix |

## Remaining / intentional

- Editor body, code/diff/preview/image surfaces, and the terminal body stay opaque: they are text
  surfaces where translucency would hurt legibility, and there is nothing behind them to refract.
- `.lang-*` badges, `.swatch-*` previews, `.win-close` red, the SVG rail gray and `.change-wrap.multi`
  tint are fixed literals (see budget below).
- Backdrop blur is only visible where something differs behind a panel; over the flat dark canvas the
  glass reads as a gradient plus edge highlight. That is the engine's limit, not a missing style.
- Settings "Font size" row overflows the panel bottom at 900px height in every theme (layout, not glass).
- The thick light divider between sidebar and editor is in all themes (resize handle), not glass-specific.
- Terminal and popup shots were taken with the LSP fake from other work in progress; the hover popup
  being added elsewhere reuses `.popup` and therefore inherits the glass rules.

## Guard test

`internal/app/css_colors_test.go` (`TestComponentCSSColorBudget`) strips comments and `--token: value;`
declarations from every `components/css/*.css` and fails if a file exceeds its allowlisted count of
`#hex` / `rgb()` / `rgba()` literals (or a new file has any). New colors must be `var(--...)` tokens.
Budgets: base 24 (lang badges), settings 18 (swatches), sidebar 9, window-control 5, editor 4,
merge 4, inline_card 3, palette 2, history/file_nav/popup 1, the rest 0. It cannot check HTML
inline `style=` attributes or Go-side theme strings, only component CSS.

## Second pass: edge lines (after user review of the live app)

The user found the dark glass theme harsh: near-white outlines and bright rules.

| Item | Cause | Fix |
|---|---|---|
| Near-white rims on inputs, tabs, buttons, cards, settings rows | `glassCSS` and `glassSkin` set `border-color` to 1px translucent `rgba(225,239,255,0.13-0.25)`, which renders near-white on glass | Opaque blended hairlines in `themes.go` (dark `#334055`, `#2a3445`; light `#cdd5df`, `#c3ccd8`) |
| Bright dividers everywhere | `--line` / `--line-soft` took the catalog rgba for glass | `lineFor` / `lineSoftFor` return opaque blends for glass |
| Muddy sidebar gradient vs flat editor | Chrome tint alpha 0.19-0.20 plus a strong gradient | Calmer tint (0.05 gradient, 0.78-0.82 base) on sidebar, tab strip, title and status bars |
| Thick pale divider beside the sidebar | `.drawer-resize` SVG had a fixed `#8f8f8f` fill | SVG fills with `currentColor`; default unchanged for flat themes, glass sets a hairline colour |
| Blue line above the tab strip | `.editor.focus` 2px accent border | 1px soft accent for glass and glass-light |

Verified by headless frames (`TestScreenshotGlass`) for `glass`, `glass-light`, and the flat `midnight` (unchanged).
Known: the coloured blur blob in the Settings frame is ownframe's glass lens effect at the default pointer position; it appears in the "before" frames too and is not part of this change.
