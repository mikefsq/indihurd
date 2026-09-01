package weather

import "github.com/mikefsq/indihurd/internal/binding"

// The ObservingConditions table, in mapping-reference order. Every sensor is a
// member of the one WEATHER_PARAMETERS vector.
var table = binding.Table{
	"AveragePeriod": {Kind: binding.Synthesised,
		Why: "bridge-held; 0 = instantaneous readings is legitimate — INDI's WEATHER_UPDATE is a poll period, not an averaging window"},
	"CloudCover":    {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_CLOUD_COVER", Fn: "CloudCover"},
	"DewPoint":      {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_DEWPOINT", Fn: "DewPoint"},
	"Humidity":      {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_HUMIDITY", Fn: "Humidity"},
	"Pressure":      {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_PRESSURE", Fn: "Pressure"},
	"RainRate":      {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_RAIN_HOUR", Fn: "RainRate"},
	"SkyBrightness": {Kind: binding.Absent, Why: "no WEATHER_* member name for lux in the  vocabulary"},
	"SkyQuality":    {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_SQM", Fn: "SkyQuality"},
	"SkyTemperature": {Kind: binding.Absent,
		Why: "not in the  member vocabulary (WEATHER_SKY_TEMPERATURE exists in some cloud sensors but is unverified — fail to absent, never to wrong, )"},
	"StarFWHM":    {Kind: binding.Absent, Why: "no INDI weather driver measures seeing"},
	"Temperature": {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_TEMPERATURE", Fn: "Temperature"},
	"WindDirection": {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_WIND_DIRECTION", Fn: "WindDirection",
		Why: "degrees both sides"},
	"WindGust": {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_WIND_GUST", Fn: "WindGust",
		Why: "km/h in several INDI drivers vs m/s in ASCOM — converted only when the member label discloses the unit"},
	"WindSpeed": {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_WIND_SPEED", Fn: "WindSpeed",
		Why: "as WindGust — the unit rides the label, never a silent constant"},
	"SensorDescription": {Kind: binding.Synthesised,
		Why: "member label + INDI property provenance — the label is where drivers disclose units"},
	"TimeSinceLastUpdate": {Kind: binding.Synthesised,
		Why: "per-member snapshot arrival times; empty name = most recent across mapped sensors"},
	"Refresh": {Kind: binding.Mapped, Prop: "WEATHER_REFRESH", Elem: "REFRESH"},
}

// consumed needs no additions: every sensor is a WEATHER_PARAMETERS member,
// which the table already names.
var consumed = table.Consumed()
