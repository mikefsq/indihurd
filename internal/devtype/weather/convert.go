package weather

import "strings"

// windToMS converts to ASCOM's m/s only when the INDI member label discloses
// the unit. Wind units are not agreed across drivers, so an undisclosed unit
// passes through rather than being assumed.
func windToMS(v float64, label string) float64 {
	l := strings.ToLower(label)
	switch {
	case strings.Contains(l, "kph") || strings.Contains(l, "km/h"):
		return v / 3.6
	case strings.Contains(l, "mph"):
		return v * 0.44704
	}
	return v
}
