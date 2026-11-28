package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Composer context attachments. Typing "@" in the OpenCode composer lists
// workspace files and the editor sources (@selection, @file, @diff,
// @problems). Picking one captures a bounded snapshot as a removable chip.
// The chips are the only context sent: nothing is read because a path
// appeared in agent output, and the exact text that will be sent is what the
// preview shows.
const (
	maxOpenCodeChips          = 8
	maxOpenCodeChipBytes      = 32 << 10
	maxOpenCodeChipTotalBytes = 128 << 10
	maxOpenCodeDiffBytes      = 48 << 10
	maxOpenCodeDiffLines      = 800
	maxOpenCodeProblemRows    = 25
	maxOpenCodeProblemPicks   = 6
	maxOpenCodeUntrackedFiles = 8
	maxOpenCodeMentionFiles   = 8
	maxOpenCodeMentionScan    = 50000
	openCodeContextMarker     = "\n\n[context attachments"
)

type openCodeChipKind string

const (
	openCodeChipFile      openCodeChipKind = "file"
	openCodeChipSelection openCodeChipKind = "selection"
	openCodeChipDiff      openCodeChipKind = "diff"
	openCodeChipProblems  openCodeChipKind = "problems"
)

// openCodeChip is one bounded context snapshot. Root and Version are
// recorded at capture time so a later send can detect that the workspace or
// the underlying text changed.
type openCodeChip struct {
	ID                 string
	Kind               openCodeChipKind
	Label              string // file name or source name
	Detail             string // line range or diagnostic/diff summary
	Path               string // workspace-relative path, when there is one
	Root               string
	Version            string
	Text               string
	StartLine, EndLine int
	Truncated          bool
	Note               string // truncation reason
	FromBuffer         bool   // text is the unsaved editor buffer, not the disk file
}

type openCodeComposer struct {
	text  string
	chips []openCodeChip
	seq   int
	// mentionIndex is the keyboard-highlighted popup row; mentionDismissed
	// hides the popup after Escape or a click away until the query changes.
	mentionIndex     int
	mentionDismissed bool
}

type openCodeMentionCandidate struct {
	Kind  openCodeChipKind
	Label string
	Path  string
	Info  string
	// Problem is the rendered line of one specific Problems entry.
	Problem string
}

// openCodeMentionRow and openCodeChipRow are the template views.
type openCodeMentionRow struct {
	Index         int
	Label, Detail string
	Active        bool
}

type openCodeChipRow struct {
	ID, Label, Detail, Title string
	Truncated                bool
}

// openCodeMentionQuery returns the text after the "@" being typed at the end
// of the composer. The "@" must start the text or follow whitespace.
func openCodeMentionQuery(text string) (query string, start int, ok bool) {
	at := strings.LastIndexByte(text, '@')
	if at < 0 {
		return "", 0, false
	}
	if at > 0 {
		prev, _ := utf8.DecodeLastRuneInString(text[:at])
		if !unicode.IsSpace(prev) {
			return "", 0, false
		}
	}
	rest := text[at+1:]
	if strings.IndexFunc(rest, unicode.IsSpace) >= 0 {
		return "", 0, false
	}
	return rest, at, true
}

// openCodeMentionKey handles the keyboard while the @ popup is showing:
// Up/Down move the highlight, Enter or Tab attach it, Escape hides it. It
// reports whether the key was consumed.
func (a *App) openCodeMentionKey(key string) bool {
	cands := a.currentOpenCodeMentions()
	if len(cands) == 0 {
		return false
	}
	c := &a.openCode.composer
	c.mentionIndex = max(0, min(c.mentionIndex, len(cands)-1))
	switch key {
	case "arrowdown":
		c.mentionIndex = (c.mentionIndex + 1) % len(cands)
	case "arrowup":
		c.mentionIndex = (c.mentionIndex + len(cands) - 1) % len(cands)
	case "enter", "tab":
		_ = a.pickOpenCodeMention(c.mentionIndex)
		return true
	case "escape":
		c.mentionDismissed = true
	default:
		return false
	}
	a.refresh()
	return true
}

