package app

// View is the data the code view template prints. Every field is a plain
// value or a prebuilt row, so one SetData call publishes one frame.
type View struct {
	EditorScroll editorScrollView
	Mode         string
	Filter       string
	Details      bool
	DevOn        bool
	Modal        bool
	MenuOpen     bool
	MenuX        int
	MenuY        int
	MenuRows     []menuViewRow

	Workspace   string
	Branch      string
	BranchQuery string
	Changed     int
	DevNote     string
	ScanBudget  string
	WatchNote   string
	HoverPath   string

	Tree         []treeRow
	StickyTree   []treeRow
	TreeCount    int
	TreeTotal    int
	TreeStart    int
	TreeVisible  int
	TreeScroll   bool
	TreeThumbTop int
	TreeThumbH   int
	TreeTrackH   int
	Filtered     bool
	FilterNote   string
	DirLoadNote  string

	// ExplorerInputOpen/Title publish the inline new-file/rename prompt
	// state for the sidebar template.
	ExplorerInputOpen  bool
	ExplorerInputTitle string

	// SideW is the drawer width in page pixels, resizable from 268 px up
	// to 348 px (about 30% wider) via the drawer-resize handle.
	// DrawerResizeLeft/Top/H position that handle on the drawer boundary.
	SideW            int
	DrawerResizeLeft int
	DrawerResizeTop  int
	DrawerResizeH    int

	Tabs            []tabRow
	TabsPrev        bool
	TabsNext        bool
	ActiveTab       int
	Active          string
	ActiveDir       string
	ActiveName      string
	ActiveDirShort  string
	ActiveNameShort string
	CmdText         string
	CmdTitle        string
	NavBack         bool
	NavForward      bool
	NavBackTitle    string
	NavForwardTitle string
	TitleBar        bool
	TitleBarMax     bool
	Lang            string
	LangLabel       string
	Badge           string
	LineCount       int
	ByteSize        string
	Encoding        string
	Indent          string
	Status          string
	StatusClass     string
	Truncated       bool

	Rows        []codeRow
	TrackTop    int
	SpaceHeight int
	Wrap        bool
	ScrollX     int
	// BlameOn is the blame gutter toggle. BlameShow reports whether the
	// column paints for the open file; BlameNote carries the busy or
	// unavailable reason (loading, sample, binary, or large file).
	BlameOn   bool
	BlameShow bool
	BlameNote string
	// Sticky pins the enclosing symbol chain above the code while it
	// scrolls. Empty in diff, preview, and merge modes and in files with
	// no symbols.
	Sticky      []stickyRow
	FindQuery   string
	FindOpen    bool
	FindCase    bool
	FindCurrent int
	FindTotal   int
	// ReplaceOpen shows the replace row under the find bar. ReplaceQuery is
	// the replacement text; ReplaceRegex and ReplaceWholeWord are the shared
	// file matcher toggles. FindError carries an invalid-regex message and
	// disables the Replace buttons.
	ReplaceOpen      bool
	ReplaceQuery     string
	ReplaceRegex     bool
	ReplaceWholeWord bool
	FindError        string
	Welcome          bool
	Preview          bool
	Previewable      bool
	PreviewRows      []previewRow
	Empty            bool
	EmptyKind        string
	EmptyTitle       string
	EmptyBody        string
	Recent           []recentRow

	Editing   bool
	EditText  string
	EditDirty bool
	CanEdit   bool
	SaveNote  string
	Dirty     int

	Split      bool
	FocusGroup int
	// FocusedPane mirrors FocusGroup for templates: 0 or 1 in split mode,
	// 0 otherwise. It exists so pane highlight reads as pane state rather
	// than a bare group index.
	FocusedPane int
	PaneLeft    int
	PaneWidth   int
	// SplitDivLeft/SplitDivW place the draggable split divider on the
	// 1 px gutter between the panes. Only meaningful when Split.
	SplitDivLeft int
	SplitDivW    int
	Second       secondaryView

	Diff         diffView
	Merge        mergeView
	History      historyView
	Terminal     terminalView
	Outline      []symbolRow
	Toast        toastView
	Confirm      confirmView
	SettingsOpen bool
	Themes       []themeRow
	Palette      paletteView
	PaletteQuery string
	SearchQuery  string
	// SearchReplace carries the workspace replace input. It is top-level
	// because data-bind only addresses top-level view fields; the panel
	// template reads it as .SearchReplace.
	SearchReplace  string
	Search         searchView
	FileNav        fileNavView
	ChangeLabel    string
	CommitMessage  string
	CommitDialog   commitDialogView
	CommitTip      commitFloatView
	InlineCard     inlineCard
	FileMenu       bool
	ProjectOpen    bool
	ProjectQuery   string
	RecentOpen     bool
	RecentFlyX     int
	RecentFlyY     int
	RecentRoots    []recentRootRow
	QuickRoots     []recentRootRow
	BrowseOpen     bool
	BrowseRoot     string
	BrowseName     string
	BrowseQuery    string
	BrowseEntries  []recentRootRow
	BrowseTotal    int
	BrowseScroll   bool
	BrowseThumbTop int
	BrowseThumbH   int
	BrowseTrackH   int
	IsWSL          bool
	IsWindows      bool
	HasWSL         bool
	Alert          string
	ZoomLabel      string
	// Settings panel state. SettingsTab selects appearance/keyboard/files;
	// FontLabel is the "12px" chip text; EditorZoom/TerminalZoom are the
	// v1 per-view overrides (0 when following the global zoom);
	// EditorZoomLabel/TerminalZoomLabel render the override percent or "—".
	SettingsTab       string
	FontLabel         string
	EditorZoom        float64
	TerminalZoom      float64
	EditorZoomLabel   string
	TerminalZoomLabel string
	KeymapRows        []keymapRow
	KeymapCapture     string
	KeymapPending     string
	ScanText          string
	SearchText        string
	// ScanBad/SearchBad are the 1-based line numbers holding invalid globs.
	ScanBad       []int
	SearchBad     []int
	UseGitignore  bool
	SearchIgnored bool
	ScanMatch     int
	SearchSkip    int
	SettingsNote  string
	// SettingsScroll drives the settings edge rail (see settings_scroll.go).
	// The rail shows only on overflow; ScrollTop is the negative pixel
	// offset of the translated inner, TrackH/ThumbTop/ThumbH the rail
	// geometry.
	SettingsScroll    bool
	SettingsScrollTop int
	SettingsTrackH    int
	SettingsThumbTop  int
	SettingsThumbH    int
	// SettingsRailLeft/Top pin the window-level fixed rail to the
	// viewport's right edge. Inside this fixed dialog the engine anchors
	// fixed coordinates to the settings panel, so both are published
	// panel-relative (page scroll cancels out of the difference).
	SettingsRailLeft int
	SettingsRailTop  int
	// AutoSave is off, afterDelay, or onFocusChange; AutoSaveDelay is the
	// afterDelay debounce in ms, clamped to 250-5000.
	AutoSave      string
	AutoSaveDelay int
	// MaxFPS is the effective desktop present cap (0=default 60, -1=uncapped);
	// FPSLabel is its chip text and FPSOptions are the Settings buttons.
	MaxFPS     int
	FPSLabel   string
	FPSOptions []fpsOptionRow
}

