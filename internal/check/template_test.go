package check

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDevtypeTemplate enforces the per-type package file shape.
func TestDevtypeTemplate(t *testing.T) {
	root := repoRoot(t)
	dirs, err := filepath.Glob(filepath.Join(root, "internal", "devtype", "*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no devtype packages found: %v", err)
	}
	required := []string{"bindings.go", "device.go", "register.go", "bindings_test.go"}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		have := map[string]bool{}
		goFiles := 0
		for _, e := range entries {
			have[e.Name()] = true
			if !e.IsDir() && filepath.Ext(e.Name()) == ".go" {
				goFiles++
			}
		}
		if goFiles == 0 {
			continue
		}
		for _, f := range required {
			if !have[f] {
				t.Errorf("%s: implemented type package missing %s",
					filepath.Base(dir), f)
			}
		}
	}
}
