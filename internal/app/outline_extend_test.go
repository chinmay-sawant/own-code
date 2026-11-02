package app

import (
	"strings"
	"testing"
)

func outlineNames(rows []symbolRow) []string {
	names := make([]string, 0, len(rows))

	for _, row := range rows {
		names = append(names, row.Name)
	}

	return names
}

func TestOutlineJSON(t *testing.T) {
	lines := []string{
		"{",
		`  "name": "beacon",`,
		`  "server": {`,
		`    "port": 8080,`,
		`    "tls": true`,
		"  },",
		`  "tags": ["a", "b"]`,
		"}",
	}

	rows := outlineFull(langJSON, lines)
	if got := outlineNames(rows); len(got) != 5 ||
		got[0] != "name" || got[1] != "server" || got[2] != "port" || got[3] != "tls" || got[4] != "tags" {
		t.Fatalf("json outline = %v", got)
	}

	if rows[2].Depth != 2 {
		t.Fatalf("nested key depth = %d, want 2", rows[2].Depth)
	}
}

func TestOutlineYAML(t *testing.T) {
	lines := []string{
		"# a comment: not a key",
		"server:",
		"  host: example.com",
		"  port: 8080",
		"  # nested comment: skipped",
		"  tls:",
		"    enabled: true",
		"- item: ignored-position",
	}

	rows := outlineFull(langYAML, lines)
	got := outlineNames(rows)

	for _, want := range []string{"server", "host", "port", "tls", "enabled"} {
		found := false

		for _, name := range got {
			if name == want {
				found = true
			}
		}

		if !found {
			t.Fatalf("yaml outline = %v, missing %q", got, want)
		}
	}

	for _, name := range got {
		if strings.Contains(name, "comment") {
			t.Fatalf("yaml outline lists a comment: %v", got)
		}
	}
}

func TestOutlineHTML(t *testing.T) {
	lines := []string{
		"<!-- <div id=\"ghost\"> -->",
		`<div id="main">`,
		"  <span>hi</span>",
		"</div>",
		`<input type="text">`,
	}

	rows := outlineFull(langHTML, lines)
	got := outlineNames(rows)

	if len(got) != 3 || got[0] != "div#main" || got[1] != "span" || got[2] != "input" {
		t.Fatalf("html outline = %v", got)
	}
}

func TestOutlineShell(t *testing.T) {
	lines := []string{
		"# comment() is not a function",
		"function serve {",
		"  echo hi",
		"}",
		"deploy() {",
		"  echo up",
		"}",
		"echo done",
	}

	rows := outlineFull(langShell, lines)
	got := outlineNames(rows)

	if len(got) != 2 || got[0] != "serve" || got[1] != "deploy" {
		t.Fatalf("shell outline = %v", got)
	}
}

func TestOutlineSkipsComments(t *testing.T) {
	goLines := []string{"// func ghost() {", "func Real() {}"}
	if rows := outlineFull(langGo, goLines); len(rows) != 1 || rows[0].Name != "Real" {
		t.Fatalf("go outline = %+v", rows)
	}

	pyLines := []string{"# def ghost():", "def real():"}
	if rows := outlineFull(langPython, pyLines); len(rows) != 1 || rows[0].Name != "real" {
		t.Fatalf("python outline = %+v", rows)
	}

	jsLines := []string{"// function ghost() {", "function real() {}"}
	if rows := outlineFull(langJS, jsLines); len(rows) != 1 || rows[0].Name != "real" {
		t.Fatalf("js outline = %+v", rows)
	}
}

func TestOutlineJSArrowsAndMethods(t *testing.T) {
	lines := []string{
		"const render = (props) => {",
		"};",
		"let load = async (url) => {",
		"};",
		"interface Shape {",
		"}",
		"enum Kind {",
		"}",
		"type ID = string;",
		"fetchData(url) {",
		"}",
		"if (open) {",
		"}",
	}

	rows := outlineFull(langJS, lines)
	got := outlineNames(rows)

	for _, want := range []string{"render", "load", "Shape", "Kind", "ID", "fetchData"} {
		found := false

		for _, name := range got {
			if name == want {
				found = true
			}
		}

		if !found {
			t.Fatalf("js outline = %v, missing %q", got, want)
		}
	}

	for _, name := range got {
		if name == "if" || name == "open" {
			t.Fatalf("js outline lists control flow: %v", got)
		}
	}
}

func TestOutlinePythonTabDepth(t *testing.T) {
	lines := []string{
		"class App:",
		"\tdef method(self):",
		"\t\tdef nested():",
		"\t\t\tpass",
	}

	rows := outlineFull(langPython, lines)
	if len(rows) != 3 {
		t.Fatalf("python outline = %+v", rows)
	}

	if rows[0].Depth != 0 || rows[1].Depth != 1 || rows[2].Depth != 2 {
		t.Fatalf("tab depths = %d,%d,%d", rows[0].Depth, rows[1].Depth, rows[2].Depth)
	}
}

func TestOutlineCapSplit(t *testing.T) {
	lines := make([]string, 0, 80)
	for i := range 80 {
		lines = append(lines, "func fn"+string(rune('a'+i%26))+string(rune('a'+i/26))+"() {}")
	}

	full := outlineFull(langGo, lines)
	if len(full) != 80 {
		t.Fatalf("full outline = %d rows, want 80", len(full))
	}

	capped := outline(langGo, lines)
	if len(capped) != maxOutlineRows {
		t.Fatalf("drawer outline = %d rows, want %d", len(capped), maxOutlineRows)
	}
}
