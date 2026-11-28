package app

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Command palette extras: fuzzy ranking with highlight runs, keybinding
// hints, recently-used pinning, task discovery, and git review commands.

const (
	paletteRecentMax = 6
	// paletteDynTTL bounds how stale cached task and stash discovery may
	// get. paletteRows runs on every render, so discovery must not touch
	// disk or git each time.
	paletteDynTTL = 3 * time.Second
)

// Ids of the commands defined here.
const (
	paletteCmdTaskPick      = "task-pick"
	paletteCmdStageHunk     = "git-stage-hunk"
	paletteCmdUnstageHunk   = "git-unstage-hunk"
	paletteCmdStashPush     = "git-stash-push"
	paletteCmdStashPick     = "git-stash-pick"
	paletteCmdComparePick   = "git-compare-pick"
	paletteCmdLSPToggle     = "lsp-toggle"
	paletteCmdOpenCodePanel = "opencode-panel-open"
	paletteCmdOpenCodeNew   = "opencode-session-new"
	paletteCmdTaskPrefix    = "task:"
	paletteCmdStashApplyPfx = "stash-apply:"
	paletteCmdComparePfx    = "compare:"
)

// paletteExtra is the App state behind the extras.
type paletteExtra struct {
	recent  []string
	compare [2]string

	dynAt   time.Time
	dynRoot string
	tasks   []taskDef
	stashes []stashRow
}

// stashRow is one `git stash list` entry.
type stashRow struct {
	Index int
	Text  string
}

// paletteExtraCatalog lists the static commands this file owns.
func paletteExtraCatalog() []paletteRow {
	return []paletteRow{
		{Cmd: paletteCmdTaskPick, Name: "Task: Run Task…", Dir: "Run a Makefile target, npm script or go test in the terminal"},
		{Cmd: paletteCmdStageHunk, Name: "Git: Stage Hunk at Cursor", Dir: "Stage the highlighted change hunk (Source Control view)"},
		{Cmd: paletteCmdUnstageHunk, Name: "Git: Unstage Hunk at Cursor", Dir: "Move the highlighted staged hunk back to unstaged"},
		{Cmd: paletteCmdStashPush, Name: "Git: Stash Changes", Dir: "Stash tracked changes (untracked files stay)"},
		{Cmd: paletteCmdStashPick, Name: "Git: Apply Stash…", Dir: "List stashes; applying keeps the stash entry"},
		{Cmd: paletteCmdComparePick, Name: "Git: Compare Refs…", Dir: "Diff any two commits or branches: compare <a> <b>"},
		{Cmd: paletteCmdLSPToggle, Name: "LSP: Toggle Language Servers", Dir: "Enable or disable every language server"},
		{Cmd: paletteCmdOpenCodePanel, Name: "OpenCode: Open Session Panel", Dir: "Show saved OpenCode sessions for this workspace"},
		{Cmd: paletteCmdOpenCodeNew, Name: "OpenCode: New Blank Session", Dir: "Create an empty session in this workspace when connected"},
	}
}

// paletteCmdChords maps a command id to the keymap action whose chord it
// shows. Commands with a fixed chord outside the keymap use paletteFixedKeys.
var paletteCmdChords = map[string]string{
	paletteCmdGotoDefinition: "goto-definition",
	paletteCmdBlame:          "toggle-blame",
	paletteCmdStickyScroll:   "toggle-sticky",
	paletteCmdStageHunk:      "stage-hunk",
}

var paletteFixedKeys = map[string]string{
	paletteCmdPinTab:    "Ctrl+K",
	paletteCmdReopenTab: "Ctrl+Shift+T",
}

// paletteKeysFor returns the current keybinding hint for one command.
func (a *App) paletteKeysFor(cmd string) string {
	if fixed, ok := paletteFixedKeys[cmd]; ok {
		return fixed
	}

	action, ok := paletteCmdChords[cmd]
	if !ok {
		return ""
	}

	if custom, has := a.settings.Keymap[action]; has {
		if norm, valid := normalizeChord(custom); valid {
			return norm
		}
	}

	chord, _ := defaultChordFor(action)

	return chord
}

