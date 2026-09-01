package covercal

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

// TestTotality is the drift gate: every server.CoverCalibrator member has
// exactly one table entry, and no entry is stale.
func TestTotality(t *testing.T) {
	problems := binding.CheckTotal(reflect.TypeOf((*server.CoverCalibrator)(nil)).Elem(), table)
	for _, p := range problems {
		t.Error(p)
	}
}

// simDefs models a flip-flat: both halves present.
const simDefs = `
<defSwitchVector device='C' name='CONNECTION' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='CONNECT'>On</defSwitch><defSwitch name='DISCONNECT'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='C' name='CAP_PARK' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='PARK'>On</defSwitch><defSwitch name='UNPARK'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='C' name='FLAT_LIGHT_CONTROL' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='FLAT_LIGHT_ON'>Off</defSwitch><defSwitch name='FLAT_LIGHT_OFF'>On</defSwitch>
</defSwitchVector>
<defNumberVector device='C' name='FLAT_LIGHT_INTENSITY' state='Ok' perm='rw'>
  <defNumber name='FLAT_LIGHT_INTENSITY_VALUE' min='0' max='255' step='10'>128</defNumber>
</defNumberVector>`

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
	dev  *CoverCal
	send *fakeSender
	st   *snapshot.Store
	up   bool
}

func newFixture(t *testing.T, defs string) *fixture {
	t.Helper()
	fx := &fixture{st: snapshot.NewStore(), send: &fakeSender{}, up: true}
	fx.apply(t, defs)
	kit := &binding.Kit{
		Device: "C",
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
	fx.dev = New(Config{Name: "TestCoverCal", Exec: "indi_x", Slot: "11208/0", Version: "test"}, kit)
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

func TestStates(t *testing.T) {
	fx := newFixture(t, simDefs)
	d := fx.dev
	if got := d.CoverState(); got != alpaca.CoverClosed {
		t.Fatalf("CoverState = %v, want Closed (PARK on)", got)
	}
	if got := d.CalibratorState(); got != alpaca.CalibratorOff {
		t.Fatalf("CalibratorState = %v, want Off", got)
	}
	if d.CoverMoving() || d.CalibratorChanging() {
		t.Fatal("motion flags true at rest")
	}
	// The driver retains intensity 128 while the light is Off; ASCOM reads 0.
	if got := d.Brightness(); got != 0 {
		t.Fatalf("Brightness while Off = %d, want 0", got)
	}
	if got := d.MaxBrightness(); got != 255 {
		t.Fatalf("MaxBrightness = %d", got)
	}
}

func TestOpenCloseCover(t *testing.T) {
	fx := newFixture(t, simDefs)
	if err := fx.dev.OpenCover(); err != nil {
		t.Fatal(err)
	}
	if len(fx.send.sent) != 1 || fx.send.sent[0] != "CAP_PARK/UNPARK(on)" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
	// In-flight window: moving from the initiator's return.
	if !fx.dev.CoverMoving() || fx.dev.CoverState() != alpaca.CoverMoving {
		t.Fatal("CoverMoving false right after OpenCover")
	}
	fx.apply(t, `<setSwitchVector device='C' name='CAP_PARK' state='Busy'>
  <oneSwitch name='PARK'>Off</oneSwitch><oneSwitch name='UNPARK'>On</oneSwitch></setSwitchVector>`)
	if fx.dev.CoverState() != alpaca.CoverMoving {
		t.Fatal("CoverState not Moving while Busy")
	}
	fx.apply(t, `<setSwitchVector device='C' name='CAP_PARK' state='Ok'>
  <oneSwitch name='PARK'>Off</oneSwitch><oneSwitch name='UNPARK'>On</oneSwitch></setSwitchVector>`)
	if got := fx.dev.CoverState(); got != alpaca.CoverOpen {
		t.Fatalf("CoverState = %v, want Open", got)
	}
	if err := fx.dev.CloseCover(); err != nil {
		t.Fatal(err)
	}
	if fx.send.sent[1] != "CAP_PARK/PARK(on)" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
}

func TestCoverAlertIsError(t *testing.T) {
	fx := newFixture(t, simDefs)
	fx.apply(t, `<setSwitchVector device='C' name='CAP_PARK' state='Alert'>
  <oneSwitch name='PARK'>On</oneSwitch></setSwitchVector>`)
	if got := fx.dev.CoverState(); got != alpaca.CoverError {
		t.Fatalf("CoverState = %v, want Error on Alert", got)
	}
}

// TestHalvesAbsent: a missing half is NotPresent, never fabricated.
func TestHalvesAbsent(t *testing.T) {
	// Cover only (dust cap).
	fx := newFixture(t, `
<defSwitchVector device='C' name='CAP_PARK' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='PARK'>On</defSwitch><defSwitch name='UNPARK'>Off</defSwitch>
</defSwitchVector>`)
	if got := fx.dev.CalibratorState(); got != alpaca.CalibratorNotPresent {
		t.Fatalf("CalibratorState = %v, want NotPresent", got)
	}
	if got := fx.dev.MaxBrightness(); got != 0 {
		t.Fatalf("MaxBrightness = %d, want 0 without a calibrator", got)
	}
	if err := fx.dev.CalibratorOn(1); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatalf("CalibratorOn err = %v, want 0x400", err)
	}

	// Calibrator only (light panel).
	fx = newFixture(t, `
<defSwitchVector device='C' name='FLAT_LIGHT_CONTROL' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='FLAT_LIGHT_ON'>On</defSwitch><defSwitch name='FLAT_LIGHT_OFF'>Off</defSwitch>
</defSwitchVector>`)
	if got := fx.dev.CoverState(); got != alpaca.CoverNotPresent {
		t.Fatalf("CoverState = %v, want NotPresent", got)
	}
	if err := fx.dev.OpenCover(); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatalf("OpenCover err = %v, want 0x400", err)
	}
	if got := fx.dev.MaxBrightness(); got != 1 {
		t.Fatalf("MaxBrightness = %d, want 1 for on/off-only", got)
	}
	if got := fx.dev.Brightness(); got != 1 {
		t.Fatalf("Brightness = %d, want 1 while on", got)
	}
}

