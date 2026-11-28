package app

import (
	"context"
	"html"
	"strconv"
	"strings"

	"github.com/chinmay-sawant/ownframe"
)

// patchDiffView updates #code-body, #crumbs, #void, #file-nav, #statusbar, and
// sidebar selection classes in place via a single batched PatchElements call
// when switching working-tree diff files or toggling split/unified mode.
// Returns false when structural chrome changed or ownframe refuses the patch,
// so the caller falls back cleanly to a full SetData refresh.
func (a *App) patchDiffView(ctx context.Context, prev, next View) bool {
	return a.patchDiffViewErr(ctx, prev, next) == nil
}

func (a *App) patchDiffViewErr(ctx context.Context, prev, next View) error {
	if !canPatchDiffView(prev, next) || a.page == nil {
		return ownframe.ErrPatchRefused
	}
	return a.page.PatchElements(ctx, buildDiffPatches(prev, next))
}

func buildDiffPatches(prev, next View) []ownframe.ElementPatch {
	patches := make([]ownframe.ElementPatch, 0, 8)

	// 1. #diff-rows: normal-flow container inside #diff-track holding all diff rows.
	rowsHTML := renderDiffRowsHTML(next)
	patches = append(patches, ownframe.ElementPatch{
		ID:    "diff-rows",
		Edits: []ownframe.ElementEdit{{InnerHTML: &rowsHTML}},
	})

	// 2. #crumbs: diff header controls & file path.
	crumbsHTML := renderDiffCrumbsHTML(next)
	patches = append(patches, ownframe.ElementPatch{
		ID:    "crumbs",
		Edits: []ownframe.ElementEdit{{InnerHTML: &crumbsHTML}},
	})

	// 3. #void: scroll spacer height when total diff height changed.
	if prev.SpaceHeight != next.SpaceHeight {
		voidHTML := `<div class="scroll-space" style="height:` + strconv.Itoa(next.SpaceHeight) + `px"></div>`
		patches = append(patches, ownframe.ElementPatch{
			ID:    "void",
			Edits: []ownframe.ElementEdit{{InnerHTML: &voidHTML}},
		})
	}

	// 4. #file-nav: bottom-center changed-files navigator.
	if prev.FileNav.Open && next.FileNav.Open && prev.FileNav != next.FileNav {
		navHTML := renderFileNavInnerHTML(next.FileNav)
		patches = append(patches, ownframe.ElementPatch{
			ID:    "file-nav",
			Edits: []ownframe.ElementEdit{{InnerHTML: &navHTML}},
		})
	}

	// 5. #statusbar: addition/deletion counts and encoding/indent when changed.
	var sbEdits []ownframe.ElementEdit
	if prev.Diff.Add != next.Diff.Add {
		t := strconv.Itoa(next.Diff.Add) + " additions"
		sbEdits = append(sbEdits, ownframe.ElementEdit{ID: "sb-diff-add", Text: &t})
	}
	if prev.Diff.Del != next.Diff.Del {
		t := strconv.Itoa(next.Diff.Del) + " deletions"
		sbEdits = append(sbEdits, ownframe.ElementEdit{ID: "sb-diff-del", Text: &t})
	}
	if prev.Encoding != next.Encoding {
		t := next.Encoding
		sbEdits = append(sbEdits, ownframe.ElementEdit{ID: "sb-encoding", Text: &t})
	}
	if prev.Indent != next.Indent {
		t := next.Indent
		sbEdits = append(sbEdits, ownframe.ElementEdit{ID: "sb-indent", Text: &t})
	}
	if len(sbEdits) > 0 {
		patches = append(patches, ownframe.ElementPatch{ID: "statusbar", Edits: sbEdits})
	}

	// 6. Sidebar selection highlight when selected path changed.
	if prev.Diff.Selected != next.Diff.Selected {
		appendDiffSidebarSelectPatches(&patches, prev.Diff, next.Diff)
	}

	return patches
}

