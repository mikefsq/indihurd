package weather

import "github.com/mikefsq/indihurd/internal/binding"

// table maps sensors to WEATHER_PARAMETERS members.
var table = binding.Table{
	"AveragePeriod": {Kind: binding.Synthesised,
		Why: "retained setting; readings remain instantaneous"},
	"CloudCover":    {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_CLOUD_COVER", Fn: "CloudCover"},
	"DewPoint":      {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_DEWPOINT", Fn: "DewPoint"},
	"Humidity":      {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_HUMIDITY", Fn: "Humidity"},
	"Pressure":      {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_PRESSURE", Fn: "Pressure"},
	"RainRate":      {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_RAIN_HOUR", Fn: "RainRate"},
	"SkyBrightness": {Kind: binding.Absent, Why: "no supported lux mapping"},
	"SkyQuality":    {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_SQM", Fn: "SkyQuality"},
	"SkyTemperature": {Kind: binding.Absent,
		Why: "no supported sky-temperature mapping"},
	"StarFWHM":    {Kind: binding.Absent, Why: "no supported seeing measurement"},
	"Temperature": {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_TEMPERATURE", Fn: "Temperature"},
	"WindDirection": {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_WIND_DIRECTION", Fn: "WindDirection",
		Why: "degrees both sides"},
	"WindGust": {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_WIND_GUST", Fn: "WindGust",
		Why: "convert to m/s when the member label identifies the unit"},
	"WindSpeed": {Kind: binding.Func, Prop: "WEATHER_PARAMETERS", Elem: "WEATHER_WIND_SPEED", Fn: "WindSpeed",
		Why: "convert to m/s when the member label identifies the unit"},
	"SensorDescription": {Kind: binding.Synthesised,
		Why: "source member name and label"},
	"TimeSinceLastUpdate": {Kind: binding.Synthesised,
		Why: "per-member snapshot arrival times; empty name = most recent across mapped sensors"},
	"Refresh": {Kind: binding.Mapped, Prop: "WEATHER_REFRESH", Elem: "REFRESH"},
}

var consumed = table.Consumed()
