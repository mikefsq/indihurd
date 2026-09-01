package switchdev

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

// TestTotality checks that every server.Switch member has exactly one table
// entry and no entry is stale.
func TestTotality(t *testing.T) {
	problems := binding.CheckTotal(reflect.TypeOf((*server.Switch)(nil)).Elem(), table)
	for _, p := range problems {
		t.Error(p)
	}
}

// mixedDefs composes the real shapes of the output, input and power
// interfaces, including a sensor with step 0 and momentary operations that
// must stay out of the id space.
const mixedDefs = `
<defSwitchVector device='S' name='CONNECTION' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='CONNECT'>On</defSwitch><defSwitch name='DISCONNECT'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='S' name='DIGITAL_INPUT_1' label='Rain Sensor' state='Ok' perm='ro' rule='OneOfMany'>
  <defSwitch name='OFF'>On</defSwitch><defSwitch name='ON'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='S' name='DIGITAL_OUTPUT_1' label='Relay 1' state='Ok' perm='rw' rule='AtMostOne'>
  <defSwitch name='OFF'>On</defSwitch><defSwitch name='ON'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='S' name='DIGITAL_OUTPUT_2' state='Ok' perm='rw' rule='AtMostOne'>
  <defSwitch name='OFF'>Off</defSwitch><defSwitch name='ON'>On</defSwitch>
</defSwitchVector>
<defNumberVector device='S' name='PULSE_0' label='Output #1' state='Idle' perm='rw'>
  <defNumber name='DURATION' label='Duration (ms)' min='0' max='60000' step='100'>0</defNumber>
</defNumberVector>
<defSwitchVector device='S' name='POWER_CHANNELS' state='Ok' perm='rw' rule='AnyOfMany'>
  <defSwitch name='POWER_CHANNEL_1' label='Mount'>On</defSwitch>
  <defSwitch name='POWER_CHANNEL_2' label='Camera'>Off</defSwitch>
</defSwitchVector>
<defNumberVector device='S' name='DEW_DUTY_CYCLES' state='Ok' perm='rw'>
  <defNumber name='DEW_1' label='Dew A' min='0' max='100' step='10'>40</defNumber>
</defNumberVector>
<defNumberVector device='S' name='POWER_SENSORS' state='Ok' perm='ro'>
  <defNumber name='SENSOR_VOLTAGE' label='Voltage (V)' min='0' max='999' step='0'>12.4</defNumber>
</defNumberVector>
<defSwitchVector device='S' name='USB_PORTS' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='PORT_1'>On</defSwitch><defSwitch name='PORT_2'>Off</defSwitch>
</defSwitchVector>
<defTextVector device='S' name='POWER_LABELS' state='Idle' perm='rw'>
  <defText name='POWER_CHANNEL_1'>Mount</defText>
</defTextVector>
<defSwitchVector device='S' name='POWER_CYCLE' state='Idle' perm='rw' rule='AtMostOne'>
  <defSwitch name='POWER_CYCLE_Toggle'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='S' name='LED_CONTROL' state='Idle' perm='rw' rule='OneOfMany'>
  <defSwitch name='INDI_ENABLED'>On</defSwitch><defSwitch name='INDI_DISABLED'>Off</defSwitch>
</defSwitchVector>`

// wantPairs is the id space mixedDefs must produce.
var wantPairs = []pair{
	{"DEW_DUTY_CYCLES", "DEW_1"},          // 0
	{"DIGITAL_INPUT_1", ""},               // 1
	{"DIGITAL_OUTPUT_1", ""},              // 2
	{"DIGITAL_OUTPUT_2", ""},              // 3
	{"POWER_CHANNELS", "POWER_CHANNEL_1"}, // 4
	{"POWER_CHANNELS", "POWER_CHANNEL_2"}, // 5
	{"POWER_SENSORS", "SENSOR_VOLTAGE"},   // 6
	{"PULSE_0", "DURATION"},               // 7
	{"USB_PORTS", "PORT_1"},               // 8
	{"USB_PORTS", "PORT_2"},               // 9
}

