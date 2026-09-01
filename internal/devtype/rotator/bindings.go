package rotator

import "github.com/mikefsq/indihurd/internal/binding"

var table = binding.Table{
	"Position": {Kind: binding.Mapped, Prop: "ABS_ROTATOR_ANGLE", Elem: "ANGLE",
		Why: "the sky angle — mechanical + sync offset, applied inside the driver"},
	"MechanicalPosition": {Kind: binding.Derived,
		Why: "INDI applies sync inside the driver and reports only the synced angle, so mechanical and sky coincide — reported as such, never an invented offset"},
	"MoveAbsolute": {Kind: binding.Func, Prop: "ABS_ROTATOR_ANGLE", Elem: "ANGLE", Fn: "moveAbsolute"},
	"Move": {Kind: binding.Func, Fn: "moveRelative",
		Why: "REL_ROTATOR_ANGLE where present, else ABS_ROTATOR_ANGLE = current + delta wrapped to [0,360)"},
	"MoveMechanical": {Kind: binding.Func, Fn: "moveMechanical",
		Why: "same write as MoveAbsolute — INDI exposes no pre-sync channel"},
	"Sync":       {Kind: binding.Mapped, Prop: "SYNC_ROTATOR_ANGLE", Elem: "ANGLE"},
	"Halt":       {Kind: binding.Mapped, Prop: "ROTATOR_ABORT_MOTION", Elem: "ABORT"},
	"Reverse":    {Kind: binding.Func, Prop: "ROTATOR_REVERSE", Elem: "INDI_ENABLED", Fn: "Reverse"},
	"IsMoving":   {Kind: binding.Derived, Why: "ABS_ROTATOR_ANGLE state Busy OR the bridge's in-flight bit — the silent-mover guard"},
	"CanReverse": {Kind: binding.Derived, Why: "ROTATOR_REVERSE defined and rw"},
	"TargetPosition": {Kind: binding.Synthesised,
		Why: "last commanded angle — INDI reports only the current one; current position before any command"},
	"StepSize": {Kind: binding.Absent,
		Why: "no driver publishes degrees-per-step reliably; server.Rotator.StepSize has no error channel, so 0 is served in place of the 0x400"},
}

// relProp is addressed by Move without appearing in a row; listing it keeps it out
// of the Actions passthrough and out of Validate's unmapped set.
var consumed = table.Consumed(relProp)
