package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConsolePathHasNoOpenCodeServerRoutes greps the non-test source of the
// Console agent packages for OpenCode Server process and route references.
func TestConsolePathHasNoOpenCodeServerRoutes(t *testing.T) {
	forbidden := []string{"opencode serve", `"/session`, "/prompt_async", `"/event`, `"/global/event`, `"/message"`}
	for _, dir := range []string{".", "../agentstore", "../agenttools", "../agentbridge"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("Glob(%s) = %v, %v", dir, files, err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			for _, needle := range forbidden {
				if strings.Contains(string(data), needle) {
					t.Errorf("%s references %q", file, needle)
				}
			}
		}
	}
}