func canPatchDiffView(prev, next View) bool {
	if prev.Mode != "changes" || next.Mode != "changes" {
		return false
	}
	if !prev.Diff.HasFiles || !next.Diff.HasFiles {
		return false
	}
	if prev.Diff.Source != "working" || next.Diff.Source != "working" {
		return false
	}
	if prev.Modal || next.Modal || prev.Details || next.Details {
		return false
	}
	if prev.FileNav.Open != next.FileNav.Open {
		return false
	}
	if prev.Diff.Count != next.Diff.Count ||
		prev.Diff.StagedCount != next.Diff.StagedCount ||
		prev.Diff.UnstagedCount != next.Diff.UnstagedCount ||
		prev.Diff.ConflictCount != next.Diff.ConflictCount ||
		prev.Diff.ScrollTop != next.Diff.ScrollTop ||
		prev.Diff.SelectedCount > 1 || next.Diff.SelectedCount > 1 {
		return false
	}
	if prev.Diff.TrackTop != next.Diff.TrackTop || prev.Diff.Split != next.Diff.Split ||
		prev.EditorScroll != next.EditorScroll {
		return false
	}
	if (prev.Active != "") != (next.Active != "") || prev.Image.On || next.Image.On {
		return false
	}
	return true
}

func appendDiffSidebarSelectPatches(patches *[]ownframe.ElementPatch, prev, next diffView) {
	if prev.Selected != "" {
		if hasDiffRow(prev.Unstaged, prev.Selected) || hasDiffRow(prev.Files, prev.Selected) {
			*patches = append(*patches, ownframe.ElementPatch{
				ID: "wrap-" + prev.Selected,
				Edits: []ownframe.ElementEdit{
					{RemoveClass: "on"},
					{ID: "change-" + prev.Selected, RemoveClass: "on"},
				},
			})
		}
		if hasDiffRow(prev.Staged, prev.Selected) {
			*patches = append(*patches, ownframe.ElementPatch{
				ID: "wrap-staged-" + prev.Selected,
				Edits: []ownframe.ElementEdit{
					{RemoveClass: "on"},
					{ID: "staged-" + prev.Selected, RemoveClass: "on"},
				},
			})
		}
	}
	if next.Selected != "" {
		if hasDiffRow(next.Unstaged, next.Selected) || hasDiffRow(next.Files, next.Selected) {
			*patches = append(*patches, ownframe.ElementPatch{
				ID: "wrap-" + next.Selected,
				Edits: []ownframe.ElementEdit{
					{AddClass: "on"},
					{ID: "change-" + next.Selected, AddClass: "on"},
				},
			})
		}
		if hasDiffRow(next.Staged, next.Selected) {
			*patches = append(*patches, ownframe.ElementPatch{
				ID: "wrap-staged-" + next.Selected,
				Edits: []ownframe.ElementEdit{
					{AddClass: "on"},
					{ID: "staged-" + next.Selected, AddClass: "on"},
				},
			})
		}
	}
}

func hasDiffRow(rows []diffFileRow, path string) bool {
	for i := range rows {
		if rows[i].Path == path {
			return true
		}
	}
	return false
}

