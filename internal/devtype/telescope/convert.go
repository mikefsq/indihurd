package telescope

import (
	"math"
	"time"
)

// Sidereal constants from libindi (libs/indimacros.h).
const (
	stellarDaySec     = 86164.098903691
	trackrateSidereal = (360.0 * 3600.0) / stellarDaySec // ≈ 15.0410686…
	siPerSiderealSec  = stellarDaySec / 86400.0          // SI seconds in one sidereal second
)

// longitudeToASCOM: INDI GEOGRAPHIC_COORD.LONG is 0–360° east-positive, ASCOM
// SiteLongitude is ±180° east-positive.
func longitudeToASCOM(l float64) float64 {
	if l > 180 {
		return l - 360
	}
	return l
}

func longitudeToINDI(l float64) float64 {
	if l < 0 {
		return l + 360
	}
	return l
}

// raRateToASCOM: INDI TRACK_RATE_RA is absolute arcsec per SI second, ASCOM
// RightAscensionRate an offset from sidereal in seconds of RA per sidereal
// second. Both read 0 when tracking normally.
func raRateToASCOM(indiArcsecPerSec float64) float64 {
	return (indiArcsecPerSec - trackrateSidereal) / 15.0 * siPerSiderealSec
}

func raRateToINDI(ascomSecRAPerSidSec float64) float64 {
	return ascomSecRAPerSidSec/siPerSiderealSec*15.0 + trackrateSidereal
}

// Dec rates: both sides are arcsec per SI second defaulting to 0, so offset
// and absolute coincide.
func decRateToASCOM(indi float64) float64 { return indi }
func decRateToINDI(ascom float64) float64 { return ascom }

// siderealTime is local apparent sidereal time in hours; INDI does not
// publish it.
func siderealTime(longitudeDeg float64, now time.Time) float64 {
	jd := float64(now.UTC().Unix())/86400.0 + 2440587.5
	d := jd - 2451545.0
	gmst := 18.697374558 + 24.06570982441908*d // hours
	lst := math.Mod(gmst+longitudeDeg/15.0, 24)
	if lst < 0 {
		lst += 24
	}
	return lst
}

// INDI TIME_UTC.UTC carries ISO-8601 without fraction or zone; ASCOM wants
// "yyyy-MM-ddTHH:mm:ss.fffZ". Parse liberally, emit each side's spelling.
var utcLayouts = []string{
	"2006-01-02T15:04:05.000Z",
	"2006-01-02T15:04:05Z",
	time.RFC3339,
	"2006-01-02T15:04:05",
}

func parseUTC(s string) (time.Time, bool) {
	for _, l := range utcLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func utcToASCOM(indi string) (string, bool) {
	t, ok := parseUTC(indi)
	if !ok {
		return "", false
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z"), true
}

func utcToINDI(ascom string) (string, bool) {
	t, ok := parseUTC(ascom)
	if !ok {
		return "", false
	}
	return t.UTC().Format("2006-01-02T15:04:05"), true
}

// TELESCOPE_INFO carries millimetres; ASCOM aperture/focal length are metres.
func mmToM(mm float64) float64 { return mm / 1000.0 }

// apertureArea in m²; ASCOM ignores central obstruction by definition.
func apertureArea(diaM float64) float64 { return math.Pi * (diaM / 2) * (diaM / 2) }
