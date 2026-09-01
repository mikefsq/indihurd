package check

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// TestDevtypeHasActions checks that every implemented type overrides both
// SupportedActions and Action; missing them fails silently at runtime.
func TestDevtypeHasActions(t *testing.T) {
	root := repoRoot(t)
	dirs, err := filepath.Glob(filepath.Join(root, "internal", "devtype", "*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no devtype packages found: %v", err)
	}
	for _, dir := range dirs {
		fset := token.NewFileSet()
		files, _ := filepath.Glob(filepath.Join(dir, "device.go"))
		if len(files) == 0 {
			continue // stub package: not yet implemented
		}
		af, perr := parser.ParseFile(fset, files[0], nil, 0)
		if perr != nil {
			t.Fatalf("%s: %v", files[0], perr)
		}
		has := map[string]bool{}
		for _, d := range af.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv != nil {
				has[fd.Name.Name] = true
			}
		}
		if !has["SupportedActions"] || !has["Action"] {
			t.Errorf("%s: device.go defines neither or only one of SupportedActions/Action — "+
				"the INDI: passthrough is not wired", filepath.Base(dir))
		}
	}
}
