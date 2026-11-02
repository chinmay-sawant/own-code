package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkWorkspace_ExpandMany guards the O(E*N log N) global re-sort
// regression: expanding many dirs must stay linear via batched Files merge
// (children-only sort), not a global re-sort per expand.
func BenchmarkWorkspace_ExpandMany(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 20; i++ {
		d := filepath.Join(dir, fmt.Sprintf("pkg%02d", i))
		if err := os.MkdirAll(d, 0o700); err != nil {
			b.Fatal(err)
		}
		for j := 0; j < 20; j++ {
			if err := os.WriteFile(filepath.Join(d, fmt.Sprintf("f%02d.go", j)), []byte("package x\n"), 0o600); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		app, err := New(dir)
		if err != nil {
			b.Fatal(err)
		}
		app.onToggleDir("pkg00")
		for j := 0; j < 200 && len(app.dirLoads) > 0; j++ {
			_ = app.tickWatch(context.Background())
		}
		app.Close()
	}
}