// TestCalibratorOnSequence: intensity BEFORE the on switch, so the light comes
// on at the requested brightness.
func TestCalibratorOnSequence(t *testing.T) {
	fx := newFixture(t, simDefs)
	if err := fx.dev.CalibratorOn(200); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"FLAT_LIGHT_INTENSITY/FLAT_LIGHT_INTENSITY_VALUE=200",
		"FLAT_LIGHT_CONTROL/FLAT_LIGHT_ON(on)",
	}
	if fmt.Sprint(fx.send.sent) != fmt.Sprint(want) {
		t.Fatalf("sent = %v, want %v", fx.send.sent, want)
	}
	if !fx.dev.CalibratorChanging() || fx.dev.CalibratorState() != alpaca.CalibratorNotReady {
		t.Fatal("CalibratorChanging false right after CalibratorOn")
	}
	fx.apply(t, `<setSwitchVector device='C' name='FLAT_LIGHT_CONTROL' state='Ok'>
  <oneSwitch name='FLAT_LIGHT_ON'>On</oneSwitch><oneSwitch name='FLAT_LIGHT_OFF'>Off</oneSwitch></setSwitchVector>
<setNumberVector device='C' name='FLAT_LIGHT_INTENSITY' state='Ok'>
  <oneNumber name='FLAT_LIGHT_INTENSITY_VALUE'>200</oneNumber></setNumberVector>`)
	if got := fx.dev.CalibratorState(); got != alpaca.CalibratorReady {
		t.Fatalf("CalibratorState = %v, want Ready", got)
	}
	if got := fx.dev.Brightness(); got != 200 {
		t.Fatalf("Brightness = %d", got)
	}
}

func TestCalibratorOnRangeCheckedNothingSent(t *testing.T) {
	fx := newFixture(t, simDefs)
	err := fx.dev.CalibratorOn(300) // beyond the driver's max 255
	if errNum(err) != alpaca.ErrNumInvalidValue {
		t.Fatalf("out-of-range CalibratorOn err = %v, want 0x401", err)
	}
	if len(fx.send.sent) != 0 {
		t.Fatalf("out-of-range CalibratorOn reached the driver: %v", fx.send.sent)
	}
	if fx.dev.CalibratorChanging() {
		t.Fatal("in-flight bit held after a refused send")
	}
}

func TestHaltCover(t *testing.T) {
	fx := newFixture(t, simDefs)
	// No CAP_ABORT defined → 0x400.
	if err := fx.dev.HaltCover(); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatalf("HaltCover err = %v, want 0x400", err)
	}
	fx.apply(t, `<defSwitchVector device='C' name='CAP_ABORT' state='Idle' perm='rw' rule='AtMostOne'>
  <defSwitch name='ABORT'>Off</defSwitch></defSwitchVector>`)
	if err := fx.dev.HaltCover(); err != nil {
		t.Fatal(err)
	}
	if fx.send.sent[len(fx.send.sent)-1] != "CAP_ABORT/ABORT(on)" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
}

// TestDeadChild: 0x407 on initiators, motion flags false.
func TestDeadChild(t *testing.T) {
	fx := newFixture(t, simDefs)
	if err := fx.dev.OpenCover(); err != nil {
		t.Fatal(err)
	}
	fx.up = false
	fx.st.Invalidate()
	if fx.dev.CoverMoving() || fx.dev.CalibratorChanging() {
		t.Fatal("motion flags true while down")
	}
	err := fx.dev.CloseCover()
	if errNum(err) != alpaca.ErrNumNotConnected {
		t.Fatalf("CloseCover err = %v, want 0x407", err)
	}
	if !strings.Contains(err.Error(), "re-acquiring") {
		t.Fatalf("reason not preserved: %v", err)
	}
	if !fx.dev.Connecting() {
		t.Fatal("Connecting() false while down")
	}
}

func TestValidateDrift(t *testing.T) {
	fx := newFixture(t, simDefs+`
<defNumberVector device='C' name='FANCY_VENDOR_KNOB' state='Ok' perm='rw'>
  <defNumber name='K'>1</defNumber>
</defNumberVector>`)
	notes := binding.Validate(table, consumed, fx.st.Current(), "C")
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
