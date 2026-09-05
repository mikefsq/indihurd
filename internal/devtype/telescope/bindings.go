package telescope

import "github.com/mikefsq/indihurd/internal/binding"

var table = binding.Table{
	"RightAscension":       {Kind: binding.Mapped, Prop: "EQUATORIAL_EOD_COORD", Elem: "RA"},
	"Declination":          {Kind: binding.Mapped, Prop: "EQUATORIAL_EOD_COORD", Elem: "DEC"},
	"Altitude":             {Kind: binding.Mapped, Prop: "HORIZONTAL_COORD", Elem: "ALT"},
	"Azimuth":              {Kind: binding.Mapped, Prop: "HORIZONTAL_COORD", Elem: "AZ"},
	"TargetRightAscension": {Kind: binding.Synthesised, Why: "bridge-held; INDI TARGET_EOD_COORD is driver-internal — goalpaca enforces read-before-set"},
	"TargetDeclination":    {Kind: binding.Synthesised, Why: "bridge-held, as TargetRightAscension"},
	"EquatorialSystem":     {Kind: binding.Derived, Why: "EQUATORIAL_EOD_COORD is JNow/topocentric by definition; report equTopocentric, never convert"},

	"SlewToCoordinates":      {Kind: binding.Func, Fn: "slewSync", Why: "mode TRACK, then EQUATORIAL_EOD_COORD write, then fenced settle (blocking form)"},
	"SlewToCoordinatesAsync": {Kind: binding.Func, Fn: "slewAsync", Why: "mode TRACK, then coordinates; Slewing is the completion"},
	"SlewToTarget":           {Kind: binding.Func, Fn: "SlewToTarget", Why: "retained targets use the TRACK-mode slew sequence"},
	"SlewToTargetAsync":      {Kind: binding.Func, Fn: "SlewToTargetAsync"},
	"SlewToAltAz":            {Kind: binding.Func, Fn: "SlewToAltAz", Why: "HORIZONTAL_COORD write where IP_RW; gated by CanSlewAltAz"},
	"SlewToAltAzAsync":       {Kind: binding.Func, Fn: "slewAltAzAsync"},
	"SyncToCoordinates":      {Kind: binding.Func, Fn: "syncCoords", Why: "mode SYNC, then coordinates"},
	"SyncToTarget":           {Kind: binding.Func, Fn: "SyncToTarget"},
	"SyncToAltAz":            {Kind: binding.Absent, Why: "no INDI alt/az sync path; CanSyncAltAz is false"},
	"Slewing":                {Kind: binding.Derived, Why: "coordinate, park, or home Busy state, or a pending bridge motion command"},
	"AbortSlew":              {Kind: binding.Mapped, Prop: "TELESCOPE_ABORT_MOTION", Elem: "ABORT"},
	"DestinationSideOfPier":  {Kind: binding.Absent, Why: "no mapping for the mount-specific meridian-flip model"},

	"AtPark":  {Kind: binding.Derived, Why: "TELESCOPE_PARK.PARK On, vector not Busy, no unacknowledged park/unpark send"},
	"Park":    {Kind: binding.Func, Fn: "park", Why: "PARK switch, non-blocking (ITelescopeV4 initiator; goalpaca server/telescope.go)"},
	"Unpark":  {Kind: binding.Func, Fn: "park"},
	"SetPark": {Kind: binding.Mapped, Prop: "TELESCOPE_PARK_OPTION", Elem: "PARK_CURRENT"},
	"AtHome":  {Kind: binding.Derived, Why: "TELESCOPE_HOME state Ok with no unacknowledged FindHome send; false where undefined"},
	"FindHome": {Kind: binding.Func, Fn: "findHome",
		Why: "TELESCOPE_HOME where defined (member names vary; first switch member), non-blocking"},

	"Tracking":      {Kind: binding.Derived, Why: "TELESCOPE_TRACK_STATE.TRACK_ON; SetTracking writes TRACK_ON/TRACK_OFF"},
	"TrackingRate":  {Kind: binding.Func, Fn: "TrackingRate", Why: "TELESCOPE_TRACK_MODE switches ↔ DriveRate; TRACK_CUSTOM has no ASCOM value"},
	"TrackingRates": {Kind: binding.Derived, Why: "the sidereal/lunar/solar members TELESCOPE_TRACK_MODE actually offers"},
	"RightAscensionRate": {Kind: binding.Func, Fn: "RightAscensionRate",
		Why: "TELESCOPE_TRACK_RATE.TRACK_RATE_RA, absolute arcsec/SI-s ↔ offset seconds-of-RA per sidereal second"},
	"DeclinationRate": {Kind: binding.Func, Fn: "DeclinationRate", Why: "TRACK_RATE_DE; offset and absolute coincide at 0"},

	"PulseGuide":     {Kind: binding.Func, Fn: "PulseGuide", Why: "TELESCOPE_TIMED_GUIDE_NS/_WE, milliseconds"},
	"IsPulseGuiding": {Kind: binding.Derived, Why: "either timed-guide vector Busy OR bridge in-flight"},
	"GuideRateRightAscension": {Kind: binding.Mapped, Prop: "GUIDE_RATE", Elem: "GUIDE_RATE_WE",
		Why: "×sidereal both sides; discovered-standard (not imposed by INDI::Telescope)"},
	"GuideRateDeclination": {Kind: binding.Mapped, Prop: "GUIDE_RATE", Elem: "GUIDE_RATE_NS"},

	"MoveAxis": {Kind: binding.Func, Fn: "MoveAxis",
		Why: "direction switches use the current INDI slew rate; zero sends explicit Off"},
	"AxisRates":   {Kind: binding.Derived, Why: "one degenerate range per movable axis — INDI's TELESCOPE_SLEW_RATE is discrete and unit-less"},
	"CanMoveAxis": {Kind: binding.Derived, Why: "presence of TELESCOPE_MOTION_NS/_WE per axis; tertiary always false"},

	// GEOGRAPHIC_COORD and TIME_UTC require complete vectors; partial writes can crash libindi.
	"SiteLatitude":  {Kind: binding.Func, Fn: "SetSiteLatitude", Prop: "GEOGRAPHIC_COORD", Elem: "LAT", Why: "complete-vector write"},
	"SiteLongitude": {Kind: binding.Func, Fn: "SiteLongitude", Prop: "GEOGRAPHIC_COORD", Elem: "LONG", Why: "0–360°E ↔ ±180°; complete-vector write"},
	"SiteElevation": {Kind: binding.Func, Fn: "SetSiteElevation", Prop: "GEOGRAPHIC_COORD", Elem: "ELEV", Why: "complete-vector write"},
	"UTCDate":       {Kind: binding.Func, Fn: "UTCDate", Why: "TIME_UTC.UTC or the host clock; writes include OFFSET"},
	"SiderealTime":  {Kind: binding.Synthesised, Why: "LST from site longitude + clock; INDI does not publish it"},

	"AlignmentMode":    {Kind: binding.Derived, Why: "TELESCOPE_MOUNT_TYPE; defaults to GermanPolar when absent"},
	"SideOfPier":       {Kind: binding.Derived, Why: "TELESCOPE_PIER_SIDE: EAST/WEST/neither → PierEast/PierWest/PierUnknown(-1), a direct three-value map"},
	"ApertureDiameter": {Kind: binding.Func, Fn: "ApertureDiameter", Prop: "TELESCOPE_INFO", Elem: "TELESCOPE_APERTURE", Why: "mm → m"},
	"ApertureArea":     {Kind: binding.Synthesised, Why: "πr² from ApertureDiameter"},
	"FocalLength":      {Kind: binding.Func, Fn: "FocalLength", Prop: "TELESCOPE_INFO", Elem: "TELESCOPE_FOCAL_LENGTH", Why: "mm → m"},
	"DoesRefraction":   {Kind: binding.Absent, Why: "INDI has no refraction-model flag"},
	"SlewSettleTime":   {Kind: binding.Synthesised, Why: "bridge-held, default 0 — ASCOM lets the driver store it"},

	"CanFindHome":              {Kind: binding.Derived, Why: "TELESCOPE_HOME defined"},
	"CanPark":                  {Kind: binding.Derived, Why: "TELESCOPE_PARK defined"},
	"CanUnpark":                {Kind: binding.Derived, Why: "TELESCOPE_PARK defined"},
	"CanSetPark":               {Kind: binding.Derived, Why: "TELESCOPE_PARK_OPTION defined"},
	"CanPulseGuide":            {Kind: binding.Derived, Why: "TELESCOPE_TIMED_GUIDE_NS defined"},
	"CanSetGuideRates":         {Kind: binding.Derived, Why: "GUIDE_RATE defined and rw"},
	"CanSetPierSide":           {Kind: binding.Derived, Why: "TELESCOPE_PIER_SIDE rw — ro on most mounts; never force a flip"},
	"CanSetRightAscensionRate": {Kind: binding.Derived, Why: "TELESCOPE_TRACK_RATE rw"},
	"CanSetDeclinationRate":    {Kind: binding.Derived, Why: "TELESCOPE_TRACK_RATE rw"},
	"CanSetTracking":           {Kind: binding.Derived, Why: "TELESCOPE_TRACK_STATE rw"},
	"CanSlew":                  {Kind: binding.Derived, Why: "EQUATORIAL_EOD_COORD rw"},
	"CanSlewAsync":             {Kind: binding.Derived, Why: "as CanSlew — INDI slews are inherently async"},
	"CanSlewAltAz":             {Kind: binding.Derived, Why: "HORIZONTAL_COORD rw (ro on most)"},
	"CanSlewAltAzAsync":        {Kind: binding.Derived, Why: "as CanSlewAltAz"},
	"CanSync":                  {Kind: binding.Derived, Why: "ON_COORD_SET offers SYNC"},
	"CanSyncAltAz":             {Kind: binding.Derived, Why: "no INDI alt/az sync — false"},
}

// consumed includes properties written by multi-step operations.
var consumed = table.Consumed(
	coordMode, parkProp, homeProp,
	trackMode, trackRate, trackState,
	guideNS, guideWE, motionNS, motionWE,
	timeProp, pierProp, mountType)