// fuzzyMatch reports whether every non-space rune of query appears in text
// in order, ignoring case. The score rewards consecutive runs, word starts
// and early matches; pos holds the matched rune indexes for highlighting.
func fuzzyMatch(query, text string) (score int, pos []int, ok bool) {
	var q []rune

	for _, r := range strings.ToLower(query) {
		if !unicode.IsSpace(r) {
			q = append(q, r)
		}
	}

	if len(q) == 0 {
		return 0, nil, true
	}

	t := []rune(text)
	lower := []rune(strings.ToLower(text))

	if len(lower) != len(t) {
		lower = t
	}

	wordStart := func(i int) bool {
		if i == 0 {
			return true
		}

		prev := t[i-1]

		return !unicode.IsLetter(prev) && !unicode.IsDigit(prev)
	}

	cursor := 0
	last := -2

	for _, qr := range q {
		found := -1

		for i := cursor; i < len(lower); i++ {
			if lower[i] != qr {
				continue
			}

			// Prefer a word start or an adjacent hit over the first one.
			if found < 0 {
				found = i
			}

			if i == last+1 || wordStart(i) {
				found = i

				break
			}
		}

		if found < 0 {
			return 0, nil, false
		}

		switch {
		case found == last+1:
			score += 8
		case wordStart(found):
			score += 6
		default:
			score++
		}

		if found < 8 {
			score += 8 - found
		}

		pos = append(pos, found)
		last = found
		cursor = found + 1
	}

	return score, pos, true
}

// nameRuns splits name into highlighted and plain runs for the match.
func nameRuns(name string, pos []int) []nameRun {
	if len(pos) == 0 {
		return nil
	}

	hit := map[int]bool{}
	for _, p := range pos {
		hit[p] = true
	}

	var runs []nameRun

	for i, r := range []rune(name) {
		h := hit[i]
		// Inline runs drop edge spaces when laid out; keep them visible.
		if r == ' ' {
			r = '\u00a0'
		}

		if n := len(runs); n > 0 && runs[n-1].Hit == h {
			runs[n-1].Text += string(r)

			continue
		}

		runs = append(runs, nameRun{Text: string(r), Hit: h})
	}

	return runs
}

// notePaletteCommand records one run for the recently-used list. Discovery
// ids (stash/compare) are not worth pinning, so only stable ids count.
func (a *App) notePaletteCommand(id string) {
	id = strings.TrimSpace(id)
	if id == "" || strings.HasPrefix(id, paletteCmdStashApplyPfx) || strings.HasPrefix(id, paletteCmdComparePfx) {
		return
	}

	out := make([]string, 0, paletteRecentMax)
	out = append(out, id)

	for _, old := range a.palx.recent {
		if old != id && len(out) < paletteRecentMax {
			out = append(out, old)
		}
	}

	a.palx.recent = out
}

// rankedCommandRows filters and orders the command list for one query. An
// empty query pins recently used commands first; a query orders by fuzzy
// score, with recent commands winning ties.
func (a *App) rankedCommandRows(query string) []paletteRow {
	all := append(paletteCommandCatalog(), paletteExtraCatalog()...)
	all = append(all, a.dynamicCommandRows(query)...)

	recentRank := map[string]int{}
	for i, id := range a.palx.recent {
		recentRank[id] = i
	}

	type scored struct {
		row   paletteRow
		score int
		order int
	}

	var hits []scored

	for i, row := range all {
		row.Keys = a.paletteKeysFor(row.Cmd)

		score, pos, ok := fuzzyMatch(query, row.Name)
		if !ok {
			continue
		}

		row.NameParts = nameRuns(row.Name, pos)

		if _, isRecent := recentRank[row.Cmd]; isRecent && query == "" {
			row.Recent = true
		}

		hits = append(hits, scored{row: row, score: score, order: i})
	}

	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]

		if query == "" {
			ra, aRecent := recentRank[a.row.Cmd]
			rb, bRecent := recentRank[b.row.Cmd]

			switch {
			case aRecent && bRecent:
				return ra < rb
			case aRecent != bRecent:
				return aRecent
			}

			return a.order < b.order
		}

		if a.score != b.score {
			return a.score > b.score
		}

		ra, aRecent := recentRank[a.row.Cmd]
		rb, bRecent := recentRank[b.row.Cmd]

		if aRecent && bRecent && ra != rb {
			return ra < rb
		}

		if aRecent != bRecent {
			return aRecent
		}

		return a.order < b.order
	})

	rows := make([]paletteRow, len(hits))
	for i, h := range hits {
		rows[i] = h.row
	}

	return rows
}