type fakeSender struct {
	sent []string // "PROP/ELEM=V" and "PROP/ELEM(on|off)"
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
	dev  *Switch
	send *fakeSender
	st   *snapshot.Store
	up   bool
}

func newFixture(t *testing.T, defs string) *fixture {
	t.Helper()
	fx := &fixture{st: snapshot.NewStore(), send: &fakeSender{}, up: true}
	fx.apply(t, defs)
	kit := &binding.Kit{
		Device: "S",
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
	fx.dev = New(Config{Name: "TestSwitch", Exec: "indi_x", Slot: "11208/0", Version: "test"}, kit)
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

func TestFlatteningOrder(t *testing.T) {
	fx := newFixture(t, mixedDefs)
	if got := fx.dev.MaxSwitch(); got != len(wantPairs) {
		t.Fatalf("MaxSwitch = %d, want %d", got, len(wantPairs))
	}
	if !reflect.DeepEqual(fx.dev.refresh(), wantPairs) {
		t.Fatalf("pinned = %v\nwant %v", fx.dev.refresh(), wantPairs)
	}
}

// TestPinAppendOnly checks that new properties append and a deleted property
// keeps its id, answering 0x400 there.
func TestPinAppendOnly(t *testing.T) {
	fx := newFixture(t, mixedDefs)
	base := fx.dev.MaxSwitch()

	fx.apply(t, `<defSwitchVector device='S' name='DIGITAL_OUTPUT_10' state='Ok' perm='rw' rule='AtMostOne'>
  <defSwitch name='OFF'>On</defSwitch><defSwitch name='ON'>Off</defSwitch></defSwitchVector>`)
	if got := fx.dev.MaxSwitch(); got != base+1 {
		t.Fatalf("MaxSwitch after new def = %d, want %d", got, base+1)
	}
	if desc, _ := fx.dev.GetSwitchDescription(2); desc != "INDI DIGITAL_OUTPUT_1" {
		t.Fatalf("id 2 renumbered: %q", desc)
	}
	if desc, _ := fx.dev.GetSwitchDescription(base); desc != "INDI DIGITAL_OUTPUT_10" {
		t.Fatalf("new pair not appended at %d: %q", base, desc)
	}

	fx.apply(t, `<delProperty device='S' name='DIGITAL_OUTPUT_1'/>`)
	if got := fx.dev.MaxSwitch(); got != base+1 {
		t.Fatalf("MaxSwitch after delProperty = %d — a missing pair must hold its id", got)
	}
	if _, err := fx.dev.GetSwitch(2); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatalf("deleted property's pinned id: err = %v, want 0x400", err)
	}
}

func TestReadsAndDescriptors(t *testing.T) {
	fx := newFixture(t, mixedDefs)
	d := fx.dev

	// Bool pairs read the ON member; per-member switches read their own.
	for id, want := range map[int]bool{1: false, 2: false, 3: true, 4: true, 5: false} {
		if got, err := d.GetSwitch(id); err != nil || got != want {
			t.Errorf("GetSwitch(%d) = %v, %v (want %v)", id, got, err, want)
		}
	}
	// Number-backed: true above the bottom of the range.
	if on, _ := d.GetSwitch(0); !on { // 40 on a 0..100 duty cycle
		t.Error("GetSwitch(0) = false for a 40% duty cycle")
	}
	if on, _ := d.GetSwitch(7); on { // 0 on a 0..60000 duration
		t.Error("GetSwitch(7) = true for a 0ms pulse")
	}
	if v, _ := d.GetSwitchValue(0); v != 40 {
		t.Errorf("GetSwitchValue(0) = %g", v)
	}
	if v, _ := d.GetSwitchValue(4); v != 1 {
		t.Errorf("GetSwitchValue(4) = %g (bool reports 0/1)", v)
	}

	type r3 struct{ min, max, step float64 }
	for id, want := range map[int]r3{
		0: {0, 100, 10}, // real analogue range
		2: {0, 1, 1},    // bool pair
		6: {0, 999, 1},  // sensor: driver step 0 substituted with 1
		7: {0, 60000, 100},
	} {
		min, _ := d.MinSwitchValue(id)
		max, _ := d.MaxSwitchValue(id)
		step, _ := d.SwitchStep(id)
		if (r3{min, max, step}) != want {
			t.Errorf("range(%d) = %v/%v/%v, want %v", id, min, max, step, want)
		}
	}

	// Names: vector label for a pair, member label per-member, name fallback.
	for id, want := range map[int]string{
		1: "Rain Sensor", 2: "Relay 1", 3: "DIGITAL_OUTPUT_2", 4: "Mount", 8: "PORT_1",
	} {
		if got, err := d.GetSwitchName(id); err != nil || got != want {
			t.Errorf("GetSwitchName(%d) = %q, %v (want %q)", id, got, err, want)
		}
	}
	if desc, _ := d.GetSwitchDescription(4); desc != "INDI POWER_CHANNELS.POWER_CHANNEL_1" {
		t.Errorf("GetSwitchDescription(4) = %q", desc)
	}

	// CanWrite from Perm: inputs and sensors read-only.
	for id, want := range map[int]bool{0: true, 1: false, 2: true, 6: false} {
		if got, err := d.CanWrite(id); err != nil || got != want {
			t.Errorf("CanWrite(%d) = %v, %v (want %v)", id, got, err, want)
		}
	}
}

func TestWrites(t *testing.T) {
	fx := newFixture(t, mixedDefs)
	d := fx.dev

	if err := d.SetSwitch(2, true); err != nil { // bool pair: both halves explicit
		t.Fatal(err)
	}
	if err := d.SetSwitch(3, false); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSwitch(5, true); err != nil { // AnyOfMany member alone
		t.Fatal(err)
	}
	if err := d.SetSwitch(4, false); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSwitch(7, true); err != nil { // number-backed: range end
		t.Fatal(err)
	}
	if err := d.SetSwitchValue(0, 70); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"DIGITAL_OUTPUT_1/ON(on)", "DIGITAL_OUTPUT_1/OFF(off)",
		"DIGITAL_OUTPUT_2/OFF(on)", "DIGITAL_OUTPUT_2/ON(off)",
		"POWER_CHANNELS/POWER_CHANNEL_2(on)",
		"POWER_CHANNELS/POWER_CHANNEL_1(off)",
		"PULSE_0/DURATION=60000",
		"DEW_DUTY_CYCLES/DEW_1=70",
	}
	if !reflect.DeepEqual(fx.send.sent, want) {
		t.Fatalf("sent = %v\nwant %v", fx.send.sent, want)
	}
}