// keymapRow is one Settings → Keyboard row.
type keymapRow struct {
	Action    string
	Label     string
	Chord     string
	Default   string
	Custom    bool
	Fixed     bool
	Capturing bool
	Conflict  bool
}

// recentRootRow is one recently opened project root.
type recentRootRow struct {
	Path   string
	Name   string
	Active bool
}

// menuViewRow is one right-click row the page paints itself.
type menuViewRow struct {
	Index   int
	ID      string
	Label   string
	Hint    string
	Enabled bool
}

// commitFloatView is the page-space commit message card that floats to the
// right of the history rail.
type commitFloatView struct {
	Show  bool
	Left  int
	Top   int
	Width int
	Lines []string
}

// fileNavView is the bottom-center changed-file navigator.
type fileNavView struct {
	Open          bool
	Label         string
	Path          string
	Name          string
	Dir           string
	Position      int
	Total         int
	Add           int
	Del           int
	Multiple      bool
	CanPrevChange bool
	CanNextChange bool
}

// secondaryView is the right-hand editor group. It carries only the fields
// that pane draws; the left group keeps the top-level View fields.
type secondaryView struct {
	EditorScroll    editorScrollView
	Open            bool
	Left            int
	Width           int
	Tabs            []tabRow
	TabsPrev        bool
	TabsNext        bool
	ActiveTab       int
	Active          string
	ActiveDir       string
	ActiveName      string
	ActiveDirShort  string
	ActiveNameShort string
	Lang            string
	LangLabel       string
	Badge           string
	LineCount       int
	ByteSize        string
	Status          string
	StatusClass     string
	Truncated       bool

	Rows     []codeRow
	TrackTop int
	ScrollX  int
	// BlameShow/BlameNote mirror the top-level blame gutter for the right
	// pane: per-group docs can differ (binary on one side, text on the
	// other), so each pane carries its own state.
	BlameShow bool
	BlameNote string
	// Sticky pins the enclosing symbol chain above the second pane's
	// code. Same exclusions as the first pane's Sticky.
	Sticky      []stickyRow
	Welcome     bool
	Preview     bool
	Previewable bool
	PreviewRows []previewRow
	Empty       bool
	EmptyKind   string
	EmptyTitle  string
	EmptyBody   string
	// Focused marks the right pane as the focused one for highlight.
	Focused bool

	Editing   bool
	EditText  string
	EditDirty bool
	CanEdit   bool
}

