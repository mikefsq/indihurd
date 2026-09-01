package safety

import "github.com/mikefsq/indihurd/internal/binding"

var table = binding.Table{
	"IsSafe": {Kind: binding.Func, Fn: "IsSafe",
		Why: "SAFETY_STATUS lights where the driver publishes them (aux safetymonitor, weather drivers), else the WEATHER_STATUS critical-parameter lights (mappings , ); every light Ok = safe, anything else — Idle, Busy/warning, Alert, absent, child down — is unsafe"},
}

// The IsSafe row names no Prop; it resolves at read time to one of these two, so both
// are listed to keep Validate from reporting them unmapped.
var consumed = table.Consumed(safetyProp, weatherProp)
