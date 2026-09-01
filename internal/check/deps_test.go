package check

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func ruleFor(dir string) (string, bool) {
	if _, ok := allowed[dir]; ok {
		return dir, true
	}
	if strings.HasPrefix(dir, "internal/devtype/") {
		return "internal/devtype/*", true
	}
	return "", false
}

// TestImportBoundaries fails on any module or goalpaca import outside the
// importing package's allowlist entry.
func TestImportBoundaries(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "third-party", "build", "testdata", "spikes", "docs", "e2e", "bin", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		dir := filepath.ToSlash(filepath.Dir(rel))
		if !strings.HasPrefix(dir, "cmd/") && !strings.HasPrefix(dir, "internal/") {
			return nil
		}
		key, ok := ruleFor(dir)
		if !ok {
			t.Errorf("%s: package dir %q has no entry in check.allowed — add it deliberately", rel, dir)
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		isTest := strings.HasSuffix(path, "_test.go")
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(p, module) && !strings.HasPrefix(p, goalpaca) {
				continue
			}
			if p == module+"/"+dir {
				continue // own package (external test package form)
			}
			if permitted(p, allowed[key]) || (isTest && permitted(p, testExtra)) {
				continue
			}
			t.Errorf("%s: import %q not allowed for %q (see internal/check/deps.go)", rel, p, key)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func permitted(imp string, prefixes []string) bool {
	for _, pre := range prefixes {
		if imp == pre || strings.HasPrefix(imp, strings.TrimSuffix(pre, "/")+"/") {
			return true
		}
	}
	return false
}
