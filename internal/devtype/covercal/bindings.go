package covercal

import "github.com/mikefsq/indihurd/internal/binding"

var table = binding.Table{
	"CoverState": {Kind: binding.Func, Prop: "CAP_PARK", Fn: "CoverState",
		Why: "PARK is closed, UNPARK is open; Busy is moving, Alert is error, absent is NotPresent"},
	"CalibratorState": {Kind: binding.Func, Prop: "FLAT_LIGHT_CONTROL", Fn: "CalibratorState",
		Why: "light switch and vector state; Busy is NotReady, Alert is error, absent is NotPresent"},
	"Brightness": {Kind: binding.Func, Prop: "FLAT_LIGHT_INTENSITY", Elem: "FLAT_LIGHT_INTENSITY_VALUE", Fn: "Brightness",
		Why: "zero unless the calibrator is Ready"},
	"MaxBrightness": {Kind: binding.Func, Fn: "MaxBrightness",
		Why: "intensity member maximum, or 1 for an on/off-only calibrator"},
	"CalibratorChanging": {Kind: binding.Derived, Why: "FLAT_LIGHT_CONTROL or FLAT_LIGHT_INTENSITY Busy, or the bridge's in-flight bit"},
	"CoverMoving":        {Kind: binding.Derived, Why: "CAP_PARK Busy or the bridge's in-flight bit"},
	"CalibratorOff":      {Kind: binding.Func, Fn: "CalibratorOff"},
	"CalibratorOn": {Kind: binding.Func, Fn: "CalibratorOn",
		Why: "set intensity before turning on the light"},
	"CloseCover": {Kind: binding.Func, Fn: "CloseCover", Why: "CAP_PARK.PARK — non-blocking initiator; CoverMoving/CoverState complete it"},
	"HaltCover": {Kind: binding.Func, Prop: "CAP_ABORT", Elem: "ABORT", Fn: "HaltCover",
		Why: "CAP_ABORT when defined; NotImplemented otherwise"},
	"OpenCover": {Kind: binding.Func, Fn: "OpenCover", Why: "CAP_PARK.UNPARK — non-blocking initiator"},
}

var consumed = table.Consumed()
