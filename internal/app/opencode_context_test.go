package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCaptureAgentContextSelectionSnapshot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("disk contents\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	document := "package demo\n\nfunc greet() string {\n\treturn \"café\"\n}\n"
	selection := strings.Index(document, "café")
	from := utf8.RuneCountInString(document[:selection])
	to := from + utf8.RuneCountInString("café")

	snapshot, err := captureAgentContext(root, "main.go", document, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.WorkspaceRoot != root || snapshot.RelativePath != "main.go" {
		t.Fatalf("path = %q/%q", snapshot.WorkspaceRoot, snapshot.RelativePath)
	}
	contextRunes := []rune(snapshot.Text)
	if snapshot.SelectionText != "café" || string(contextRunes[snapshot.SelectionFrom:snapshot.SelectionTo]) != "café" {
		t.Fatalf("selection text/range = %q, %d..%d", snapshot.SelectionText, snapshot.SelectionFrom, snapshot.SelectionTo)
	}
	if snapshot.Version == "" || snapshot.Version != agentContextVersion(document) {
		t.Fatalf("version = %q, want document digest", snapshot.Version)
	}
	if !strings.Contains(snapshot.Text, "return \"café\"") {
		t.Fatalf("snapshot omitted selected editor contents: %q", snapshot.Text)
	}
	if snapshot.DocumentFrom != from || snapshot.DocumentTo != to {
		t.Fatalf("document range = %d..%d, want %d..%d", snapshot.DocumentFrom, snapshot.DocumentTo, from, to)
	}
	reversed, err := captureAgentContext(root, "main.go", document, to, from)
	if err != nil {
		t.Fatal(err)
	}
	if reversed.SelectionText != "café" || reversed.DocumentFrom != from || reversed.DocumentTo != to {
		t.Fatalf("reversed selection = %+v", reversed)
	}

	// The captured value and hash do not change if the live buffer is edited.
	document = strings.ReplaceAll(document, "café", "tea")
	if snapshot.SelectionText != "café" || snapshot.Version == agentContextVersion(document) {
		t.Fatal("snapshot changed when the live document changed")
	}
}

func TestCaptureAgentContextBoundsExcerptAndKeepsCaretLine(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte("placeholder\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 1200)
	for i := range lines {
		lines[i] = strings.Repeat("x", 80) + " marker " + strings.Repeat("y", 80)
	}
	document := strings.Join(lines, "\n")
	line := 700
	caret := line*utf8.RuneCountInString(lines[0]+"\n") + 90

	snapshot, err := captureAgentContext(root, "large.txt", document, caret, caret)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Text) > maxOpenCodeContextBytes {
		t.Fatalf("excerpt is %d bytes, limit %d", len(snapshot.Text), maxOpenCodeContextBytes)
	}
	if !snapshot.Truncated || !strings.Contains(snapshot.Text, "marker") || snapshot.DocumentFrom != caret {
		t.Fatalf("snapshot lost caret context: %+v", snapshot)
	}
}

func TestCaptureAgentContextRejectsUnsafePathsAndSelections(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(root, ".env")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureAgentContext(root, ".env", "secret", 0, 1); !errors.Is(err, errSensitiveAgentContext) {
		t.Fatalf("sensitive file error = %v", err)
	}
	if _, err := captureAgentContext(root, "../outside", "text", 0, 0); !errors.Is(err, errInvalidAgentContextPath) {
		t.Fatalf("traversal error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secrets", "provider.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureAgentContext(root, "secrets/provider.txt", "secret", 0, 1); !errors.Is(err, errSensitiveAgentContext) {
		t.Fatalf("sensitive directory error = %v", err)
	}
	if _, err := captureAgentContext(root, "missing.txt", "text", 0, 0); !errors.Is(err, errInvalidAgentContextPath) {
		t.Fatalf("missing file error = %v", err)
	}
	if _, err := captureAgentContext(root, ".env", "text", 3, 1); !errors.Is(err, errSensitiveAgentContext) {
		t.Fatalf("sensitive path should fail before selection validation, got %v", err)
	}
}

func TestCaptureAgentContextRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if _, err := captureAgentContext(root, "linked/secret.txt", "secret", 0, 6); !errors.Is(err, errInvalidAgentContextPath) {
		t.Fatalf("symlink escape error = %v", err)
	}
}

func TestCaptureAgentContextRejectsInvalidSelectionAndOversizedSelection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("contents\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureAgentContext(root, "main.txt", "abc", -1, 1); !errors.Is(err, errInvalidAgentSelection) {
		t.Fatalf("negative selection error = %v", err)
	}
	if _, err := captureAgentContext(root, "main.txt", "abc", 2, 4); !errors.Is(err, errInvalidAgentSelection) {
		t.Fatalf("out of range error = %v", err)
	}
	oversized := strings.Repeat("é", maxOpenCodeContextBytes)
	if _, err := captureAgentContext(root, "main.txt", oversized, 0, utf8.RuneCountInString(oversized)); !errors.Is(err, errAgentContextTooLarge) {
		t.Fatalf("oversized selection error = %v", err)
	}

	longLine := strings.Repeat("x", maxOpenCodeContextBytes+4096)
	caret := len(longLine) / 2
	snapshot, err := captureAgentContext(root, "main.txt", longLine, caret, caret+1)
	if err != nil {
		t.Fatalf("small selection on a long line: %v", err)
	}
	if len(snapshot.Text) > maxOpenCodeContextBytes || snapshot.SelectionText != "x" || !snapshot.Truncated {
		t.Fatalf("long-line snapshot bytes=%d selection=%q truncated=%t", len(snapshot.Text), snapshot.SelectionText, snapshot.Truncated)
	}
}

func agentContextVersion(text string) string {
	// Keep the expected digest independent from implementation details except
	// for the public snapshot contract: SHA-256 over the complete editor text.
	digest := sha256.Sum256([]byte(text))
	return hex.EncodeToString(digest[:])
}
