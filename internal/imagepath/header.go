package imagepath

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	blockSize = 2880
	cardSize  = 80
)

// Header holds the FITS fields used by Transcode.
type Header struct {
	Bitpix     int
	Naxis      int
	Axes       [3]int // NAXIS1..NAXIS3: width, height, planes
	BZero      float64
	BScale     float64
	RowOrder   string // "TOP-DOWN", "BOTTOM-UP", or "" when absent
	BayerPat   string // BAYERPAT, verbatim; empty when absent
	DataOffset int    // first data byte: header blocks are 2880-aligned
}

// ParseHeader scans cards until END; INDI writes single-HDU FITS, so the
// primary header is all there is.
func ParseHeader(fits []byte) (Header, error) {
	h := Header{BScale: 1}
	if len(fits) < cardSize || !strings.HasPrefix(string(fits[:8]), "SIMPLE") {
		return h, fmt.Errorf("imagepath: not a FITS header (no SIMPLE card)")
	}
	for off := 0; ; off += cardSize {
		if off+cardSize > len(fits) {
			return h, fmt.Errorf("imagepath: header has no END card in %d bytes", len(fits))
		}
		card := string(fits[off : off+cardSize])
		key := strings.TrimRight(card[:8], " ")
		if key == "END" {
			h.DataOffset = ((off + cardSize + blockSize - 1) / blockSize) * blockSize
			return h, nil
		}
		if len(card) < 10 || card[8] != '=' {
			continue // COMMENT, HISTORY, blank
		}
		val := card[10:]
		if i := strings.IndexByte(val, '/'); i >= 0 && !strings.HasPrefix(strings.TrimSpace(val), "'") {
			val = val[:i]
		}
		val = strings.TrimSpace(val)
		switch key {
		case "BITPIX":
			h.Bitpix = atoi(val)
		case "NAXIS":
			h.Naxis = atoi(val)
		case "NAXIS1":
			h.Axes[0] = atoi(val)
		case "NAXIS2":
			h.Axes[1] = atoi(val)
		case "NAXIS3":
			h.Axes[2] = atoi(val)
		case "BZERO":
			h.BZero, _ = strconv.ParseFloat(val, 64)
		case "BSCALE":
			h.BScale, _ = strconv.ParseFloat(val, 64)
		case "ROWORDER":
			h.RowOrder = strings.ToUpper(quoted(val))
		case "BAYERPAT":
			h.BayerPat = strings.ToUpper(quoted(val))
		}
	}
}

func atoi(s string) int {
	// Accept integer-valued FITS cards written as floats.
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int(f)
}

// quoted strips FITS string quoting; a comment after the closing quote is cut
// here because ParseHeader cannot cut on '/' inside a string.
func quoted(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '\'' {
		return s
	}
	s = s[1:]
	if i := strings.Index(s, "'"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimRight(s, " ")
}
