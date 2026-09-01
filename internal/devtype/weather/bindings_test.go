package weather

import (
	"context"
	"fmt"
	"io"
	"math"
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

// TestTotality checks that every server.ObservingConditions member has exactly
// one table entry and no entry is stale.
func TestTotality(t *testing.T) {
	problems := binding.CheckTotal(reflect.TypeOf((*server.ObservingConditions)(nil)).Elem(), table)
	for _, p := range problems {
		t.Error(p)
	}
}

// simDefs mirrors the INDI Weather Simulator: five parameters, wind in kph
// disclosed only by its label.
const simDefs = `
<defSwitchVector device='X' name='CONNECTION' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='CONNECT'>On</defSwitch><defSwitch name='DISCONNECT'>Off</defSwitch>
</defSwitchVector>
<defNumberVector device='X' name='WEATHER_PARAMETERS' state='Ok' perm='ro'>
  <defNumber name='WEATHER_FORECAST' label='Weather'>0</defNumber>
  <defNumber name='WEATHER_TEMPERATURE' label='Temperature (C)'>12.5</defNumber>
  <defNumber name='WEATHER_HUMIDITY' label='Humidity (%)'>40</defNumber>
  <defNumber name='WEATHER_WIND_SPEED' label='Wind (kph)'>18</defNumber>
  <defNumber name='WEATHER_WIND_GUST' label='Gust (kph)'>36</defNumber>
  <defNumber name='WEATHER_RAIN_HOUR' label='Precip (mm)'>0</defNumber>
</defNumberVector>
<defSwitchVector device='X' name='WEATHER_REFRESH' state='Idle' perm='rw' rule='AtMostOne'>
  <defSwitch name='REFRESH'>Off</defSwitch>
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
	dev  *Weather
	send *fakeSender
	st   *snapshot.Store
	up   bool
}

func newFixture(t *testing.T, defs string) *fixture {
	t.Helper()
	fx := &fixture{st: snapshot.NewStore(), send: &fakeSender{}, up: true}
	fx.apply(t, defs)
	kit := &binding.Kit{
		Device: "X",
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
	fx.dev = New(Config{Name: "TestWeather", Exec: "indi_x", Slot: "11208/0", Version: "test"}, kit)
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

func TestReads(t *testing.T) {
	fx := newFixture(t, simDefs)
	d := fx.dev
	if v, err := d.Temperature(); err != nil || v != 12.5 {
		t.Fatalf("Temperature = %v, %v", v, err)
	}
	if v, err := d.Humidity(); err != nil || v != 40 {
		t.Fatalf("Humidity = %v, %v", v, err)
	}
	if v, err := d.RainRate(); err != nil || v != 0 {
		t.Fatalf("RainRate = %v, %v", v, err)
	}
}

// TestWindUnitFromLabel checks that 18 kph, disclosed only in the label, reads
// 5 m/s rather than a silent 18.
func TestWindUnitFromLabel(t *testing.T) {
	fx := newFixture(t, simDefs)
	if v, err := fx.dev.WindSpeed(); err != nil || math.Abs(v-5) > 1e-9 {
		t.Fatalf("WindSpeed = %v, %v; want 5 m/s from 18 kph", v, err)
	}
	if v, err := fx.dev.WindGust(); err != nil || math.Abs(v-10) > 1e-9 {
		t.Fatalf("WindGust = %v, %v; want 10 m/s from 36 kph", v, err)
	}
}

// TestWindUndisclosedUnitPassesThrough checks that a label with no unit yields
// the driver's number verbatim.
func TestWindUndisclosedUnitPassesThrough(t *testing.T) {
	fx := newFixture(t, `
<defNumberVector device='X' name='WEATHER_PARAMETERS' state='Ok' perm='ro'>
  <defNumber name='WEATHER_WIND_SPEED' label='Wind'>7</defNumber>
</defNumberVector>`)
	if v, err := fx.dev.WindSpeed(); err != nil || v != 7 {
		t.Fatalf("WindSpeed = %v, %v; want pass-through 7", v, err)
	}
}

// TestLabelDiscovery checks that a driver-named member is found by its label.
func TestLabelDiscovery(t *testing.T) {
	fx := newFixture(t, `
<defNumberVector device='X' name='WEATHER_PARAMETERS' state='Ok' perm='ro'>
  <defNumber name='AMBIENT_T' label='WEATHER_TEMPERATURE'>-3</defNumber>
</defNumberVector>`)
	if v, err := fx.dev.Temperature(); err != nil || v != -3 {
		t.Fatalf("Temperature by label = %v, %v", v, err)
	}
}

// TestUnimplementedSensors checks that sensors the driver does not publish
// answer 0x400, never zero.
func TestUnimplementedSensors(t *testing.T) {
	fx := newFixture(t, simDefs)
	for name, get := range map[string]func() (float64, error){
		"CloudCover":     fx.dev.CloudCover,
		"DewPoint":       fx.dev.DewPoint,
		"Pressure":       fx.dev.Pressure,
		"SkyBrightness":  fx.dev.SkyBrightness,
		"SkyQuality":     fx.dev.SkyQuality,
		"SkyTemperature": fx.dev.SkyTemperature,
		"StarFWHM":       fx.dev.StarFWHM,
		"WindDirection":  fx.dev.WindDirection,
	} {
		if _, err := get(); errNum(err) != alpaca.ErrNumNotImplemented {
			t.Fatalf("%s err = %v, want 0x400", name, err)
		}
	}
	if _, err := fx.dev.SensorDescription("Pressure"); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatal("SensorDescription for an absent sensor must be 0x400")
	}
	if _, err := fx.dev.TimeSinceLastUpdate("Pressure"); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatal("TimeSinceLastUpdate for an absent sensor must be 0x400")
	}
}

func TestSensorDescriptionCarriesLabel(t *testing.T) {
	fx := newFixture(t, simDefs)
	desc, err := fx.dev.SensorDescription("WindSpeed")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(desc, "WEATHER_WIND_SPEED") || !strings.Contains(desc, "Wind (kph)") {
		t.Fatalf("SensorDescription = %q; want member and unit-bearing label", desc)
	}
}

// TestTimeSinceLastUpdate checks that ages come from per-member arrival, not
// vector update: only the members a set names get a fresh Arrived.
func TestTimeSinceLastUpdate(t *testing.T) {
	fx := newFixture(t, simDefs)
	time.Sleep(20 * time.Millisecond)
	fx.apply(t, `<setNumberVector device='X' name='WEATHER_PARAMETERS' state='Ok'>
  <oneNumber name='WEATHER_TEMPERATURE'>13</oneNumber></setNumberVector>`)
	tsT, err := fx.dev.TimeSinceLastUpdate("Temperature")
	if err != nil {
		t.Fatal(err)
	}
	tsH, err := fx.dev.TimeSinceLastUpdate("Humidity")
	if err != nil {
		t.Fatal(err)
	}
	if tsT < 0 || tsH < tsT+0.015 {
		t.Fatalf("Temperature age %v should be well under Humidity age %v", tsT, tsH)
	}
	// "" = the most recently updated sensor, the temperature that just landed.
	tsAny, err := fx.dev.TimeSinceLastUpdate("")
	if err != nil || math.Abs(tsAny-tsT) > 0.05 {
		t.Fatalf(`TimeSinceLastUpdate("") = %v, %v; want ~%v`, tsAny, err, tsT)
	}
}

func TestAveragePeriodRoundTrip(t *testing.T) {
	fx := newFixture(t, simDefs)
	if got := fx.dev.AveragePeriod(); got != 0 {
		t.Fatalf("AveragePeriod default = %g, want 0", got)
	}
	if err := fx.dev.SetAveragePeriod(1.0); err != nil {
		t.Fatal(err)
	}
	if got := fx.dev.AveragePeriod(); got != 1.0 {
		t.Fatalf("AveragePeriod = %g after set", got)
	}
	if err := fx.dev.SetAveragePeriod(-1); errNum(err) != alpaca.ErrNumInvalidValue {
		t.Fatal("negative AveragePeriod must be 0x401")
	}
}

func TestRefresh(t *testing.T) {
	fx := newFixture(t, simDefs)
	if err := fx.dev.Refresh(); err != nil {
		t.Fatal(err)
	}
	if len(fx.send.sent) != 1 || fx.send.sent[0] != "WEATHER_REFRESH/REFRESH(on)" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
}

func TestRefreshAbsent(t *testing.T) {
	fx := newFixture(t, `
<defNumberVector device='X' name='WEATHER_PARAMETERS' state='Ok' perm='ro'>
  <defNumber name='WEATHER_TEMPERATURE'>1</defNumber>
</defNumberVector>`)
	if err := fx.dev.Refresh(); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatalf("Refresh err = %v, want 0x400", err)
	}
}

// TestDeadChild checks that reads answer 0x407 with the supervisor's reason.
func TestDeadChild(t *testing.T) {
	fx := newFixture(t, simDefs)
	fx.up = false
	fx.st.Invalidate()
	_, err := fx.dev.Temperature()
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

func TestValidateDrift(t *testing.T) {
	fx := newFixture(t, simDefs+`
<defNumberVector device='X' name='FANCY_VENDOR_KNOB' state='Ok' perm='rw'>
  <defNumber name='K'>1</defNumber>
</defNumberVector>`)
	notes := binding.Validate(table, consumed, fx.st.Current(), "X")
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
