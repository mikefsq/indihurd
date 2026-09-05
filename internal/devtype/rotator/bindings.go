package rotator

import "github.com/mikefsq/indihurd/internal/binding"

var table = binding.Table{
	"Position": {Kind: binding.Mapped, Prop: "ABS_ROTATOR_ANGLE", Elem: "ANGLE",
		Why: "the sky angle — mechanical + sync offset, applied inside the driver"},
	"MechanicalPosition": {Kind: binding.Derived,
		Why: "INDI reports only the synced angle; mechanical position uses the same value"},
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
		Why: "no angular step-size mapping; returns zero"},
}

// Move uses relProp indirectly; exclude it from passthrough actions.
var consumed = table.Consumed(relProp)
