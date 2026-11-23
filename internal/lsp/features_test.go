package lsp

import (
	"encoding/json"
	"testing"
)

func TestParseCompletionShapes(t *testing.T) {
	list := ParseCompletion(json.RawMessage(`{"isIncomplete":false,"items":[{"label":"a","kind":3,"insertTextFormat":2,"insertText":"a($1)","textEdit":{"newText":"a($1)","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":4}}}},{"label":"b","textEdit":{"newText":"b","insert":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"replace":{"start":{"line":0,"character":0},"end":{"line":0,"character":3}}}}]}`))
	if len(list) != 2 || list[0].TextEdit == nil || list[0].TextEdit.Range.End.Character != 4 || list[0].InsertTextFormat != InsertSnippet {
		t.Fatalf("list = %+v", list)
	}

	if list[1].TextEdit == nil || list[1].TextEdit.Range.End.Character != 3 {
		t.Fatalf("insert/replace edit must use the replace range: %+v", list[1].TextEdit)
	}

	if got := ParseCompletion(json.RawMessage(`[{"label":"x"}]`)); len(got) != 1 || got[0].Label != "x" {
		t.Fatalf("array = %+v", got)
	}

	if ParseCompletion(json.RawMessage(`null`)) != nil || ParseCompletion(nil) != nil {
		t.Fatal("null must parse to nil")
	}
}

func TestParseDocumentSymbolsHierarchicalAndFlat(t *testing.T) {
	h := ParseDocumentSymbols(json.RawMessage(`[{"name":"T","kind":23,"range":{"start":{"line":0,"character":0},"end":{"line":9,"character":0}},"selectionRange":{"start":{"line":0,"character":5},"end":{"line":0,"character":6}},"children":[{"name":"M","kind":6,"range":{"start":{"line":2,"character":0},"end":{"line":3,"character":0}},"selectionRange":{"start":{"line":2,"character":5},"end":{"line":2,"character":6}}}]}]`))
	if len(h) != 2 || h[0].Depth != 0 || h[1].Depth != 1 || h[1].Range.Start.Line != 2 {
		t.Fatalf("hierarchical = %+v", h)
	}

	f := ParseDocumentSymbols(json.RawMessage(`[{"name":"F","kind":12,"location":{"uri":"file:///a.go","range":{"start":{"line":4,"character":0},"end":{"line":5,"character":0}}}}]`))
	if len(f) != 1 || f[0].URI != "file:///a.go" || f[0].Range.Start.Line != 4 {
		t.Fatalf("flat = %+v", f)
	}

	if SymbolKindName(12) != "fn" || SymbolKindName(23) != "type" || SymbolKindName(5) != "class" {
		t.Fatal("kind names")
	}
}

func TestWorkspaceEditFileEdits(t *testing.T) {
	var we WorkspaceEdit

	raw := `{"changes":{"file:///a.go":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"X"}]},
	"documentChanges":[{"textDocument":{"uri":"file:///b.go","version":1},"edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"Y"}]},{"kind":"rename","oldUri":"file:///c","newUri":"file:///d"}]}`
	if err := json.Unmarshal([]byte(raw), &we); err != nil {
		t.Fatal(err)
	}

	edits, unsupported := we.FileEdits()
	if len(edits) != 2 || unsupported != 1 {
		t.Fatalf("edits=%v unsupported=%d", edits, unsupported)
	}
}

func TestApplyEditsUTF16AndOverlap(t *testing.T) {
	text := "a😀b foo\nfoo\n"
	// "foo" on line 0 starts at UTF-16 col 5 (a=1, emoji=2, b=1, space=1).
	edits := []TextEdit{
		{Range: Range{Start: Position{0, 5}, End: Position{0, 8}}, NewText: "bar"},
		{Range: Range{Start: Position{1, 0}, End: Position{1, 3}}, NewText: "bar"},
	}

	got, err := ApplyEdits(text, edits)
	if err != nil || got != "a😀b bar\nbar\n" {
		t.Fatalf("got %q err %v", got, err)
	}

	_, err = ApplyEdits(text, []TextEdit{
		{Range: Range{Start: Position{0, 0}, End: Position{0, 4}}, NewText: "x"},
		{Range: Range{Start: Position{0, 2}, End: Position{0, 5}}, NewText: "y"},
	})
	if err == nil {
		t.Fatal("overlapping edits must fail")
	}
}

func TestParseProgress(t *testing.T) {
	p, ok := ParseProgress(json.RawMessage(`{"token":"t1","value":{"kind":"begin","title":"Indexing","percentage":5}}`))
	if !ok || p.Token != "t1" || p.Kind != "begin" || p.Percent != 5 {
		t.Fatalf("%+v", p)
	}

	p, ok = ParseProgress(json.RawMessage(`{"token":7,"value":{"kind":"end"}}`))
	if !ok || p.Token != "7" || p.Percent != -1 {
		t.Fatalf("%+v", p)
	}

	if _, ok := ParseProgress(json.RawMessage(`{"token":1,"value":{}}`)); ok {
		t.Fatal("empty value is not progress")
	}
}
