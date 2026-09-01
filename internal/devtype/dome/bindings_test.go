package dome

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

// TestTotality is the drift gate: every server.Dome member has exactly one
// table entry, and no entry is stale.
func TestTotality(t *testing.T) {
	problems := binding.CheckTotal(reflect.TypeOf((*server.Dome)(nil)).Elem(), table)
	for _, p := range problems {
		t.Error(p)
	}
}

// simDefs mirrors the INDI Dome Simulator's capability set: abs/rel move,
// abort, park, shutter, slaving. No sync, no home.
const simDefs = `
<defSwitchVector device='D' name='CONNECTION' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='CONNECT'>On</defSwitch><defSwitch name='DISCONNECT'>Off</defSwitch>
</defSwitchVector>
<defNumberVector device='D' name='ABS_DOME_POSITION' state='Ok' perm='rw'>
  <defNumber name='DOME_ABSOLUTE_POSITION' min='0' max='360' step='1'>0</defNumber>
</defNumberVector>
<defNumberVector device='D' name='REL_DOME_POSITION' state='Ok' perm='rw'>
  <defNumber name='DOME_RELATIVE_POSITION' min='-180' max='180' step='10'>0</defNumber>
</defNumberVector>
<defSwitchVector device='D' name='DOME_MOTION' state='Ok' perm='rw' rule='AtMostOne'>
  <defSwitch name='DOME_CW'>Off</defSwitch><defSwitch name='DOME_CCW'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='D' name='DOME_ABORT_MOTION' state='Idle' perm='rw' rule='AtMostOne'>
  <defSwitch name='ABORT'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='D' name='DOME_SHUTTER' state='Ok' perm='rw' rule='AtMostOne'>
  <defSwitch name='SHUTTER_OPEN'>Off</defSwitch><defSwitch name='SHUTTER_CLOSE'>On</defSwitch>
</defSwitchVector>
<defSwitchVector device='D' name='DOME_PARK' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='PARK'>Off</defSwitch><defSwitch name='UNPARK'>On</defSwitch>
</defSwitchVector>
<defSwitchVector device='D' name='DOME_PARK_OPTION' state='Idle' perm='rw' rule='AtMostOne'>
  <defSwitch name='PARK_CURRENT'>Off</defSwitch><defSwitch name='PARK_DEFAULT'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='D' name='DOME_AUTOSYNC' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='DOME_AUTOSYNC_ENABLE'>Off</defSwitch><defSwitch name='DOME_AUTOSYNC_DISABLE'>On</defSwitch>
</defSwitchVector>`

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
	dev  *Dome
	send *fakeSender
	st   *snapshot.Store
	up   bool
}

func newFixture(t *testing.T, defs string) *fixture {
	t.Helper()
	fx := &fixture{st: snapshot.NewStore(), send: &fakeSender{}, up: true}
	fx.apply(t, defs)
	kit := &binding.Kit{
		Device: "D",
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
	fx.dev = New(Config{Name: "TestDome", Exec: "indi_x", Slot: "11208/0", Version: "test"}, kit)
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

func TestCapabilities(t *testing.T) {
	fx := newFixture(t, simDefs)
	d := fx.dev
	for name, got := range map[string]bool{
		"CanFindHome":    d.CanFindHome(),
		"CanSetAltitude": d.CanSetAltitude(),
	} {
		if got {
			t.Errorf("%s = true; INDI::Dome has neither", name)
		}
	}
	for name, got := range map[string]bool{
		"CanPark":       d.CanPark(),
		"CanSetAzimuth": d.CanSetAzimuth(),
		"CanSetPark":    d.CanSetPark(),
		"CanSetShutter": d.CanSetShutter(),
		"CanSlave":      d.CanSlave(),
	} {
		if !got {
			t.Errorf("%s = false with the property defined", name)
		}
	}
	if d.CanSyncAzimuth() {
		t.Error("CanSyncAzimuth = true without DOME_SYNC")
	}
}

// TestRollOffRoof: neither an azimuth nor a shutter is fabricated for a roof
// that has neither.
func TestRollOffRoof(t *testing.T) {
	fx := newFixture(t, `
<defSwitchVector device='D' name='DOME_PARK' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='PARK'>On</defSwitch><defSwitch name='UNPARK'>Off</defSwitch>
</defSwitchVector>`)
	if fx.dev.CanSetAzimuth() {
		t.Fatal("CanSetAzimuth true on a roll-off roof")
	}
	if _, err := fx.dev.Azimuth(); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatalf("Azimuth err = %v, want 0x400, never a fabricated 0", err)
	}
	if _, err := fx.dev.ShutterStatus(); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatalf("ShutterStatus err = %v, want 0x400, never closed", err)
	}
	if !fx.dev.AtPark() {
		t.Fatal("AtPark false with PARK on")
	}
}