// renderDiffRowsHTML produces the exact inner HTML of #diff-rows for
// mode == "changes" with .Diff.HasFiles == true, matching components/html/editor.html.
func renderDiffRowsHTML(v View) string {
	d := v.Diff
	var b strings.Builder
	b.Grow(len(d.Rows) * 256)

	for i := range d.Rows {
		row := &d.Rows[i]
		switch row.Kind {
		case "hunk":
			b.WriteString(`<div class="drow hunk`)
			if row.Current {
				b.WriteString(` current`)
			}
			if row.Cont {
				b.WriteString(` cont`)
			}
			b.WriteString(`"><span class="dhunk">`)
			b.WriteString(html.EscapeString(row.Hunk))
			b.WriteString(`</span>`)
			if row.AgentHunk != "" {
				b.WriteString(`<span class="agent-hunk-ctl state-`)
				b.WriteString(html.EscapeString(row.AgentState))
				b.WriteString(`">`)
				if row.AgentAccept {
					b.WriteString(`<button class="line-revert agent-hunk-btn" id="agent-hunk-accept-`)
					b.WriteString(html.EscapeString(row.AgentHunk))
					b.WriteString(`" data-action="agent-hunk:accept:`)
					b.WriteString(html.EscapeString(row.AgentHunk))
					b.WriteString(`" type="button" title="Apply this hunk to your file or open buffer">Accept</button>`)
				}
				if row.AgentReject {
					b.WriteString(`<button class="line-revert agent-hunk-btn" id="agent-hunk-reject-`)
					b.WriteString(html.EscapeString(row.AgentHunk))
					b.WriteString(`" data-action="agent-hunk:reject:`)
					b.WriteString(html.EscapeString(row.AgentHunk))
					b.WriteString(`" type="button" title="Dismiss this hunk without changing files">Reject</button>`)
				}
				if row.AgentRevert {
					b.WriteString(`<button class="line-revert agent-hunk-btn" id="agent-hunk-revert-`)
					b.WriteString(html.EscapeString(row.AgentHunk))
					b.WriteString(`" data-action="agent-hunk:revert:`)
					b.WriteString(html.EscapeString(row.AgentHunk))
					b.WriteString(`" type="button" title="Undo this accepted hunk, keeping your other edits">Revert</button>`)
				}
				if row.AgentNote != "" {
					b.WriteString(`<span class="agent-hunk-note" role="alert">`)
					b.WriteString(html.EscapeString(row.AgentNote))
					b.WriteString(`</span>`)
				}
				b.WriteString(`</span>`)
			}
			b.WriteString(`</div>`)

		case "meta":
			b.WriteString(`<div class="drow meta`)
			if row.Cont {
				b.WriteString(` cont`)
			}
			b.WriteString(`"><span class="dmeta">`)
			b.WriteString(html.EscapeString(row.Hunk))
			b.WriteString(`</span></div>`)

		case "split":
			b.WriteString(`<div class="drow split`)
			if row.Current {
				b.WriteString(` current`)
			}
			if row.LeftCont || row.RightCont {
				b.WriteString(` cont`)
			}
			b.WriteString(`"><span class="dnum `)
			b.WriteString(html.EscapeString(row.LeftKind))
			b.WriteString(`">`)
			b.WriteString(html.EscapeString(row.LeftNo))
			b.WriteString(`</span><span class="dcell `)
			b.WriteString(html.EscapeString(row.LeftKind))
			b.WriteString(`">`)
			if !row.LeftPad {
				writeDiffLineText(&b, row.Left, v.Wrap, row.ScrollX)
			}
			b.WriteString(`</span>`)
			if d.CanRevert && row.HasRevert && (row.LeftKind == "del" || row.RightKind == "add") &&
				(!row.LeftPad || !row.RightPad) && !row.LeftCont && !row.RightCont {
				b.WriteString(`<span class="dmid"><button class="line-revert divider" id="revert-line-`)
				b.WriteString(strconv.Itoa(row.RevertHunk))
				b.WriteByte('-')
				b.WriteString(html.EscapeString(row.LeftNo))
				b.WriteByte('-')
				b.WriteString(html.EscapeString(row.RightNo))
				b.WriteString(`" data-action="revert-hunk:`)
				b.WriteString(strconv.Itoa(row.RevertHunk))
				b.WriteString(`" type="button" title="Discard changes" aria-label="Discard changes">→</button></span>`)
			}
			b.WriteString(`<span class="dnum `)
			b.WriteString(html.EscapeString(row.RightKind))
			b.WriteString(`">`)
			b.WriteString(html.EscapeString(row.RightNo))
			b.WriteString(`</span><span class="dcell `)
			b.WriteString(html.EscapeString(row.RightKind))
			b.WriteString(`">`)
			if !row.RightPad {
				writeDiffLineText(&b, row.Right, v.Wrap, row.ScrollX)
			}
			b.WriteString(`</span></div>`)

		case "expand":
			idx := strconv.Itoa(row.Expand.Index)
			b.WriteString(`<div class="drow expand" id="diff-expand-row-`)
			b.WriteString(idx)
			b.WriteString(`">`)
			if d.Split {
				b.WriteString(`<span class="dnum dnum-expand">···</span>`)
			} else {
				b.WriteString(`<span class="dnum dnum-expand">···</span><span class="dnum dnum-expand">···</span><span class="dsign">↕</span>`)
			}
			b.WriteString(`<div class="dcell-expand">`)
			if row.Expand.CanDown {
				b.WriteString(`<button class="expand-btn expand-down" id="diff-expand-down-`)
				b.WriteString(idx)
				b.WriteString(`" data-action="diff-expand-down:`)
				b.WriteString(idx)
				b.WriteString(`" type="button" title="`)
				b.WriteString(html.EscapeString(row.Expand.DownText))
				b.WriteString(`">`)
				b.WriteString(html.EscapeString(row.Expand.DownText))
				b.WriteString(`</button>`)
			}
			b.WriteString(`<button class="expand-btn expand-all" id="diff-expand-all-`)
			b.WriteString(idx)
			b.WriteString(`" data-action="diff-expand-all:`)
			b.WriteString(idx)
			b.WriteString(`" type="button" title="`)
			b.WriteString(html.EscapeString(row.Expand.AllText))
			b.WriteString(`">`)
			b.WriteString(html.EscapeString(row.Expand.AllText))
			b.WriteString(`</button>`)
			if row.Expand.CanUp {
				b.WriteString(`<button class="expand-btn expand-up" id="diff-expand-up-`)
				b.WriteString(idx)
				b.WriteString(`" data-action="diff-expand-up:`)
				b.WriteString(idx)
				b.WriteString(`" type="button" title="`)
				b.WriteString(html.EscapeString(row.Expand.UpText))
				b.WriteString(`">`)
				b.WriteString(html.EscapeString(row.Expand.UpText))
				b.WriteString(`</button>`)
			}
			b.WriteString(`</div></div>`)

		default:
			b.WriteString(`<div class="drow `)
			b.WriteString(html.EscapeString(row.Kind))
			if row.Current {
				b.WriteString(` current`)
			}
			if row.Cont {
				b.WriteString(` cont`)
			}
			b.WriteString(`"><span class="dnum">`)
			b.WriteString(html.EscapeString(row.OldNo))
			b.WriteString(`</span><span class="dnum">`)
			b.WriteString(html.EscapeString(row.NewNo))
			b.WriteString(`</span><span class="dsign">`)
			if !row.Cont {
				if row.Kind == "add" {
					b.WriteByte('+')
				} else if row.Kind == "del" {
					b.WriteString("−")
				} else {
					b.WriteByte(' ')
				}
			}
			b.WriteString(`</span><span class="dcell">`)
			writeDiffLineText(&b, row.Left, v.Wrap, row.ScrollX)
			b.WriteString(`</span>`)
			if d.CanRevert && row.HasRevert && (row.Kind == "add" || row.Kind == "del") && !row.Cont {
				b.WriteString(`<button class="line-revert" id="revert-line-`)
				b.WriteString(strconv.Itoa(row.RevertHunk))
				b.WriteByte('-')
				b.WriteString(html.EscapeString(row.OldNo))
				b.WriteByte('-')
				b.WriteString(html.EscapeString(row.NewNo))
				b.WriteString(`" data-action="revert-hunk:`)
				b.WriteString(strconv.Itoa(row.RevertHunk))
				b.WriteString(`" type="button" title="Discard changes" aria-label="Discard changes">↩</button>`)
			}
			b.WriteString(`</div>`)
		}
	}

	return b.String()
}

