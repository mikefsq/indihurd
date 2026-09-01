package filterwheel

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mikefsq/goalpaca/alpaca"
	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/binding"
	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

// TestTotality checks every server.FilterWheel member has exactly one table entry.
func TestTotality(t *testing.T) {
	problems := binding.CheckTotal(reflect.TypeOf((*server.FilterWheel)(nil)).Elem(), table)
	for _, p := range problems {
		t.Error(p)
	}
}

const simDefs = `
<defSwitchVector device='W' name='CONNECTION' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='CONNECT'>On</defSwitch><defSwitch name='DISCONNECT'>Off</defSwitch>
</defSwitchVector>
<defNumberVector device='W' name='FILTER_SLOT' state='Ok' perm='rw'>
  <defNumber name='FILTER_SLOT_VALUE' min='1' max='8' step='1'>1</defNumber>
</defNumberVector>
<defTextVector device='W' name='FILTER_NAME' state='Ok' perm='rw'>
  <defText name='FILTER_SLOT_NAME_1'>Red</defText>
  <defText name='FILTER_SLOT_NAME_2'>Green</defText>
  <defText name='FILTER_SLOT_NAME_3'>Blue</defText>
  <defText name='FILTER_SLOT_NAME_4'>H_Alpha</defText>
  <defText name='FILTER_SLOT_NAME_5'>SII</defText>
  <defText name='FILTER_SLOT_NAME_6'>OIII</defText>
  <defText name='FILTER_SLOT_NAME_7'>LPR</defText>
  <defText name='FILTER_SLOT_NAME_8'>Luminance</defText>
</defTextVector>`

type fakeSender struct {
	sent []string
}

func (s *fakeSender) SetNumber(_ context.Context, _, prop string, v map[string]float64) error {
	for e, val := range v {
		s.sent = append(s.sent, fmt.Sprintf("%s/%s=%g", prop, e, val))
	}
	return nil
}
func (s *fakeSender) SetSwitch(_ context.Context, _, prop string, on, off []string) error {
	for _, e := range on {
		s.sent = append(s.sent, prop+"/"+e+"(on)")
	}
	for _, e := range off {
		s.sent = append(s.sent, prop+"/"+e+"(off)")
	}
	return nil
}
func (s *fakeSender) SetText(context.Context, string, string, map[string]string) error { return nil }
func (s *fakeSender) WaitSettle(context.Context, string, string, time.Time, time.Duration) (indiwire.State, string, error) {
	return indiwire.Ok, "", nil
}
func (s *fakeSender) WaitUpdate(context.Context, string, string, time.Time, time.Duration) error {
	return nil
}

type fixture struct {
	dev  *FilterWheel
	send *fakeSender
	st   *snapshot.Store
	up   bool
}

func newFixture(t *testing.T, defs string) *fixture {
	t.Helper()
	fx := &fixture{st: snapshot.NewStore(), send: &fakeSender{}, up: true}
	fx.apply(t, defs)
	kit := &binding.Kit{
		Device: "W",
		Snap:   fx.st.Current,
		Send:   fx.send,
		Avail: func() (bool, string) {
			if fx.up {
				return true, ""
			}
			return false, "INDI child indi_x exited; re-acquiring"
		},
		Run: func(context.Context) {},
	}
	fx.dev = New(Config{Name: "TestWheel", Exec: "indi_x", Slot: "11208/0", Version: "test"}, kit)
	return fx
}

func (fx *fixture) apply(t *testing.T, stream string) {
	t.Helper()
	p := indiwire.NewParser(strings.NewReader(stream))
	for {
		el, err := p.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		fx.st.Apply(el, time.Now())
	}
}

// TestReads checks the mapped reads against the simulator defs.
func TestReads(t *testing.T) {
	fx := newFixture(t, simDefs)
	d := fx.dev
	if got := d.Position(); got != 0 {
		t.Fatalf("Position = %d, want 0 (INDI slot 1 → ASCOM 0)", got)
	}
	names := d.Names()
	if len(names) != 8 || names[0] != "Red" || names[7] != "Luminance" {
		t.Fatalf("Names = %v", names)
	}
	offs := d.FocusOffsets()
	if len(offs) != 8 {
		t.Fatalf("FocusOffsets len = %d, want 8", len(offs))
	}
	for i, o := range offs {
		if o != 0 {
			t.Fatalf("FocusOffsets[%d] = %d, want 0", i, o)
		}
	}
}