func TestSlewToAzimuth(t *testing.T) {
	fx := newFixture(t, simDefs)
	if err := fx.dev.SlewToAzimuth(90); err != nil {
		t.Fatal(err)
	}
	if len(fx.send.sent) != 1 || fx.send.sent[0] != "ABS_DOME_POSITION/DOME_ABSOLUTE_POSITION=90" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
	if !fx.dev.Slewing() {
		t.Fatal("Slewing false right after SlewToAzimuth (in-flight window)")
	}
	fx.apply(t, `<setNumberVector device='D' name='ABS_DOME_POSITION' state='Busy'>
  <oneNumber name='DOME_ABSOLUTE_POSITION'>10</oneNumber></setNumberVector>`)
	if !fx.dev.Slewing() {
		t.Fatal("Slewing false while Busy")
	}
	fx.apply(t, `<setNumberVector device='D' name='ABS_DOME_POSITION' state='Ok'>
  <oneNumber name='DOME_ABSOLUTE_POSITION'>90</oneNumber></setNumberVector>`)
	if fx.dev.Slewing() {
		t.Fatal("Slewing true after arrival")
	}
	if az, err := fx.dev.Azimuth(); err != nil || az != 90 {
		t.Fatalf("Azimuth = %v, %v", az, err)
	}
}

func TestSlewRangeCheckedNothingSent(t *testing.T) {
	fx := newFixture(t, simDefs)
	err := fx.dev.SlewToAzimuth(400)
	if errNum(err) != alpaca.ErrNumInvalidValue {
		t.Fatalf("out-of-range SlewToAzimuth err = %v, want 0x401", err)
	}
	if len(fx.send.sent) != 0 {
		t.Fatalf("out-of-range SlewToAzimuth reached the driver: %v", fx.send.sent)
	}
	if fx.dev.Slewing() {
		t.Fatal("in-flight bit held after a refused send")
	}
}

// TestShutterStateMachine: five ASCOM values from the two-member switch plus
// the vector state.
func TestShutterStateMachine(t *testing.T) {
	fx := newFixture(t, simDefs)
	if s, err := fx.dev.ShutterStatus(); err != nil || s != alpaca.ShutterClosed {
		t.Fatalf("ShutterStatus = %v, %v; want Closed", s, err)
	}
	if err := fx.dev.OpenShutter(); err != nil {
		t.Fatal(err)
	}
	if len(fx.send.sent) != 1 || fx.send.sent[0] != "DOME_SHUTTER/SHUTTER_OPEN(on)" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
	// In-flight window: the members still show SHUTTER_CLOSE on, so the answer
	// comes from the recorded send direction.
	if s, _ := fx.dev.ShutterStatus(); s != alpaca.ShutterOpening {
		t.Fatalf("ShutterStatus in-flight = %v, want Opening", s)
	}
	if !fx.dev.Slewing() {
		t.Fatal("Slewing false during shutter motion (IDomeV3)")
	}
	fx.apply(t, `<setSwitchVector device='D' name='DOME_SHUTTER' state='Busy'>
  <oneSwitch name='SHUTTER_OPEN'>On</oneSwitch><oneSwitch name='SHUTTER_CLOSE'>Off</oneSwitch></setSwitchVector>`)
	if s, _ := fx.dev.ShutterStatus(); s != alpaca.ShutterOpening {
		t.Fatalf("ShutterStatus while Busy = %v, want Opening", s)
	}
	fx.apply(t, `<setSwitchVector device='D' name='DOME_SHUTTER' state='Ok'>
  <oneSwitch name='SHUTTER_OPEN'>On</oneSwitch><oneSwitch name='SHUTTER_CLOSE'>Off</oneSwitch></setSwitchVector>`)
	if s, _ := fx.dev.ShutterStatus(); s != alpaca.ShutterOpen {
		t.Fatalf("ShutterStatus = %v, want Open", s)
	}
	if fx.dev.Slewing() {
		t.Fatal("Slewing true after the shutter settled")
	}
	fx.apply(t, `<setSwitchVector device='D' name='DOME_SHUTTER' state='Alert'>
  <oneSwitch name='SHUTTER_OPEN'>On</oneSwitch></setSwitchVector>`)
	if s, _ := fx.dev.ShutterStatus(); s != alpaca.ShutterErr {
		t.Fatalf("ShutterStatus on Alert = %v, want Error", s)
	}
}

