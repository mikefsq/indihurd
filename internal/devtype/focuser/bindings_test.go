package focuser

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

func TestTotality(t *testing.T) {
	problems := binding.CheckTotal(reflect.TypeOf((*server.Focuser)(nil)).Elem(), table)
	for _, p := range problems {
		t.Error(p)
	}
}

const simDefs = `
<defSwitchVector device='F' name='CONNECTION' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='CONNECT'>On</defSwitch><defSwitch name='DISCONNECT'>Off</defSwitch>
</defSwitchVector>
<defNumberVector device='F' name='ABS_FOCUS_POSITION' state='Ok' perm='rw'>
  <defNumber name='FOCUS_ABSOLUTE_POSITION' min='0' max='60000' step='1'>17000</defNumber>
</defNumberVector>
<defNumberVector device='F' name='REL_FOCUS_POSITION' state='Ok' perm='rw'>
  <defNumber name='FOCUS_RELATIVE_POSITION' min='0' max='5000' step='1'>0</defNumber>
</defNumberVector>
<defNumberVector device='F' name='FOCUS_MAX' state='Ok' perm='rw'>
  <defNumber name='FOCUS_MAX_VALUE' min='1' max='300000' step='1'>60000</defNumber>
</defNumberVector>
<defSwitchVector device='F' name='FOCUS_ABORT_MOTION' state='Idle' perm='rw' rule='AtMostOne'>
  <defSwitch name='ABORT'>Off</defSwitch>
</defSwitchVector>
<defNumberVector device='F' name='FOCUS_TEMPERATURE' state='Ok' perm='ro'>
  <defNumber name='TEMPERATURE'>12.5</defNumber>
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
	dev  *Focuser
	send *fakeSender
	st   *snapshot.Store
	up   bool
}

func newFixture(t *testing.T, defs string) *fixture {
	t.Helper()
	fx := &fixture{st: snapshot.NewStore(), send: &fakeSender{}, up: true}
	fx.apply(t, defs)
	kit := &binding.Kit{
		Device: "F",
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
	fx.dev = New(Config{Name: "TestFocuser", Exec: "indi_x", Slot: "11208/0", Version: "test"}, kit)
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

func TestDriverIdentity(t *testing.T) {
	fx := newFixture(t, simDefs)
	if got, want := fx.dev.Description(), "INDI F via indihurd"; got != want {
		t.Fatalf("Description before DRIVER_INFO = %q, want %q", got, want)
	}
	if got, want := fx.dev.DriverInfo(), "indihurd test INDI bridge"; got != want {
		t.Fatalf("DriverInfo before DRIVER_INFO = %q, want %q", got, want)
	}

	fx.apply(t, `
<defTextVector device='F' name='DRIVER_INFO' state='Idle' perm='ro'>
  <defText name='DRIVER_NAME'>Focuser Simulator</defText>
  <defText name='DRIVER_EXEC'>indi_x</defText>
  <defText name='DRIVER_VERSION'>1.0</defText>
  <defText name='DRIVER_INTERFACE'>8</defText>
</defTextVector>`)
	if got, want := fx.dev.Description(), "INDI Focuser Simulator 1.0 (F) via indihurd"; got != want {
		t.Fatalf("Description = %q, want %q", got, want)
	}
	if got, want := fx.dev.DriverInfo(), "indihurd test bridging Focuser Simulator 1.0 (indi_x)"; got != want {
		t.Fatalf("DriverInfo = %q, want %q", got, want)
	}

	fx.up = false
	if got, want := fx.dev.Description(), "INDI F via indihurd"; got != want {
		t.Fatalf("Description while down = %q, want %q", got, want)
	}
	if got, want := fx.dev.DriverInfo(), "indihurd test INDI bridge"; got != want {
		t.Fatalf("DriverInfo while down = %q, want %q", got, want)
	}
}

func TestReads(t *testing.T) {
	fx := newFixture(t, simDefs)
	d := fx.dev
	if pos, err := d.Position(); err != nil || pos != 17000 {
		t.Fatalf("Position = %d, %v", pos, err)
	}
	if !d.Absolute() {
		t.Fatal("Absolute = false with ABS defined")
	}
	if d.IsMoving() {
		t.Fatal("IsMoving with Ok state")
	}
	if got := d.MaxStep(); got != 60000 {
		t.Fatalf("MaxStep = %d", got)
	}
	if got := d.MaxIncrement(); got != 5000 {
		t.Fatalf("MaxIncrement = %d (want REL member max)", got)
	}
	if temp, err := d.Temperature(); err != nil || temp != 12.5 {
		t.Fatalf("Temperature = %v, %v", temp, err)
	}
	if _, err := d.StepSize(); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatalf("StepSize err = %v, want 0x400", err)
	}
	if d.TempCompAvailable() || d.TempComp() {
		t.Fatal("temp comp should be unavailable")
	}
}

func TestMoveRangeCheckedNothingSent(t *testing.T) {
	fx := newFixture(t, simDefs)
	err := fx.dev.Move(70000) // beyond the driver's max 60000
	if errNum(err) != alpaca.ErrNumInvalidValue {
		t.Fatalf("out-of-range Move err = %v, want 0x401", err)
	}
	if len(fx.send.sent) != 0 {
		t.Fatalf("out-of-range Move reached the driver: %v", fx.send.sent)
	}
	if err := fx.dev.Move(30000); err != nil {
		t.Fatal(err)
	}
	if len(fx.send.sent) != 1 || fx.send.sent[0] != "ABS_FOCUS_POSITION/FOCUS_ABSOLUTE_POSITION=30000" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
}

func TestHaltAndBusy(t *testing.T) {
	fx := newFixture(t, simDefs)
	if err := fx.dev.Halt(); err != nil {
		t.Fatal(err)
	}
	if len(fx.send.sent) != 1 || fx.send.sent[0] != "FOCUS_ABORT_MOTION/ABORT(on)" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
	fx.apply(t, `<setNumberVector device='F' name='ABS_FOCUS_POSITION' state='Busy'>
  <oneNumber name='FOCUS_ABSOLUTE_POSITION'>20000</oneNumber></setNumberVector>`)
	if !fx.dev.IsMoving() || !fx.dev.Busy() {
		t.Fatal("Busy state not reflected")
	}
}

func TestSilentMoverInflight(t *testing.T) {
	fx := newFixture(t, simDefs)
	if err := fx.dev.Move(30000); err != nil {
		t.Fatal(err)
	}
	if !fx.dev.IsMoving() {
		t.Fatal("IsMoving false right after Move on a silent driver")
	}
	fx.apply(t, `<setNumberVector device='F' name='ABS_FOCUS_POSITION' state='Ok'>
  <oneNumber name='FOCUS_ABSOLUTE_POSITION'>30000</oneNumber></setNumberVector>`)
	if fx.dev.IsMoving() {
		t.Fatal("IsMoving true after the echo")
	}
	if pos, _ := fx.dev.Position(); pos != 30000 {
		t.Fatalf("Position = %d", pos)
	}
}

func TestRelativeOnlyFocuser(t *testing.T) {
	fx := newFixture(t, `
