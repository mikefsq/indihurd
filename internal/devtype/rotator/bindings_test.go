package rotator

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

// TestTotality checks every server.Rotator member has exactly one table entry.
func TestTotality(t *testing.T) {
	problems := binding.CheckTotal(reflect.TypeOf((*server.Rotator)(nil)).Elem(), table)
	for _, p := range problems {
		t.Error(p)
	}
}

const simDefs = `
<defSwitchVector device='R' name='CONNECTION' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='CONNECT'>On</defSwitch><defSwitch name='DISCONNECT'>Off</defSwitch>
</defSwitchVector>
<defNumberVector device='R' name='ABS_ROTATOR_ANGLE' state='Ok' perm='rw'>
  <defNumber name='ANGLE' min='0' max='360' step='10'>90</defNumber>
</defNumberVector>
<defNumberVector device='R' name='SYNC_ROTATOR_ANGLE' state='Ok' perm='rw'>
  <defNumber name='ANGLE' min='0' max='360' step='10'>0</defNumber>
</defNumberVector>
<defSwitchVector device='R' name='ROTATOR_ABORT_MOTION' state='Idle' perm='rw' rule='AtMostOne'>
  <defSwitch name='ABORT'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='R' name='ROTATOR_REVERSE' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='INDI_ENABLED'>Off</defSwitch><defSwitch name='INDI_DISABLED'>On</defSwitch>
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
	dev  *Rotator
	send *fakeSender
	st   *snapshot.Store
	up   bool
}

func newFixture(t *testing.T, defs string) *fixture {
	t.Helper()
	fx := &fixture{st: snapshot.NewStore(), send: &fakeSender{}, up: true}
	fx.apply(t, defs)
	kit := &binding.Kit{
		Device: "R",
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
	fx.dev = New(Config{Name: "TestRotator", Exec: "indi_x", Slot: "11208/0", Version: "test"}, kit)
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
	if got := d.Position(); got != 90 {
		t.Fatalf("Position = %g", got)
	}
	if got := d.MechanicalPosition(); got != 90 {
		t.Fatalf("MechanicalPosition = %g, want the same synced angle", got)
	}
	if got := d.TargetPosition(); got != 90 {
		t.Fatalf("TargetPosition before any command = %g, want current position", got)
	}
	if d.IsMoving() {
		t.Fatal("IsMoving with Ok state")
	}
	if !d.CanReverse() {
		t.Fatal("CanReverse false with rw ROTATOR_REVERSE")
	}
	if d.Reverse() {
		t.Fatal("Reverse true with INDI_DISABLED on")
	}
	if got := d.StepSize(); got != 0 {
		t.Fatalf("StepSize = %g, want 0 (absent; no error channel)", got)
	}
}

// TestMoveAbsolute checks the write, the retained target, and the in-flight bit.
func TestMoveAbsolute(t *testing.T) {
	fx := newFixture(t, simDefs)
	if err := fx.dev.MoveAbsolute(120); err != nil {
		t.Fatal(err)
	}
	if len(fx.send.sent) != 1 || fx.send.sent[0] != "ABS_ROTATOR_ANGLE/ANGLE=120" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
	if got := fx.dev.TargetPosition(); got != 120 {
		t.Fatalf("TargetPosition = %g, want the commanded 120", got)
	}
	if !fx.dev.IsMoving() {
		t.Fatal("IsMoving false right after MoveAbsolute (silent-mover guard)")
	}
	fx.apply(t, `<setNumberVector device='R' name='ABS_ROTATOR_ANGLE' state='Ok'>
  <oneNumber name='ANGLE'>120</oneNumber></setNumberVector>`)
	if fx.dev.IsMoving() {
		t.Fatal("IsMoving true after the echo")
	}
}

// TestMoveRelativeWraps checks that without a REL vector, Move writes current + delta
// wrapped into [0,360) rather than clipped.
func TestMoveRelativeWraps(t *testing.T) {
	fx := newFixture(t, simDefs)
	fx.apply(t, `<setNumberVector device='R' name='ABS_ROTATOR_ANGLE' state='Ok'>
  <oneNumber name='ANGLE'>350</oneNumber></setNumberVector>`)
	if err := fx.dev.Move(20); err != nil {
		t.Fatal(err)
	}
	if len(fx.send.sent) != 1 || fx.send.sent[0] != "ABS_ROTATOR_ANGLE/ANGLE=10" {
		t.Fatalf("sent = %v (want 350+20 wrapped to 10)", fx.send.sent)
	}
}

// TestMoveRelativeUsesRelVector checks Move prefers REL_ROTATOR_ANGLE where the driver
// defines one.
func TestMoveRelativeUsesRelVector(t *testing.T) {
	fx := newFixture(t, simDefs+`
