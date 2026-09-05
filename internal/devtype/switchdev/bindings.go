package switchdev

import "github.com/mikefsq/indihurd/internal/binding"

// table maps ASCOM members to flattened switch IDs.
var table = binding.Table{
	"MaxSwitch":            {Kind: binding.Derived, Why: "count of pinned (property, member) pairs — enumeration pinned on first sight"},
	"GetSwitch":            {Kind: binding.Func, Fn: "GetSwitch"},
	"SetSwitch":            {Kind: binding.Func, Fn: "SetSwitch"},
	"GetSwitchValue":       {Kind: binding.Func, Fn: "GetSwitchValue", Why: "number member value; a bool switch reports 0/1"},
	"SetSwitchValue":       {Kind: binding.Func, Fn: "SetSwitchValue"},
	"MinSwitchValue":       {Kind: binding.Func, Fn: "MinSwitchValue", Why: "number member's own min; bool 0"},
	"MaxSwitchValue":       {Kind: binding.Func, Fn: "MaxSwitchValue", Why: "number member's own max; bool 1"},
	"SwitchStep":           {Kind: binding.Func, Fn: "SwitchStep", Why: "number member's own step; bool 1"},
	"GetSwitchName":        {Kind: binding.Func, Fn: "GetSwitchName", Why: "member label (driver fills it from *_LABELS); the vector's for an ON/OFF pair"},
	"GetSwitchDescription": {Kind: binding.Func, Fn: "GetSwitchDescription", Why: "names the source vector — outlet 3 vs dew channel 3"},
	"SetSwitchName":        {Kind: binding.Absent, Why: "not in INDI"},
	"CanWrite":             {Kind: binding.Derived, Why: "writable unless permission is read-only"},
	"CanAsync":             {Kind: binding.Derived, Why: "writable properties accept asynchronous sends"},
	"SetAsync":             {Kind: binding.Func, Fn: "SetAsync"},
	"SetAsyncValue":        {Kind: binding.Func, Fn: "SetAsyncValue"},
	"StateChangeComplete":  {Kind: binding.Derived, Why: "vector state left Busy"},
	"CancelAsync":          {Kind: binding.Absent, Why: "no standard abort property for switch operations"},
}

// families identifies switch properties. Momentary operations remain passthrough actions.
var families = []struct {
	name     string
	numbered bool
}{
	{"DIGITAL_OUTPUT_", true}, // OUTPUT_INTERFACE: ON/OFF pair per channel
	{"PULSE_", true},          // OUTPUT_INTERFACE: pulse duration per channel
	{"DIGITAL_INPUT_", true},  // INPUT_INTERFACE: ON/OFF pair, IP_RO
	{"ANALOG_INPUT_", true},   // INPUT_INTERFACE: reading, IP_RO
	{"POWER_CHANNELS", false}, // POWER_INTERFACE: one switch per member from here down
	{"DEW_CHANNELS", false},
	{"USB_PORTS", false},
	{"VARIABLE_CHANNELS", false},
	{"VARIABLE_VOLTAGES", false},
	{"DEW_DUTY_CYCLES", false},
	{"POWER_SENSORS", false}, // sensor vectors: read-only gauges
	{"POWER_CURRENTS", false},
	{"DEW_CURRENTS", false},
}

// consumed matches dynamically named switch properties.
var consumed binding.Consumed = consumedByMapping
