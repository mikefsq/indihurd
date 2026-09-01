package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mikefsq/indihurd/internal/binding"
	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

// defs exercises every filter clause at once: typed-consumed, bridge-managed,
// Light, BLOB, reserved video, IP_RO, and plainly reachable properties.
const defs = `
<defSwitchVector device='F' name='CONNECTION' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='CONNECT'>On</defSwitch><defSwitch name='DISCONNECT'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='F' name='DEBUG' state='Idle' perm='rw' rule='OneOfMany'>
  <defSwitch name='ENABLE'>Off</defSwitch><defSwitch name='DISABLE'>On</defSwitch>
</defSwitchVector>
<defTextVector device='F' name='DRIVER_INFO' state='Idle' perm='ro'>
  <defText name='DRIVER_NAME'>Sim</defText>
</defTextVector>
<defNumberVector device='F' name='ABS_FOCUS_POSITION' state='Ok' perm='rw'>
  <defNumber name='FOCUS_ABSOLUTE_POSITION' min='0' max='60000' step='1'>17000</defNumber>
</defNumberVector>
<defLightVector device='F' name='WEATHER_STATUS' state='Ok'>
  <defLight name='WEATHER_TEMPERATURE'>Ok</defLight>
</defLightVector>
<defBLOBVector device='F' name='CCD1' state='Idle' perm='ro'>
  <defBLOB name='CCD1' label='Image'/>
</defBLOBVector>
<defSwitchVector device='F' name='CCD_VIDEO_STREAM' state='Idle' perm='rw' rule='OneOfMany'>
  <defSwitch name='STREAM_ON'>Off</defSwitch><defSwitch name='STREAM_OFF'>On</defSwitch>
</defSwitchVector>
<defTextVector device='F' name='RECORD_FILE' state='Idle' perm='rw'>
  <defText name='RECORD_FILE_NAME'>indi_record</defText>
</defTextVector>
<defNumberVector device='F' name='FOCUS_TEMPERATURE' state='Ok' perm='ro'>
  <defNumber name='TEMPERATURE' min='-50' max='70' step='0.1'>12.5</defNumber>
</defNumberVector>
<defNumberVector device='F' name='FOCUS_SPEED' state='Ok' perm='rw' label='Speed'>
  <defNumber name='FOCUS_SPEED_VALUE' label='Focus Speed' min='1' max='5' step='1'>1</defNumber>
</defNumberVector>
<defSwitchVector device='F' name='FOCUS_BACKLASH_TOGGLE' state='Idle' perm='rw' rule='OneOfMany'>
  <defSwitch name='INDI_ENABLED'>Off</defSwitch><defSwitch name='INDI_DISABLED'>On</defSwitch>
</defSwitchVector>
<defTextVector device='F' name='VENDOR_NOTE' state='Idle' perm='rw'>
  <defText name='NOTE'>hello</defText>
</defTextVector>`

// consumedTable mimics a devtype table naming the properties typed mapping owns.
var consumedTable = binding.Table{
	"Position": {Kind: binding.Func, Prop: "ABS_FOCUS_POSITION", Elem: "FOCUS_ABSOLUTE_POSITION", Fn: "Position"},
}

