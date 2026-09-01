package imagepath

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/mikefsq/goalpaca/alpaca"
)

// fits builds a minimal synthetic FITS: the given cards, an END card, block
// padding, then the big-endian data.
func fits(cards []string, data []byte) []byte {
	var b strings.Builder
	for _, c := range append(cards, "END") {
		b.WriteString(c)
		b.WriteString(strings.Repeat(" ", cardSize-len(c)))
	}
	for b.Len()%blockSize != 0 {
		b.WriteString(strings.Repeat(" ", cardSize))
	}
	return append([]byte(b.String()), data...)
}

func card(key string, val any) string {
	switch v := val.(type) {
	case string:
		return fmt.Sprintf("%-8s= '%s'", key, v)
	default:
		return fmt.Sprintf("%-8s= %v", key, val)
	}
}

func be16(vals ...uint16) []byte {
	out := make([]byte, 2*len(vals))
	for i, v := range vals {
		binary.BigEndian.PutUint16(out[2*i:], v)
	}
	return out
}

func le16(t *testing.T, pixels []byte) []uint16 {
	t.Helper()
	out := make([]uint16, len(pixels)/2)
	for i := range out {
		out[i] = binary.LittleEndian.Uint16(pixels[2*i:])
	}
	return out
}

func header(bitpix, w, h int, extra ...string) []string {
	return append([]string{
		card("SIMPLE", "T"), card("BITPIX", bitpix), card("NAXIS", 2),
		card("NAXIS1", w), card("NAXIS2", h),
	}, extra...)
}

// TestUnsigned16BZero: BITPIX=16 with BZERO=32768 carries unsigned data as
// signed, so a pass-through halves the sky and wraps the stars.
func TestUnsigned16BZero(t *testing.T) {
	// Stored as signed big-endian: -32768→0, 0→32768, 1000→33768, 32767→65535.
	data := be16(0x8000, 0x0000, uint16(1000), 0x7FFF)
	frame, h, err := Transcode(fits(header(16, 2, 2, card("BZERO", 32768), card("BSCALE", 1), card("ROWORDER", "TOP-DOWN")), data))
	if err != nil {
		t.Fatal(err)
	}
	if h.RowOrder != "TOP-DOWN" || h.BZero != 32768 {
		t.Fatalf("header = %+v", h)
	}
	if frame.Rank != 2 || frame.Width != 2 || frame.Height != 2 ||
		frame.ElementType != alpaca.ImgInt32 || frame.TransmissionElementType != alpaca.ImgUInt16 {
		t.Fatalf("frame meta = %+v", frame)
	}
	want := []uint16{0, 32768, 33768, 65535}
	for i, got := range le16(t, frame.Pixels) {
		if got != want[i] {
			t.Fatalf("pixel %d = %d, want %d", i, got, want[i])
		}
	}
}

// TestSigned16 has no BZERO: byte swap only, transmitted as Int16.
func TestSigned16(t *testing.T) {
	data := be16(0xFFFF /* -1 */, 0x0102)
	frame, _, err := Transcode(fits(header(16, 2, 1), data))
	if err != nil {
		t.Fatal(err)
	}
	if frame.TransmissionElementType != alpaca.ImgInt16 {
		t.Fatalf("tx type = %v", frame.TransmissionElementType)
	}
	got := le16(t, frame.Pixels)
	if int16(got[0]) != -1 || got[1] != 0x0102 {
		t.Fatalf("pixels = %v", got)
	}
}

func TestByteFrame(t *testing.T) {
	frame, _, err := Transcode(fits(header(8, 3, 2), []byte{1, 2, 3, 4, 5, 6}))
	if err != nil {
		t.Fatal(err)
	}
	if frame.TransmissionElementType != alpaca.ImgByte || string(frame.Pixels) != "\x01\x02\x03\x04\x05\x06" {
		t.Fatalf("frame = %+v", frame)
	}
}

// TestBottomUpFlip: BOTTOM-UP data is flipped to top-down, byte swap included.
func TestBottomUpFlip(t *testing.T) {
	data := be16(10, 11, 20, 21) // file row 0 = bottom row (10, 11)
	frame, _, err := Transcode(fits(header(16, 2, 2, card("BZERO", 32768), card("ROWORDER", "BOTTOM-UP")), data))
	if err != nil {
		t.Fatal(err)
	}
	want := []uint16{32788, 32789, 32778, 32779} // top row first, +32768 recovery
	for i, got := range le16(t, frame.Pixels) {
		if got != want[i] {
			t.Fatalf("pixel %d = %d, want %d", i, got, want[i])
		}
	}
}

// TestRowOrderAbsent: with no ROWORDER card nothing is flipped, and the header
// reports the absence.
func TestRowOrderAbsent(t *testing.T) {
	data := be16(10, 11, 20, 21)
	frame, h, err := Transcode(fits(header(16, 2, 2, card("BZERO", 32768)), data))
	if err != nil {
		t.Fatal(err)
	}
	if h.RowOrder != "" {
		t.Fatalf("RowOrder = %q, want empty", h.RowOrder)
	}
	if got := le16(t, frame.Pixels)[0]; got != 32778 {
		t.Fatalf("pixel 0 = %d (flipped without a ROWORDER?)", got)
	}
}

// TestSecondHeaderBlock: cards spilling into a second 2880-byte block move the
// data offset with them.
func TestSecondHeaderBlock(t *testing.T) {
	cards := header(16, 1, 1, card("BZERO", 32768), card("BAYERPAT", "RGGB"))
	for i := 0; i < 35; i++ {
		cards = append(cards, fmt.Sprintf("COMMENT filler %d", i))
	}
	frame, h, err := Transcode(fits(cards, be16(0)))
	if err != nil {
		t.Fatal(err)
	}
	if h.DataOffset != 2*blockSize || h.BayerPat != "RGGB" {
		t.Fatalf("header = %+v", h)
	}
	if got := le16(t, frame.Pixels)[0]; got != 32768 {
		t.Fatalf("pixel = %d", got)
	}
}

func TestRejects(t *testing.T) {
	cases := map[string][]byte{
		"not fits":     []byte("HELLO"),
		"no END":       []byte(strings.Repeat(" ", blockSize)),
		"truncated":    fits(header(16, 100, 100, card("BZERO", 32768)), be16(1, 2, 3)),
		"naxis3":       fits(append(header(8, 2, 2), card("NAXIS", 3), card("NAXIS3", 3)), make([]byte, 12)),
		"float bitpix": fits(header(-32, 2, 2), make([]byte, 16)),
		"bscale":       fits(header(16, 2, 2, card("BSCALE", 2)), be16(1, 2, 3, 4)),
	}
	for name, raw := range cases {
		if _, _, err := Transcode(raw); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
