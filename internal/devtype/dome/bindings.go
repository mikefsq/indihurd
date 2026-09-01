package dome

import "github.com/mikefsq/indihurd/internal/binding"

var table = binding.Table{
	"Azimuth": {Kind: binding.Func, Prop: "ABS_DOME_POSITION", Elem: "DOME_ABSOLUTE_POSITION", Fn: "Azimuth",
		Why: "same N=0°/E=90° convention both sides; absent on a roll-off roof → 0x400, never a fabricated 0"},
	"Altitude": {Kind: binding.Absent,
		Why: "INDI::Dome has no altitude property — 0x400; CanSetAltitude false keeps goalpaca gating SlewToAltitude"},
	"AtHome": {Kind: binding.Derived, Why: "always false — INDI::Dome defines no home property; CanFindHome false"},
	"AtPark": {Kind: binding.Derived, Why: "DOME_PARK.PARK On, vector not Busy, no unacknowledged park send (the telescope  pattern)"},
	"ShutterStatus": {Kind: binding.Func, Prop: "DOME_SHUTTER", Fn: "ShutterStatus",
		Why: "two-member switch + vector state → five ASCOM values; a shutterless roof reports 0x400, never closed"},
	"Slaved": {Kind: binding.Func, Prop: "DOME_AUTOSYNC", Fn: "Slaved",
		Why: "Slaved IS INDI's DOME_AUTOSYNC, not a bridge concept"},
	"Slewing": {Kind: binding.Derived,
		Why: "ABS/REL position, DOME_MOTION, DOME_PARK or DOME_SHUTTER Busy, or a bridge in-flight bit — IDomeV3 counts shutter motion as Slewing"},
	"AbortSlew":    {Kind: binding.Mapped, Prop: "DOME_ABORT_MOTION", Elem: "ABORT"},
	"CloseShutter": {Kind: binding.Func, Fn: "closeShutter", Why: "DOME_SHUTTER.SHUTTER_CLOSE — non-blocking initiator; ShutterStatus/Slewing complete it"},
	"FindHome":     {Kind: binding.Absent, Why: "no INDI dome home property; CanFindHome false keeps goalpaca answering 0x400"},
	"OpenShutter":  {Kind: binding.Func, Fn: "openShutter", Why: "DOME_SHUTTER.SHUTTER_OPEN — non-blocking initiator"},
	"Park": {Kind: binding.Func, Prop: "DOME_PARK", Fn: "park",
		Why: "DOME_PARK.PARK, non-blocking; ASCOM Dome has no Unpark member — a slew unparks first (IDomeV3: AtPark is 'reset with any slew operation'), so DOME_PARK stays typed-consumed"},
	"SetPark":        {Kind: binding.Mapped, Prop: "DOME_PARK_OPTION", Elem: "PARK_CURRENT"},
	"SlewToAltitude": {Kind: binding.Absent, Why: "as Altitude"},
	"SlewToAzimuth": {Kind: binding.Func, Prop: "ABS_DOME_POSITION", Elem: "DOME_ABSOLUTE_POSITION", Fn: "slewToAzimuth",
		Why: "range-checked against the driver's own descriptor; in-flight bit completes Slewing"},
	"SyncToAzimuth": {Kind: binding.Mapped, Prop: "DOME_SYNC", Elem: "DOME_SYNC_VALUE",
		Why: "defined only by drivers with the CAN_SYNC capability"},

	"CanFindHome":    {Kind: binding.Derived, Why: "false — see FindHome"},
	"CanPark":        {Kind: binding.Derived, Why: "DOME_PARK defined"},
	"CanSetAltitude": {Kind: binding.Derived, Why: "false — see Altitude"},
	"CanSetAzimuth":  {Kind: binding.Derived, Why: "ABS_DOME_POSITION defined and rw — false on a roll-off roof, the  trap"},
	"CanSetPark":     {Kind: binding.Derived, Why: "DOME_PARK_OPTION defined"},
	"CanSetShutter":  {Kind: binding.Derived, Why: "DOME_SHUTTER defined"},
	"CanSlave":       {Kind: binding.Derived, Why: "DOME_AUTOSYNC defined and rw"},
	"CanSyncAzimuth": {Kind: binding.Derived, Why: "DOME_SYNC defined and rw"},
}

var consumed = table.Consumed(relProp, motionProp)