// TestSlewUnparks: a slew sends UNPARK first, or a parked dome ignores it and
// AtPark never clears.
func TestSlewUnparks(t *testing.T) {
	fx := newFixture(t, simDefs)
	fx.apply(t, `<setSwitchVector device='D' name='DOME_PARK' state='Ok'>
  <oneSwitch name='PARK'>On</oneSwitch><oneSwitch name='UNPARK'>Off</oneSwitch></setSwitchVector>`)
	if !fx.dev.AtPark() {
		t.Fatal("fixture is not parked")
	}
	if err := fx.dev.SlewToAzimuth(90); err != nil {
		t.Fatal(err)
	}
	if len(fx.send.sent) != 2 {
		t.Fatalf("want unpark then move, sent = %v", fx.send.sent)
	}
	if fx.send.sent[0] != "DOME_PARK/UNPARK(on)" {
		t.Fatalf("first send = %q, want the unpark to precede the move", fx.send.sent[0])
	}
	if !strings.Contains(fx.send.sent[1], "ABS_DOME_POSITION") {
		t.Fatalf("second send = %q, want the azimuth write", fx.send.sent[1])
	}
	// The fixture's WaitSettle returns instantly, so apply the driver's unpark
	// echo by hand.
	fx.apply(t, `<setSwitchVector device='D' name='DOME_PARK' state='Ok'>
  <oneSwitch name='PARK'>Off</oneSwitch><oneSwitch name='UNPARK'>On</oneSwitch></setSwitchVector>`)
	if fx.dev.AtPark() {
		t.Fatal("AtPark still true after a slew — the IDomeV3 contract says it resets")
	}
	if !fx.dev.Slewing() {
		t.Fatal("Slewing false right after the slew")
	}

	// An unparked dome must not emit a redundant unpark.
	fx2 := newFixture(t, simDefs)
	if err := fx2.dev.SlewToAzimuth(90); err != nil {
		t.Fatal(err)
	}
	if len(fx2.send.sent) != 1 || strings.Contains(fx2.send.sent[0], "UNPARK") {
		t.Fatalf("unparked slew sent = %v, want the move alone", fx2.send.sent)
	}
}

func TestPark(t *testing.T) {
	fx := newFixture(t, simDefs)
	if fx.dev.AtPark() {
		t.Fatal("AtPark true with UNPARK on")
	}
	if err := fx.dev.Park(); err != nil {
		t.Fatal(err)
	}
	if len(fx.send.sent) != 1 || fx.send.sent[0] != "DOME_PARK/PARK(on)" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
	if !fx.dev.Slewing() {
		t.Fatal("Slewing false right after Park")
	}
	fx.apply(t, `<setSwitchVector device='D' name='DOME_PARK' state='Busy'>
  <oneSwitch name='PARK'>On</oneSwitch><oneSwitch name='UNPARK'>Off</oneSwitch></setSwitchVector>`)
	if fx.dev.AtPark() {
		t.Fatal("AtPark true while park motion is Busy")
	}
	fx.apply(t, `<setSwitchVector device='D' name='DOME_PARK' state='Ok'>
  <oneSwitch name='PARK'>On</oneSwitch><oneSwitch name='UNPARK'>Off</oneSwitch></setSwitchVector>`)
	if !fx.dev.AtPark() {
		t.Fatal("AtPark false after the park settled")
	}
	if err := fx.dev.SetPark(); err != nil {
		t.Fatal(err)
	}
	if fx.send.sent[1] != "DOME_PARK_OPTION/PARK_CURRENT(on)" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
}