// searchView is the workspace content search panel.
type searchView struct {
	Open         bool
	Query        string
	Case         bool
	Index        int
	Files        int
	Total        int
	Note         string
	Truncated    bool
	Busy         bool
	Scroll       bool
	ScrollTop    int
	ScrollHeight int
	Rows         []searchRow
	// Replace is the workspace replacement text; Regex and WholeWord are
	// the shared workspace matcher toggles. Error carries an invalid-regex
	// message. TotalAll counts every match the last pass saw (past the
	// preview caps) and Capped reports the count itself hit the ceiling.
	// CanReplace gates the Replace All button; ReplaceFiles and
	// ReplaceTotal count the checked files and their preview hits.
	// Skipped summarizes the apply eligibility report; CanUndoReplace and
	// UndoNote drive the explicit workspace-undo button.
	Replace        string
	Regex          bool
	WholeWord      bool
	Error          string
	TotalAll       int
	Capped         bool
	CanReplace     bool
	ReplaceFiles   int
	ReplaceTotal   int
	Skipped        string
	CanUndoReplace bool
	UndoNote       string
}

// searchRow is one content-search match.
type searchRow struct {
	Index    int
	Path     string
	Name     string
	Dir      string
	Line     int
	Col      int
	Before   string
	Match    string
	After    string
	Selected bool
	// Replacement is the replace-mode preview for this hit. Checked is the
	// file-level checkbox state shared by every hit in the file. GroupFirst
	// marks the first visible hit of a file and GroupCount carries that
	// file's preview hit count for the group header.
	Replacement string
	Checked     bool
	GroupFirst  bool
	GroupCount  int
}

// treeRow is one file or folder in the explorer.
type treeRow struct {
	Path string
	Name string
	// Short is the clipped display name. The engine wraps a long name with
	// spaces onto a second row instead of truncating it, so the row clips
	// the name in Go and marks the cut with an ellipsis. The button title
	// keeps the full path.
	Short   string
	Dir     bool
	Open    bool
	Ignored bool
	// IgnoredNote renders the "ignored" pill. It is set only on the
	// topmost ignored row; children of an ignored parent stay dim
	// without repeating the label.
	IgnoredNote bool
	Indent      int
	Lang        string
	Badge       string
	Status      string
	StatusClass string
	// Selected marks a multi-select explorer row. Ctrl+click toggles it and
	// Shift+click extends the range; Copy and Cut stage every selected row.
	Selected bool
	// Dirty mirrors the tab-dot so the explorer shows unsaved edits even
	// when the file's tab is scrolled out of the strip window.
	Dirty bool
	// Loading marks the synthetic spinner child of an expanding folder.
	// LoadErr marks the synthetic retry row. More marks the trailing
	// "N more…" cap row. All three are non-focusable placeholders excluded
	// from TreeCount and the filter scope.
	Loading   bool
	LoadErr   string
	More      bool
	MoreCount int
}

