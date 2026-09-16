package indiserve

import (
	"testing"

	"github.com/mikefsq/indihurd/internal/indiwire"
)

func ccdInfo(x, y, bits float64) *indiwire.Element {
	return &indiwire.Element{Device: "cam", Name: "CCD_INFO", Members: []indiwire.Member{
		{Name: "CCD_MAX_X", Value: x}, {Name: "CCD_MAX_Y", Value: y}, {Name: "CCD_BITSPERPIXEL", Value: bits},
	}}
}

func TestBudgetFloorAndGrowth(t *testing.T) {
	s := New("", nil)
	if got := s.budget(); got != queueBytes {
		t.Fatalf("budget = %d, want the %d floor", got, queueBytes)
	}
	// A small sensor stays under the floor.
	s.noteFrameSize(ccdInfo(1280, 960, 8))
	if got := s.budget(); got != queueBytes {
		t.Errorf("budget = %d, want the floor for a small sensor", got)
	}
	// ASI6200 at 16-bit: 9576*6388*2 = 122MB raw, ~163MB encoded.
	s.noteFrameSize(ccdInfo(9576, 6388, 16))
	want := int64(9576) * 6388 * 2 / 3 * 4 * 2
	if got := s.budget(); got != want {
		t.Errorf("budget = %d, want %d", got, want)
	}
	// A later smaller camera must not lower the high-water mark.
	s.noteFrameSize(ccdInfo(3856, 2180, 8))
	if got := s.budget(); got != want {
		t.Errorf("budget = %d after a small sensor, want %d retained", got, want)
	}
}

func TestBudgetIgnoresIncompleteInfo(t *testing.T) {
	s := New("", nil)
	s.noteFrameSize(&indiwire.Element{Device: "cam", Name: "CCD_EXPOSURE", Members: []indiwire.Member{{Name: "CCD_MAX_X", Value: 9576}}})
	s.noteFrameSize(ccdInfo(9576, 0, 16))
	if got := s.budget(); got != queueBytes {
		t.Errorf("budget = %d, want the floor when CCD_INFO is unusable", got)
	}
}