func TestWriteGuards(t *testing.T) {
	fx := newFixture(t, mixedDefs)
	d := fx.dev

	// Read-only: ASCOM wants NotImplemented from writes where CanWrite=false.
	if err := d.SetSwitch(1, true); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Errorf("SetSwitch on input err = %v, want 0x400", err)
	}
	if err := d.SetSwitchValue(6, 5); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Errorf("SetSwitchValue on sensor err = %v, want 0x400", err)
	}
	// Out of range: 0x401 with nothing sent.
	if err := d.SetSwitchValue(0, 150); errNum(err) != alpaca.ErrNumInvalidValue {
		t.Errorf("out-of-range err = %v, want 0x401", err)
	}
	// OneOfMany cannot be turned all-off through one member.
	if err := d.SetSwitch(8, false); errNum(err) != alpaca.ErrNumInvalidOperation {
		t.Errorf("OneOfMany all-off err = %v, want 0x40B", err)
	}
	if err := d.SetSwitchName(0, "x"); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Errorf("SetSwitchName err = %v, want 0x400", err)
	}
	if len(fx.send.sent) != 0 {
		t.Fatalf("guarded writes reached the driver: %v", fx.send.sent)
	}
}

func TestAsyncQuartet(t *testing.T) {
	fx := newFixture(t, mixedDefs)
	d := fx.dev
	if can, _ := d.CanAsync(0); !can {
		t.Error("CanAsync(0) = false for a writable channel")
	}
	if can, _ := d.CanAsync(1); can {
		t.Error("CanAsync(1) = true for a read-only input")
	}
	if done, _ := d.StateChangeComplete(0); !done {
		t.Error("StateChangeComplete(0) = false with state Ok")
	}
	fx.apply(t, `<setNumberVector device='S' name='DEW_DUTY_CYCLES' state='Busy'>
  <oneNumber name='DEW_1'>70</oneNumber></setNumberVector>`)
	if done, _ := d.StateChangeComplete(0); done {
		t.Error("StateChangeComplete(0) = true while Busy")
	}
	if err := d.SetAsync(2, true); err != nil {
		t.Fatal(err)
	}
	if err := d.CancelAsync(0); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Errorf("CancelAsync err = %v, want 0x400 (no INDI abort exists)", err)
	}
}

