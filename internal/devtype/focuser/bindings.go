package focuser

import "github.com/mikefsq/indihurd/internal/binding"

var table = binding.Table{
	"Position":     {Kind: binding.Func, Fn: "Position", Why: "ABS_FOCUS_POSITION; absent on a relative-only focuser, not zero"},
	"Move":         {Kind: binding.Func, Prop: "ABS_FOCUS_POSITION", Elem: "FOCUS_ABSOLUTE_POSITION", Fn: "Move"},
	"Halt":         {Kind: binding.Mapped, Prop: "FOCUS_ABORT_MOTION", Elem: "ABORT"},
	"MaxStep":      {Kind: binding.Func, Fn: "MaxStep", Why: "FOCUS_MAX value, else ABS member max"},
	"MaxIncrement": {Kind: binding.Func, Fn: "MaxIncrement", Why: "REL_FOCUS_POSITION member max where present, else MaxStep"},
	"IsMoving":     {Kind: binding.Derived, Why: "ABS_FOCUS_POSITION (or REL) state == Busy"},
	"Absolute":     {Kind: binding.Derived, Why: "presence of ABS_FOCUS_POSITION"},
	"Temperature":  {Kind: binding.Mapped, Prop: "FOCUS_TEMPERATURE", Elem: "TEMPERATURE"},
	"TempComp": {Kind: binding.Derived,
		Why: "no standard INDI property; available only when a driver defines one (none scanned does) — goalpaca gates SetTempComp on TempCompAvailable"},
	"TempCompAvailable": {Kind: binding.Derived, Why: "presence of a temperature-compensation property; none is standard"},
	"StepSize": {Kind: binding.Absent,
		Why: "µm per step has no standard INDI property, and guessing mis-scales a client's autofocus step"},
}

// The extra props are addressed by Func members without appearing in a row; listing
// them keeps them out of the Actions passthrough and out of Validate's unmapped set.
var consumed = table.Consumed(relProp, maxProp, "FOCUS_MOTION")