// dismissOpenCodeMentions hides the popup when a click lands outside it and
// outside the composer. The click itself still proceeds.
func (a *App) dismissOpenCodeMentions(boxID string) {
	c := &a.openCode.composer
	if c.mentionDismissed || !a.openCode.panelOpen || strings.HasPrefix(boxID, "opencode-mention") || boxID == "opencode-prompt" {
		return
	}
	if len(a.currentOpenCodeMentions()) == 0 {
		return
	}
	c.mentionDismissed = true
	a.refresh()
}

// openCodeMentionCandidates lists the editor sources whose names match the
// query, then workspace files by fuzzy score. Ignored, sensitive, binary and
// oversized-listing entries never appear. The result is bounded.
func (a *App) openCodeMentionCandidates(query string) []openCodeMentionCandidate {
	var out []openCodeMentionCandidate
	lq := strings.ToLower(query)
	for _, s := range []struct {
		name string
		kind openCodeChipKind
		info string
	}{
		{"selection", openCodeChipSelection, "current editor selection"},
		{"file", openCodeChipFile, "active file"},
		{"diff", openCodeChipDiff, "working-tree git diff"},
		{"problems", openCodeChipProblems, "LSP problems"},
	} {
		if strings.HasPrefix(s.name, lq) {
			out = append(out, openCodeMentionCandidate{Kind: s.kind, Label: "@" + s.name, Info: s.info})
		}
	}
	if len(lq) >= 3 && strings.HasPrefix("problems", lq) && a != nil && a.lspm != nil {
		a.lspm.rebuildProblems()
		for _, p := range a.lspm.problems {
			if len(out) >= 4+maxOpenCodeProblemPicks || isSensitiveAgentContextPath(p.Path) {
				continue
			}
			out = append(out, openCodeMentionCandidate{Kind: openCodeChipProblems, Label: "@problem", Path: p.Path,
				Info: fmt.Sprintf("%d:%d %s", p.Line+1, p.Col+1, clipProblemText(p.Message, 80)), Problem: openCodeProblemLine(p)})
		}
	}
	if a == nil || a.ws == nil || a.ws.Sample {
		return out
	}
	type scored struct {
		meta  *fileMeta
		score int
	}
	var hits []scored
	for i, f := range a.ws.Files {
		if i >= maxOpenCodeMentionScan {
			break
		}
		if f == nil || f.Ignored || f.Big || isSensitiveAgentContextPath(f.Path) || pathIgnored(a.ws.ignored, f.Path) ||
			containsAgentContextPathComponent(f.Path, ".git") {
			continue
		}
		score, _, ok := fuzzyMatch(query, f.Path)
		if !ok {
			continue
		}
		hits = append(hits, scored{f, score})
	}
	slices.SortStableFunc(hits, func(x, y scored) int {
		if x.score != y.score {
			return y.score - x.score
		}
		return strings.Compare(x.meta.Path, y.meta.Path)
	})
	for _, h := range hits[:min(len(hits), maxOpenCodeMentionFiles)] {
		out = append(out, openCodeMentionCandidate{Kind: openCodeChipFile, Label: filepath.Base(h.meta.Path), Path: h.meta.Path, Info: h.meta.Dir})
	}
	return out
}

// currentOpenCodeMentions is the popup for the composer text, or nil.
func (a *App) currentOpenCodeMentions() []openCodeMentionCandidate {
	if !a.openCode.panelOpen || a.openCode.composer.mentionDismissed {
		return nil
	}
	q, _, ok := openCodeMentionQuery(a.openCode.composer.text)
	if !ok {
		return nil
	}
	return a.openCodeMentionCandidates(q)
}

