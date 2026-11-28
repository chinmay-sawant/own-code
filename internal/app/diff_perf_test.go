package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func benchRunGit(tb testing.TB, dir string, args ...string) {
	tb.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func setupDiffPerfApp(tb testing.TB, split bool) *App {
	tb.Helper()
	dir := tb.TempDir()
	benchRunGit(tb, dir, "init", "-q")
	benchRunGit(tb, dir, "config", "user.email", "bench@example.com")
	benchRunGit(tb, dir, "config", "user.name", "Bench")

	var orig, next strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&orig, "func handler%d(ctx context.Context, input string) (int, error) { return len(input) + %d, nil }\n", i, i)
		if i%3 == 0 {
			fmt.Fprintf(&next, "func handler%d(ctx context.Context, input string, flag bool) (int, error) { return len(input)*2 + %d, nil }\n", i, i)
		} else {
			fmt.Fprintf(&next, "func handler%d(ctx context.Context, input string) (int, error) { return len(input) + %d, nil }\n", i, i)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "service.go"), []byte(orig.String()), 0o600); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.go"), []byte(orig.String()), 0o600); err != nil {
		tb.Fatal(err)
	}
	benchRunGit(tb, dir, "add", "-A")
	benchRunGit(tb, dir, "commit", "-q", "-m", "initial")

	if err := os.WriteFile(filepath.Join(dir, "service.go"), []byte(next.String()), 0o600); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.go"), []byte(next.String()), 0o600); err != nil {
		tb.Fatal(err)
	}

	a, err := New(dir)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(a.Close)

	ctx := context.Background()
	a.Page().SetSize(1580, 1032)
	a.ReloadChanges(ctx, false)
	a.setMode(ctx, "changes")
	a.setSplit(split)
	a.selectDiff("service.go")
	if err := a.Page().Redraw(ctx); err != nil {
		tb.Fatal(err)
	}
	return a
}

func BenchmarkDiffHoverAndA11y(b *testing.B) {
	a := setupDiffPerfApp(b, true)
	ctx := context.Background()

	codeBody, ok := findBoxID(a.Page().Boxes(), "code-body")
	if !ok {
		b.Fatal("no code-body box")
	}
	type pt struct{ x, y float64 }
	var pts []pt
	for i := 0; i < 40 && i < len(a.View().Diff.Rows); i++ {
		pts = append(pts, pt{x: codeBody.X + 200, y: codeBody.Y + float64(i*rowH) + float64(rowH)/2})
	}
	if len(pts) < 2 {
		b.Fatalf("expected >= 2 diff row hover points, got %d", len(pts))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := pts[i%len(pts)]
		if err := a.Page().Edit().Hover(ctx, p.x, p.y); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDiffSelectSwitch(b *testing.B) {
	a := setupDiffPerfApp(b, true)
	ctx := context.Background()
	paths := []string{"service.go", "other.go"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.selectDiff(paths[i%len(paths)])
		if err := a.Page().Redraw(ctx); err != nil {
			b.Fatal(err)
		}
		if _, err := a.Page().Access().AccessibilityTree(); err != nil {
			b.Fatal(err)
		}
	}
}
