// Package check enforces the import boundaries and the per-type package template.
package check

// Import checks cover these module roots.
const (
	module   = "github.com/mikefsq/indihurd"
	goalpaca = "github.com/mikefsq/goalpaca"
)

// allowed maps package directories to permitted import prefixes.
var allowed = map[string][]string{
	"cmd/indihurd": {module + "/internal/host"},

	"internal/indiwire":  {}, // stdlib only
	"internal/transport": {module + "/internal/indiwire"},
	"internal/snapshot":  {module + "/internal/indiwire"},
	"internal/supervisor": {
		module + "/internal/transport",
		module + "/internal/indiwire",
		module + "/internal/snapshot",
		module + "/internal/binding",
		goalpaca + "/server",
	},
	"internal/binding": {
		module + "/internal/indiwire",
		module + "/internal/snapshot",
		goalpaca + "/server",
		goalpaca + "/alpaca",
	},
	"internal/imagepath": {goalpaca + "/alpaca"},
	"internal/indiserve": {
		module + "/internal/indiwire",
		module + "/internal/snapshot",
	},
	"internal/actions": {
		module + "/internal/binding",
		module + "/internal/snapshot",
		module + "/internal/indiwire",
	},
	"internal/devtype/*": {
		module + "/internal/binding",
		module + "/internal/snapshot",
		module + "/internal/indiwire",
		module + "/internal/imagepath",
		module + "/internal/actions",
		goalpaca + "/server",
		goalpaca + "/alpaca",
		goalpaca + "/registry",
	},
	"internal/host": {
		module + "/internal/supervisor",
		module + "/internal/indiserve",
		module + "/internal/indiwire",

		module + "/internal/devtype/",
		module + "/internal/binding",
		module + "/internal/snapshot",
		goalpaca + "/server",
		goalpaca + "/registry",
		goalpaca + "/devicemain",
	},
	"internal/corpus": {},
	"internal/check":  {},
}

// testExtra is importable by _test.go files in any package, on top of the allowlist.
var testExtra = []string{
	module + "/internal/corpus",
	module + "/internal/indiwire",
	module + "/internal/snapshot",
}