// onOpenCodePromptChange records the typed text and redraws only when the
// popup could change, so ordinary typing does not re-render the panel.
func (a *App) onOpenCodePromptChange(text string) {
	text = clampOpenCodePrompt(text)
	c := &a.openCode.composer
	prevQ, _, prevOK := openCodeMentionQuery(c.text)
	nextQ, _, nextOK := openCodeMentionQuery(text)
	c.text = text
	if prevOK != nextOK || prevQ != nextQ {
		c.mentionIndex, c.mentionDismissed = 0, false
		a.refresh()
	}
}

func clampOpenCodePrompt(s string) string {
	if len(s) > 12000 {
		return s[:utf8PrefixLength([]byte(s), 12000)]
	}
	return s
}

// pickOpenCodeMention adds the chip for popup row index and removes the
// "@query" token from the composer text.
func (a *App) pickOpenCodeMention(index int) error {
	cands := a.currentOpenCodeMentions()
	if index < 0 || index >= len(cands) {
		return nil
	}
	chip, err := a.captureOpenCodeChip(cands[index])
	if err != nil {
		a.openCode.panelStatus = err.Error()
		a.refresh()
		return nil
	}
	if err := a.addOpenCodeChip(chip); err != nil {
		a.openCode.panelStatus = err.Error()
		a.refresh()
		return nil
	}
	c := &a.openCode.composer
	if _, start, ok := openCodeMentionQuery(c.text); ok {
		c.text = c.text[:start]
	}
	c.mentionIndex, c.mentionDismissed = 0, false
	if a.page != nil {
		a.page.Form().SetFormValue("opencode-prompt", c.text)
	}
	a.openCode.panelStatus = "Added " + chip.Label + " to the context."
	a.refresh()
	return nil
}

func (a *App) removeOpenCodeChip(id string) {
	c := &a.openCode.composer
	c.chips = slices.DeleteFunc(c.chips, func(ch openCodeChip) bool { return ch.ID == id })
	a.refresh()
}

func (a *App) clearOpenCodeComposer() {
	a.openCode.composer = openCodeComposer{seq: a.openCode.composer.seq}
}

// addOpenCodeChip enforces the chip-count and total-byte limits. A snapshot
// that only partly fits is cut and marked truncated.
func (a *App) addOpenCodeChip(chip openCodeChip) error {
	c := &a.openCode.composer
	if len(c.chips) >= maxOpenCodeChips {
		return fmt.Errorf("At most %d context sources can be attached.", maxOpenCodeChips)
	}
	for _, e := range c.chips {
		if e.Kind == chip.Kind && e.Path == chip.Path && e.Label == chip.Label && e.Detail == chip.Detail {
			return errors.New("That context is already attached.")
		}
	}
	used := 0
	for _, e := range c.chips {
		used += len(e.Text)
	}
	remaining := maxOpenCodeChipTotalBytes - used
	if remaining <= 0 {
		return errors.New("The total context limit is reached; remove a source first.")
	}
	if len(chip.Text) > remaining {
		chip.Text = chip.Text[:utf8PrefixLength([]byte(chip.Text), remaining)]
		chip.Truncated = true
		chip.Note = "cut to fit the total context limit"
	}
	c.seq++
	chip.ID = "c" + strconv.Itoa(c.seq)
	c.chips = append(c.chips, chip)
	return nil
}

func (a *App) openCodeChipRoot() (string, error) {
	if a == nil || a.ws == nil || a.ws.Sample {
		return "", errors.New("Open a project folder first.")
	}
	return canonicalWorkspaceRoot(a.ws.Root)
}

