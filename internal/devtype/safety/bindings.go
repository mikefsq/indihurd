package safety

import "github.com/mikefsq/indihurd/internal/binding"

var table = binding.Table{
	"IsSafe": {Kind: binding.Func, Fn: "IsSafe",
		Why: "SAFETY_STATUS, else WEATHER_STATUS; requires Ok at vector or all-member level, with no Busy or Alert"},
}

// IsSafe resolves either property at runtime.
var consumed = table.Consumed(safetyProp, weatherProp)