func writeDiffLineText(b *strings.Builder, segs []segment, wrap bool, scrollX int) {
	b.WriteString(`<span class="dlinetext"`)
	if !wrap {
		b.WriteString(` style="left: -`)
		b.WriteString(strconv.Itoa(scrollX))
		b.WriteString(`px"`)
	}
	b.WriteByte('>')
	for i := range segs {
		seg := &segs[i]
		if seg.Class == "plain" {
			b.WriteString(html.EscapeString(seg.Text))
		} else {
			b.WriteString(`<span class="t-`)
			b.WriteString(html.EscapeString(seg.Class))
			b.WriteString(`">`)
			b.WriteString(html.EscapeString(seg.Text))
			b.WriteString(`</span>`)
		}
	}
	b.WriteString(`</span>`)
}

// renderDiffCrumbsHTML produces the exact inner HTML of #crumbs for
// mode == "changes" with .Diff.HasFiles == true, matching components/html/editor.html.
func renderDiffCrumbsHTML(v View) string {
	d := v.Diff
	var b strings.Builder
	b.Grow(1024)

	if d.CommitShort != "" {
		b.WriteString(`<span class="crumb-commit mono" id="crumb-commit">`)
		b.WriteString(html.EscapeString(d.CommitShort))
		b.WriteString(`</span><span class="crumb-subject">`)
		b.WriteString(html.EscapeString(d.CommitSubject))
		b.WriteString(`</span><button class="crumb-btn" id="commit-copy-message" data-action="copy-commit-message:`)
		b.WriteString(html.EscapeString(d.CommitHash))
		b.WriteString(`" type="button" title="Copy commit message" aria-label="Copy commit message">Copy message</button>`)
		b.WriteString(`<button class="crumb-btn" id="commit-copy-id" data-action="copy-commit-id:`)
		b.WriteString(html.EscapeString(d.CommitHash))
		b.WriteString(`" type="button" title="Copy commit ID" aria-label="Copy commit ID">Copy ID</button>`)
	}

	b.WriteString(`<span class="crumb-path diff-path" id="diff-path" title="`)
	b.WriteString(html.EscapeString(d.Path))
	b.WriteString(`">`)
	if d.DirShort != "" {
		b.WriteString(`<span class="diff-dir">`)
		b.WriteString(html.EscapeString(d.DirShort))
		b.WriteString(`/</span>`)
	}
	b.WriteString(`<b>`)
	b.WriteString(html.EscapeString(d.NameShort))
	b.WriteString(`</b></span><span class="tabs-fill"></span><span id="diff-status" class="cstat `)
	b.WriteString(html.EscapeString(d.StatusCls))
	b.WriteString(`">`)
	b.WriteString(html.EscapeString(d.Status))
	b.WriteString(`</span><span id="diff-stats" class="crumb-stats"><b class="add">+`)
	b.WriteString(strconv.Itoa(d.Add))
	b.WriteString(`</b><b class="del">−`)
	b.WriteString(strconv.Itoa(d.Del))
	b.WriteString(`</b></span><div class="seg"><button class="seg-btn`)
	if !d.Split {
		b.WriteString(` on`)
	}
	b.WriteString(`" id="seg-unified" data-action="diff-unified" type="button" aria-pressed="`)
	if d.Split {
		b.WriteString(`false`)
	} else {
		b.WriteString(`true`)
	}
	b.WriteString(`">Unified</button><button class="seg-btn`)
	if d.Split {
		b.WriteString(` on`)
	}
	b.WriteString(`" id="seg-split" data-action="diff-split" type="button" aria-pressed="`)
	if d.Split {
		b.WriteString(`true`)
	} else {
		b.WriteString(`false`)
	}
	b.WriteString(`">Split</button></div>`)

	b.WriteString(`<button class="crumb-btn`)
	if !d.CanPrevChange {
		b.WriteString(` off`)
	}
	b.WriteString(`" id="hunk-prev" data-action="hunk-prev" type="button" title="Previous change" aria-label="Previous change"`)
	if !d.CanPrevChange {
		b.WriteString(` disabled`)
	}
	b.WriteString(`><svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M8 13V3M4 7l4-4 4 4"/></svg></button>`)

	b.WriteString(`<button class="crumb-btn`)
	if !d.CanNextChange {
		b.WriteString(` off`)
	}
	b.WriteString(`" id="hunk-next" data-action="hunk-next" type="button" title="Next change" aria-label="Next change"`)
	if !d.CanNextChange {
		b.WriteString(` disabled`)
	}
	b.WriteString(`><svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M8 3v10M4 9l4 4 4-4"/></svg></button>`)

	b.WriteString(`<button class="crumb-btn`)
	if v.Wrap {
		b.WriteString(` on`)
	}
	b.WriteString(`" id="diff-wrap-toggle" data-action="wrap-toggle" type="button" title="Wrap long diff lines (Z)" aria-pressed="`)
	if v.Wrap {
		b.WriteString(`true`)
	} else {
		b.WriteString(`false`)
	}
	b.WriteString(`">Wrap</button>`)

	if d.CanEdit {
		b.WriteString(`<button class="crumb-btn" id="diff-edit" data-action="diff-edit" type="button" title="Edit file (click a line or press Ctrl+S to save)">Edit</button>`)
	}
	b.WriteString(`<button class="crumb-btn" id="crumb-refresh" data-action="diff-refresh" type="button">Refresh</button><span class="crumb-meta">`)
	b.WriteString(strconv.Itoa(d.Hunks))
	b.WriteString(` hunks</span>`)
	return b.String()
}

