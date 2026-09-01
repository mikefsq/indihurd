package covercal

import "github.com/mikefsq/indihurd/internal/binding"

var table = binding.Table{
	"CoverState": {Kind: binding.Func, Prop: "CAP_PARK", Fn: "CoverState",
		Why: "PARK on = closed (the cap covers the optics), UNPARK on = open; Busy/in-flight = moving, Alert = error, absent = NotPresent"},
	"CalibratorState": {Kind: binding.Func, Prop: "FLAT_LIGHT_CONTROL", Fn: "CalibratorState",
		Why: "FLAT_LIGHT_ON/OFF + vector state; Busy (either light vector) = NotReady, Alert = error, absent = NotPresent"},
	"Brightness": {Kind: binding.Func, Prop: "FLAT_LIGHT_INTENSITY", Elem: "FLAT_LIGHT_INTENSITY_VALUE", Fn: "Brightness",
		Why: "0 unless the calibrator is Ready — ICoverCalibratorV1 reads 0 while Off, whatever intensity the driver retains"},
	"MaxBrightness": {Kind: binding.Func, Fn: "MaxBrightness",
		Why: "FLAT_LIGHT_INTENSITY member max; 1 for an on/off-only calibrator (ASCOM requires ≥1 wherever a calibrator exists)"},
	"CalibratorChanging": {Kind: binding.Derived, Why: "FLAT_LIGHT_CONTROL or FLAT_LIGHT_INTENSITY Busy, or the bridge's in-flight bit"},
	"CoverMoving":        {Kind: binding.Derived, Why: "CAP_PARK Busy or the bridge's in-flight bit"},
	"CalibratorOff":      {Kind: binding.Func, Fn: "CalibratorOff"},
	"CalibratorOn": {Kind: binding.Func, Fn: "CalibratorOn",
		Why: "intensity first, then FLAT_LIGHT_ON — the light comes on at the requested brightness, not the retained one"},
	"CloseCover": {Kind: binding.Func, Fn: "CloseCover", Why: "CAP_PARK.PARK — non-blocking initiator; CoverMoving/CoverState complete it"},
	"HaltCover": {Kind: binding.Func, Prop: "CAP_ABORT", Elem: "ABORT", Fn: "HaltCover",
		Why: "CAP_ABORT where the driver defines it (DOME_CAN_ABORT-style capability); 0x400 otherwise"},
	"OpenCover": {Kind: binding.Func, Fn: "OpenCover", Why: "CAP_PARK.UNPARK — non-blocking initiator"},
}

var consumed = table.Consumed()