// captureOpenCodeChip reads only what the picked source names.
func (a *App) captureOpenCodeChip(cand openCodeMentionCandidate) (openCodeChip, error) {
	root, err := a.openCodeChipRoot()
	if err != nil {
		return openCodeChip{}, err
	}
	switch cand.Kind {
	case openCodeChipSelection:
		snap, err := a.currentOpenCodeContext()
		if err != nil {
			return openCodeChip{}, err
		}
		if snap.SelectionText == "" {
			return openCodeChip{}, errors.New("Select text in the editor first.")
		}
		return openCodeChip{
			Kind: openCodeChipSelection, Label: "selection", Path: snap.RelativePath,
			Detail: filepath.Base(snap.RelativePath) + ":" + openCodeLineSpan(snap.StartLine, snap.EndLine),
			Root:   snap.WorkspaceRoot, Version: snap.Version, Text: snap.Text,
			StartLine: snap.StartLine, EndLine: snap.EndLine, Truncated: snap.Truncated,
			Note: boolNote(snap.Truncated, "excerpt around the selection"),
		}, nil
	case openCodeChipFile:
		path := cand.Path
		if path == "" {
			path = a.editPath()
			if path == "" {
				return openCodeChip{}, errors.New("There is no active file.")
			}
		}
		return a.captureOpenCodeFileChip(root, path)
	case openCodeChipDiff:
		return a.captureOpenCodeDiffChip(root)
	case openCodeChipProblems:
		if cand.Problem != "" {
			return openCodeSingleProblemChip(root, cand), nil
		}
		return a.captureOpenCodeProblemsChip(root)
	}
	return openCodeChip{}, errors.New("Unknown context source.")
}

func boolNote(b bool, s string) string {
	if b {
		return s
	}
	return ""
}

func (a *App) captureOpenCodeFileChip(root, path string) (openCodeChip, error) {
	if a.editing && a.edit != nil && a.editPath() == path && a.dirty[path] {
		return a.captureOpenCodeBufferChip(root, path)
	}
	col, err := collectSelectedAgentFiles(root, []string{path}, a.ws.ignored)
	if err != nil {
		return openCodeChip{}, err
	}
	if len(col.Files) == 0 {
		reason := "the file cannot be attached"
		if len(col.Excluded) > 0 {
			reason = col.Excluded[0].Reason
		}
		return openCodeChip{}, errors.New("Not attached: " + reason + ".")
	}
	f := col.Files[0]
	text := f.Text
	truncated, note := f.Truncated, boolNote(f.Truncated, "file exceeds the per-file byte or line limit")
	if len(text) > maxOpenCodeChipBytes {
		text = text[:utf8PrefixLength([]byte(text), maxOpenCodeChipBytes)]
		truncated, note = true, "file exceeds the per-file byte limit"
	}
	return openCodeChip{
		Kind: openCodeChipFile, Label: filepath.Base(f.RelativePath), Path: f.RelativePath,
		Detail: "lines " + openCodeLineSpan(f.StartLine, f.EndLine),
		Root:   col.WorkspaceRoot, Version: f.Version, Text: text,
		StartLine: f.StartLine, EndLine: f.EndLine, Truncated: truncated, Note: note,
	}, nil
}

// captureOpenCodeBufferChip attaches the unsaved editor text of a dirty file
// so the model sees what the user sees. The same path rules apply as for a
// disk file; the chip is marked as a buffer snapshot.
func (a *App) captureOpenCodeBufferChip(root, path string) (openCodeChip, error) {
	col, err := collectSelectedAgentFiles(root, []string{path}, a.ws.ignored)
	if err != nil {
		return openCodeChip{}, err
	}
	if len(col.Files) == 0 {
		reason := "the file cannot be attached"
		if len(col.Excluded) > 0 {
			reason = col.Excluded[0].Reason
		}
		return openCodeChip{}, errors.New("Not attached: " + reason + ".")
	}
	full := a.edit.Text()
	sum := sha256.Sum256([]byte(full))
	data := []byte(full)
	data = data[:utf8PrefixLength(data, min(len(data), maxOpenCodeChipBytes))]
	data, lineCut := limitAgentContextLines(data, 400)
	truncated := len(data) < len(full) || lineCut
	end := strings.Count(string(data), "\n") + 1
	return openCodeChip{
		Kind: openCodeChipFile, Label: filepath.Base(path), Path: path,
		Detail: "unsaved buffer, lines 1-" + strconv.Itoa(end),
		Root:   root, Version: hex.EncodeToString(sum[:]), Text: string(data),
		StartLine: 1, EndLine: end, Truncated: truncated, FromBuffer: true,
		Note: boolNote(truncated, "buffer exceeds the per-file byte or line limit"),
	}, nil
}