// TestDeadChild checks that ids hold and reads answer 0x407 with the
// supervisor's reason.
func TestDeadChild(t *testing.T) {
	fx := newFixture(t, mixedDefs)
	max := fx.dev.MaxSwitch()
	fx.up = false
	fx.st.Invalidate()
	if got := fx.dev.MaxSwitch(); got != max {
		t.Fatalf("MaxSwitch while down = %d, want the pinned %d", got, max)
	}
	_, err := fx.dev.GetSwitch(0)
	if errNum(err) != alpaca.ErrNumNotConnected {
		t.Fatalf("err = %v, want 0x407", err)
	}
	if !strings.Contains(err.Error(), "re-acquiring") {
		t.Fatalf("reason not preserved: %v", err)
	}
	if !fx.dev.Connecting() {
		t.Fatal("Connecting() false while down")
	}
}

func TestActionsExclusions(t *testing.T) {
	fx := newFixture(t, mixedDefs)
	got := map[string]bool{}
	for _, a := range fx.dev.SupportedActions() {
		got[a] = true
	}
	for _, want := range []string{"INDI:POWER_CYCLE", "INDI:LED_CONTROL", "INDI:_PROPERTIES"} {
		if !got[want] {
			t.Errorf("SupportedActions missing %s: %v", want, fx.dev.SupportedActions())
		}
	}
	for _, banned := range []string{"INDI:DIGITAL_OUTPUT_1", "INDI:POWER_CHANNELS", "INDI:POWER_LABELS", "INDI:CONNECTION"} {
		if got[banned] {
			t.Errorf("SupportedActions leaks %s (flattened or bridge-managed)", banned)
		}
	}
}

func TestValidateConsumesFamilies(t *testing.T) {
	fx := newFixture(t, mixedDefs)
	joined := strings.Join(Validate(fx.st.Current(), "S"), "\n")
	if strings.Contains(joined, "DIGITAL_OUTPUT_1") || strings.Contains(joined, "POWER_LABELS") {
		t.Fatalf("flattened properties reported as drift:\n%s", joined)
	}
	if !strings.Contains(joined, "LED_CONTROL") {
		t.Fatalf("Actions-only property not reported:\n%s", joined)
	}
}

func TestRecognise(t *testing.T) {
	fx := newFixture(t, mixedDefs)
	if !Recognise(fx.st.Current(), "S") {
		t.Fatal("Recognise = false for a device full of switch families")
	}
	plain := newFixture(t, `<defNumberVector device='S' name='ABS_FOCUS_POSITION' state='Ok' perm='rw'>
  <defNumber name='FOCUS_ABSOLUTE_POSITION' min='0' max='60000' step='1'>0</defNumber></defNumberVector>`)
	if Recognise(plain.st.Current(), "S") {
		t.Fatal("Recognise = true for a focuser-shaped device")
	}
}

func errNum(err error) int {
	if ae, ok := err.(*alpaca.AlpacaError); ok {
		return ae.Number
	}
	return -1
}
