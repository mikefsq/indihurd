// Package corpus resolves shared testdata paths from any package in the module.
package corpus

import (
	"path/filepath"
	"runtime"
)

// Dir returns the module's testdata directory.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata")
}

// Recording returns the path of a captured driver session by name.
func Recording(name string) string { return filepath.Join(Dir(), "recordings", name) }

// Dump returns the path of a contributed property dump by name.
func Dump(name string) string { return filepath.Join(Dir(), "dumps", name) }
