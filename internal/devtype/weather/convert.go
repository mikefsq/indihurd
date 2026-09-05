package weather

import "strings"

// windToMS converts labelled wind units to m/s, leaving unknown units unchanged.
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
