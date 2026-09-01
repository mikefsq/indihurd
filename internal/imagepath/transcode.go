// Package imagepath turns a FITS BLOB into a goalpaca ImageFrame in one pass.
package imagepath

import (
	"encoding/binary"
	"fmt"

	"github.com/mikefsq/goalpaca/alpaca"
)

// Transcode converts a FITS frame in one pass: big-endian to little-endian,
// BZERO unsigned recovery, and row order corrected to top-down.
func Transcode(fits []byte) (alpaca.ImageFrame, Header, error) {
	h, err := ParseHeader(fits)
	if err != nil {
		return alpaca.ImageFrame{}, h, err
	}
	if h.Naxis != 2 {
		return alpaca.ImageFrame{}, h, fmt.Errorf("imagepath: NAXIS=%d unsupported (only 2-axis frames)", h.Naxis)
	}
	w, ht := h.Axes[0], h.Axes[1]
	if w <= 0 || ht <= 0 {
		return alpaca.ImageFrame{}, h, fmt.Errorf("imagepath: bad dimensions %dx%d", w, ht)
	}
	if h.BScale != 1 {
		return alpaca.ImageFrame{}, h, fmt.Errorf("imagepath: BSCALE=%g unsupported (integer frames only)", h.BScale)
	}
	es := h.Bitpix / 8
	if h.Bitpix != 8 && h.Bitpix != 16 && h.Bitpix != 32 {
		return alpaca.ImageFrame{}, h, fmt.Errorf("imagepath: BITPIX=%d unsupported (INDI CCDs emit 8/16/32-bit integer frames)", h.Bitpix)
	}
	need := h.DataOffset + w*ht*es
	if len(fits) < need {
		return alpaca.ImageFrame{}, h, fmt.Errorf("imagepath: truncated data: have %d bytes, frame needs %d", len(fits), need)
	}

	var elem, tx alpaca.ImageElementType
	var xor uint32 // BZERO recovery: +2^(n-1) on two's complement is a sign-bit flip
	switch {
	case h.Bitpix == 8 && h.BZero == 0:
		elem, tx = alpaca.ImgInt32, alpaca.ImgByte
	case h.Bitpix == 16 && h.BZero == 0:
		elem, tx = alpaca.ImgInt32, alpaca.ImgInt16
	case h.Bitpix == 16 && h.BZero == 32768:
		elem, tx, xor = alpaca.ImgInt32, alpaca.ImgUInt16, 0x8000
	case h.Bitpix == 32 && h.BZero == 0:
		elem, tx = alpaca.ImgInt32, alpaca.ImgInt32
	case h.Bitpix == 32 && h.BZero == 2147483648:
		elem, tx, xor = alpaca.ImgUInt32, alpaca.ImgUInt32, 0x80000000
	default:
		return alpaca.ImageFrame{}, h, fmt.Errorf("imagepath: BZERO=%g with BITPIX=%d unsupported", h.BZero, h.Bitpix)
	}

	src := fits[h.DataOffset:need]
	out := make([]byte, w*ht*es)
	flip := h.RowOrder == "BOTTOM-UP" // output is top-down, the order CCD_CFA offsets describe
	row := w * es
	for y := 0; y < ht; y++ {
		sy := y
		if flip {
			sy = ht - 1 - y
		}
		s := src[sy*row : sy*row+row]
		d := out[y*row : y*row+row]
		switch es {
		case 1:
			copy(d, s)
		case 2:
			x16 := uint16(xor)
			for i := 0; i < row; i += 2 {
				binary.LittleEndian.PutUint16(d[i:], binary.BigEndian.Uint16(s[i:])^x16)
			}
		case 4:
			for i := 0; i < row; i += 4 {
				binary.LittleEndian.PutUint32(d[i:], binary.BigEndian.Uint32(s[i:])^xor)
			}
		}
	}

	return alpaca.ImageFrame{
		Rank:                    2,
		Width:                   w,
		Height:                  ht,
		ElementType:             elem,
		TransmissionElementType: tx,
		Pixels:                  out,
	}, h, nil
}
