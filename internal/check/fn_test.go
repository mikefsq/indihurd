package check

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestFuncBindingsResolve checks that every Func binding's Fn names a function
// or method that exists in its type package.
func TestFuncBindingsResolve(t *testing.T) {
	root := repoRoot(t)
	dirs, err := filepath.Glob(filepath.Join(root, "internal", "devtype", "*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no devtype packages found: %v", err)
	}
	for _, dir := range dirs {
		fset := token.NewFileSet()
		bf, err := parser.ParseFile(fset, filepath.Join(dir, "bindings.go"), nil, 0)
		if err != nil {
			continue // stub package: no table yet
		}
		var fns []string
		ast.Inspect(bf, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			if id, ok := kv.Key.(*ast.Ident); !ok || id.Name != "Fn" {
				return true
			}
			if lit, ok := kv.Value.(*ast.BasicLit); ok {
				if s, uerr := strconv.Unquote(lit.Value); uerr == nil {
					fns = append(fns, s)
				}
			}
			return true
		})
		if len(fns) == 0 {
			continue
		}
		decls := map[string]bool{}
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			af, perr := parser.ParseFile(fset, f, nil, 0)
			if perr != nil {
				t.Fatalf("%s: %v", f, perr)
			}
			for _, d := range af.Decls {
				if fd, ok := d.(*ast.FuncDecl); ok {
					decls[fd.Name.Name] = true
				}
			}
		}
		for _, fn := range fns {
			if !decls[fn] {
				t.Errorf("%s: bindings.go Fn %q names no function or method in the package",
					filepath.Base(dir), fn)
			}
		}
	}
}