func TestSlaved(t *testing.T) {
	fx := newFixture(t, simDefs)
	if fx.dev.Slaved() {
		t.Fatal("Slaved true with DISABLE on")
	}
	if err := fx.dev.SetSlaved(true); err != nil {
		t.Fatal(err)
	}
	if fx.send.sent[0] != "DOME_AUTOSYNC/DOME_AUTOSYNC_ENABLE(on)" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
	fx.apply(t, `<setSwitchVector device='D' name='DOME_AUTOSYNC' state='Ok'>
  <oneSwitch name='DOME_AUTOSYNC_ENABLE'>On</oneSwitch><oneSwitch name='DOME_AUTOSYNC_DISABLE'>Off</oneSwitch></setSwitchVector>`)
	if !fx.dev.Slaved() {
		t.Fatal("Slaved false with ENABLE on")
	}
}

// TestSetSlavedFalseWithoutAutosync: ConformU disables slaving even when
// CanSlave is false, which is a no-op success rather than an error.
func TestSetSlavedFalseWithoutAutosync(t *testing.T) {
	fx := newFixture(t, `
<defNumberVector device='D' name='ABS_DOME_POSITION' state='Ok' perm='rw'>
  <defNumber name='DOME_ABSOLUTE_POSITION' min='0' max='360' step='1'>0</defNumber>
</defNumberVector>`)
	if err := fx.dev.SetSlaved(false); err != nil {
		t.Fatal(err)
	}
	if err := fx.dev.SetSlaved(true); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatalf("SetSlaved(true) err = %v, want 0x400", err)
	}
}

func TestAbortClearsInflight(t *testing.T) {
	fx := newFixture(t, simDefs)
	if err := fx.dev.SlewToAzimuth(180); err != nil {
		t.Fatal(err)
	}
	if err := fx.dev.AbortSlew(); err != nil {
		t.Fatal(err)
	}
	if fx.dev.Slewing() {
		t.Fatal("Slewing held by the in-flight bit after AbortSlew")
	}
	if fx.send.sent[1] != "DOME_ABORT_MOTION/ABORT(on)" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
}

// TestDeadChild: 0x407 with the supervisor's reason; in-flight state fails
// rather than holds.
func TestDeadChild(t *testing.T) {
	fx := newFixture(t, simDefs)
	if err := fx.dev.Park(); err != nil {
		t.Fatal(err)
	}
	fx.up = false
	fx.st.Invalidate()
	if fx.dev.Slewing() {
		t.Fatal("Slewing true while down")
	}
	if fx.dev.AtPark() {
		t.Fatal("AtPark asserted from an invalid snapshot")
	}
	_, err := fx.dev.Azimuth()
	if errNum(err) != alpaca.ErrNumNotConnected {
		t.Fatalf("Azimuth err = %v, want 0x407", err)
	}
	if !strings.Contains(err.Error(), "re-acquiring") {
		t.Fatalf("reason not preserved: %v", err)
	}
	if err := fx.dev.Park(); errNum(err) != alpaca.ErrNumNotConnected {
		t.Fatalf("Park err = %v, want 0x407", err)
	}
	if !fx.dev.Connecting() {
		t.Fatal("Connecting() false while down")
	}
}

func TestValidateDrift(t *testing.T) {
	fx := newFixture(t, simDefs+`
<defNumberVector device='D' name='FANCY_VENDOR_KNOB' state='Ok' perm='rw'>
  <defNumber name='K'>1</defNumber>
</defNumberVector>`)
	notes := binding.Validate(table, consumed, fx.st.Current(), "D")
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