// tabRow is one open editor tab. Index is the tab's position in the
// group's full list, so a windowed strip keeps working actions and ids.
type tabRow struct {
	Index       int
	Path        string
	Name        string
	Lang        string
	Badge       string
	Status      string
	StatusClass string
	Active      bool
	Dirty       bool
	Preview     bool
	Pinned      bool
}

// codeRow is one visual source row with its prebuilt spans. No is zero on a
// wrapped continuation row.
type codeRow struct {
	No        int
	Line      int
	Start     int
	Segments  []segment
	Mark      string
	MarkClass string
	Hint      string
	// Blame is the gutter annotation for the line's last commit: the short
	// hash, or "Not committed" for uncommitted lines. BlameTitle is the
	// hover text (hash plus author) and BlameHash is the full commit hash,
	// empty for uncommitted lines. All three are empty on wrapped
	// continuation rows and while blame is off.
	Blame          string
	BlameTitle     string
	BlameHash      string
	ChangeClass    string
	ChangeCurrent  bool
	ChangeLine     int
	Caret          bool
	CaretLeft      int
	Selection      bool
	SelectionLeft  int
	SelectionWidth int
	ScrollX        int
	// RelNo is the gutter display number (relative or absolute). Zero
	// blanks the number cell on wrapped continuation rows.
	RelNo int
}

// recentRow is one quick-open entry on the welcome panel.
type recentRow struct {
	Path  string
	Name  string
	Dir   string
	Lang  string
	Badge string
}

// diffView is the Source Control panel and the diff header.
type diffView struct {
	Files    []diffFileRow
	Staged   []diffFileRow
	Unstaged []diffFileRow
	// Conflicts holds unmerged paths shown above the staged and unstaged
	// sections. ConflictCount is included in Count and HasChanges.
	Conflicts     []diffFileRow
	ConflictCount int
	HasConflicts  bool
	Selected      string
	SelectedCount int
	Split         bool
	Status        string
	StatusCls     string
	Path          string
	Name          string
	Dir           string
	NameShort     string
	DirShort      string
	Lang          string
	Badge         string
	Add           int
	Del           int
	TotalAdd      int
	TotalDel      int
	Count         int
	StagedCount   int
	UnstagedCount int
	Hunks         int
	Rows          []diffRow
	TrackTop      int
	ScrollX       int
	HasFiles      bool
	HasChanges    bool
	HasStaged     bool
	HasUnstaged   bool
	CanEdit       bool
	// CanRevert gates the per-hunk revert arrow in the unified and split
	// diff. It is working-tree only, skips binary and oversized files, and
	// skips renames whose patch would also undo the rename.
	CanRevert bool
	// CanPrevChange and CanNextChange gate the crumbs up/down arrows. They
	// track change blocks, not hunk headers, so they agree with the
	// bottom-center navigator.
	CanPrevChange bool
	CanNextChange bool
	Page          int
	Pages         int
	Scroll        bool
	ScrollTop     int
	ScrollHeight  int
	ScrollTrack   int
	RailLeft      int
	RailTop       int
	RailThumbTop  int
	ThumbCells    int
	AtTop         bool
	AtEnd         bool
	Sample        bool
	Note          string
	EmptyTitle    string
	Source        string
	CommitHash    string
	CommitShort   string
	CommitSubject string
	CommitAuthor  string
	CommitDate    string
	Loading       bool
}