var refNameRE = regexp.MustCompile(`^[A-Za-z0-9._/@{}~^+-]{1,200}$`)

// validRefArg accepts a conservative ref spelling. Colons, spaces, leading
// dashes and shell metacharacters never pass, so a typed ref cannot become
// a git option or a second argument.
func validRefArg(ref string) bool {
	return refNameRE.MatchString(ref) && !strings.HasPrefix(ref, "-") && !strings.Contains(ref, "..")
}

// parseCompareQuery reads "compare a b", "diff a..b" or "compare a".
func parseCompareQuery(query string) (a, b string, ok bool) {
	fields := strings.Fields(query)
	if len(fields) < 2 {
		return "", "", false
	}

	switch strings.ToLower(fields[0]) {
	case "compare", "diff":
	default:
		return "", "", false
	}

	args := fields[1:]

	switch {
	case len(args) == 1 && strings.Contains(args[0], ".."):
		left, right, _ := strings.Cut(args[0], "..")
		right = strings.TrimPrefix(right, ".")
		a, b = left, right
	case len(args) == 1:
		a, b = args[0], "HEAD"
	case len(args) == 2:
		a, b = args[0], args[1]
	default:
		return "", "", false
	}

	if !validRefArg(a) || !validRefArg(b) {
		return "", "", false
	}

	return a, b, true
}

// dynamicCommandRows returns query-dependent rows: detected tasks, stash
// entries and a typed ref comparison.
func (a *App) dynamicCommandRows(query string) []paletteRow {
	var rows []paletteRow

	if left, right, ok := parseCompareQuery(query); ok {
		rows = append(rows, paletteRow{
			Cmd:  paletteCmdComparePfx + left + ":" + right,
			Name: "Git: Compare " + left + " with " + right,
			Dir:  "Show the diff between the two refs in Source Control",
		})
	}

	if query == "" {
		return rows
	}

	a.refreshPaletteDiscovery()

	for _, task := range a.palx.tasks {
		rows = append(rows, paletteRow{
			Cmd:  paletteCmdTaskPrefix + task.ID,
			Name: "Task: " + task.Label,
			Dir:  "Run in the terminal: " + task.Line,
		})
	}

	for _, st := range a.palx.stashes {
		rows = append(rows, paletteRow{
			Cmd:  paletteCmdStashApplyPfx + strconv.Itoa(st.Index),
			Name: "Git: Apply Stash " + strconv.Itoa(st.Index) + " — " + st.Text,
			Dir:  "git stash apply stash@{" + strconv.Itoa(st.Index) + "} (the stash entry is kept)",
		})
	}

	return rows
}

// refreshPaletteDiscovery reloads tasks and stashes at most once per TTL.
func (a *App) refreshPaletteDiscovery() {
	if a.ws == nil || a.ws.Sample || a.ws.Root == "" {
		a.palx.tasks, a.palx.stashes = nil, nil

		return
	}

	if a.palx.dynRoot == a.ws.Root && time.Since(a.palx.dynAt) < paletteDynTTL {
		return
	}

	a.palx.dynRoot, a.palx.dynAt = a.ws.Root, time.Now()
	a.palx.tasks = detectTasks(a.ws.Root)

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()

	a.palx.stashes = listStashes(ctx, a.ws.Root)
}

// listStashes reads `git stash list`; any failure yields no rows.
func listStashes(ctx context.Context, root string) []stashRow {
	raw, err := gitBytes(ctx, root, "stash", "list", "--format=%gd%x00%gs")
	if err != nil {
		return nil
	}

	var rows []stashRow

	for _, line := range strings.Split(string(raw), "\n") {
		name, text, ok := strings.Cut(line, "\x00")
		if !ok || !strings.HasPrefix(name, "stash@{") {
			continue
		}

		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "stash@{"), "}"))
		if err != nil {
			continue
		}

		if len(text) > 80 {
			text = text[:80]
		}

		rows = append(rows, stashRow{Index: n, Text: text})
		if len(rows) >= 20 {
			break
		}
	}

	return rows
}
