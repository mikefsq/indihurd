package switchdev

import "github.com/mikefsq/indihurd/internal/binding"

// The Switch table, in mapping-reference order. Rows operate on the flattened
// id space, not on a single INDI property.
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
	"CanWrite":             {Kind: binding.Derived, Why: "Perm != IP_RO — every INPUT_* and sensor reading is read-only"},
	"CanAsync":             {Kind: binding.Derived, Why: "writable — the checkable superset of 'the vector goes Busy'; SetAsync is the same non-blocking send"},
	"SetAsync":             {Kind: binding.Func, Fn: "SetAsync"},
	"SetAsyncValue":        {Kind: binding.Func, Fn: "SetAsyncValue"},
	"StateChangeComplete":  {Kind: binding.Derived, Why: "vector state left Busy"},
	"CancelAsync":          {Kind: binding.Absent, Why: "spec  maps it to 'the driver's abort where one exists' — no standard OUTPUT/INPUT/POWER vector defines one"},
}

// families are the contributing INDI properties; numbered entries match
// NAME_<n>. Momentary operations (POWER_CYCLE, POWER_OFF_DISCONNECT) are
// deliberately absent: they belong to Actions, not the switch id space.
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

// consumed is a rule, not a list: family property names are driver-generated
// (DIGITAL_OUTPUT_3, POWER_CHANNELS…), so no table row can name them.
var consumed binding.Consumed = consumedByMapping
