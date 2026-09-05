package filterwheel

import "github.com/mikefsq/indihurd/internal/binding"

var table = binding.Table{
	"Position": {Kind: binding.Func, Prop: "FILTER_SLOT", Elem: "FILTER_SLOT_VALUE", Fn: "Position",
		Why: "INDI is 1-based, ASCOM 0-based; -1 while Busy or in-flight — never the slot it is heading for"},
	"Names": {Kind: binding.Func, Prop: "FILTER_NAME", Fn: "Names",
		Why: "FILTER_NAME in slot order, or names generated from FILTER_SLOT bounds"},
	"FocusOffsets": {Kind: binding.Synthesised,
		Why: "not in INDI; zeros, one per Name — clients keep offsets in their own calibration"},
}

var consumed = table.Consumed()
