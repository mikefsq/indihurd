package camera

import "math"

func maxADUFromBits(bits float64) int {
	if bits <= 0 || bits > 32 {
		return 0
	}
	return int(math.Pow(2, bits)) - 1
}

func toBinned(unbinned float64, bin int) int {
	if bin < 1 {
		bin = 1
	}
	return int(unbinned) / bin
}

func toUnbinned(binned, bin int) float64 {
	if bin < 1 {
		bin = 1
	}
	return float64(binned * bin)
}

func percentCompleted(remaining, total float64) int {
	if total <= 0 {
		return 0
	}
	p := int(100 * (1 - remaining/total))
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}