type fakeSender struct {
	sent []string // "PROP/ELEM=V", "PROP/ELEM(on|off)", "PROP/ELEM=txt"
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
func (s *fakeSender) SetText(_ context.Context, _, prop string, v map[string]string) error {
	for e, val := range v {
		s.sent = append(s.sent, prop+"/"+e+"="+val)
	}
	return nil
}
func (s *fakeSender) WaitSettle(context.Context, string, string, time.Time, time.Duration) (indiwire.State, string, error) {
	return indiwire.Ok, "", nil
}
func (s *fakeSender) WaitUpdate(context.Context, string, string, time.Time, time.Duration) error {
	return nil
}

type fixture struct {
	eng  *Engine
	send *fakeSender
	st   *snapshot.Store
	up   bool
}

func newFixture(t *testing.T, stream string) *fixture {
	t.Helper()
	fx := &fixture{st: snapshot.NewStore(), send: &fakeSender{}, up: true}
	fx.apply(t, stream)
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
	fx.eng = New(kit, consumedTable.Consumed("FOCUS_MAX"))
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

func TestSupportedFilter(t *testing.T) {
	fx := newFixture(t, defs)
	got := fx.eng.Supported()
	want := []string{
		"INDI:FOCUS_BACKLASH_TOGGLE",
		"INDI:FOCUS_SPEED",
		"INDI:FOCUS_TEMPERATURE", // IP_RO is listed. Reads work; writes refuse
		"INDI:VENDOR_NOTE",
		"INDI:_PROPERTIES",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Supported = %v, want %v", got, want)
	}
}

func TestReadDocument(t *testing.T) {
	fx := newFixture(t, defs)
	res, handled, err := fx.eng.Do("INDI:FOCUS_SPEED", "")
	if !handled || err != nil {
		t.Fatalf("read: handled=%v err=%v", handled, err)
	}
	var doc struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		State   string `json:"state"`
		Perm    string `json:"perm"`
		Members []struct {
			Name  string  `json:"name"`
			Value float64 `json:"value"`
			Min   float64 `json:"min"`
			Max   float64 `json:"max"`
			Step  float64 `json:"step"`
		} `json:"members"`
	}
	if err := json.Unmarshal([]byte(res), &doc); err != nil {
		t.Fatalf("read result is not JSON: %v\n%s", err, res)
	}
	if doc.Name != "FOCUS_SPEED" || doc.Type != "Number" || doc.Perm != "rw" || doc.State != "Ok" {
		t.Fatalf("doc header = %+v", doc)
	}
	m := doc.Members[0]
	if m.Name != "FOCUS_SPEED_VALUE" || m.Value != 1 || m.Min != 1 || m.Max != 5 || m.Step != 1 {
		t.Fatalf("member = %+v", m)
	}
}

func TestWriteNumber(t *testing.T) {
	fx := newFixture(t, defs)
	if _, handled, err := fx.eng.Do("INDI:FOCUS_SPEED", `{"FOCUS_SPEED_VALUE": 3}`); !handled || err != nil {
		t.Fatalf("write: handled=%v err=%v", handled, err)
	}
	if len(fx.send.sent) != 1 || fx.send.sent[0] != "FOCUS_SPEED/FOCUS_SPEED_VALUE=3" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
}

func TestWriteNumberRangeCheckedNothingSent(t *testing.T) {
	fx := newFixture(t, defs)
	_, handled, err := fx.eng.Do("INDI:FOCUS_SPEED", `{"FOCUS_SPEED_VALUE": 99}`)
	if !handled || errNum(err) != 0x401 {
		t.Fatalf("out-of-range write: handled=%v err=%v", handled, err)
	}
	if len(fx.send.sent) != 0 {
		t.Fatalf("out-of-range write reached the driver: %v", fx.send.sent)
	}
}

func TestWriteBadPayloads(t *testing.T) {
	fx := newFixture(t, defs)
	for name, params := range map[string]string{
		"not json":       `speed=3`,
		"empty object":   `{}`,
		"unknown member": `{"NOPE": 3}`,
		"wrong type":     `{"FOCUS_SPEED_VALUE": "fast"}`,
	} {
		if _, handled, err := fx.eng.Do("INDI:FOCUS_SPEED", params); !handled || errNum(err) != 0x401 {
			t.Errorf("%s: handled=%v err=%v, want 0x401", name, handled, err)
		}
	}
	if len(fx.send.sent) != 0 {
		t.Fatalf("bad payloads reached the driver: %v", fx.send.sent)
	}
}

func TestWriteSwitchAndText(t *testing.T) {
	fx := newFixture(t, defs)
	if _, _, err := fx.eng.Do("INDI:FOCUS_BACKLASH_TOGGLE", `{"INDI_ENABLED": true, "INDI_DISABLED": false}`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fx.eng.Do("INDI:VENDOR_NOTE", `{"NOTE": "hi"}`); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"FOCUS_BACKLASH_TOGGLE/INDI_ENABLED(on)",
		"FOCUS_BACKLASH_TOGGLE/INDI_DISABLED(off)",
		"VENDOR_NOTE/NOTE=hi",
	}
	if !reflect.DeepEqual(fx.send.sent, want) {
		t.Fatalf("sent = %v, want %v", fx.send.sent, want)
	}
}

func TestWriteReadOnlyRefused(t *testing.T) {
	fx := newFixture(t, defs)
	_, handled, err := fx.eng.Do("INDI:FOCUS_TEMPERATURE", `{"TEMPERATURE": 0}`)
	if !handled || errNum(err) != 0x40B {
		t.Fatalf("IP_RO write: handled=%v err=%v, want 0x40B", handled, err)
	}
	if len(fx.send.sent) != 0 {
		t.Fatalf("IP_RO write reached the driver: %v", fx.send.sent)
	}
	// The read half of an IP_RO property stays reachable.
	if _, handled, err := fx.eng.Do("INDI:FOCUS_TEMPERATURE", ""); !handled || err != nil {
		t.Fatalf("IP_RO read: handled=%v err=%v", handled, err)
	}
}

func TestUnknownAndFilteredUnhandled(t *testing.T) {
	fx := newFixture(t, defs)
	for _, name := range []string{
		"TelescopeAction",         // not our namespace
		"INDI:NO_SUCH_PROP",       // absent
		"INDI:CONNECTION",         // bridge-managed
		"INDI:ABS_FOCUS_POSITION", // typed-consumed
		"INDI:WEATHER_STATUS",     // Light
		"INDI:CCD1",               // BLOB
		"INDI:CCD_VIDEO_STREAM",   // reserved video
		"INDI:RECORD_FILE",        // reserved video prefix
	} {
		if _, handled, _ := fx.eng.Do(name, ""); handled {
			t.Errorf("%s: handled, want fallback to the base 0x40C", name)
		}
	}
}

func TestDownAnswers407(t *testing.T) {
	fx := newFixture(t, defs)
	fx.up = false
	fx.st.Invalidate()
	if got := fx.eng.Supported(); !reflect.DeepEqual(got, []string{"INDI:_PROPERTIES"}) {
		t.Fatalf("Supported while down = %v", got)
	}
	_, handled, err := fx.eng.Do("INDI:FOCUS_SPEED", "")
	if !handled || errNum(err) != 0x407 {
		t.Fatalf("down read: handled=%v err=%v, want 0x407", handled, err)
	}
	if !strings.Contains(err.Error(), "re-acquiring") {
		t.Fatalf("reason not preserved: %v", err)
	}
}

func TestIntrospection(t *testing.T) {
	fx := newFixture(t, defs)
	res, handled, err := fx.eng.Do("INDI:_PROPERTIES", "")
	if !handled || err != nil {
		t.Fatalf("introspect: handled=%v err=%v", handled, err)
	}
	var props map[string]json.RawMessage
	if err := json.Unmarshal([]byte(res), &props); err != nil {
		t.Fatalf("introspection is not JSON: %v", err)
	}
	for _, want := range []string{"FOCUS_SPEED", "FOCUS_TEMPERATURE", "VENDOR_NOTE"} {
		if _, ok := props[want]; !ok {
			t.Errorf("introspection missing %s: %v", want, res)
		}
	}
	for _, banned := range []string{"CONNECTION", "ABS_FOCUS_POSITION", "WEATHER_STATUS", "CCD1", "CCD_VIDEO_STREAM"} {
		if _, ok := props[banned]; ok {
			t.Errorf("introspection leaks %s", banned)
		}
	}
}

// errNum extracts the Alpaca error number by reflection: these tests may not
// import goalpaca.
func errNum(err error) int {
	v := reflect.ValueOf(err)
	if v.Kind() == reflect.Ptr && !v.IsNil() {
		if f := v.Elem().FieldByName("Number"); f.IsValid() && f.CanInt() {
			return int(f.Int())
		}
	}
	return -1
}