// sensitiveDiffExcludes keeps credential-like files out of the diff.
var sensitiveDiffExcludes = []string{
	"**/.env", "**/.env.*", "**/*.key", "**/*.pem", "**/*.p12", "**/*.pfx", "**/*.keystore",
	"**/id_rsa*", "**/id_ed25519*", "**/id_ecdsa*", "**/*credential*", "**/*secret*", "**/*password*", "**/*token*",
}

func (a *App) captureOpenCodeDiffChip(root string) (openCodeChip, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	args := []string{"diff", "HEAD", "--no-ext-diff", "--no-color", "--no-textconv", "--", "."}
	for _, p := range sensitiveDiffExcludes {
		args = append(args, ":(exclude,glob,icase)"+p)
	}
	data, capped, err := gitBytesCapped(ctx, root, maxOpenCodeDiffBytes, args...)
	if err != nil {
		return openCodeChip{}, errors.New("The working-tree diff is unavailable (is this a Git repository with a commit?).")
	}
	if !capped {
		if extra := a.openCodeUntrackedDiff(ctx, root, maxOpenCodeDiffBytes-len(data)); extra != "" {
			if len(data) > 0 && data[len(data)-1] != '\n' {
				data = append(data, '\n')
			}
			data = append(data, extra...)
		}
	}
	if len(data) == 0 {
		return openCodeChip{}, errors.New("There are no uncommitted changes to attach.")
	}
	data = data[:utf8PrefixLength(data, len(data))]
	data, lineCut := limitAgentContextLines(data, maxOpenCodeDiffLines)
	files := strings.Count(string(data), "\ndiff --git ") + 1
	sum := sha256.Sum256(data)
	truncated := capped || lineCut
	return openCodeChip{
		Kind: openCodeChipDiff, Label: "diff",
		Detail: strconv.Itoa(files) + " file(s), " + strconv.Itoa(strings.Count(string(data), "\n")+1) + " lines",
		Root:   root, Version: hex.EncodeToString(sum[:]), Text: string(data),
		StartLine: 1, EndLine: strings.Count(string(data), "\n") + 1, Truncated: truncated,
		Note: boolNote(truncated, "diff cut at the byte or line limit"),
	}, nil
}

// openCodeProblemLine renders one problem deterministically.
func openCodeProblemLine(p lspProblem) string {
	sev := "info"
	switch p.Severity {
	case 1:
		sev = "error"
	case 2:
		sev = "warning"
	}
	return fmt.Sprintf("%s:%d:%d %s: %s", p.Path, p.Line+1, p.Col+1, sev, clipProblemText(p.Message, 300))
}

func openCodeSingleProblemChip(root string, cand openCodeMentionCandidate) openCodeChip {
	sum := sha256.Sum256([]byte(cand.Problem))
	loc, _, _ := strings.Cut(cand.Problem, " ")
	return openCodeChip{
		Kind: openCodeChipProblems, Label: "problem", Path: cand.Path, Detail: loc,
		Root: root, Version: hex.EncodeToString(sum[:]), Text: cand.Problem, StartLine: 1, EndLine: 1,
	}
}