<defNumberVector device='R' name='REL_ROTATOR_ANGLE' state='Ok' perm='rw'>
  <defNumber name='ANGLE' min='-180' max='180' step='10'>0</defNumber>
</defNumberVector>`)
	if err := fx.dev.Move(-15); err != nil {
		t.Fatal(err)
	}
	if len(fx.send.sent) != 1 || fx.send.sent[0] != "REL_ROTATOR_ANGLE/ANGLE=-15" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
	if got := fx.dev.TargetPosition(); got != 75 {
		t.Fatalf("TargetPosition = %g, want 90-15", got)
	}
}

// TestMoveRangeCheckedNothingSent checks an out-of-range move fails without reaching the driver.
func TestMoveRangeCheckedNothingSent(t *testing.T) {
	fx := newFixture(t, simDefs)
	err := fx.dev.MoveAbsolute(400)
	if errNum(err) != alpaca.ErrNumInvalidValue {
		t.Fatalf("out-of-range MoveAbsolute err = %v, want 0x401", err)
	}
	if len(fx.send.sent) != 0 {
		t.Fatalf("out-of-range MoveAbsolute reached the driver: %v", fx.send.sent)
	}
	if fx.dev.IsMoving() {
		t.Fatal("in-flight bit held after a refused send")
	}
}

// TestSyncHaltReverse checks each writes its mapped property.
func TestSyncHaltReverse(t *testing.T) {
	fx := newFixture(t, simDefs)
	if err := fx.dev.Sync(45); err != nil {
		t.Fatal(err)
	}
	if err := fx.dev.Halt(); err != nil {
		t.Fatal(err)
	}
	if err := fx.dev.SetReverse(true); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"SYNC_ROTATOR_ANGLE/ANGLE=45",
		"ROTATOR_ABORT_MOTION/ABORT(on)",
		"ROTATOR_REVERSE/INDI_ENABLED(on)",
	}
	if fmt.Sprint(fx.send.sent) != fmt.Sprint(want) {
		t.Fatalf("sent = %v, want %v", fx.send.sent, want)
	}
}

// TestReverseReadOnly checks a read-only ROTATOR_REVERSE gives CanReverse false and
// SetReverse 0x400.
func TestReverseReadOnly(t *testing.T) {
	fx := newFixture(t, `
<defNumberVector device='R' name='ABS_ROTATOR_ANGLE' state='Ok' perm='rw'>
  <defNumber name='ANGLE' min='0' max='360' step='10'>90</defNumber>
</defNumberVector>
<defSwitchVector device='R' name='ROTATOR_REVERSE' state='Ok' perm='ro' rule='OneOfMany'>
  <defSwitch name='INDI_ENABLED'>Off</defSwitch><defSwitch name='INDI_DISABLED'>On</defSwitch>
</defSwitchVector>`)
	if fx.dev.CanReverse() {
		t.Fatal("CanReverse true with ro vector")
	}
	if err := fx.dev.SetReverse(true); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatalf("SetReverse err = %v, want 0x400", err)
	}
}

// TestDeadChild checks a down child gives 0x407 with the supervisor's reason and clears
// the in-flight bit.
func TestDeadChild(t *testing.T) {
	fx := newFixture(t, simDefs)
	if err := fx.dev.MoveAbsolute(10); err != nil {
		t.Fatal(err)
	}
	fx.up = false
	fx.st.Invalidate()
	if fx.dev.IsMoving() {
		t.Fatal("IsMoving true while down")
	}
	err := fx.dev.MoveAbsolute(20)
	if errNum(err) != alpaca.ErrNumNotConnected {
		t.Fatalf("MoveAbsolute err = %v, want 0x407", err)
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
<defNumberVector device='R' name='FANCY_VENDOR_KNOB' state='Ok' perm='rw'>
  <defNumber name='K'>1</defNumber>
</defNumberVector>`)
	notes := binding.Validate(table, consumed, fx.st.Current(), "R")
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