// renderFileNavInnerHTML produces the exact inner HTML of #file-nav matching
// components/html/file_nav.html.
func renderFileNavInnerHTML(fn fileNavView) string {
	single := fn.Total <= 1
	var b strings.Builder
	b.Grow(512)

	b.WriteString(`<button class="file-nav-arrow`)
	if single {
		b.WriteString(` off`)
	}
	b.WriteString(`" id="file-nav-prev" data-action="file-nav-prev" type="button" tabindex="-1" title="Previous changed file" aria-label="Previous changed file"`)
	if single {
		b.WriteString(` disabled`)
	}
	b.WriteString(`>‹</button><span class="file-nav-pos mono" id="file-nav-pos" aria-live="polite" aria-atomic="true">`)
	b.WriteString(strconv.Itoa(fn.Position))
	b.WriteByte('/')
	b.WriteString(strconv.Itoa(fn.Total))
	b.WriteString(`</span><button class="file-nav-current" id="file-nav-current" data-action="file-nav-open" type="button" tabindex="-1" title="Open `)
	if fn.Path != "" {
		b.WriteString(html.EscapeString(fn.Path))
		b.WriteByte('/')
	}
	b.WriteString(html.EscapeString(fn.Label))
	b.WriteString(`" aria-label="Open `)
	b.WriteString(html.EscapeString(fn.Label))
	b.WriteString(`, changed file `)
	b.WriteString(strconv.Itoa(fn.Position))
	b.WriteString(` of `)
	b.WriteString(strconv.Itoa(fn.Total))
	b.WriteString(`">`)
	b.WriteString(html.EscapeString(fn.Label))
	b.WriteString(`</button>`)
	if fn.Path != "" {
		b.WriteString(`<div class="file-nav-dir" title="`)
		b.WriteString(html.EscapeString(fn.Path))
		b.WriteString(`">`)
		b.WriteString(html.EscapeString(fn.Path))
		b.WriteString(`</div>`)
	}
	b.WriteString(`<div class="file-nav-fill"></div><span class="file-nav-stats"><b class="add">+`)
	b.WriteString(strconv.Itoa(fn.Add))
	b.WriteString(`</b><b class="del">−`)
	b.WriteString(strconv.Itoa(fn.Del))
	b.WriteString(`</b></span><button class="file-nav-arrow file-nav-change`)
	if !fn.CanPrevChange {
		b.WriteString(` off`)
	}
	b.WriteString(`" id="file-nav-change-prev" data-action="file-nav-change-prev" type="button" tabindex="-1" title="Previous change in this file" aria-label="Previous change in this file"`)
	if !fn.CanPrevChange {
		b.WriteString(` disabled`)
	}
	b.WriteString(`>↑</button><button class="file-nav-arrow file-nav-change`)
	if !fn.CanNextChange {
		b.WriteString(` off`)
	}
	b.WriteString(`" id="file-nav-change-next" data-action="file-nav-change-next" type="button" tabindex="-1" title="Next change in this file" aria-label="Next change in this file"`)
	if !fn.CanNextChange {
		b.WriteString(` disabled`)
	}
	b.WriteString(`>↓</button><button class="file-nav-arrow`)
	if single {
		b.WriteString(` off`)
	}
	b.WriteString(`" id="file-nav-next" data-action="file-nav-next" type="button" tabindex="-1" title="Next changed file" aria-label="Next changed file"`)
	if single {
		b.WriteString(` disabled`)
	}
	b.WriteString(`>›</button>`)
	return b.String()
}