// openCodeProblemsStale re-reads the live problem list at send time.
func (a *App) openCodeProblemsStale(root string, chip openCodeChip) string {
	if a.lspm == nil {
		return "no language-server problems are available now"
	}
	if chip.Label == "problem" {
		a.lspm.rebuildProblems()
		for _, p := range a.lspm.problems {
			if openCodeProblemLine(p) == chip.Text {
				return ""
			}
		}
		return "that problem is no longer reported"
	}
	cur, err := a.captureOpenCodeProblemsChip(root)
	if err != nil || cur.Version != chip.Version {
		return "the problem list changed since it was attached"
	}
	return ""
}

// openCodeUntrackedDiff renders untracked, non-ignored text files as
// new-file diffs. Sensitive, binary, symlinked and oversized files are
// skipped by collectSelectedAgentFiles; the output is bounded by budget.
func (a *App) openCodeUntrackedDiff(ctx context.Context, root string, budget int) string {
	if budget <= 0 {
		return ""
	}
	out, _, err := gitBytesCapped(ctx, root, 64<<10, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil || len(out) == 0 {
		return ""
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" || isSensitiveAgentContextPath(p) || containsAgentContextPathComponent(p, ".git") {
			continue
		}
		paths = append(paths, p)
		if len(paths) >= maxOpenCodeUntrackedFiles {
			break
		}
	}
	if len(paths) == 0 {
		return ""
	}
	col, err := collectSelectedAgentFiles(root, paths, a.ws.ignored)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, f := range col.Files {
		lines := strings.Split(strings.TrimSuffix(f.Text, "\n"), "\n")
		fmt.Fprintf(&b, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", f.RelativePath, f.RelativePath, f.RelativePath, len(lines))
		for _, l := range lines {
			b.WriteString("+" + l + "\n")
		}
		if f.Truncated {
			b.WriteString("\\ [untracked file truncated]\n")
		}
		if b.Len() > budget {
			break
		}
	}
	res := b.String()
	if len(res) > budget {
		res = res[:utf8PrefixLength([]byte(res), budget)]
	}
	return res
}

func (a *App) captureOpenCodeProblemsChip(root string) (openCodeChip, error) {
	if a.lspm == nil {
		return openCodeChip{}, errors.New("No language-server problems are available.")
	}
	a.lspm.rebuildProblems()
	var b strings.Builder
	n, total := 0, 0
	for _, p := range a.lspm.problems {
		if isSensitiveAgentContextPath(p.Path) {
			continue
		}
		total++
		if n >= maxOpenCodeProblemRows {
			continue
		}
		b.WriteString(openCodeProblemLine(p) + "\n")
		n++
	}
	if n == 0 {
		return openCodeChip{}, errors.New("There are no problems to attach.")
	}
	text := b.String()
	sum := sha256.Sum256([]byte(text))
	truncated := total > n
	return openCodeChip{
		Kind: openCodeChipProblems, Label: "problems",
		Detail: strconv.Itoa(n) + " of " + strconv.Itoa(total) + " problem(s)",
		Root:   root, Version: hex.EncodeToString(sum[:]), Text: strings.TrimRight(text, "\n"),
		StartLine: 1, EndLine: n, Truncated: truncated,
		Note: boolNote(truncated, "only the first "+strconv.Itoa(maxOpenCodeProblemRows)+" problems are included"),
	}, nil
}

// openCodeChipStale reports why a chip no longer matches the workspace, or "".
func (a *App) openCodeChipStale(chip openCodeChip) string {
	root, err := a.openCodeChipRoot()
	if err != nil || root != chip.Root {
		return "the workspace changed"
	}
	switch chip.Kind {
	case openCodeChipFile:
		if chip.FromBuffer {
			if !a.editing || a.edit == nil || a.editPath() != chip.Path {
				return "the editor no longer shows that file"
			}
			sum := sha256.Sum256([]byte(a.edit.Text()))
			if hex.EncodeToString(sum[:]) != chip.Version {
				return "the editor buffer changed since the file was attached"
			}
			return ""
		}
		col, err := collectSelectedAgentFiles(root, []string{chip.Path}, a.ws.ignored)
		if err != nil || len(col.Files) == 0 || col.Files[0].Version != chip.Version {
			return "the file changed since it was attached"
		}
	case openCodeChipDiff:
		cur, err := a.captureOpenCodeDiffChip(root)
		if err != nil || cur.Version != chip.Version {
			return "the working tree changed since the diff was attached"
		}
	case openCodeChipProblems:
		return a.openCodeProblemsStale(root, chip)
	case openCodeChipSelection:
		if !a.editing || a.edit == nil || a.editPath() != chip.Path {
			return "the editor no longer shows that file"
		}
		sum := sha256.Sum256([]byte(a.edit.Text()))
		if hex.EncodeToString(sum[:]) != chip.Version {
			return "the editor buffer changed since the selection was attached"
		}
	}
	return ""
}

// renderOpenCodeContextBlock is the exact text appended to the user message
// and shown in the preview. It is deterministic in chip order.
func renderOpenCodeContextBlock(chips []openCodeChip) string {
	if len(chips) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(openCodeContextMarker + ": " + strconv.Itoa(len(chips)) + " source(s), explicitly attached by the user]\n")
	for _, c := range chips {
		b.WriteString("\n--- " + string(c.Kind))
		if c.Path != "" {
			b.WriteString(" " + c.Path)
		}
		b.WriteString(" (" + c.Detail + ", version " + c.Version[:min(12, len(c.Version))] + ")")
		if c.Truncated {
			b.WriteString(" [TRUNCATED: " + c.Note + "]")
		}
		b.WriteString(" ---\n" + c.Text + "\n")
	}
	b.WriteString("--- end of context attachments ---")
	return b.String()
}

// openCodeTypedPart is the user's own text without an embedded context block.
func openCodeTypedPart(text string) string {
	if i := strings.Index(text, openCodeContextMarker); i >= 0 {
		return text[:i]
	}
	return text
}

// composeOpenCodeUserText appends the attached context to the typed prompt.
// A stale source blocks the send so the model never sees outdated text. A
// prompt that already carries a block (a retry) is left unchanged.
func (a *App) composeOpenCodeUserText(prompt string) (string, error) {
	chips := a.openCode.composer.chips
	if len(chips) == 0 || strings.Contains(prompt, openCodeContextMarker) {
		return prompt, nil
	}
	for _, c := range chips {
		if why := a.openCodeChipStale(c); why != "" {
			return "", fmt.Errorf("Context %s is stale: %s. Remove it and attach it again.", c.Label, why)
		}
	}
	return prompt + renderOpenCodeContextBlock(chips), nil
}

// openCodeComposerView fills the composer fields of the panel view.
func (a *App) openCodeComposerView(v *openCodePanelView) {
	c := a.openCode.composer
	for i, cand := range a.currentOpenCodeMentions() {
		detail := cand.Info
		if cand.Path != "" {
			detail = cand.Path
		}
		if cand.Problem != "" {
			detail = cand.Path + " " + cand.Info
		}
		v.Mentions = append(v.Mentions, openCodeMentionRow{Index: i, Label: cand.Label, Detail: detail, Active: i == c.mentionIndex})
	}
	for _, ch := range c.chips {
		title := ch.Path
		if ch.Truncated {
			title += " (truncated: " + ch.Note + ")"
		}
		v.Chips = append(v.Chips, openCodeChipRow{ID: ch.ID, Label: ch.Label, Detail: ch.Detail, Title: title, Truncated: ch.Truncated})
	}
	v.ComposerPreview = renderOpenCodeContextBlock(c.chips)
}

func sameOpenCodeComposer(p, n openCodePanelView) bool {
	return slices.Equal(p.Mentions, n.Mentions) && slices.Equal(p.Chips, n.Chips) && p.ComposerPreview == n.ComposerPreview
}