<defNumberVector device='F' name='REL_FOCUS_POSITION' state='Ok' perm='rw'>
  <defNumber name='FOCUS_RELATIVE_POSITION' min='0' max='5000' step='1'>0</defNumber>
</defNumberVector>`)
	if fx.dev.Absolute() {
		t.Fatal("Absolute without ABS property")
	}
	if _, err := fx.dev.Position(); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatalf("Position err = %v, want 0x400", err)
	}
	if err := fx.dev.Move(100); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatalf("Move err = %v, want 0x400", err)
	}
}

func TestDeadChild(t *testing.T) {
	fx := newFixture(t, simDefs)
	fx.up = false
	fx.st.Invalidate()
	_, err := fx.dev.Position()
	if errNum(err) != alpaca.ErrNumNotConnected {
		t.Fatalf("err = %v, want 0x407", err)
	}
	if !strings.Contains(err.Error(), "re-acquiring") {
		t.Fatalf("reason not preserved: %v", err)
	}
	if !fx.dev.Connecting() {
		t.Fatal("Connecting() false while down")
	}
	if fx.dev.IsMoving() {
		t.Fatal("IsMoving true while down")
	}
	if err := fx.dev.Move(100); errNum(err) != alpaca.ErrNumNotConnected {
		t.Fatalf("Move err = %v, want 0x407", err)
	}
}

func TestDeviceStateOneSnapshot(t *testing.T) {
	fx := newFixture(t, simDefs)
	sv := fx.dev.DeviceState()
	got := map[string]any{}
	for _, s := range sv {
		got[s.Name] = s.Value
	}
	if got["Position"] != 17000 || got["IsMoving"] != false || got["Temperature"] != 12.5 {
		t.Fatalf("DeviceState = %v", got)
	}
}

func TestValidateDrift(t *testing.T) {
	fx := newFixture(t, simDefs+`
<defNumberVector device='F' name='FANCY_VENDOR_KNOB' state='Ok' perm='rw'>
  <defNumber name='K'>1</defNumber>
</defNumberVector>`)
	notes := binding.Validate(table, consumed, fx.st.Current(), "F")
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "FANCY_VENDOR_KNOB") {
		t.Fatalf("unmapped property not reported:\n%s", joined)
	}
}

func TestActionsWiring(t *testing.T) {
	fx := newFixture(t, simDefs+`
<defNumberVector device='F' name='FOCUS_SPEED' state='Ok' perm='rw'>
  <defNumber name='FOCUS_SPEED_VALUE' min='1' max='5' step='1'>1</defNumber>
</defNumberVector>`)
	got := map[string]bool{}
	for _, a := range fx.dev.SupportedActions() {
		got[a] = true
	}
	if !got["INDI:FOCUS_SPEED"] || !got["INDI:_PROPERTIES"] {
		t.Fatalf("SupportedActions missing the reachable set: %v", fx.dev.SupportedActions())
	}
	for _, banned := range []string{"INDI:ABS_FOCUS_POSITION", "INDI:REL_FOCUS_POSITION", "INDI:FOCUS_MAX", "INDI:CONNECTION"} {
		if got[banned] {
			t.Errorf("SupportedActions leaks %s", banned)
		}
	}
	if _, err := fx.dev.Action("INDI:FOCUS_SPEED", `{"FOCUS_SPEED_VALUE": 3}`); err != nil {
		t.Fatal(err)
	}
	if want := "FOCUS_SPEED/FOCUS_SPEED_VALUE=3"; len(fx.send.sent) != 1 || fx.send.sent[0] != want {
		t.Fatalf("sent = %v, want [%s]", fx.send.sent, want)
	}
	if _, err := fx.dev.Action("INDI:ABS_FOCUS_POSITION", ""); errNum(err) != alpaca.ErrNumActionNotImplemented {
		t.Fatalf("typed-consumed action err = %v, want 0x40C", err)
	}
}

func TestUniqueIDStable(t *testing.T) {
	a := binding.UniqueID("indi_x", "F", "serial123")
	b := binding.UniqueID("indi_x", "F", "serial123")
	c := binding.UniqueID("indi_x", "F", "serial124")
	if a != b || a == c || len(a) != 36 {
		t.Fatalf("uuid: %s %s %s", a, b, c)
	}
}

func errNum(err error) int {
	if ae, ok := err.(*alpaca.AlpacaError); ok {
		return ae.Number
	}
	return -1
}