// historyView is the commit rail above the diff.
type historyView struct {
	Visible      bool
	Sample       bool
	Height       int
	Branch       string
	HeadBranch   string
	Browsing     bool
	Detached     bool
	Branches     []branchRow
	BranchOpen   bool
	BranchQuery  string
	BranchMore   int
	Commits      []commitRow
	Page         int
	Pages        int
	Tip          commitTipView
	Note         string
	Unborn       bool
	Loading      bool
	Scroll       bool
	ScrollTop    int
	ScrollHeight int
	ScrollTrack  int
	AtTop        bool
	AtEnd        bool
	LoadingMore  bool
	RailLeft     int
	RailTop      int
	RailThumbTop int
	ThumbCells   int
}

// commitRow is one commit in the history rail.
type commitRow struct {
	Hash        string
	Short       string
	Subject     string
	Lines       []string
	Author      string
	Date        string
	Body        string
	Add         int
	Del         int
	Files       []commitFileRow
	More        int
	Expanded    bool
	Selected    bool
	Head        bool
	Preview     []diffRow
	PreviewPath string
	PreviewErr  string
	Graph       []graphCell
	GraphEdges  []graphEdge
	GraphX      int
	GraphColor  string
	Lanes       int
}

// commitFileRow is one changed file inside an expanded commit.
type commitFileRow struct {
	Path        string
	Name        string
	Dir         string
	Status      string
	StatusClass string
	Lang        string
	Badge       string
	Add         int
	Del         int
	Selected    bool
}

// commitTipView is the hover card for one commit.
type commitTipView struct {
	Show    bool
	Top     int
	Subject string
	Body    string
	Meta    string
}

// diffFileRow is one changed file in the source control list. Staged marks
// the staged section and Multi marks the Ctrl-click and Ctrl+A set.
type diffFileRow struct {
	Path        string
	Name        string
	Dir         string
	Status      string
	StatusClass string
	Lang        string
	Badge       string
	Add         int
	Del         int
	Selected    bool
	Staged      bool
	Multi       bool
	// Conflicts is the number of marker blocks in an unmerged path.
	Conflicts int
}

// mergeView is the merge editor header and its windowed rows.
type mergeView struct {
	Open        bool
	Unavailable bool
	Path        string
	Name        string
	Dir         string
	NameShort   string
	DirShort    string
	// Index is 1-based and only meaningful when Total is greater than zero.
	Index      int
	Total      int
	Compare    bool
	Dirty      bool
	CanStage   bool
	HasMarkers bool
	// Narrow shortens the header controls in the split-width editor.
	Narrow     bool
	BaseSource string
	Note       string
	TrackTop   int
	Rows       []mergeRow
}

// mergeRow is one fixed-height row in the merge editor: a conflict header,
// one side's source line, an action row, or a fallback note.
type mergeRow struct {
	Kind     string
	Side     string
	Conflict int
	Current  bool
	// SideFirst marks the first line of a side block. Only that row prints
	// the side name; the rest keep the side background without the label.
	SideFirst bool
	Label     string
	No        string
	Text      string
}

// symbolRow is one outline entry in the details panel.
type symbolRow struct {
	Name  string
	Kind  string
	Line  int
	No    int
	Depth int
}

// commitDialogView is the commit dialog: staged files with stats, the
// shortstat line, the message draft, and the inline error. Message is the
// draft text; the template binds the textarea to the top-level
// CommitMessage field because data-bind only addresses top-level fields.
type commitDialogView struct {
	Show      bool
	Message   string
	Files     []diffFileRow
	FileCount int
	Shortstat string
	Error     string
}

// toastView is the transient message card.
type toastView struct {
	Show    bool
	Kind    string
	Title   string
	Message string
}

// confirmView is the destructive-action dialog, such as Discard. OkLabel
// names the confirm button: Discard for discards, Replace for replace-all.
// Kind names the dialog: discard kinds plus unsaved-close-one/many, whose
// Save/Don't Save/Cancel arms the template paints instead.
type confirmView struct {
	Show    bool
	Kind    string
	Title   string
	Message string
	Files   []string
	Count   int
	OkLabel string
	OkHint  string
}