// TestNamesSynthesised checks a driver with FILTER_SLOT but no FILTER_NAME still reports names.
func TestNamesSynthesised(t *testing.T) {
	fx := newFixture(t, `
<defNumberVector device='W' name='FILTER_SLOT' state='Ok' perm='rw'>
  <defNumber name='FILTER_SLOT_VALUE' min='1' max='5' step='1'>2</defNumber>
</defNumberVector>`)
	names := fx.dev.Names()
	if len(names) != 5 || names[0] != "Filter 1" {
		t.Fatalf("Names = %v", names)
	}
	if got := fx.dev.Position(); got != 1 {
		t.Fatalf("Position = %d, want 1", got)
	}
}

// TestSetPositionOffByOne checks ASCOM slot 3 reaches the driver as INDI slot 4.
func TestSetPositionOffByOne(t *testing.T) {
	fx := newFixture(t, simDefs)
	if err := fx.dev.SetPosition(3); err != nil {
		t.Fatal(err)
	}
	if len(fx.send.sent) != 1 || fx.send.sent[0] != "FILTER_SLOT/FILTER_SLOT_VALUE=4" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
}

// TestSetPositionRangeCheckedNothingSent checks an out-of-range slot fails without reaching the driver.
func TestSetPositionRangeCheckedNothingSent(t *testing.T) {
	fx := newFixture(t, simDefs)
	err := fx.dev.SetPosition(8) // INDI slot 9, beyond max 8
	if errNum(err) != alpaca.ErrNumInvalidValue {
		t.Fatalf("out-of-range SetPosition err = %v, want 0x401", err)
	}
	if len(fx.send.sent) != 0 {
		t.Fatalf("out-of-range SetPosition reached the driver: %v", fx.send.sent)
	}
}

// TestMovingSentinel checks Position reads -1 while FILTER_SLOT is Busy, never the target slot.
func TestMovingSentinel(t *testing.T) {
	fx := newFixture(t, simDefs)
	fx.apply(t, `<setNumberVector device='W' name='FILTER_SLOT' state='Busy'>
  <oneNumber name='FILTER_SLOT_VALUE'>4</oneNumber></setNumberVector>`)
	if got := fx.dev.Position(); got != -1 {
		t.Fatalf("Position while Busy = %d, want -1", got)
	}
	if !fx.dev.Busy() {
		t.Fatal("Busy false while moving")
	}
	fx.apply(t, `<setNumberVector device='W' name='FILTER_SLOT' state='Ok'>
  <oneNumber name='FILTER_SLOT_VALUE'>4</oneNumber></setNumberVector>`)
	if got := fx.dev.Position(); got != 3 {
		t.Fatalf("Position after move = %d, want 3", got)
	}
}

// TestSilentMoverInflight checks Position reads -1 from SetPosition's return, before any driver echo.
func TestSilentMoverInflight(t *testing.T) {
	fx := newFixture(t, simDefs)
	if err := fx.dev.SetPosition(2); err != nil {
		t.Fatal(err)
	}
	if got := fx.dev.Position(); got != -1 {
		t.Fatalf("Position right after SetPosition = %d, want -1", got)
	}
	fx.apply(t, `<setNumberVector device='W' name='FILTER_SLOT' state='Ok'>
  <oneNumber name='FILTER_SLOT_VALUE'>3</oneNumber></setNumberVector>`)
	if got := fx.dev.Position(); got != 2 {
		t.Fatalf("Position after echo = %d, want 2", got)
	}
}

// TestDeadChild checks a down child gives 0x407 with the supervisor's reason, and Position -1.
func TestDeadChild(t *testing.T) {
	fx := newFixture(t, simDefs)
	fx.up = false
	fx.st.Invalidate()
	if got := fx.dev.Position(); got != -1 {
		t.Fatalf("Position while down = %d, want -1", got)
	}
	err := fx.dev.SetPosition(2)
	if errNum(err) != alpaca.ErrNumNotConnected {
		t.Fatalf("SetPosition err = %v, want 0x407", err)
	}
	if !strings.Contains(err.Error(), "re-acquiring") {
		t.Fatalf("reason not preserved: %v", err)
	}
	if !fx.dev.Connecting() {
		t.Fatal("Connecting() false while down")
	}
}

// TestValidateDrift checks an unmapped driver property is reported.
func TestValidateDrift(t *testing.T) {
	fx := newFixture(t, simDefs+`
<defNumberVector device='W' name='FANCY_VENDOR_KNOB' state='Ok' perm='rw'>
  <defNumber name='K'>1</defNumber>
</defNumberVector>`)
	notes := binding.Validate(table, consumed, fx.st.Current(), "W")
	if !strings.Contains(strings.Join(notes, "\n"), "FANCY_VENDOR_KNOB") {
		t.Fatalf("unmapped property not reported:\n%s", strings.Join(notes, "\n"))
	}
}

func errNum(err error) int {
	if ae, ok := err.(*alpaca.AlpacaError); ok {
		return ae.Number
	}
	return -1
}
